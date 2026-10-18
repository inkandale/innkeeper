package library

import (
	"context"
	"maps"
	"slices"
	"testing"
	"time"
)

const brakka = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
	"She hates elves, loves good ale, and never turns away a customer with honest coin."

func TestDuplicateGroups(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	orig, _ := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+brakka+`"}`)
	dup, _ := newCard(t, lib, "copy", `{"name": "Brakka", "description": " `+brakka+`\r\n"}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka 2", "description": "`+brakka+` She has a cat."}`)
	newCard(t, lib, "elf", `{"name": "Lia", "description": "A cheerful elven bard wanders the coast singing songs of the sea."}`)
	owned, _ := newCard(t, lib, "owned", `{"name": "Brakka", "description": "`+brakka+`"}`)
	if err := lib.Cards.SetArchived(ctx, owned, true); err != nil {
		t.Fatal(err)
	}
	other := newLibraryRow(t, lib, "Other")
	if _, _, err := lib.Cards.Create(ctx, other, "elsewhere", "upload", edit(`{"name": "Brakka", "description": "`+brakka+`"}`)); err != nil {
		t.Fatal(err)
	}

	exact, err := lib.DuplicateGroups(ctx, DefaultLibrary, -1)
	if err != nil {
		t.Fatal(err)
	}
	// The owner's archived copy and the other library's copy are in no group.
	if len(exact) != 1 || !slices.Equal(exact[0], []int64{orig, dup}) {
		t.Fatalf("exact groups = %v", exact)
	}
	near, err := lib.DuplicateGroups(ctx, DefaultLibrary, NearDistance)
	if err != nil {
		t.Fatal(err)
	}
	if len(near) != 1 || !slices.Equal(near[0], []int64{orig, dup, edited}) {
		t.Fatalf("near groups = %v", near)
	}
}

func TestSetHiddenLeavesTheOwnersArchivedCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	owned, _ := newCard(t, lib, "owned", `{"name": "Owned"}`)
	if err := lib.Cards.SetArchived(ctx, owned, true); err != nil {
		t.Fatal(err)
	}
	reason := func(id int64) (bool, string) {
		t.Helper()
		e, err := lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return e.ArchivedAt != nil, e.ArchivedReason
	}

	hidden, unhidden, err := lib.SetHidden(ctx, DefaultLibrary, map[int64]string{a: ReasonLowScore, owned: ReasonDuplicate})
	if err != nil || hidden != 1 || unhidden != 0 {
		t.Fatalf("first run: %d hidden, %d unhidden, %v", hidden, unhidden, err)
	}
	if archived, r := reason(a); !archived || r != ReasonLowScore {
		t.Fatalf("a = %v %q", archived, r)
	}
	if archived, r := reason(owned); !archived || r != "" {
		t.Fatalf("the owner's card = %v %q", archived, r)
	}

	// A new run un-hides what now passes and hides what doesn't.
	hidden, unhidden, err = lib.SetHidden(ctx, DefaultLibrary, map[int64]string{b: ReasonDuplicate})
	if err != nil || hidden != 1 || unhidden != 1 {
		t.Fatalf("second run: %d hidden, %d unhidden, %v", hidden, unhidden, err)
	}
	if archived, _ := reason(a); archived {
		t.Fatal("a is still hidden")
	}

	// Archiving by hand makes the card the owner's.
	if err := lib.Cards.SetArchived(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.SetHidden(ctx, DefaultLibrary, nil); err != nil {
		t.Fatal(err)
	}
	if archived, r := reason(b); !archived || r != "" {
		t.Fatalf("b after the owner archived it = %v %q", archived, r)
	}
}

func TestPlanHide(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	score := func(f float64) *float64 { return &f }
	cands := []HideCandidate{
		{ID: 1, CreatedAt: day(1), Score: score(40)},
		{ID: 2, CreatedAt: day(2), Score: score(90)},
		{ID: 3, CreatedAt: day(3), Score: score(70)},
		{ID: 4, CreatedAt: day(4), Score: score(10)},
		{ID: 5, CreatedAt: day(5)},
	}
	groups := [][]int64{{1, 2, 3}, {4, 5}}
	cases := []struct {
		keep      string
		threshold *float64
		want      map[int64]string
	}{
		{"best", nil, map[int64]string{1: ReasonDuplicate, 3: ReasonDuplicate, 5: ReasonDuplicate}},
		{"oldest", nil, map[int64]string{2: ReasonDuplicate, 3: ReasonDuplicate, 5: ReasonDuplicate}},
		{"newest", nil, map[int64]string{1: ReasonDuplicate, 2: ReasonDuplicate, 4: ReasonDuplicate}},
		// A low score wins over duplicate; a card without a score isn't low.
		{"best", score(50), map[int64]string{1: ReasonLowScore, 3: ReasonDuplicate, 4: ReasonLowScore, 5: ReasonDuplicate}},
	}
	for _, c := range cases {
		if got := PlanHide(cands, groups, c.threshold, c.keep); !maps.Equal(got, c.want) {
			t.Errorf("keep %s, threshold %v: %v, want %v", c.keep, c.threshold, got, c.want)
		}
	}
}

func TestHideCandidatesReadTheScoreInstance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	scored, rev := newCard(t, lib, "scored", `{"name": "Scored"}`)
	unscored, _ := newCard(t, lib, "unscored", `{"name": "Unscored"}`)
	owned, _ := newCard(t, lib, "owned", `{"name": "Owned"}`)
	if err := lib.Cards.SetArchived(ctx, owned, true); err != nil {
		t.Fatal(err)
	}
	var inst int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'fake') RETURNING id`).Scan(&inst); err != nil {
		t.Fatal(err)
	}
	if err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: scored, RevisionID: rev, Key: "quality", Value: []byte(`72`)},
		{CardID: scored, RevisionID: rev, Key: "voice", Value: []byte(`5`)}}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.HideCandidates(ctx, DefaultLibrary, inst, "quality")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != scored || got[0].Score == nil || *got[0].Score != 72 || got[1].ID != unscored || got[1].Score != nil {
		t.Fatalf("candidates %+v", got)
	}
}
