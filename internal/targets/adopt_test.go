package targets

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

// changed pushes the card, then changes first_mes and clears description in
// the app, and checks.
func changed(t *testing.T) (*env, Binding) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote("r1", "first_mes", "Well met.")
	e.editRemote("r1", "description", nil)
	return e, e.check(b)
}

func TestAdoptIntoBase(t *testing.T) {
	e, b := changed(t)
	b, err := e.s.Adopt(e.ctx, b.ID, IntoBase, e.head(e.card).ID)
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "ahead" || b.RemoteState != "unchanged" || b.Changes != nil {
		t.Fatalf("binding = %+v", b)
	}
	rev := e.head(e.card)
	var data map[string]any
	_ = json.Unmarshal(rev.Data, &data)
	if data["first_mes"] != "Well met." || data["description"] != nil || data["scenario"] != "A forge." || rev.Source != "adopt" {
		t.Fatalf("head = %s from %s", rev.Data, rev.Source)
	}
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoBase, rev.ID); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("second adopt: %v", err)
	}
}

func TestAdoptIntoOverride(t *testing.T) {
	e, b := changed(t)
	before := e.head(e.card)
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoOverride, before.ID); err != nil {
		t.Fatal(err)
	}
	rev := e.head(e.card)
	var overrides map[string]map[string]any
	_ = json.Unmarshal(rev.Overrides, &overrides)
	patch, hasDescription := overrides[e.key()]["description"]
	if string(rev.Data) != string(before.Data) || overrides[e.key()]["first_mes"] != "Well met." || !hasDescription || patch != nil {
		t.Fatalf("head data %s, overrides %s", rev.Data, rev.Overrides)
	}
	// The next push sends the override, so the app keeps its version.
	b = e.push(e.card)
	if got := e.remoteData(b.RemoteID); b.Status != "in_sync" || got["first_mes"] != "Well met." || got["description"] != nil {
		t.Fatalf("after push: %+v, app has %v", b, got)
	}
}

func TestAdoptIgnore(t *testing.T) {
	e, b := changed(t)
	b, err := e.s.Adopt(e.ctx, b.ID, IntoIgnore, 0)
	if err != nil || b.Status != "in_sync" {
		t.Fatalf("ignore: %+v, %v", b, err)
	}
	if hist, _ := e.lib.Cards.History(e.ctx, e.card); len(hist) != 1 {
		t.Fatalf("ignore added %d revisions", len(hist)-1)
	}
}

func TestAdoptStaleBase(t *testing.T) {
	e, b := changed(t)
	stale := e.head(e.card).ID
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "scenario", "A cold forge.") })
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoBase, stale); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("adopt on a stale base: %v", err)
	}
	if s := e.status(e.card); s != "diverged" {
		t.Fatalf("a failed adopt changed the status to %s", s)
	}
	if _, err := e.s.Adopt(e.ctx, b.ID, "elsewhere", stale); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("bad into: %v", err)
	}
}
