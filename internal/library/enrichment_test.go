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
		err := lib.AddEnrichments(ctx, inst, 0, []NewEnrichment{{CardID: card, RevisionID: rev, Key: "quality", Value: json.RawMessage(value),
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
	if err := lib.AddEnrichments(ctx, 1, 0, []NewEnrichment{{CardID: other, RevisionID: first, Key: "quality", Value: json.RawMessage("1")}}); err != nil {
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
		if err := lib.AddEnrichments(ctx, 1, 0, []NewEnrichment{{CardID: card, RevisionID: rev, Key: "quality", Value: json.RawMessage(value)}}); err != nil {
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
	res, err := lib.Search(ctx, Card, Query{Scorers: []Scorer{{Instance: 1, Key: "quality"}}, Browsable: browsable(Scorer{Instance: 1, Key: "quality"}), Include: []string{"score"}, Filter: &Filter{Enrichment: ptr("quality"), Gte: ptr(70.0)}})
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
	// The database's clock, so the test can't skew against it.
	var since time.Time
	if err := lib.pool.QueryRow(ctx, `SELECT now()`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	fresh, _ := newCard(t, lib, "fresh", `{"name": "Fresh"}`)
	scored, scoredRev := newCard(t, lib, "scored", `{"name": "Scored"}`)
	failed, failedRev := newCard(t, lib, "failed", `{"name": "Failed"}`)
	archived, _ := newCard(t, lib, "archived", `{"name": "Archived"}`)
	if err := lib.AddEnrichments(ctx, inst, 0, []NewEnrichment{{CardID: scored, RevisionID: scoredRev, Key: "k", Value: json.RawMessage(`"v"`)}}); err != nil {
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

func TestChangedUnenrichedEdgeCases(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	rescored, oldRev := newCard(t, lib, "rescored", `{"name": "Rescored"}`)
	if err := lib.AddEnrichments(ctx, inst, 0, []NewEnrichment{{CardID: rescored, RevisionID: oldRev, Key: "k", Value: json.RawMessage(`"v"`)}}); err != nil {
		t.Fatal(err)
	}
	var since time.Time
	if err := lib.pool.QueryRow(ctx, `SELECT now()`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	// Scored at an old revision, then edited: the new head holds no value.
	if _, err := lib.Cards.Update(ctx, rescored, oldRev, edit(`{"name": "Rescored", "description": "More."}`)); err != nil {
		t.Fatal(err)
	}
	embedded, _ := newCard(t, lib, "embedded", `{"name": "Embedded"}`)
	retry, retryRev := newCard(t, lib, "retry", `{"name": "Retry"}`)
	embedCards(t, lib, inst, map[int64][]float32{embedded: {1, 2}})
	if err := lib.RecordEnrichFailures(ctx, inst, []RevisionRef{{CardID: retry, RevisionID: retryRev}}); err != nil {
		t.Fatal(err)
	}

	got, err := lib.ChangedUnenriched(ctx, inst, since, 10)
	if err != nil || !slices.Equal(got, []int64{rescored}) {
		t.Fatalf("changed = %v, %v; want only the re-edited card (embedded has a vector, retry just failed)", got, err)
	}

	// A failure older than a day no longer suppresses the card.
	if _, err := lib.pool.Exec(ctx, `UPDATE enrich_failure SET failed_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if got, _ = lib.ChangedUnenriched(ctx, inst, since, 10); !slices.Equal(got, []int64{rescored, retry}) {
		t.Fatalf("after the failure aged = %v", got)
	}
}

func TestAVectorInstanceWantsAVectorEvenWithValues(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	var since time.Time
	if err := lib.pool.QueryRow(ctx, `SELECT now()`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	valued, valuedRev := newCard(t, lib, "valued", `{"name": "Valued"}`)
	embedded, embeddedRev := newCard(t, lib, "embedded", `{"name": "Embedded"}`)
	if err := lib.AddEnrichments(ctx, inst, 0, []NewEnrichment{{CardID: valued, RevisionID: valuedRev, Key: "k", Value: json.RawMessage(`"v"`)}}); err != nil {
		t.Fatal(err)
	}
	embedCards(t, lib, inst, map[int64][]float32{embedded: {1, 2}})

	if got, err := lib.ChangedUnenriched(ctx, inst, since, 10); err != nil || !slices.Equal(got, []int64{valued}) {
		t.Fatalf("changed = %v, %v; want the card with values and no vector", got, err)
	}
	got, err := lib.EnrichedRevisions(ctx, inst, 0, []int64{valued, embedded}, []int64{valuedRev, embeddedRev})
	if err != nil || !slices.Equal(got, []int64{embeddedRev}) {
		t.Fatalf("enriched = %v, %v; want only the embedded revision", got, err)
	}
}

func TestEnrichedRevisionsLeavesAnOlderEnrichVersionToRescore(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	old, oldRev := newCard(t, lib, "old", `{"name": "Old"}`)
	cur, curRev := newCard(t, lib, "cur", `{"name": "Cur"}`)
	if err := lib.AddEnrichments(ctx, inst, 1, []NewEnrichment{{CardID: old, RevisionID: oldRev, Key: "k", Value: json.RawMessage(`1`)}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.AddEnrichments(ctx, inst, 2, []NewEnrichment{{CardID: cur, RevisionID: curRev, Key: "k", Value: json.RawMessage(`1`)}}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.EnrichedRevisions(ctx, inst, 2, []int64{old, cur}, []int64{oldRev, curRev})
	if err != nil || !slices.Equal(got, []int64{curRev}) {
		t.Fatalf("enriched at version 2 = %v, %v; want only the revision scored at 2", got, err)
	}
	got, err = lib.EnrichedRevisions(ctx, inst, 0, []int64{old, cur}, []int64{oldRev, curRev})
	if slices.Sort(got); err != nil || !slices.Equal(got, []int64{oldRev, curRev}) {
		t.Fatalf("enriched at version 0 = %v, %v; want both", got, err)
	}
}

func TestEnrichedRevisionsLeavesAnOlderEnrichVersionsVector(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	id, rev := newCard(t, lib, "vec", `{"name": "Vec"}`)
	if err := lib.SaveVectors(ctx, inst, 1, []NewVector{{CardID: id, RevisionID: rev, Space: "s1", Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if got, err := lib.EnrichedRevisions(ctx, inst, 2, []int64{id}, []int64{rev}); err != nil || len(got) != 0 {
		t.Fatalf("enriched at version 2 = %v, %v; want none", got, err)
	}
	if err := lib.SaveVectors(ctx, inst, 2, []NewVector{{CardID: id, RevisionID: rev, Space: "s1", Vector: []float32{1, 0}}}); err != nil {
		t.Fatal(err)
	}
	if got, err := lib.EnrichedRevisions(ctx, inst, 2, []int64{id}, []int64{rev}); err != nil || !slices.Equal(got, []int64{rev}) {
		t.Fatalf("after rescoring = %v, %v", got, err)
	}
}

// Raising enrich_version on a costly plugin must not start a run over every card it scored.
func TestChangedUnenrichedIgnoresAnOlderEnrichVersion(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	var since time.Time
	if err := lib.pool.QueryRow(ctx, `SELECT now()`).Scan(&since); err != nil {
		t.Fatal(err)
	}
	scored, scoredRev := newCard(t, lib, "scored", `{"name": "Scored"}`)
	fresh, _ := newCard(t, lib, "fresh", `{"name": "Fresh"}`)
	if err := lib.AddEnrichments(ctx, inst, 0, []NewEnrichment{{CardID: scored, RevisionID: scoredRev, Key: "k", Value: json.RawMessage(`1`)}}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.ChangedUnenriched(ctx, inst, since, 10)
	if err != nil || !slices.Equal(got, []int64{fresh}) {
		t.Fatalf("changed = %v, %v; want only the card with no value", got, err)
	}
}
