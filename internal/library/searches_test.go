package library

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSavedSearchKeepsItsFilterAndSort(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	f := Filter{Or: []Filter{{Tag: new("elf")}, {Name: new("brakka")}}}
	s, err := lib.CreateSavedSearch(ctx, "Elves", f, "name")
	if err != nil {
		t.Fatal(err)
	}
	got, err := lib.GetSavedSearch(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(f)
	have, _ := json.Marshal(got.Filter)
	if string(have) != string(want) || got.Sort != "name" || got.InstanceID != nil {
		t.Fatalf("saved %+v, filter %s", got, have)
	}
	if _, err := lib.CreateSavedSearch(ctx, "Elves", f, ""); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("same name: %v", err)
	}
	if _, err := lib.CreateSavedSearch(ctx, " Elves", f, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("padded name: %v", err)
	}
	renamed, err := lib.UpdateSavedSearch(ctx, s.ID, new("Elf cast"), nil, nil)
	if err != nil || renamed.Name != "Elf cast" || renamed.Sort != "name" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	if err := lib.DeleteSavedSearch(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.GetSavedSearch(ctx, s.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}

func TestDeleteInstanceSearchesLeavesTheOwners(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	if _, err := lib.pool.Exec(ctx, `INSERT INTO saved_search (name, filter, instance_id) VALUES ('Junk', '{"tag": "x"}', 7)`); err != nil {
		t.Fatal(err)
	}
	own, err := lib.CreateSavedSearch(ctx, "Mine", Filter{Tag: new("x")}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteInstanceSearches(ctx, 7); err != nil {
		t.Fatal(err)
	}
	all, err := lib.SavedSearches(ctx)
	if err != nil || len(all) != 1 || all[0].ID != own.ID {
		t.Fatalf("left = %+v, %v", all, err)
	}
}

func TestCheckFilterRefusesWhatSearchRefuses(t *testing.T) {
	if err := CheckFilter(Filter{Tag: new("a"), Name: new("b")}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("two conditions: %v", err)
	}
	merged := Filter{Enrichment: new("quality"), Gte: new(70.0)}
	if err := CheckFilter(merged, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("a key nothing browses: %v", err)
	}
	if err := CheckFilter(merged, map[string][]Scorer{"quality": {{Instance: 3, Key: "quality"}}}); err != nil {
		t.Fatalf("a browsable key: %v", err)
	}
}
