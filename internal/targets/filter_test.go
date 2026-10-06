package targets

import (
	"slices"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestFilterTargetState(t *testing.T) {
	e := setup(t, nil)
	edited, unpushed := e.newCard("Carrow"), e.newCard("Dunmore")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	search := func(f library.Filter) []int64 {
		t.Helper()
		ids, err := e.lib.SearchIDs(e.ctx, library.Query{Filter: &f})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	state := func(is string) library.Filter {
		return library.Filter{TargetState: &library.TargetState{Instance: e.inst, Is: is}}
	}
	if got := search(state("ahead")); !slices.Equal(got, []int64{edited}) {
		t.Fatalf("ahead = %v", got)
	}
	if got := search(state("in_sync")); !slices.Equal(got, []int64{e.card}) {
		t.Fatalf("in_sync = %v", got)
	}
	other := library.Filter{TargetState: &library.TargetState{Instance: e.inst + 1000, Is: "in_sync"}}
	if got := search(other); len(got) != 0 {
		t.Fatalf("in_sync on another target = %v", got)
	}
	notInSync := state("in_sync")
	if got := search(library.Filter{Not: &notInSync}); !slices.Equal(got, []int64{edited, unpushed}) {
		t.Fatalf("not in_sync = %v", got)
	}
}

func TestSearchTargets(t *testing.T) {
	e := setup(t, nil)
	edited := e.newCard("Carrow")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	got, err := e.lib.Search(e.ctx, library.Query{Include: []string{"targets"}, Facets: []string{"targets"}})
	if err != nil {
		t.Fatal(err)
	}
	states := map[int64][]library.TargetState{}
	for _, h := range got.Hits {
		states[h.ID] = h.Targets
	}
	if !slices.Equal(states[e.card], []library.TargetState{{Instance: e.inst, Is: "in_sync"}}) ||
		!slices.Equal(states[edited], []library.TargetState{{Instance: e.inst, Is: "ahead"}}) {
		t.Fatalf("targets = %+v", states)
	}
	want := []library.TargetCount{{Instance: e.inst, Is: "ahead", Cards: 1}, {Instance: e.inst, Is: "in_sync", Cards: 1}}
	if !slices.Equal(got.Facets.Targets, want) {
		t.Fatalf("targets facet = %+v", got.Facets.Targets)
	}
}
