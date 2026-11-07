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
