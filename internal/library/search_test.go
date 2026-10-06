package library

import (
	"context"
	"errors"
	"slices"
	"testing"
)

type searchCards struct{ brakka, carrow, dellin int64 }

func seedSearch(t *testing.T, lib *Library) searchCards {
	t.Helper()
	ctx := context.Background()
	var s searchCards
	s.brakka, _ = newCard(t, lib, "brakka", `{"name": "Brakka", "description": "A grumpy dwarf blacksmith."}`)
	s.carrow, _ = newCard(t, lib, "carrow", `{"name": "Carrow", "description": "A sailor who owes Brakka money."}`)
	s.dellin, _ = newCard(t, lib, "dellin", `{"name": "Dellin", "description": "A retired dwarf blacksmith."}`)
	if err := lib.SetCardTags(ctx, s.brakka, []string{"fantasy"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.SetCardTags(ctx, s.carrow, []string{"pirate"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.Cards.SetArchived(ctx, s.dellin, true); err != nil {
		t.Fatal(err)
	}
	return s
}

func hitIDs(r Results) []int64 {
	out := []int64{}
	for _, h := range r.Hits {
		out = append(out, h.ID)
	}
	return out
}

func ptr[T any](v T) *T { return &v }

func TestSearch(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)

	for name, tc := range map[string]struct {
		q    Query
		want []int64
	}{
		"everything not archived, newest first": {Query{}, []int64{s.carrow, s.brakka}},
		"text, stemmed":                         {Query{Text: "blacksmiths"}, []int64{s.brakka}},
		"text ranks names first":                {Query{Text: "brakka"}, []int64{s.brakka, s.carrow}},
		"text terms are ANDed":                  {Query{Text: "sailor blacksmith"}, []int64{}},
		"tag, any case":                         {Query{Filter: &Filter{Tag: ptr("Fantasy")}}, []int64{s.brakka}},
		"not tag":                               {Query{Filter: &Filter{Not: &Filter{Tag: ptr("fantasy")}}}, []int64{s.carrow}},
		"archived only":                         {Query{Filter: &Filter{Archived: ptr(true)}}, []int64{s.dellin}},
		"archived or not": {Query{Text: "blacksmith", Filter: &Filter{Or: []Filter{
			{Archived: ptr(true)}, {Archived: ptr(false)}}}}, []int64{s.dellin, s.brakka}},
		"not active": {Query{Filter: &Filter{Not: &Filter{Archived: ptr(false)}}}, []int64{s.dellin}},
		"or of tags": {Query{Filter: &Filter{Or: []Filter{{Tag: ptr("fantasy")}, {Tag: ptr("pirate")}}}}, []int64{s.carrow, s.brakka}},
		"and":        {Query{Filter: &Filter{And: []Filter{{Tag: ptr("fantasy")}, {Tag: ptr("pirate")}}}}, []int64{}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := lib.Search(ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hitIDs(got), tc.want) {
				t.Fatalf("hits = %v, want %v", hitIDs(got), tc.want)
			}
		})
	}
}

func manyTags(n int) []Filter {
	out := make([]Filter, n)
	for i := range out {
		out[i] = Filter{Tag: ptr("t")}
	}
	return out
}

func TestSearchQuotedTagAndBareAvatar(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "obrien", `{"name": "O", "avatar": "from-data"}`)
	if err := lib.SetCardTags(ctx, id, []string{"o'brien"}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(ctx, Query{Filter: &Filter{Tag: ptr("o'brien")}, Fields: []string{"avatar"}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{id}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
	if _, ok := got.Hits[0].Fields["avatar"]; ok {
		t.Fatalf("avatar leaked from card data: %v", got.Hits[0].Fields)
	}
}

func TestSearchCollection(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	c, err := lib.CreateCollection(ctx, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, c.ID, s.carrow); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(ctx, Query{Filter: &Filter{Collection: &c.ID}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
}

func TestSearchOutput(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	got, err := lib.Search(ctx, Query{Filter: &Filter{Tag: ptr("fantasy")},
		Fields: []string{"name", "avatar", "missing"}, Include: []string{"tags", "lorebooks"}})
	if err != nil {
		t.Fatal(err)
	}
	h := got.Hits[0]
	if h.ID != s.brakka || h.Slug != "brakka" || h.Fields["name"] != "Brakka" || len(h.Fields) != 1 ||
		!slices.Equal(h.Tags, []string{"fantasy"}) || h.Lorebooks == nil {
		t.Fatalf("hit = %+v", h)
	}
	plain, _ := lib.Search(ctx, Query{})
	if plain.Hits[0].Fields["name"] == nil || plain.Hits[0].Tags != nil {
		t.Fatalf("default output = %+v", plain.Hits[0])
	}
}

func TestSearchPaging(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	first, err := lib.Search(ctx, Query{Page: Page{Size: 1}})
	if err != nil || !slices.Equal(hitIDs(first), []int64{s.carrow}) || first.NextCursor == nil {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := lib.Search(ctx, Query{Page: Page{Size: 1, Cursor: first.NextCursor}})
	if err != nil || !slices.Equal(hitIDs(second), []int64{s.brakka}) || second.NextCursor != nil {
		t.Fatalf("second page = %+v, %v", second, err)
	}
}

func TestSearchRejectsBadQueries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	for name, q := range map[string]Query{
		"two conditions":   {Filter: &Filter{Tag: ptr("a"), Archived: ptr(true)}},
		"no condition":     {Filter: &Filter{Not: &Filter{}}},
		"semantic":         {Semantic: true},
		"catalog source":   {Sources: []string{"catalog:chub-archive"}},
		"bad include":      {Include: []string{"enrichments"}},
		"negative size":    {Page: Page{Size: -1}},
		"huge size":        {Page: Page{Size: 501}},
		"huge filter":      {Filter: &Filter{Or: manyTags(201)}},
		"bad cursor":       {Page: Page{Cursor: ptr("abc")}},
		"bad target state": {Filter: &Filter{TargetState: &TargetState{Instance: 1, Is: "pushed"}}},
	} {
		if _, err := lib.Search(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestSearchIDs(t *testing.T) {
	lib := newLibrary(t)
	s := seedSearch(t, lib)
	got, err := lib.SearchIDs(context.Background(), Query{Text: "brakka", Page: Page{Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	// Paging is ignored, and the archived card is left out.
	if !slices.Equal(got, []int64{s.brakka, s.carrow}) {
		t.Fatalf("ids = %v", got)
	}
}

func TestSearchSort(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	// Renaming Brakka makes it the most recently updated card, and first by name.
	c, err := lib.Cards.Get(ctx, s.brakka)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Update(ctx, s.brakka, c.HeadRevisionID, edit(`{"name": "Aaron Brakka"}`)); err != nil {
		t.Fatal(err)
	}
	for sort, want := range map[string][]int64{
		"":          {s.carrow, s.brakka},
		"relevance": {s.carrow, s.brakka},
		"created":   {s.carrow, s.brakka},
		"updated":   {s.brakka, s.carrow},
		"name":      {s.brakka, s.carrow},
	} {
		got, err := lib.Search(ctx, Query{Sort: sort})
		if err != nil || !slices.Equal(hitIDs(got), want) {
			t.Errorf("sort %q: hits = %v, %v; want %v", sort, hitIDs(got), err, want)
		}
	}
	if _, err := lib.Search(ctx, Query{Sort: "size"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad sort: %v", err)
	}
}

func TestSearchPageNumberAndTotal(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	got, err := lib.Search(ctx, Query{Page: Page{Size: 1, Number: 2}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.brakka}) || got.Total != 2 {
		t.Fatalf("page 2 = %+v, %v", got, err)
	}
	past, err := lib.Search(ctx, Query{Page: Page{Size: 1, Number: 9}})
	if err != nil || len(past.Hits) != 0 || past.Total != 2 {
		t.Fatalf("past the end = %+v, %v", past, err)
	}
	if _, err := lib.Search(ctx, Query{Page: Page{Number: 1, Cursor: ptr("1")}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("number and cursor: %v", err)
	}
	if _, err := lib.Search(ctx, Query{Page: Page{Number: -1}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative number: %v", err)
	}
	if _, err := lib.Search(ctx, Query{Page: Page{Number: 1 << 40}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge number: %v", err)
	}
}

func TestSearchSourceFilter(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	if _, err := lib.Cards.AddSource(ctx, s.carrow, Source{Kind: "chub", Ref: "carrow"}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(ctx, Query{Filter: &Filter{Source: ptr("chub")}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
}

func TestSearchFacets(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	if err := lib.SetCardTags(ctx, s.carrow, []string{"pirate", "fantasy"}); err != nil {
		t.Fatal(err)
	}
	crew, err := lib.CreateCollection(ctx, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, crew.ID, s.carrow); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.AddSource(ctx, s.brakka, Source{Kind: "chub", Ref: "brakka"}); err != nil {
		t.Fatal(err)
	}

	got, err := lib.Search(ctx, Query{Facets: []string{"tags", "collections", "targets", "sources"}})
	if err != nil {
		t.Fatal(err)
	}
	f := got.Facets
	if f == nil ||
		!slices.Equal(f.Tags, []TagCount{{"fantasy", 2}, {"pirate", 1}}) ||
		!slices.Equal(f.Collections, []CollectionCount{{crew.ID, "Crew", 1}}) ||
		!slices.Equal(f.Sources, []SourceCount{{"chub", 1}}) ||
		len(f.Targets) != 0 {
		t.Fatalf("facets = %+v", f)
	}

	// Counts follow the query: only Brakka is tagged fantasy and not pirate.
	got, err = lib.Search(ctx, Query{Facets: []string{"tags"},
		Filter: &Filter{Not: &Filter{Tag: ptr("pirate")}}})
	if err != nil || !slices.Equal(got.Facets.Tags, []TagCount{{"fantasy", 1}}) {
		t.Fatalf("filtered facets = %+v, %v", got.Facets, err)
	}

	if plain, _ := lib.Search(ctx, Query{}); plain.Facets != nil {
		t.Fatalf("facets without asking: %+v", plain.Facets)
	}
	if _, err := lib.Search(ctx, Query{Facets: []string{"colors"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad facet: %v", err)
	}
}

func TestSearchIDsFilter(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	// Dellin is archived, so an ids filter alone still hides it.
	got, err := lib.Search(ctx, Query{Filter: &Filter{IDs: []int64{s.carrow, s.dellin}}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("ids = %v, %v", hitIDs(got), err)
	}
	ids, err := lib.SearchIDs(ctx, Query{Filter: &Filter{Or: []Filter{{IDs: []int64{s.brakka}}, {Tag: ptr("pirate")}}}})
	if err != nil || !slices.Equal(ids, []int64{s.brakka, s.carrow}) {
		t.Fatalf("ids or tag = %v, %v", ids, err)
	}
	none, err := lib.SearchIDs(ctx, Query{Filter: &Filter{IDs: []int64{}}})
	if err != nil || len(none) != 0 {
		t.Fatalf("empty ids = %v, %v", none, err)
	}
}

func TestSearchLorebooks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	forge, _, err := lib.Lorebooks.Create(ctx, "forge", "upload", edit(`{"name": "Forge", "description": "Smithing lore.",
		"entries": [{"keys": ["anvil"], "content": "Heavy iron."}, {"keys": ["bellows"], "content": "Pumps air."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sea, _, err := lib.Lorebooks.Create(ctx, "sea", "upload", edit(`{"name": "Sea", "entries": [{"keys": ["kraken"], "content": "Many arms."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	card, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	if err := lib.LinkLorebook(ctx, card, forge.ID); err != nil {
		t.Fatal(err)
	}

	// Names, descriptions, entry keys, and entry content all match.
	for _, text := range []string{"forge", "smithing", "anvil", "iron"} {
		res, err := lib.SearchLorebooks(ctx, Query{Text: text})
		if err != nil || len(res.Hits) != 1 || res.Hits[0].ID != forge.ID {
			t.Fatalf("text %q = %+v, %v", text, res.Hits, err)
		}
	}

	res, err := lib.SearchLorebooks(ctx, Query{Include: []string{"cards", "targets"}, Sort: "name", Facets: []string{"targets", "sources"}})
	if err != nil || res.Total != 2 || res.Hits[0].ID != forge.ID {
		t.Fatalf("all = %+v, %v", res, err)
	}
	if h := res.Hits[0]; !slices.Equal(h.Cards, []int64{card}) || h.Entries == nil || *h.Entries != 2 {
		t.Fatalf("forge hit = %+v", h)
	}

	if err := lib.Lorebooks.SetArchived(ctx, sea.ID, true); err != nil {
		t.Fatal(err)
	}
	if ids, err := lib.SearchLorebookIDs(ctx, Query{Filter: &Filter{Archived: ptr(true)}}); err != nil || !slices.Equal(ids, []int64{sea.ID}) {
		t.Fatalf("archived = %v, %v", ids, err)
	}

	for _, q := range []Query{
		{Filter: &Filter{Tag: ptr("x")}},
		{Filter: &Filter{Collection: ptr(int64(1))}},
		{Facets: []string{"tags"}},
		{Include: []string{"lorebooks"}},
	} {
		if _, err := lib.SearchLorebooks(ctx, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("lorebooks %+v: got %v, want ErrInvalid", q, err)
		}
	}
	if _, err := lib.Search(ctx, Query{Include: []string{"cards"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("cards include cards: got %v, want ErrInvalid", err)
	}
}
