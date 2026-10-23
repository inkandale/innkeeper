package api

import (
	"fmt"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestSimilarRouteFallsBackToTags(t *testing.T) {
	e := newEnv(t)
	me := e.createCard(`{"name": "Brakka", "tags": ["dwarf", "smith"]}`)
	other := e.createCard(`{"name": "Dorna", "tags": ["dwarf", "smith"]}`)
	var got library.SimilarPage
	e.call("GET", fmt.Sprintf("/cards/%d/similar?scope=all", me.ID), nil, 200, &got)
	if got.Basis != "tags" || len(got.Items) != 1 || got.Items[0].CardID != other.ID || got.Items[0].Name != "Dorna" {
		t.Fatalf("similar = %+v", got)
	}
	e.call("GET", "/cards/999999/similar", nil, 404, nil)
	e.call("GET", fmt.Sprintf("/cards/%d/similar?scope=galaxy", me.ID), nil, 422, nil)
}

func TestRelatedRouteListsNonEmptyGroups(t *testing.T) {
	e := newEnv(t)
	me := e.createCard(`{"name": "Brakka", "creator": "dg"}`)
	other := e.createCard(`{"name": "Dorna", "creator": "dg"}`)
	var all struct{ Groups []library.RelatedPage }
	e.call("GET", fmt.Sprintf("/cards/%d/related", me.ID), nil, 200, &all)
	if len(all.Groups) != 1 || all.Groups[0].Group != "creator" || all.Groups[0].Items[0].CardID != other.ID {
		t.Fatalf("related = %+v", all)
	}
	var one struct{ Groups []library.RelatedPage }
	e.call("GET", fmt.Sprintf("/cards/%d/related?group=lorebook", me.ID), nil, 200, &one)
	if len(one.Groups) != 1 || one.Groups[0].Group != "lorebook" || len(one.Groups[0].Items) != 0 {
		t.Fatalf("one group = %+v", one)
	}
}
