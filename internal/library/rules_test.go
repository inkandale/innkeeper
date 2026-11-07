package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func newSavedSearch(t *testing.T, lib *Library, name string, f Filter) int64 {
	t.Helper()
	s, err := lib.CreateSavedSearch(context.Background(), name, f, "")
	if err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func newRule(t *testing.T, lib *Library, spec RuleSpec) Rule {
	t.Helper()
	r, err := lib.CreateRule(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func enableRule(t *testing.T, lib *Library, r Rule) Rule {
	t.Helper()
	spec := r.RuleSpec
	spec.Enabled = true
	_, after, err := lib.UpdateRule(context.Background(), r.ID, spec)
	if err != nil {
		t.Fatal(err)
	}
	return after
}

func TestCollectRuleCoversOnlyItsCollectionsLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	other := newLibraryRow(t, lib, "Other")
	c, err := lib.CreateCollection(ctx, DefaultLibrary, "Elves", "")
	if err != nil {
		t.Fatal(err)
	}
	spec := RuleSpec{Name: "Collect elves", SavedSearchID: newSavedSearch(t, lib, "Elves", Filter{Tag: new("elf")}),
		Action: RuleCollect, CollectionID: &c.ID, AllLibraries: true, Enabled: true}
	if _, err := lib.CreateRule(ctx, spec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("all libraries: %v", err)
	}
	spec.AllLibraries, spec.Libraries = false, []int64{other}
	if _, err := lib.CreateRule(ctx, spec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("another library: %v", err)
	}
	spec.Libraries = []int64{DefaultLibrary}
	r := newRule(t, lib, spec)
	if r.Enabled || r.SavedSearch != "Elves" || r.Filter.Tag == nil || !slices.Equal(r.Libraries, []int64{DefaultLibrary}) {
		t.Fatalf("created %+v", r)
	}
	spec.Action, spec.CollectionID = RuleArchive, nil
	if _, _, err := lib.UpdateRule(ctx, r.ID, spec); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a new action: %v", err)
	}
}

func TestRulesInListsCoveringRulesAndOnesThatCollectedCardsThere(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	other := newLibraryRow(t, lib, "Other")
	search := newSavedSearch(t, lib, "Junk", Filter{Name: new("junk")})
	all := enableRule(t, lib, newRule(t, lib, RuleSpec{Name: "All", SavedSearchID: search, Action: RuleArchive, AllLibraries: true}))
	enableRule(t, lib, newRule(t, lib, RuleSpec{Name: "Elsewhere", SavedSearchID: search, Action: RuleArchive, Libraries: []int64{other}}))
	c, err := lib.CreateCollection(ctx, DefaultLibrary, "Junk", "")
	if err != nil {
		t.Fatal(err)
	}
	off := newRule(t, lib, RuleSpec{Name: "Off", SavedSearchID: search, Action: RuleCollect, CollectionID: &c.ID, Libraries: []int64{DefaultLibrary}})
	card, _ := newCard(t, lib, "junk", `{"name": "Junk"}`)
	// A membership off added while it was on.
	if _, err := lib.pool.Exec(ctx, `INSERT INTO collection_card (collection_id, card_id, rule_id) VALUES ($1, $2, $3)`, c.ID, card, off.ID); err != nil {
		t.Fatal(err)
	}

	in, err := lib.RulesIn(ctx, DefaultLibrary)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, r := range in {
		ids = append(ids, r.ID)
	}
	if !slices.Equal(ids, []int64{all.ID, off.ID}) {
		t.Fatalf("rules in library 1 = %v", ids)
	}
	third := newLibraryRow(t, lib, "Third")
	for lib1, want := range map[int64]bool{other: true, third: true} {
		if work, err := lib.HasRuleWork(ctx, lib1); err != nil || work != want {
			t.Errorf("library %d: work = %v, %v", lib1, work, err)
		}
	}
}

func TestRuleMatchesIncludesArchivedCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "Junk a"}`)
	b, _ := newCard(t, lib, "b", `{"name": "Junk b"}`)
	c, _ := newCard(t, lib, "c", `{"name": "Keeper"}`)
	if err := lib.Cards.SetArchived(ctx, b, true); err != nil {
		t.Fatal(err)
	}
	junk := Filter{Name: new("junk")}
	if got, err := lib.RuleMatches(ctx, DefaultLibrary, junk, nil, nil); err != nil || !slices.Equal(got, []int64{a, b}) {
		t.Fatalf("matches = %v, %v", got, err)
	}
	if got, err := lib.RuleMatches(ctx, DefaultLibrary, junk, nil, []int64{b, c}); err != nil || !slices.Equal(got, []int64{b}) {
		t.Fatalf("matches among b, c = %v, %v", got, err)
	}
}

func TestArchiveRuleOnATagFilterArchivesWhatItMatches(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	tagged, _ := newCard(t, lib, "tagged", `{"name": "A", "tags": ["NSFW"]}`)
	newCard(t, lib, "plain", `{"name": "B", "tags": ["sfw"]}`)
	r := newRule(t, lib, RuleSpec{Name: "Hide nsfw", SavedSearchID: newSavedSearch(t, lib, "NSFW", Filter{Tag: new("nsfw")}),
		Action: RuleArchive, AllLibraries: true})
	matches, err := lib.RuleMatches(ctx, DefaultLibrary, r.Filter, nil, nil)
	if err != nil || !slices.Equal(matches, []int64{tagged}) {
		t.Fatalf("matches = %v, %v", matches, err)
	}
	if add, remove, err := lib.RuleChanges(ctx, r, DefaultLibrary, matches, nil); err != nil || !slices.Equal(add, []int64{tagged}) || len(remove) != 0 {
		t.Fatalf("changes: add %v, remove %v, %v", add, remove, err)
	}
}

func TestCollectRuleLeavesTheOwnersMemberships(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	c, err := lib.CreateCollection(ctx, DefaultLibrary, "Drafts", "")
	if err != nil {
		t.Fatal(err)
	}
	owned, _ := newCard(t, lib, "owned", `{"name": "Draft owned"}`)
	match, _ := newCard(t, lib, "match", `{"name": "Draft new"}`)
	if err := lib.AddToCollection(ctx, c.ID, owned); err != nil {
		t.Fatal(err)
	}
	r := newRule(t, lib, RuleSpec{Name: "Collect drafts", SavedSearchID: newSavedSearch(t, lib, "Drafts", Filter{Name: new("draft")}),
		Action: RuleCollect, CollectionID: &c.ID, Libraries: []int64{DefaultLibrary}})

	add, _, err := lib.RuleChanges(ctx, r, DefaultLibrary, []int64{owned, match}, nil)
	if err != nil || !slices.Equal(add, []int64{match}) {
		t.Fatalf("add = %v, %v", add, err)
	}
	if err := lib.CollectByRule(ctx, r, add, false); err != nil {
		t.Fatal(err)
	}
	if _, remove, err := lib.RuleChanges(ctx, r, DefaultLibrary, nil, nil); err != nil || !slices.Equal(remove, []int64{match}) {
		t.Fatalf("remove = %v, %v", remove, err)
	}
	// Adding it by hand makes the membership the owner's.
	if err := lib.AddToCollection(ctx, c.ID, match); err != nil {
		t.Fatal(err)
	}
	if _, remove, err := lib.RuleChanges(ctx, r, DefaultLibrary, nil, nil); err != nil || len(remove) != 0 {
		t.Fatalf("remove after the owner's add = %v, %v", remove, err)
	}
}
