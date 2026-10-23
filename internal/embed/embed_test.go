package embed

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

type env struct {
	lib  *library.Library
	reg  *plugins.Registry
	run  *Runner
	inst int64
}

// newEnv has one fake ai instance offering ai.embed, not yet the embed instance.
func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(nil, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(mgr.Close)
	inst, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Embed"),
		Config: map[string]any{"services": []string{protocol.MethodAIEmbed}}})
	if err != nil {
		t.Fatal(err)
	}
	lib := library.New(pool, store)
	return env{lib: lib, reg: reg, run: &Runner{Lib: lib, Registry: reg, Plugins: mgr}, inst: inst}
}

func (e env) card(t *testing.T, slug, data string) (int64, int64) {
	t.Helper()
	c, rev, err := e.lib.Cards.Create(context.Background(), library.DefaultLibrary, slug, "upload",
		library.Edit{Data: json.RawMessage(data), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID, rev.ID
}

func TestEmbedJobDoesNothingWithoutEmbedInstance(t *testing.T) {
	e := newEnv(t)
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.run.Run(context.Background(), &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(context.Background(), e.inst, 10); len(got) != 1 {
		t.Fatalf("a run without an embed instance embedded the card: %+v", got)
	}
}

func TestEmbedJobEmbedsEveryCardInBatches(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := range batchSize + 3 {
		e.card(t, "card-"+strconv.Itoa(i), `{"name": "Card `+strconv.Itoa(i)+`"}`)
	}
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	r := &jobs.Run{}
	if err := e.run.Run(ctx, r); err != nil {
		t.Fatal(err)
	}
	if got, err := e.lib.Unembedded(ctx, e.inst, 100); err != nil || len(got) != 0 {
		t.Fatalf("left unembedded: %d, %v", len(got), err)
	}
}

func TestEmbedJobReembedsEditedCard(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, rev := e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, strconv.FormatInt(e.inst, 10)); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.lib.Cards.Update(ctx, id, rev, library.Edit{Data: json.RawMessage(`{"name": "Brakka the Bold"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 10); len(got) != 1 {
		t.Fatalf("an edited card isn't due: %+v", got)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.lib.Unembedded(ctx, e.inst, 10); len(got) != 0 {
		t.Fatalf("still due after a run: %+v", got)
	}
}

func TestEmbedJobIgnoresADeletedEmbedInstance(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.card(t, "brakka", `{"name": "Brakka"}`)
	if err := e.reg.SetSetting(ctx, InstanceKey, "999999"); err != nil {
		t.Fatal(err)
	}
	if err := e.run.Run(ctx, &jobs.Run{}); err != nil {
		t.Fatalf("a run naming a missing instance: %v, want nil", err)
	}
}
