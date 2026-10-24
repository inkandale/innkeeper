package library

import (
	"context"
	"maps"
	"math/rand/v2"
	"slices"
	"testing"
	"time"
)

const brakka = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
	"She hates elves, loves good ale, and never turns away a customer with honest coin."

func TestDuplicatePairs(t *testing.T) {
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

	exact, err := lib.DuplicatePairs(ctx, DefaultLibrary, -1)
	if err != nil {
		t.Fatal(err)
	}
	// The owner's archived copy and the other library's copy are in no pair.
	if !slices.Equal(exact, []DuplicatePair{{orig, dup, true}}) {
		t.Fatalf("exact pairs = %v", exact)
	}
	near, err := lib.DuplicatePairs(ctx, DefaultLibrary, NearDistance)
	if err != nil {
		t.Fatal(err)
	}
	// orig and dup match by content and by text, and are listed once, as exact.
	if !slices.Equal(near, []DuplicatePair{{orig, dup, true}, {orig, edited, false}, {dup, edited, false}}) {
		t.Fatalf("near pairs = %v", near)
	}
	if groups := GroupPairs(near); len(groups) != 1 || !slices.Equal(groups[0], []int64{orig, dup, edited}) {
		t.Fatalf("groups = %v", groups)
	}
}

func TestHashBands16ShareABandWithin3Bits(t *testing.T) {
	lib := newLibrary(t)
	rng := rand.New(rand.NewPCG(1, 2))
	for range 2000 {
		h := int64(rng.Uint64())
		near := h
		for range 1 + rng.IntN(band16Distance) {
			near ^= 1 << rng.IntN(64)
		}
		var share bool
		if err := lib.pool.QueryRow(context.Background(), `SELECT hash_bands16($1) && hash_bands16($2)`, h, near).Scan(&share); err != nil {
			t.Fatal(err)
		}
		if !share {
			t.Fatalf("%016x and %016x share no band", uint64(h), uint64(near))
		}
	}
}

func TestDuplicatePairsWithin3BitsFindsNearText(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	orig, _ := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+brakka+`"}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka", "description": "`+brakka+` Gold."}`)
	// 5 bits from orig: past the asked distance.
	newCard(t, lib, "far", `{"name": "Brakka", "description": "`+brakka+` Ale."}`)
	near, err := lib.DuplicatePairs(ctx, DefaultLibrary, band16Distance)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(near, []DuplicatePair{{orig, edited, false}}) {
		t.Fatalf("near pairs = %v", near)
	}
}

func TestDuplicatePairsMatchANameWithASimilarAvatar(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const art = 0x0f0f_0f0f_0f0f_0f0f
	kichiro := avatarCard(t, lib, 0, art, `{"name": "Kichiro", "description": "A shut-in younger brother who codes."}`)
	repost := avatarCard(t, lib, 1, flip(art, 3, 40), `{"name": " kichiro ", "description": "<overview>Kichiro, 18, studies online.</overview>"}`)
	avatarCard(t, lib, 2, art, `{"name": "Mira", "description": "A quiet librarian."}`)
	avatarCard(t, lib, 3, ^art, `{"name": "Kichiro", "description": "A fox spirit guarding a shrine."}`)

	near, err := lib.DuplicatePairs(ctx, DefaultLibrary, NearDistance)
	if err != nil {
		t.Fatal(err)
	}
	// Reused art under another name, and the name on other art, are no pair.
	if !slices.Equal(near, []DuplicatePair{{kichiro, repost, false}}) {
		t.Fatalf("near pairs = %v", near)
	}
	if exact, err := lib.DuplicatePairs(ctx, DefaultLibrary, -1); err != nil || len(exact) != 0 {
		t.Fatalf("exact pairs = %v, %v", exact, err)
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

	hidden, unhidden, err := lib.SetHidden(ctx, DefaultLibrary, map[int64]string{a: ReasonDuplicate, owned: ReasonDuplicate})
	if err != nil || hidden != 1 || unhidden != 0 {
		t.Fatalf("first run: %d hidden, %d unhidden, %v", hidden, unhidden, err)
	}
	if archived, r := reason(a); !archived || r != ReasonDuplicate {
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
		keep string
		want map[int64]string
	}{
		{"best", map[int64]string{1: ReasonDuplicate, 3: ReasonDuplicate, 5: ReasonDuplicate}},
		{"oldest", map[int64]string{2: ReasonDuplicate, 3: ReasonDuplicate, 5: ReasonDuplicate}},
		{"newest", map[int64]string{1: ReasonDuplicate, 2: ReasonDuplicate, 4: ReasonDuplicate}},
	}
	for _, c := range cases {
		if got := PlanHide(cands, groups, c.keep); !maps.Equal(got, c.want) {
			t.Errorf("keep %s: %v, want %v", c.keep, got, c.want)
		}
	}
}

func TestHideCandidatesReadTheHighestPriorityScore(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	both, bothRev := newCard(t, lib, "both", `{"name": "Both"}`)
	linted, lintedRev := newCard(t, lib, "linted", `{"name": "Linted"}`)
	unscored, _ := newCard(t, lib, "unscored", `{"name": "Unscored"}`)
	owned, _ := newCard(t, lib, "owned", `{"name": "Owned"}`)
	if err := lib.Cards.SetArchived(ctx, owned, true); err != nil {
		t.Fatal(err)
	}
	var eval, lint int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'eval') RETURNING id`).Scan(&eval); err != nil {
		t.Fatal(err)
	}
	if err := lib.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'lint') RETURNING id`).Scan(&lint); err != nil {
		t.Fatal(err)
	}
	if err := lib.AddEnrichments(ctx, eval, []NewEnrichment{{CardID: both, RevisionID: bothRev, Key: "quality", Value: []byte(`72`)},
		{CardID: both, RevisionID: bothRev, Key: "voice", Value: []byte(`5`)}}); err != nil {
		t.Fatal(err)
	}
	if err := lib.AddEnrichments(ctx, lint, []NewEnrichment{{CardID: both, RevisionID: bothRev, Key: "lint", Value: []byte(`99`)},
		{CardID: linted, RevisionID: lintedRev, Key: "lint", Value: []byte(`30`)}}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.HideCandidates(ctx, DefaultLibrary, []Scorer{{eval, "quality"}, {lint, "lint"}})
	if err != nil {
		t.Fatal(err)
	}
	score := func(c HideCandidate) any {
		if c.Score == nil {
			return nil
		}
		return *c.Score
	}
	if len(got) != 3 || got[0].ID != both || score(got[0]) != 72.0 || got[1].ID != linted || score(got[1]) != 30.0 ||
		got[2].ID != unscored || got[2].Score != nil {
		t.Fatalf("candidates %+v", got)
	}
}

func TestGroupPairsJoinsChains(t *testing.T) {
	got := GroupPairs([]DuplicatePair{{5, 9, false}, {1, 5, false}, {2, 3, true}})
	if len(got) != 2 || !slices.Equal(got[0], []int64{1, 5, 9}) || !slices.Equal(got[1], []int64{2, 3}) {
		t.Fatalf("groups = %v", got)
	}
}
