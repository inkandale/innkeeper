package library

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"
)

func TestEnrichmentCurrentAndStale(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, first := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	add := func(inst, rev int64, value string) {
		t.Helper()
		err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: card, RevisionID: rev, Key: "quality", Value: json.RawMessage(value),
			Meta: json.RawMessage(`{"model": "m"}`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	current := func(history bool) []Enrichment {
		t.Helper()
		list, err := lib.Enrichments(ctx, card, history)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}

	add(1, first, "60")
	if got := current(false); len(got) != 1 || string(got[0].Value) != "60" || !got[0].Current || got[0].Stale ||
		string(got[0].Meta) != `{"model": "m"}` {
		t.Fatalf("after first value: %+v", got)
	}

	second, err := lib.Cards.Update(ctx, card, first, edit(`{"name": "Brakka", "description": "Bigger."}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := current(false); len(got) != 1 || !got[0].Stale {
		t.Fatalf("after a new revision: %+v", got)
	}

	add(1, second.ID, "80")
	add(2, first, "10")
	got := current(false)
	if len(got) != 2 || got[0].InstanceID != 1 || string(got[0].Value) != "80" || got[0].Stale ||
		got[1].InstanceID != 2 || !got[1].Stale {
		t.Fatalf("current values: %+v", got)
	}
	hist := current(true)
	if len(hist) != 3 || string(hist[1].Value) != "60" || hist[1].Current || !hist[1].Stale {
		t.Fatalf("history: %+v", hist)
	}

	other, _ := newCard(t, lib, "carrow", `{"name": "Carrow"}`)
	if err := lib.AddEnrichments(ctx, 1, []NewEnrichment{{CardID: other, RevisionID: first, Key: "quality", Value: json.RawMessage("1")}}); err != nil {
		t.Fatal(err)
	}
	if list, _ := lib.Enrichments(ctx, other, true); len(list) != 0 {
		t.Fatalf("a value on another card's revision was stored: %+v", list)
	}
}

func TestEnrichmentOnAnOlderRevisionDoesNotReplaceANewerOne(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	card, first := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	second, err := lib.Cards.Update(ctx, card, first, edit(`{"name": "Brakka", "description": "Bigger."}`))
	if err != nil {
		t.Fatal(err)
	}
	add := func(rev int64, value string) {
		t.Helper()
		if err := lib.AddEnrichments(ctx, 1, []NewEnrichment{{CardID: card, RevisionID: rev, Key: "quality", Value: json.RawMessage(value)}}); err != nil {
			t.Fatal(err)
		}
	}
	// A slow run that read the first revision finishes after a run on the head.
	add(second.ID, "80")
	add(first, "40")

	list, err := lib.Enrichments(ctx, card, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || string(list[0].Value) != "80" || !list[0].Current || list[0].Stale || list[1].Current {
		t.Fatalf("values = %+v", list)
	}
	res, err := lib.Search(ctx, Card, Query{Scorers: []Scorer{{1, "quality"}}, Include: []string{"score"}, Filter: &Filter{Enrichment: ptr("quality"), Gte: ptr(70.0)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || string(res.Hits[0].Score.Value) != "80" {
		t.Fatalf("search = %+v", res.Hits)
	}
}

func TestChangedUnenrichedListsOnlyNewUnscoredUnfailedCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	old, _ := newCard(t, lib, "old", `{"name": "Old"}`)
	since := time.Now()
	time.Sleep(10 * time.Millisecond)
	fresh, _ := newCard(t, lib, "fresh", `{"name": "Fresh"}`)
	scored, scoredRev := newCard(t, lib, "scored", `{"name": "Scored"}`)
	failed, failedRev := newCard(t, lib, "failed", `{"name": "Failed"}`)
	archived, _ := newCard(t, lib, "archived", `{"name": "Archived"}`)
	if err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: scored, RevisionID: scoredRev, Key: "k", Value: json.RawMessage(`"v"`)}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.RecordEnrichFailures(ctx, inst, []RevisionRef{{CardID: failed, RevisionID: failedRev}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetArchived(ctx, archived, true); err != nil {
		t.Fatal(err)
	}

	got, err := lib.ChangedUnenriched(ctx, inst, since, 10)
	if err != nil || !slices.Equal(got, []int64{fresh}) {
		t.Fatalf("changed = %v, %v", got, err)
	}

	// Restoring an old card counts as a change.
	if err := lib.Cards.SetArchived(ctx, old, true); err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetArchived(ctx, old, false); err != nil {
		t.Fatal(err)
	}
	if got, _ = lib.ChangedUnenriched(ctx, inst, since, 10); !slices.Equal(got, []int64{old, fresh}) {
		t.Fatalf("after restore = %v", got)
	}
}
