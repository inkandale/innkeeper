package targets

import (
	"slices"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestFilterTargetState(t *testing.T) {
	e := setup(t, nil)
	edited, unpushed := e.newCard("Carrow"), e.newCard("Dunmore")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	search := func(f library.Filter) []int64 {
		t.Helper()
		ids, err := e.lib.SearchIDs(e.ctx, library.Card, library.Query{Filter: &f})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	match := func(instances []int64, states ...string) library.Filter {
		return library.Filter{TargetState: &library.TargetFilter{Instances: instances, States: states}}
	}
	here := []int64{e.inst}
	inSyncHere := match(here, "in_sync")
	for name, tc := range map[string]struct {
		f    library.Filter
		want []int64
	}{
		"state on a target":       {match(here, "ahead"), []int64{edited}},
		"other state on a target": {match(here, "in_sync"), []int64{e.card}},
		"state on another target": {match([]int64{e.inst + 1000}, "in_sync"), nil},
		"state on any target":     {match(nil, "ahead"), []int64{edited}},
		"any state on a target":   {match(here), []int64{e.card, edited}},
		"pushed anywhere":         {match(nil), []int64{e.card, edited}},
		"either of two states":    {match(here, "ahead", "in_sync"), []int64{e.card, edited}},
		"not in sync on a target": {library.Filter{Not: &inSyncHere}, []int64{edited, unpushed}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := search(tc.f); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSearchTargets(t *testing.T) {
	e := setup(t, nil)
	edited := e.newCard("Carrow")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	got, err := e.lib.Search(e.ctx, library.Card, library.Query{Include: []string{"targets"}, Facets: []string{"targets"}})
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

func TestSearchLeavesOutFollowedSources(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	vyr := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	if b := e.follow(vyr); b.RemoteState != remoteChanged {
		t.Fatalf("followed = %+v", b)
	}
	got, err := e.lib.Search(e.ctx, library.Card, library.Query{Include: []string{"targets"}, Facets: []string{"targets"},
		Filter: &library.Filter{IDs: []int64{vyr}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || len(got.Hits[0].Targets) != 0 || len(got.Facets.Targets) != 0 {
		t.Fatalf("hits = %+v, facets = %+v", got.Hits, got.Facets)
	}
	ids, err := e.lib.SearchIDs(e.ctx, library.Card, library.Query{Filter: &library.Filter{TargetState: &library.TargetFilter{}}})
	if err != nil || slices.Contains(ids, vyr) {
		t.Fatalf("pushed anywhere = %v, %v", ids, err)
	}
}
