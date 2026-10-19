package plugins

import (
	"context"
	"errors"
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
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
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
	create := func(name string, services ...string) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: new(name),
			Config: map[string]any{"services": services}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	chat := create("Chat", protocol.MethodAIChat)
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
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
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

	chat := create("Chat", protocol.MethodAIChat)
	_, err = reg.Decide(ctx, mgr, chat, p)
	if !errors.Is(err, ErrIncapable) || !strings.Contains(err.Error(), "doesn't offer ai.decide") {
		t.Fatalf("Decide on an instance without ai.decide: %v, want ErrIncapable naming ai.decide", err)
	}
	res, err := reg.Decide(ctx, mgr, create("Decide", protocol.MethodAIDecide), p)
	if err != nil || res.Validate(p.Questions) != nil {
		t.Fatalf("Decide = %+v, %v; want an answer to every question", res, err)
	}
}
