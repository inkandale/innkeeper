package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

func TestPushCreatesThenUpdates(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	if b.RemoteID != "r1" || b.Status != "in_sync" || b.RemoteState != "unchanged" || b.LastPush.RemoteID != "r1" {
		t.Fatalf("first push: %+v", b)
	}
	if got := e.remoteData("r1"); got["name"] != "Brakka" || got["scenario"] != nil {
		t.Fatalf("app has %v; want the name and no scenario, which it doesn't store", got)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grumpy dwarf smith.") })
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after an edit: %s", s)
	}
	b = e.push(e.card)
	if b.RemoteID != "r1" || b.Status != "in_sync" {
		t.Fatalf("second push: %+v", b)
	}
	if got := e.remoteData("r1")["description"]; got != "A grumpy dwarf smith." {
		t.Fatalf("app description = %v", got)
	}
	pushes, err := e.s.Pushes(e.ctx, b.ID)
	if err != nil || len(pushes) != 2 || pushes[0].RevisionID != e.head(e.card).ID {
		t.Fatalf("pushes = %+v, %v", pushes, err)
	}
}

func TestPushAppliesOverrideAndEmbedsLorebooks(t *testing.T) {
	e := setup(t, nil)
	lb, _, err := e.lib.Lorebooks.Create(e.ctx, "forge-lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": [{"keys": ["anvil"], "content": "Old iron."}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.LinkLorebook(e.ctx, e.card, lb.ID); err != nil {
		t.Fatal(err)
	}
	overrides := func(other string) json.RawMessage {
		return fmt.Appendf(nil, `{%q: {"name": "Brakka (Fake)"}, "999": {"name": %q}}`, e.key(), other)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Overrides = overrides("Other") })
	b := e.push(e.card)
	got := e.remoteData(b.RemoteID)
	book, _ := got["character_book"].(map[string]any)
	if entries, _ := book["entries"].([]any); got["name"] != "Brakka (Fake)" || len(entries) != 1 {
		t.Fatalf("app has %v", got)
	}
	if !slices.Equal(b.LastPush.LorebookRevisionIDs, []int64{lb.HeadRevisionID}) {
		t.Fatalf("lorebook revisions = %v", b.LastPush.LorebookRevisionIDs)
	}

	// Another target's override doesn't make this one ahead.
	e.update(e.card, func(ed *library.Edit) { ed.Overrides = overrides("Changed") })
	if s := e.status(e.card); s != "in_sync" {
		t.Fatalf("after another target's override changed: %s", s)
	}
	// An embedded lorebook's edit does.
	if _, err := e.lib.Lorebooks.Update(e.ctx, lb.ID, lb.HeadRevisionID, library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": []}`)}); err != nil {
		t.Fatal(err)
	}
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after a lorebook edit: %s", s)
	}
}

func TestLinkingALorebookMakesTheCardAhead(t *testing.T) {
	e := setup(t, nil)
	if b := e.push(e.card); b.Status != "in_sync" {
		t.Fatalf("first push: %+v", b)
	}
	lb, _, err := e.lib.Lorebooks.Create(e.ctx, "forge-lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": [{"keys": ["anvil"], "content": "Old iron."}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.LinkLorebook(e.ctx, e.card, lb.ID); err != nil {
		t.Fatal(err)
	}
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after linking a lorebook: %s", s)
	}
	b := e.push(e.card)
	if b.Status != "in_sync" || !slices.Equal(b.LastPush.LorebookRevisionIDs, []int64{lb.HeadRevisionID}) {
		t.Fatalf("second push: %+v", b)
	}
}

func TestPushOnlyTarget(t *testing.T) {
	e := setup(t, map[string]any{"pull": false, "lorebooks": "none"})
	lb, _, err := e.lib.Lorebooks.Create(e.ctx, "forge-lore", "upload", library.Edit{Source: "ui",
		Data: json.RawMessage(`{"name": "Forge lore", "entries": []}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.lib.LinkLorebook(e.ctx, e.card, lb.ID); err != nil {
		t.Fatal(err)
	}
	b := e.push(e.card)
	if b.Status != "cant_check" || b.RemoteState != "unknown" || b.CheckedAt != nil || b.LastPush.LorebookRevisionIDs != nil {
		t.Fatalf("push: %+v", b)
	}
	if _, ok := e.remoteData(b.RemoteID)["character_book"]; ok {
		t.Fatal("a target without lorebooks got one")
	}
	if b = e.check(b); b.Status != "cant_check" {
		t.Fatalf("check: %+v", b)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after an edit: %s", s)
	}
}

func TestCreateOnlyTarget(t *testing.T) {
	e := setup(t, map[string]any{"update": false})
	e.push(e.card)
	if _, err := e.s.Push(e.ctx, e.inst, e.card, false); !errors.Is(err, ErrNewCopy) {
		t.Fatalf("second push: %v, want ErrNewCopy", err)
	}
	p, err := e.s.Preview(e.ctx, e.inst, e.card)
	if err != nil || !p.NewCopy {
		t.Fatalf("preview = %+v, %v", p, err)
	}
	b, err := e.s.Push(e.ctx, e.inst, e.card, true)
	if err != nil || b.RemoteID != "r2" {
		t.Fatalf("push with new copy: %+v, %v", b, err)
	}
	if pushes, _ := e.s.Pushes(e.ctx, b.ID); len(pushes) != 2 || pushes[1].RemoteID != "r1" {
		t.Fatalf("pushes = %+v", pushes)
	}
}

func TestPushRecreatesMissingCopy(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	gone := func() {
		if err := os.Remove(filepath.Join(e.dir, b.RemoteID+".json")); err != nil {
			t.Fatal(err)
		}
	}
	// Unchecked: the update fails, so the push makes a new copy.
	gone()
	if b = e.push(e.card); b.Status != "in_sync" {
		t.Fatalf("push after the app lost it: %+v", b)
	}
	e.remote(b.RemoteID)
	// Checked: the binding shows missing, and a push makes a new copy.
	gone()
	if b = e.check(b); b.Status != "missing" {
		t.Fatalf("check: %+v", b)
	}
	if b = e.push(e.card); b.Status != "in_sync" {
		t.Fatalf("push after missing: %+v", b)
	}
	e.remote(b.RemoteID)
}

func TestPushFailures(t *testing.T) {
	for mode, retryable := range map[string]bool{"retryable": true, "fatal": false} {
		t.Run(mode, func(t *testing.T) {
			e := setup(t, map[string]any{"fail_put": mode})
			_, err := e.s.Push(e.ctx, e.inst, e.card, false)
			if err == nil || plugins.Retryable(err) != retryable {
				t.Fatalf("push: %v, want retryable %v", err, retryable)
			}
			if list, _ := e.s.Bindings(e.ctx, e.card); len(list) != 0 {
				t.Fatalf("a failed push left bindings: %+v", list)
			}
		})
	}
}

func TestPreview(t *testing.T) {
	e := setup(t, nil)
	p, err := e.s.Preview(e.ctx, e.inst, e.card)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(p.DroppedFields, []string{"scenario"}) || p.NewCopy || !strings.Contains(string(p.Card), `"Brakka"`) {
		t.Fatalf("preview = %+v", p)
	}
	if _, err := e.s.Preview(e.ctx, e.inst, 999); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("missing card: %v", err)
	}
}

// A push holds one connection while it calls the plugin, so pushes must not
// need a second one: with a one-connection pool they would deadlock.
func TestConcurrentPushesNeedOneConnection(t *testing.T) {
	e := setup(t, nil)
	cfg := e.pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(e.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s := New(pool, library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()}), e.reg, e.s.plugins, quiet)
	cards := []int64{e.card, e.newCard("Carrow"), e.newCard("Dunmore")}
	ctx, cancel := context.WithTimeout(e.ctx, 20*time.Second)
	defer cancel()
	errs := make(chan error, len(cards))
	for _, c := range cards {
		go func() {
			_, err := s.Push(ctx, e.inst, c, false)
			errs <- err
		}()
	}
	for range cards {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
}

// A push killed after the app made the card records nothing, so its job runs
// again; the put key stops that making a second copy.
func TestPushRerunAfterCreateReusesCopy(t *testing.T) {
	e := setup(t, map[string]any{"fail_put": "crash"})
	if _, err := e.s.push(e.ctx, e.inst, e.card, false, "job-1", nil); !errors.Is(err, ErrUncertain) {
		t.Fatalf("first run: %v, want ErrUncertain", err)
	}
	if err := e.reg.UpdateInstance(e.ctx, e.inst, plugins.InstanceEdit{Config: map[string]any{"dir": e.dir}}); err != nil {
		t.Fatal(err)
	}
	b, err := e.s.push(e.ctx, e.inst, e.card, false, "job-1", nil)
	if err != nil || b.RemoteID != "r1" {
		t.Fatalf("re-run: %+v, %v; want remote r1", b, err)
	}
	if files, _ := os.ReadDir(e.dir); len(files) != 1 {
		t.Fatalf("the app has %d cards, want 1", len(files))
	}
}
