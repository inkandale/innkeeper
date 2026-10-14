package targets

import (
	"fmt"
	"slices"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func (e *env) preview(card int64) Preview {
	e.t.Helper()
	p, err := e.s.Preview(e.ctx, library.Card, e.inst, card)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

func TestPreviewOfANewCardReplacesNothing(t *testing.T) {
	e := setup(t, nil)
	if p := e.preview(e.card); p.Replaces || len(p.Changes) != 0 || p.Changes == nil {
		t.Fatalf("preview = %+v", p)
	}
}

func TestPreviewListsWhatAPushChanges(t *testing.T) {
	e := setup(t, nil)
	e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	p := e.preview(e.card)
	if !p.Replaces || p.Stale || len(p.Changes) != 1 || p.Changes[0].Field != "first_mes" ||
		string(p.Changes[0].Before) != `"Hail."` || string(p.Changes[0].After) != `"Well met."` {
		t.Fatalf("preview = %+v", p)
	}
}

// An edit made in the app shows as a change the push would overwrite.
func TestPreviewShowsAppEditsItOverwrites(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote(b.RemoteID, "description", "A smith, retired.")
	p := e.preview(e.card)
	if len(p.Changes) != 1 || string(p.Changes[0].Before) != `"A smith, retired."` || string(p.Changes[0].After) != `"A dwarf smith."` {
		t.Fatalf("preview = %+v", p)
	}
}

// A target that can't pull is compared against what the last push sent.
func TestPreviewOfPushOnlyTarget(t *testing.T) {
	e := setup(t, map[string]any{"pull": false})
	e.push(e.card)
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grumpy smith.") })
	if p := e.preview(e.card); !p.Replaces || len(p.Changes) != 1 || p.Changes[0].Field != "description" {
		t.Fatalf("preview = %+v", p)
	}
}

func TestPreviewListsLorebooksToCreate(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	a, b := e.newLorebook("Forge lore"), e.newLorebook("Mine lore")
	e.link(e.card, a)
	e.pushLorebook(a)
	e.link(e.card, b)
	if p := e.preview(e.card); !slices.Equal(p.CreateLorebooks, []int64{b}) {
		t.Fatalf("create_lorebooks = %v, want [%d]", p.CreateLorebooks, b)
	}
}

func TestPreviewLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.pushLorebook(lb)
	e.updateLorebook(lb, func(ed *library.Edit) { ed.Data = setField(ed.Data, "entries", newEntries) })
	p, err := e.s.Preview(e.ctx, library.Lorebook, e.inst, lb)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Replaces || p.Card != nil || p.Lorebook == nil || len(p.Changes) != 1 || p.Changes[0].Field != "entries" {
		t.Fatalf("preview = %+v", p)
	}
	if fmt.Sprint(p.DroppedFields) != "[scan_depth]" {
		t.Fatalf("dropped = %v", p.DroppedFields)
	}
}
