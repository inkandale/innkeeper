package targets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/secrets"
	"github.com/davidgibbons/innkeeper/internal/testdb"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

type env struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	lib  *library.Library
	reg  *plugins.Registry
	s    *Service
	inst int64
	// dir is where the fake app keeps its cards.
	dir  string
	card int64
}

// setup returns a Service with one fake target instance, set up by config
// plus a dir, and one card.
func setup(t *testing.T, config map[string]any) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	lib := library.New(pool, store)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"push", "pull"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store, box)
	mgr := pluginhost.NewManager(noCore, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	e := &env{t: t, ctx: ctx, pool: pool, lib: lib, reg: reg, s: New(pool, lib, reg, mgr, quiet), dir: t.TempDir()}
	cfg := map[string]any{"dir": e.dir}
	maps.Copy(cfg, config)
	if e.inst, err = reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: "Fake app", Config: cfg}); err != nil {
		t.Fatal(err)
	}
	e.card = e.newCard("Brakka")
	return e
}

func (e *env) newCard(name string) int64 {
	e.t.Helper()
	data := fmt.Sprintf(`{"name": %q, "description": "A dwarf smith.", "first_mes": "Hail.", "scenario": "A forge."}`, name)
	c, _, err := e.lib.Cards.CreateFromName(e.ctx, name, "upload", library.Edit{Data: json.RawMessage(data), Source: "ui"})
	if err != nil {
		e.t.Fatal(err)
	}
	return c.ID
}

func (e *env) head(card int64) library.Revision {
	e.t.Helper()
	c, err := e.lib.Cards.Get(e.ctx, card)
	if err != nil {
		e.t.Fatal(err)
	}
	rev, err := e.lib.Cards.Revision(e.ctx, c.HeadRevisionID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rev
}

// update adds a revision to card, made by change editing the head's content.
func (e *env) update(card int64, change func(*library.Edit)) {
	e.t.Helper()
	rev := e.head(card)
	ed := library.Edit{Data: rev.Data, Overrides: rev.Overrides, Source: "ui"}
	if rev.AvatarBlob != nil {
		ed.AvatarBlob = *rev.AvatarBlob
	}
	change(&ed)
	if _, err := e.lib.Cards.Update(e.ctx, card, rev.ID, ed); err != nil {
		e.t.Fatal(err)
	}
}

// setField returns data with key set to value, or removed if value is nil.
func setField(data json.RawMessage, key string, value any) json.RawMessage {
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if value == nil {
		delete(m, key)
	} else {
		m[key] = value
	}
	out, _ := json.Marshal(m)
	return out
}

// key is the instance's override key.
func (e *env) key() string { return strconv.FormatInt(e.inst, 10) }

func (e *env) push(card int64) Binding {
	e.t.Helper()
	b, err := e.s.Push(e.ctx, e.inst, card, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) check(b Binding) Binding {
	e.t.Helper()
	b, err := e.s.Check(e.ctx, b.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) status(card int64) string {
	e.t.Helper()
	list, err := e.s.Bindings(e.ctx, card)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(list) != 1 {
		e.t.Fatalf("card %d has %d bindings", card, len(list))
	}
	return list[0].Status
}

// remote reads the whole file the fake app keeps for a card.
func (e *env) remote(id string) map[string]any {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, id+".json"))
	if err != nil {
		e.t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal(raw, &card); err != nil {
		e.t.Fatal(err)
	}
	return card
}

// remoteData is the data object of the app's copy.
func (e *env) remoteData(id string) map[string]any {
	e.t.Helper()
	data, _ := e.remote(id)["data"].(map[string]any)
	return data
}

// editRemote changes the app's copy as the owner would in the app. A nil
// value removes the field.
func (e *env) editRemote(id, key string, value any) {
	e.t.Helper()
	card := e.remote(id)
	data, _ := card["data"].(map[string]any)
	if value == nil {
		delete(data, key)
	} else {
		data[key] = value
	}
	raw, _ := json.Marshal(card)
	if err := os.WriteFile(filepath.Join(e.dir, id+".json"), raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}
