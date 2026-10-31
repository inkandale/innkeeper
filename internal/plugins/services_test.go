package plugins

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

func TestInstancesOfferingListsOnlyInstancesThatOfferTheService(t *testing.T) {
	ctx := context.Background()
	reg, mgr, chat := newServiceInstance(t, []string{"ai"}, protocol.MethodAIChat)
	create := func(name string, services ...string) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new(name),
			Config: map[string]any{"services": services}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	create("Embed", protocol.MethodAIEmbed)
	off := create("Off", protocol.MethodAIChat)
	disabled := true
	if err := reg.UpdateInstance(ctx, off, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}

	got, err := reg.InstancesOffering(ctx, mgr, protocol.MethodAIChat)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != chat {
		t.Fatalf("instances offering ai.chat = %+v, want only %d", got, chat)
	}
	if _, err := reg.InstancesOffering(ctx, mgr, "ai.draw"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown service: %v, want ErrInvalid", err)
	}
}

func TestDecideRefusesAnAIInstanceWithoutAIDecide(t *testing.T) {
	ctx := context.Background()
	reg, mgr, chat := newServiceInstance(t, []string{"ai"}, protocol.MethodAIChat)
	create := func(name string, services ...string) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new(name),
			Config: map[string]any{"services": services}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	p := protocol.AIDecideParams{State: []byte(`"Hello."`),
		Questions: map[string]protocol.DecideQuestion{"greeting": {Type: "noul", Instructions: "The state is a greeting."}}}

	_, err := reg.Decide(ctx, mgr, chat, p)
	if !errors.Is(err, ErrIncapable) || !strings.Contains(err.Error(), "doesn't offer ai.decide") {
		t.Fatalf("Decide on an instance without ai.decide: %v, want ErrIncapable naming ai.decide", err)
	}
	res, err := reg.Decide(ctx, mgr, create("Decide", protocol.MethodAIDecide), p)
	if err != nil || res.Validate(p.Questions) != nil {
		t.Fatalf("Decide = %+v, %v; want an answer to every question", res, err)
	}
}

// newServiceInstance creates a fake instance with caps that offers service.
func newServiceInstance(t *testing.T, caps []string, service string) (*Registry, *pluginhost.Manager, int64) {
	t.Helper()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, caps))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	id, err := reg.CreateInstance(context.Background(), NewInstance{Plugin: "fake", Name: new("Svc"),
		Config: map[string]any{"services": []string{service}}})
	if err != nil {
		t.Fatal(err)
	}
	return reg, mgr, id
}

func TestVectorQueryReturnsTheInstancesVector(t *testing.T) {
	r, mgr, id := newServiceInstance(t, []string{"enrich"}, protocol.MethodVectorQuery)
	got, err := r.VectorQuery(context.Background(), mgr, id, "abc")
	if err != nil || got.Space != "fake" || len(got.Vector) != 2 || got.Vector[0] != 3 {
		t.Fatalf("VectorQuery = %+v, %v", got, err)
	}
}

func TestVectorInstanceIgnoresAMissingOrDisabledInstance(t *testing.T) {
	r, _, id := newServiceInstance(t, []string{"enrich"}, protocol.MethodVectorQuery)
	ctx := context.Background()
	if err := r.SetSetting(ctx, VectorInstanceKey, strconv.FormatInt(id, 10)); err != nil {
		t.Fatal(err)
	}
	if got, err := r.VectorInstance(ctx); err != nil || got != id {
		t.Fatalf("set: %d, %v", got, err)
	}
	disabled := true
	if err := r.UpdateInstance(ctx, id, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if got, err := r.VectorInstance(ctx); err != nil || got != 0 {
		t.Fatalf("disabled: %d, %v", got, err)
	}
	disabled = false
	if err := r.UpdateInstance(ctx, id, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if err := r.DeleteInstance(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got, err := r.VectorInstance(ctx); err != nil || got != 0 {
		t.Fatalf("deleted: %d, %v", got, err)
	}
}

func TestVectorInstanceWithNoSettingPicksTheOnlyWorkingVectorInstance(t *testing.T) {
	r, _, first := newServiceInstance(t, []string{"enrich"}, protocol.MethodVectorQuery)
	ctx := context.Background()
	// The fake's enrichments have no vector key; declare one.
	r.manifests["fake"].Enrichments["embedding"] = protocol.Enrichment{Type: "vector", Label: "Embedding"}
	want := func(step string, id int64) {
		t.Helper()
		if got, err := r.VectorInstance(ctx); err != nil || got != id {
			t.Fatalf("%s: %d, %v; want %d", step, got, err, id)
		}
	}
	want("one instance", first)

	second, err := r.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new("Second")})
	if err != nil {
		t.Fatal(err)
	}
	want("two instances", 0)

	disabled := true
	if err := r.UpdateInstance(ctx, second, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	want("second disabled", first)

	disabled = false
	if err := r.UpdateInstance(ctx, second, InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = 'boom' WHERE id = $1`, second); err != nil {
		t.Fatal(err)
	}
	want("second broken", first)
}
