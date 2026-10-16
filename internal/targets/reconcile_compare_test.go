package targets

import (
	"errors"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/merge"
)

// statuses maps each field path to its hunk statuses.
func statuses(fields []merge.Field) map[string][]merge.Status {
	out := map[string][]merge.Status{}
	for _, f := range fields {
		for _, h := range f.Hunks {
			out[f.Path] = append(out[f.Path], h.Status)
		}
	}
	return out
}

// differing counts fields with any non-same hunk, and conflict hunks, as a
// reconcile candidate does.
func differing(fields []merge.Field) (n, conflicts int) {
	for _, f := range fields {
		d := false
		for _, h := range f.Hunks {
			d = d || h.Status != merge.Same
			if h.Status == merge.Conflict {
				conflicts++
			}
		}
		if d {
			n++
		}
	}
	return n, conflicts
}

func TestCompareItem(t *testing.T) {
	e := setup(t, nil)
	bound := e.push(e.card)
	e.editRemote(bound.RemoteID, "first_mes", "Hail, traveler.")
	e.appCard("r9", map[string]any{"name": "Brakka", "description": "A dwarf smith.", "first_mes": "Go away."}, nil)

	c, err := e.s.CompareItem(e.ctx, library.Card, e.inst, bound.RemoteID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if c.Base != "accepted" || c.CardID != e.card || c.AppHash == "" || c.Avatar != nil {
		t.Errorf("bound comparison = %+v", c)
	}
	if got := statuses(c.Fields)["first_mes"]; len(got) != 1 || got[0] != merge.AutoApp {
		t.Errorf("first_mes hunks = %v", got)
	}

	c, err = e.s.CompareItem(e.ctx, library.Card, e.inst, "r9", e.card)
	if err != nil {
		t.Fatal(err)
	}
	st := statuses(c.Fields)
	if c.Base != "none" || len(st["description"]) != 1 || st["description"][0] != merge.Same ||
		len(st["first_mes"]) == 0 || st["first_mes"][0] == merge.Same {
		t.Errorf("unbound comparison = %+v, %v", c, st)
	}
	// The job's counts for the same pair come from the same comparison.
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range snap.Items {
		if it.RemoteID != "r9" {
			continue
		}
		n, conflicts := differing(c.Fields)
		cand := it.Candidates[0]
		if it.AppHash != c.AppHash || cand.CardID != e.card || cand.DifferingFields != n || cand.Conflicts != conflicts {
			t.Errorf("job item = %+v, compare = %d differing, %d conflicts", it, n, conflicts)
		}
	}

	// A bound item set against another card has no ancestor.
	other := e.newCard("Iko")
	if c, err := e.s.CompareItem(e.ctx, library.Card, e.inst, bound.RemoteID, other); err != nil || c.Base != "none" {
		t.Errorf("bound item vs other card = %+v, %v", c, err)
	}
	if _, err := e.s.CompareItem(e.ctx, library.Card, e.inst, "r9", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbound with card 0: err = %v", err)
	}
}

func TestCompareItemAvatar(t *testing.T) {
	e := setup(t, nil)
	stripes := patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) })
	rings := patternPNG(t, func(x, y int) uint8 { return uint8((x-32)*(x-32)+(y-32)*(y-32)) / 128 * 255 })
	e.setAvatar(e.card, stripes)
	data := map[string]any{"name": "Brakka", "description": "A dwarf smith."}
	e.appCard("same", data, map[string]any{"avatar": stripes})
	e.appCard("other", data, map[string]any{"avatar": rings})

	c, err := e.s.CompareItem(e.ctx, library.Card, e.inst, "same", e.card)
	if err != nil || c.Avatar != nil {
		t.Errorf("same avatar = %+v, %v", c.Avatar, err)
	}
	c, err = e.s.CompareItem(e.ctx, library.Card, e.inst, "other", e.card)
	if err != nil || c.Avatar == nil || c.Avatar.Library == "" || c.Avatar.App == "" ||
		c.Avatar.PhashDistance == nil || *c.Avatar.PhashDistance == 0 {
		t.Errorf("other avatar = %+v, %v", c.Avatar, err)
	}
}

func TestCompareLorebookItem(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	b := e.pushLorebook(lb)
	e.writeRemote("l7", map[string]any{"lorebook": map[string]any{"name": "Forge lore",
		"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge is cold."}}}})

	c, err := e.s.CompareItem(e.ctx, library.Lorebook, e.inst, b.RemoteID, 0)
	if err != nil || c.Base != "accepted" || c.CardID != lb {
		t.Fatalf("bound lorebook = %+v, %v", c, err)
	}
	if n, _ := differing(c.Fields); n != 0 {
		t.Errorf("in-sync lorebook differs: %v", statuses(c.Fields))
	}
	c, err = e.s.CompareItem(e.ctx, library.Lorebook, e.inst, "l7", lb)
	if err != nil || c.Base != "none" {
		t.Fatalf("unbound lorebook = %+v, %v", c, err)
	}
	// The app dropped the description and rewrote the entry; the name matches.
	if n, _ := differing(c.Fields); n != 2 || statuses(c.Fields)["name"][0] != merge.Same {
		t.Errorf("lorebook hunks = %v", statuses(c.Fields))
	}
}

func TestCompareItemAvatarFailure(t *testing.T) {
	e := setup(t, nil)
	e.appCard("pic", map[string]any{"name": "Brakka", "description": "A dwarf smith."},
		map[string]any{"avatar": patternPNG(t, func(x, y int) uint8 { return uint8(x + y) })})
	e.breakBlobStore()
	// Resolve trusts this comparison, so an avatar it couldn't read is an error.
	if _, err := e.s.CompareItem(e.ctx, library.Card, e.inst, "pic", e.card); err == nil {
		t.Error("compare ignored a failed avatar ingest")
	}
}
