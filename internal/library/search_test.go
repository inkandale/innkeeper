package library

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

type searchCards struct{ brakka, carrow, dellin int64 }

func seedSearch(t *testing.T, lib *Library) searchCards {
	t.Helper()
	ctx := context.Background()
	var s searchCards
	s.brakka, _ = newCard(t, lib, "brakka", `{"name": "Brakka", "description": "A grumpy dwarf blacksmith.", "tags": ["fantasy"]}`)
	s.carrow, _ = newCard(t, lib, "carrow", `{"name": "Carrow", "description": "A sailor who owes Brakka money.", "tags": ["pirate"]}`)
	s.dellin, _ = newCard(t, lib, "dellin", `{"name": "Dellin", "description": "A retired dwarf blacksmith."}`)
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
		"not active":              {Query{Filter: &Filter{Not: &Filter{Archived: ptr(false)}}}, []int64{s.dellin}},
		"or of tags":              {Query{Filter: &Filter{Or: []Filter{{Tag: ptr("fantasy")}, {Tag: ptr("pirate")}}}}, []int64{s.carrow, s.brakka}},
		"and":                     {Query{Filter: &Filter{And: []Filter{{Tag: ptr("fantasy")}, {Tag: ptr("pirate")}}}}, []int64{}},
		"name contains, any case": {Query{Filter: &Filter{Name: ptr("RAK")}}, []int64{s.brakka}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := lib.Search(ctx, Card, tc.q)
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
	id, _ := newCard(t, lib, "obrien", `{"name": "O", "avatar": "from-data", "tags": ["o'brien"]}`)
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Tag: ptr("o'brien")}, Fields: []string{"avatar"}})
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
	c, err := lib.CreateCollection(ctx, DefaultLibrary, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, c.ID, s.carrow); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Collection: &c.ID}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
}

func TestSearchOutput(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Tag: ptr("fantasy")},
		Fields: []string{"name", "avatar", "missing"}, Include: []string{"tags", "lorebooks"}})
	if err != nil {
		t.Fatal(err)
	}
	h := got.Hits[0]
	if h.ID != s.brakka || h.Slug != "brakka" || h.Fields["name"] != "Brakka" || len(h.Fields) != 1 ||
		!slices.Equal(h.Tags, []string{"fantasy"}) || h.Lorebooks == nil {
		t.Fatalf("hit = %+v", h)
	}
	plain, _ := lib.Search(ctx, Card, Query{})
	if plain.Hits[0].Fields["name"] == nil || plain.Hits[0].Tags != nil {
		t.Fatalf("default output = %+v", plain.Hits[0])
	}
}

func TestSearchPaging(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	first, err := lib.Search(ctx, Card, Query{Page: Page{Size: 1}})
	if err != nil || !slices.Equal(hitIDs(first), []int64{s.carrow}) || first.NextCursor == nil {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	second, err := lib.Search(ctx, Card, Query{Page: Page{Size: 1, Cursor: first.NextCursor}})
	if err != nil || !slices.Equal(hitIDs(second), []int64{s.brakka}) || second.NextCursor != nil {
		t.Fatalf("second page = %+v, %v", second, err)
	}
}

func TestSearchRejectsBadQueries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	for name, q := range map[string]Query{
		"two conditions":          {Filter: &Filter{Tag: ptr("a"), Archived: ptr(true)}},
		"no condition":            {Filter: &Filter{Not: &Filter{}}},
		"semantic without text":   {Semantic: true, Embedding: []float32{1, 0}, EmbedInstance: 1},
		"semantic without vector": {Semantic: true, Text: "smith"},
		"semantic by name":        {Semantic: true, Text: "smith", Embedding: []float32{1, 0}, EmbedInstance: 1, Sort: "name"},
		"catalog source":          {Sources: []string{"catalog:chub-archive"}},
		"bad include":             {Include: []string{"summaries"}},
		"negative size":           {Page: Page{Size: -1}},
		"huge size":               {Page: Page{Size: 501}},
		"huge filter":             {Filter: &Filter{Or: manyTags(201)}},
		"bad cursor":              {Page: Page{Cursor: ptr("abc")}},
		"bad target state":        {Filter: &Filter{TargetState: &TargetFilter{States: []string{"pushed"}}}},
	} {
		if _, err := lib.Search(ctx, Card, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestSearchStatesFacetCountsEachCardOncePerState(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	var a, b int64
	if err := lib.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'a'), ('fake', 'b') RETURNING id`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	b = a + 1
	// Brakka is in sync on a and changed on b; Carrow is missing on a.
	for _, bind := range []struct {
		instance, card int64
		remote         string
	}{{a, s.brakka, "unchanged"}, {b, s.brakka, "changed"}, {a, s.carrow, "missing"}} {
		if _, err := lib.pool.Exec(ctx, `WITH b AS (INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data, accepted_hash)
			VALUES ($1::bigint, $2::bigint, $1::bigint || '-' || $2::bigint, $3, '{}', 'h') RETURNING id)
			INSERT INTO push (binding_id, revision_id, pushed_hash, remote_id)
			SELECT b.id, c.head_revision_id, 'h', 'r' FROM b, card c WHERE c.id = $2::bigint`, bind.instance, bind.card, bind.remote); err != nil {
			t.Fatal(err)
		}
	}

	got, err := lib.Search(ctx, Card, Query{Facets: []string{"states"}})
	if err != nil || !slices.Equal(got.Facets.States, []StateCount{{"in_sync", 1}, {"missing", 1}, {"remote_changed", 1}}) {
		t.Fatalf("states = %+v, %v", got.Facets, err)
	}
	// Named targets narrow the count to how cards stand there.
	got, err = lib.Search(ctx, Card, Query{Facets: []string{fmt.Sprintf("states:%d", a)}})
	if err != nil || !slices.Equal(got.Facets.States, []StateCount{{"in_sync", 1}, {"missing", 1}}) {
		t.Fatalf("states on a = %+v, %v", got.Facets, err)
	}
	if _, err := lib.Search(ctx, Card, Query{Facets: []string{"states:x"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad states facet: %v", err)
	}
}

func TestSearchIDs(t *testing.T) {
	lib := newLibrary(t)
	s := seedSearch(t, lib)
	got, err := lib.SearchIDs(context.Background(), Card, Query{Text: "brakka", Page: Page{Size: 1}})
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
		got, err := lib.Search(ctx, Card, Query{Sort: sort})
		if err != nil || !slices.Equal(hitIDs(got), want) {
			t.Errorf("sort %q: hits = %v, %v; want %v", sort, hitIDs(got), err, want)
		}
	}
	if _, err := lib.Search(ctx, Card, Query{Sort: "size"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad sort: %v", err)
	}
}

func TestSearchPageNumberAndTotal(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	got, err := lib.Search(ctx, Card, Query{Page: Page{Size: 1, Number: 2}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.brakka}) || got.Total != 2 {
		t.Fatalf("page 2 = %+v, %v", got, err)
	}
	past, err := lib.Search(ctx, Card, Query{Page: Page{Size: 1, Number: 9}})
	if err != nil || len(past.Hits) != 0 || past.Total != 2 {
		t.Fatalf("past the end = %+v, %v", past, err)
	}
	if _, err := lib.Search(ctx, Card, Query{Page: Page{Number: 1, Cursor: ptr("1")}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("number and cursor: %v", err)
	}
	if _, err := lib.Search(ctx, Card, Query{Page: Page{Number: -1}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative number: %v", err)
	}
	if _, err := lib.Search(ctx, Card, Query{Page: Page{Number: 1 << 40}}); !errors.Is(err, ErrInvalid) {
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
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Source: ptr("chub")}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
}

func TestSearchFacets(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	if err := lib.EditCardTags(ctx, s.carrow, []string{"fantasy"}, nil); err != nil {
		t.Fatal(err)
	}
	crew, err := lib.CreateCollection(ctx, DefaultLibrary, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddToCollection(ctx, crew.ID, s.carrow); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.AddSource(ctx, s.brakka, Source{Kind: "chub", Ref: "brakka"}); err != nil {
		t.Fatal(err)
	}

	got, err := lib.Search(ctx, Card, Query{Facets: []string{"tags", "collections", "targets", "sources"}})
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
	got, err = lib.Search(ctx, Card, Query{Facets: []string{"tags"},
		Filter: &Filter{Not: &Filter{Tag: ptr("pirate")}}})
	if err != nil || !slices.Equal(got.Facets.Tags, []TagCount{{"fantasy", 1}}) {
		t.Fatalf("filtered facets = %+v, %v", got.Facets, err)
	}

	if plain, _ := lib.Search(ctx, Card, Query{}); plain.Facets != nil {
		t.Fatalf("facets without asking: %+v", plain.Facets)
	}
	if _, err := lib.Search(ctx, Card, Query{Facets: []string{"colors"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad facet: %v", err)
	}
}

func TestSearchIDsFilter(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	// Dellin is archived, so an ids filter alone still hides it.
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{IDs: []int64{s.carrow, s.dellin}}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("ids = %v, %v", hitIDs(got), err)
	}
	ids, err := lib.SearchIDs(ctx, Card, Query{Filter: &Filter{Or: []Filter{{IDs: []int64{s.brakka}}, {Tag: ptr("pirate")}}}})
	if err != nil || !slices.Equal(ids, []int64{s.brakka, s.carrow}) {
		t.Fatalf("ids or tag = %v, %v", ids, err)
	}
	none, err := lib.SearchIDs(ctx, Card, Query{Filter: &Filter{IDs: []int64{}}})
	if err != nil || len(none) != 0 {
		t.Fatalf("empty ids = %v, %v", none, err)
	}
}

func TestSearchLorebooks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	forge, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "forge", "upload", edit(`{"name": "Forge", "description": "Smithing lore.",
		"entries": [{"keys": ["anvil"], "content": "Heavy iron."}, {"keys": ["bellows"], "content": "Pumps air."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sea, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "sea", "upload", edit(`{"name": "Sea", "entries": [{"keys": ["kraken"], "content": "Many arms."}]}`))
	if err != nil {
		t.Fatal(err)
	}
	card, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	if err := lib.LinkLorebook(ctx, card, forge.ID); err != nil {
		t.Fatal(err)
	}

	if res, err := lib.Search(ctx, Lorebook, Query{Filter: &Filter{Name: ptr("orG")}}); err != nil || !slices.Equal(hitIDs(res), []int64{forge.ID}) {
		t.Fatalf("name filter = %v, %v", hitIDs(res), err)
	}

	// Names, descriptions, entry keys, and entry content all match.
	for _, text := range []string{"forge", "smithing", "anvil", "iron"} {
		res, err := lib.Search(ctx, Lorebook, Query{Text: text})
		if err != nil || len(res.Hits) != 1 || res.Hits[0].ID != forge.ID {
			t.Fatalf("text %q = %+v, %v", text, res.Hits, err)
		}
	}

	res, err := lib.Search(ctx, Lorebook, Query{Include: []string{"cards", "targets"}, Sort: "name", Facets: []string{"targets", "sources"}})
	if err != nil || res.Total != 2 || res.Hits[0].ID != forge.ID {
		t.Fatalf("all = %+v, %v", res, err)
	}
	if h := res.Hits[0]; !slices.Equal(h.Cards, []int64{card}) || h.Entries == nil || *h.Entries != 2 {
		t.Fatalf("forge hit = %+v", h)
	}

	if err := lib.Lorebooks.SetArchived(ctx, sea.ID, true); err != nil {
		t.Fatal(err)
	}
	if ids, err := lib.SearchIDs(ctx, Lorebook, Query{Filter: &Filter{Archived: ptr(true)}}); err != nil || !slices.Equal(ids, []int64{sea.ID}) {
		t.Fatalf("archived = %v, %v", ids, err)
	}

	for _, q := range []Query{
		{Filter: &Filter{Tag: ptr("x")}},
		{Filter: &Filter{Collection: ptr(int64(1))}},
		{Facets: []string{"tags"}},
		{Include: []string{"lorebooks"}},
	} {
		if _, err := lib.Search(ctx, Lorebook, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("lorebooks %+v: got %v, want ErrInvalid", q, err)
		}
	}
	if _, err := lib.Search(ctx, Card, Query{Include: []string{"cards"}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("cards include cards: got %v, want ErrInvalid", err)
	}
}

func TestSearchLorebookSourcesAndBareEntries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	sourced, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "sourced", "upload", edit(`{"name": "Sourced", "entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	// Missing entries count as none, and searching them doesn't fail.
	bare, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "bare", "upload", edit(`{"name": "Bare"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Lorebooks.AddSource(ctx, sourced.ID, Source{Kind: "chub", Ref: "u/sourced"}); err != nil {
		t.Fatal(err)
	}

	res, err := lib.Search(ctx, Lorebook, Query{Filter: &Filter{Source: ptr("chub")}, Facets: []string{"sources"}})
	if err != nil || res.Total != 1 || res.Hits[0].ID != sourced.ID {
		t.Fatalf("source filter = %+v, %v", res, err)
	}
	if f := res.Facets.Sources; len(f) != 1 || f[0].Kind != "chub" || f[0].Cards != 1 {
		t.Fatalf("sources facet = %+v", f)
	}
	res, err = lib.Search(ctx, Lorebook, Query{Text: "bare"})
	if err != nil || len(res.Hits) != 1 || res.Hits[0].ID != bare.ID || res.Hits[0].Entries == nil || *res.Hits[0].Entries != 0 {
		t.Fatalf("bare = %+v, %v", res.Hits, err)
	}
}

func TestSearchFacetsOnlySkipsHitsAndTotal(t *testing.T) {
	lib := newLibrary(t)
	s := seedSearch(t, lib)
	if err := lib.EditCardTags(context.Background(), s.carrow, []string{"fantasy"}, nil); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(context.Background(), Card, Query{Facets: []string{"tags"}, FacetsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 0 || got.Total != 0 || got.NextCursor != nil {
		t.Fatalf("facets-only search returned hits %d, total %d, cursor %v", len(got.Hits), got.Total, got.NextCursor)
	}
	if got.Facets == nil || !slices.Equal(got.Facets.Tags, []TagCount{{"fantasy", 2}, {"pirate", 1}}) {
		t.Fatalf("facets = %+v", got.Facets)
	}
	if _, err := lib.Search(context.Background(), Card, Query{Facets: []string{"nope"}, FacetsOnly: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown facet: err = %v, want ErrInvalid", err)
	}
}

func TestSearchSamplesTheFacetsOfABigMatch(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	sampled := 0
	rare := int64(0)
	for i := range 40 {
		id, _ := newCard(t, lib, fmt.Sprintf("c%d", i), `{"name": "C", "tags": ["all"]}`)
		if id%10 == 0 {
			sampled++
		} else if rare == 0 {
			rare = id
		}
	}
	if err := lib.EditCardTags(ctx, rare, []string{"rare"}, nil); err != nil {
		t.Fatal(err)
	}
	q := Query{Facets: []string{"tags"}, FacetsOnly: true, SampleOver: 20}
	got, err := lib.Search(ctx, Card, q)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Facets.Approximate || !slices.Equal(got.Facets.Tags, []TagCount{{"all", 10 * sampled}}) {
		t.Fatalf("sampled facets = %+v", got.Facets)
	}
	q.SampleOver = 40
	if got, err = lib.Search(ctx, Card, q); err != nil || got.Facets.Approximate || !slices.Equal(got.Facets.Tags, []TagCount{{"all", 40}, {"rare", 1}}) {
		t.Fatalf("facets of a match no bigger than SampleOver = %+v, %v", got.Facets, err)
	}
}

func TestSearchTagFacetCountsEveryTagOncePerCard(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	tags := make([]string, 60)
	for i := range tags {
		tags[i] = fmt.Sprintf("t%02d", i)
	}
	raw, _ := json.Marshal(map[string]any{"name": "Many", "tags": append(tags, "T00")})
	newCard(t, lib, "many", string(raw))
	got, err := lib.Search(ctx, Card, Query{Facets: []string{"tags"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Facets.Tags) != 60 || got.Facets.Tags[0].Cards != 1 {
		t.Fatalf("tags = %+v", got.Facets.Tags)
	}
}

func TestSearchIgnoresNonStringTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "n", `{"name": "N", "tags": ["fantasy", null, 3]}`)
	tags, err := lib.Tags(ctx, DefaultLibrary)
	if err != nil || len(tags) != 1 || tags[0].Name != "fantasy" {
		t.Fatalf("Tags = %v, %v", tags, err)
	}
	got, err := lib.Search(ctx, Card, Query{Facets: []string{"tags"}, Include: []string{"tags"}})
	if err != nil || len(got.Facets.Tags) != 1 || got.Facets.Tags[0].Name != "fantasy" {
		t.Fatalf("facet = %+v, %v", got.Facets, err)
	}
	if !slices.Equal(got.Hits[0].Tags, []string{"fantasy"}) {
		t.Fatalf("include tags = %v", got.Hits[0].Tags)
	}
}

func TestSearchEnrichments(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	const score, other = 7, 8
	heads := map[int64]int64{}
	for _, id := range []int64{s.brakka, s.carrow} {
		c, err := lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		heads[id] = c.HeadRevisionID
	}
	add := func(inst, card int64, value string) {
		t.Helper()
		if err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: card, RevisionID: heads[card], Key: "quality", Value: json.RawMessage(value)}}); err != nil {
			t.Fatal(err)
		}
	}
	add(score, s.brakka, "40")
	add(score, s.brakka, "90") // newer, so current
	add(other, s.carrow, "95")
	// Carrow's next revision makes this value stale.
	add(score, s.carrow, "70")
	if _, err := lib.Cards.Update(ctx, s.carrow, heads[s.carrow], edit(`{"name": "Carrow", "description": "Changed."}`)); err != nil {
		t.Fatal(err)
	}
	quality := ptr("quality")
	for name, tc := range map[string]struct {
		q    Query
		want []int64
	}{
		"range uses the current value":     {Query{Filter: &Filter{Enrichment: quality, Gte: ptr(80.0)}}, []int64{s.brakka}},
		"range ends are included":          {Query{Filter: &Filter{Enrichment: quality, Gte: ptr(70.0), Lte: ptr(70.0)}}, []int64{s.carrow}},
		"only the score instance's values": {Query{Filter: &Filter{Enrichment: quality, Gte: ptr(91.0)}}, []int64{}},
		"a named instance's values":        {Query{Filter: &Filter{Enrichment: quality, Instance: ptr(int64(other)), Gte: ptr(91.0)}}, []int64{s.carrow}},
		"one query, two instances": {Query{Filter: &Filter{And: []Filter{
			{Enrichment: quality, Instance: ptr(int64(other))}, {Enrichment: quality, Lte: ptr(75.0)}}}}, []int64{s.carrow}},
		"has a value":                       {Query{Filter: &Filter{Enrichment: quality}}, []int64{s.carrow, s.brakka}},
		"missing":                           {Query{Filter: &Filter{Enrichment: ptr("voice"), Missing: ptr(true)}}, []int64{s.carrow, s.brakka}},
		"sort highest first":                {Query{Sort: "enrichment:quality"}, []int64{s.brakka, s.carrow}},
		"no value sorts last":               {Query{Sort: "enrichment:quality", Filter: &Filter{Archived: ptr(true)}}, []int64{s.dellin}},
		"sort by a named instance's values": {Query{Sort: fmt.Sprintf("enrichment:%d:quality", other)}, []int64{s.carrow, s.brakka}},
	} {
		t.Run(name, func(t *testing.T) {
			tc.q.ScoreInstance = score
			got, err := lib.Search(ctx, Card, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(hitIDs(got), tc.want) {
				t.Fatalf("hits = %v, want %v", hitIDs(got), tc.want)
			}
		})
	}

	got, err := lib.Search(ctx, Card, Query{Include: []string{"enrichments"}, Sort: "enrichment:quality", ScoreInstance: score,
		Filter: &Filter{Or: []Filter{{Archived: ptr(true)}, {Archived: ptr(false)}}}})
	if err != nil {
		t.Fatal(err)
	}
	if ids := hitIDs(got); !slices.Equal(ids, []int64{s.brakka, s.carrow, s.dellin}) {
		t.Fatalf("sorted = %v", ids)
	}
	b, c, d := got.Hits[0].Enrichments["quality"], got.Hits[1].Enrichments["quality"], got.Hits[2].Enrichments
	if string(b.Value) != "90" || b.Stale || string(c.Value) != "70" || !c.Stale || d != nil {
		t.Fatalf("includes = %+v, %+v, %+v", b, c, d)
	}

	for name, q := range map[string]Query{
		"filter without a score instance":  {Filter: &Filter{Enrichment: quality}},
		"sort without a score instance":    {Sort: "enrichment:quality"},
		"sort by instance 0":               {Sort: "enrichment:0:quality", ScoreInstance: score},
		"include without a score instance": {Include: []string{"enrichments"}},
		"gte without enrichment":           {Filter: &Filter{Gte: ptr(1.0)}, ScoreInstance: score},
		"instance without enrichment":      {Filter: &Filter{Instance: ptr(int64(other))}, ScoreInstance: score},
		"missing and a range":              {Filter: &Filter{Enrichment: quality, Missing: ptr(true), Lte: ptr(1.0)}, ScoreInstance: score},
	} {
		if _, err := lib.Search(ctx, Card, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if got, err := lib.Search(ctx, Card, Query{Sort: fmt.Sprintf("enrichment:%d:quality", other)}); err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow, s.brakka}) {
		t.Errorf("sort by a named instance without a score instance: %v, %v", hitIDs(got), err)
	}
	if ids, err := lib.SearchIDs(ctx, Card, Query{Filter: &Filter{Enrichment: quality, Instance: ptr(int64(other))}}); err != nil || !slices.Equal(ids, []int64{s.carrow}) {
		t.Errorf("named instance without a score instance: %v, %v", ids, err)
	}
	if _, err := lib.Search(ctx, Lorebook, Query{Filter: &Filter{Enrichment: quality}, ScoreInstance: score}); !errors.Is(err, ErrInvalid) {
		t.Errorf("lorebook filter: got %v, want ErrInvalid", err)
	}
	if ids, err := lib.SearchIDs(ctx, Card, Query{Filter: &Filter{Enrichment: quality, Lte: ptr(75.0)}, ScoreInstance: score}); err != nil || !slices.Equal(ids, []int64{s.carrow}) {
		t.Errorf("SearchIDs = %v, %v", ids, err)
	}
}

func TestSearchEnrichmentValues(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	const lint = 7
	add := func(card int64, key, value string) {
		t.Helper()
		c, err := lib.Cards.Get(ctx, card)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.AddEnrichments(ctx, lint, []NewEnrichment{{CardID: card, RevisionID: c.HeadRevisionID, Key: key, Value: json.RawMessage(value)}}); err != nil {
			t.Fatal(err)
		}
	}
	add(s.brakka, "language", `"en"`)
	add(s.brakka, "problems", `["markup", "oversized"]`)
	add(s.carrow, "language", `"zh"`)
	add(s.carrow, "problems", `["markup"]`)

	for name, tc := range map[string]struct {
		f    Filter
		want []int64
	}{
		"text is":         {Filter{Enrichment: ptr("language"), Is: ptr("zh")}, []int64{s.carrow}},
		"list holds":      {Filter{Enrichment: ptr("problems"), Is: ptr("oversized")}, []int64{s.brakka}},
		"list holds both": {Filter{Enrichment: ptr("problems"), Is: ptr("markup")}, []int64{s.carrow, s.brakka}},
		"no such value":   {Filter{Enrichment: ptr("language"), Is: ptr("ja")}, []int64{}},
		"not a list item": {Filter{Not: &Filter{Enrichment: ptr("problems"), Is: ptr("oversized")}}, []int64{s.carrow}},
	} {
		t.Run(name, func(t *testing.T) {
			ids, err := lib.SearchIDs(ctx, Card, Query{Filter: &tc.f, ScoreInstance: lint})
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(tc.want)
			if !slices.Equal(ids, tc.want) {
				t.Fatalf("ids = %v, want %v", ids, tc.want)
			}
		})
	}

	got, err := lib.Search(ctx, Card, Query{Facets: []string{"enrichment:problems", "enrichment:language", "tags"}, ScoreInstance: lint})
	if err != nil {
		t.Fatal(err)
	}
	problems, languages := got.Facets.Enrichments["problems"], got.Facets.Enrichments["language"]
	if !slices.Equal(problems, []ValueCount{{"markup", 2}, {"oversized", 1}}) || !slices.Equal(languages, []ValueCount{{"en", 1}, {"zh", 1}}) {
		t.Fatalf("facets = %+v, %+v", problems, languages)
	}
	if len(got.Facets.Tags) == 0 {
		t.Error("tags facet missing beside enrichment facets")
	}
	got, err = lib.Search(ctx, Card, Query{Facets: []string{"enrichment:problems"}, ScoreInstance: lint,
		Filter: &Filter{Enrichment: ptr("language"), Is: ptr("zh")}})
	if err != nil || !slices.Equal(got.Facets.Enrichments["problems"], []ValueCount{{"markup", 1}}) {
		t.Fatalf("filtered facet = %+v, %v", got.Facets, err)
	}

	got, err = lib.Search(ctx, Card, Query{Facets: []string{"enrichment:7:language"}})
	if err != nil || !slices.Equal(got.Facets.Enrichments["7:language"], []ValueCount{{"en", 1}, {"zh", 1}}) {
		t.Fatalf("named instance's facet without a score instance = %+v, %v", got.Facets, err)
	}
	got, err = lib.Search(ctx, Card, Query{Facets: []string{"enrichment:language", "enrichment:7:language", "enrichment:language", "enrichment:unset"}, ScoreInstance: lint})
	if err != nil || !slices.Equal(got.Facets.Enrichments["language"], languages) || !slices.Equal(got.Facets.Enrichments["7:language"], languages) {
		t.Fatalf("one key under two names = %+v, %v", got.Facets, err)
	}
	if unset, ok := got.Facets.Enrichments["unset"]; !ok || len(unset) != 0 {
		t.Fatalf("key no card has = %v, %v; want an empty list", unset, ok)
	}

	for name, q := range map[string]Query{
		"facet without a score instance": {Facets: []string{"enrichment:language"}},
		"facet on instance 0":            {Facets: []string{"enrichment:0:language"}, ScoreInstance: lint},
		"is and a range":                 {Filter: &Filter{Enrichment: ptr("language"), Is: ptr("en"), Gte: ptr(1.0)}, ScoreInstance: lint},
		"is without enrichment":          {Filter: &Filter{Is: ptr("en")}, ScoreInstance: lint},
	} {
		if _, err := lib.Search(ctx, Card, q); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if _, err := lib.Search(ctx, Lorebook, Query{Facets: []string{"enrichment:language"}, ScoreInstance: lint}); !errors.Is(err, ErrInvalid) {
		t.Errorf("lorebook facet: got %v, want ErrInvalid", err)
	}
}

func TestQueryUsesEnrichments(t *testing.T) {
	for q, want := range map[*Query]bool{
		{}:                                 false,
		{Sort: "name"}:                     false,
		{Sort: "enrichment:quality"}:       true,
		{Include: []string{"enrichments"}}: true,
		{Filter: &Filter{Or: []Filter{{Tag: ptr("a")}, {Not: &Filter{Enrichment: ptr("quality")}}}}}: true,
		{Filter: &Filter{And: []Filter{{Tag: ptr("a")}}}}:                                            false,
		{Facets: []string{"enrichment:language"}}:                                                    true,
		{Facets: []string{"tags"}}:                                                                   false,
	} {
		if got := q.UsesEnrichments(); got != want {
			t.Errorf("%+v: %v, want %v", *q, got, want)
		}
	}
}

func TestSearchVariantOfRoot(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	root, _ := newCard(t, lib, "root", `{"name": "Root"}`)
	child, _ := newCard(t, lib, "child", `{"name": "Child"}`)
	grand, _ := newCard(t, lib, "grand", `{"name": "Grand"}`)
	newCard(t, lib, "other", `{"name": "Other"}`)
	setVariantOf(t, lib.Cards, child, root)
	setVariantOf(t, lib.Cards, grand, child)
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{VariantOfRoot: ptr(child)}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{grand, child}) {
		t.Fatalf("below child = %v, %v", hitIDs(got), err)
	}
	got, err = lib.Search(ctx, Card, Query{Filter: &Filter{VariantOfRoot: ptr(root)}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{grand, child, root}) {
		t.Fatalf("below root = %v, %v", hitIDs(got), err)
	}
}

func TestSearchGroupsALibraryWithoutVariantsOneCardAFamily(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A", "description": "A dwarf."}`)
	b, _ := newCard(t, lib, "b", `{"name": "B", "description": "An elf."}`)
	c, _ := newCard(t, lib, "c", `{"name": "C", "description": "A dwarf."}`)
	gone, _ := newCard(t, lib, "gone", `{"name": "Gone"}`)
	if err := lib.Cards.SetArchived(ctx, gone, true); err != nil {
		t.Fatal(err)
	}
	solo := func(r Results, ids ...int64) bool {
		if !slices.Equal(hitIDs(r), ids) {
			return false
		}
		for _, h := range r.Hits {
			if h.Family == nil || h.Family.RootID != h.ID || h.Family.Size != 1 || !slices.Equal(h.Family.Matched, []int64{h.ID}) {
				return false
			}
		}
		return true
	}
	first, err := lib.Search(ctx, Card, Query{Group: "family", Page: Page{Size: 2}})
	if err != nil || !solo(first, c, b) || first.Total != 3 || first.NextCursor == nil {
		t.Fatalf("page 1 = %+v, %v", first, err)
	}
	rest, err := lib.Search(ctx, Card, Query{Group: "family", Page: Page{Size: 2, Cursor: first.NextCursor}})
	if err != nil || !solo(rest, a) || rest.NextCursor != nil {
		t.Fatalf("page after the cursor = %+v, %v", rest, err)
	}
	past, err := lib.Search(ctx, Card, Query{Group: "family", Page: Page{Size: 2, Number: 3}})
	if err != nil || len(past.Hits) != 0 || past.Total != 3 {
		t.Fatalf("page past the end = %+v, %v", past, err)
	}
	text, err := lib.Search(ctx, Card, Query{Group: "family", Text: "dwarf"})
	if err != nil || !solo(text, c, a) || text.Total != 2 {
		t.Fatalf("text = %+v, %v", text, err)
	}
	archived, err := lib.Search(ctx, Card, Query{Group: "family", Filter: &Filter{Archived: ptr(true)}})
	if err != nil || !solo(archived, gone) || archived.Total != 1 {
		t.Fatalf("archived = %+v, %v", archived, err)
	}
}

func TestSearchGroupsFamilies(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	root, _ := newCard(t, lib, "root", `{"name": "Root", "description": "An elf."}`)
	child, _ := newCard(t, lib, "child", `{"name": "Child", "description": "An elf."}`)
	grand, grandRev := newCard(t, lib, "grand", `{"name": "Grand", "description": "A dwarf smith."}`)
	solo, soloRev := newCard(t, lib, "solo", `{"name": "Solo", "description": "A dwarf smith."}`)
	setVariantOf(t, lib.Cards, child, root)
	setVariantOf(t, lib.Cards, grand, child)
	if err := lib.Cards.SetArchived(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	families := func(r Results) []Family {
		t.Helper()
		var out []Family
		for _, h := range r.Hits {
			if h.Family == nil {
				t.Fatalf("hit %d has no family", h.ID)
			}
			out = append(out, *h.Family)
		}
		return out
	}

	all, err := lib.Search(ctx, Card, Query{Group: "family"})
	if err != nil {
		t.Fatal(err)
	}
	// The root is archived, so the face is the shallowest live member.
	if !slices.Equal(hitIDs(all), []int64{solo, child}) || all.Total != 2 {
		t.Fatalf("hits = %v, total %d", hitIDs(all), all.Total)
	}
	if f := families(all)[1]; f.RootID != root || f.Size != 2 || !slices.Equal(f.Matched, []int64{child, grand}) {
		t.Fatalf("family = %+v", f)
	}

	text, err := lib.Search(ctx, Card, Query{Group: "family", Text: "dwarf"})
	if err != nil || !slices.Equal(hitIDs(text), []int64{solo, child}) || !slices.Equal(families(text)[1].Matched, []int64{grand}) {
		t.Fatalf("text: hits = %v, %v", hitIDs(text), err)
	}

	first, err := lib.Search(ctx, Card, Query{Group: "family", Page: Page{Size: 1}})
	if err != nil || !slices.Equal(hitIDs(first), []int64{solo}) || first.Total != 2 || first.NextCursor == nil {
		t.Fatalf("page 1 = %+v, %v", first, err)
	}
	second, err := lib.Search(ctx, Card, Query{Group: "family", Page: Page{Size: 1, Number: 2}})
	if err != nil || !slices.Equal(hitIDs(second), []int64{child}) || second.Total != 2 || second.NextCursor != nil {
		t.Fatalf("page 2 = %+v, %v", second, err)
	}
	past, err := lib.Search(ctx, Card, Query{Group: "family", Page: Page{Size: 1, Number: 3}})
	if err != nil || len(past.Hits) != 0 || past.Total != 2 {
		t.Fatalf("page past the end = %+v, %v", past, err)
	}

	archived, err := lib.Search(ctx, Card, Query{Group: "family", Filter: &Filter{Archived: ptr(true)}})
	if err != nil {
		t.Fatal(err)
	}
	// Under the archived filter, size counts every member.
	if f := families(archived); !slices.Equal(hitIDs(archived), []int64{child}) || f[0].Size != 3 || !slices.Equal(f[0].Matched, []int64{root}) {
		t.Fatalf("archived: hits = %v, families %+v", hitIDs(archived), f)
	}

	// An enrichment sort places the family by its best match, grand, though the face is child.
	const score = 7
	if err := lib.AddEnrichments(ctx, score, []NewEnrichment{
		{CardID: grand, RevisionID: grandRev, Key: "quality", Value: json.RawMessage("90")},
		{CardID: solo, RevisionID: soloRev, Key: "quality", Value: json.RawMessage("40")},
	}); err != nil {
		t.Fatal(err)
	}
	sorted, err := lib.Search(ctx, Card, Query{Group: "family", Sort: "enrichment:quality", Include: []string{"enrichments"},
		Filter: &Filter{Enrichment: ptr("quality")}, Facets: []string{"tags"}, ScoreInstance: score})
	if err != nil || !slices.Equal(hitIDs(sorted), []int64{child, solo}) || sorted.Hits[0].Enrichments != nil || string(sorted.Hits[1].Enrichments["quality"].Value) != "40" {
		t.Fatalf("enrichment sort: hits = %v, %v", hitIDs(sorted), err)
	}

	plain, err := lib.Search(ctx, Card, Query{})
	if err != nil || len(plain.Hits) != 3 || plain.Hits[0].Family != nil {
		t.Fatalf("ungrouped = %+v, %v", plain, err)
	}
	if _, err := lib.Search(ctx, Card, Query{Group: "tree"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown group: %v", err)
	}
}

func TestSearchSharesSource(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	other, _ := newCard(t, lib, "other", `{"name": "Other"}`)
	add := func(id int64, ref string) Source {
		t.Helper()
		src, err := lib.Cards.AddSource(ctx, id, Source{Kind: "chub", Ref: ref})
		if err != nil {
			t.Fatal(err)
		}
		return src
	}
	add(s.brakka, "dg/brakka")
	shared := add(other, "dg/brakka")
	add(s.carrow, "dg/carrow")
	q := Query{Filter: &Filter{SharesSource: ptr(true)}}
	got, err := lib.Search(ctx, Card, q)
	if err != nil || !slices.Equal(hitIDs(got), []int64{other, s.brakka}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
	if err := lib.Cards.RemoveSource(ctx, other, shared.ID); err != nil {
		t.Fatal(err)
	}
	if got, err = lib.Search(ctx, Card, q); err != nil || len(got.Hits) != 0 {
		t.Fatalf("after removing the shared source = %v, %v", hitIDs(got), err)
	}
}

func TestSearchTerminatesOnAVariantCycle(t *testing.T) {
	lib := newLibrary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	tail, _ := newCard(t, lib, "tail", `{"name": "Tail"}`)
	setVariantOf(t, lib.Cards, tail, b)
	// SetVariantOf refuses a cycle, but a restored backup could hold one.
	if _, err := lib.pool.Exec(ctx, `UPDATE card SET variant_of_id = CASE id WHEN $1::bigint THEN $2::bigint ELSE $1::bigint END WHERE id IN ($1, $2)`, a, b); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Search(ctx, Card, Query{Group: "family"}); err != nil {
		t.Fatalf("grouped search: %v", err)
	}
	if _, err := lib.Search(ctx, Card, Query{Filter: &Filter{VariantOfRoot: ptr(a)}}); err != nil {
		t.Fatalf("variant_of_root: %v", err)
	}
}

func TestSearchStaysInLibrary(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	mine, _ := newCard(t, lib, "mine", `{"name": "Brakka", "description": "A dwarf.", "tags": ["dwarf"]}`)
	theirs, _ := newCard(t, lib, "theirs", `{"name": "Brakka", "description": "A dwarf.", "tags": ["dwarf"]}`)
	archive := newLibraryRow(t, lib, "Archive")
	moveCard(t, lib, theirs, archive)

	for name, tc := range map[string]struct {
		library int64
		want    int64
	}{"default": {0, mine}, "archive": {archive, theirs}} {
		q := Query{Library: tc.library, Text: "dwarf", Facets: []string{"tags"}}
		res, err := lib.Search(ctx, Card, q)
		if err != nil || !slices.Equal(hitIDs(res), []int64{tc.want}) || res.Total != 1 || res.Facets.Tags[0].Cards != 1 {
			t.Fatalf("%s: search = %+v, %v", name, res, err)
		}
		q.Group = "family"
		if res, err = lib.Search(ctx, Card, q); err != nil || !slices.Equal(hitIDs(res), []int64{tc.want}) || res.Total != 1 {
			t.Fatalf("%s: family search = %+v, %v", name, res, err)
		}
		if ids, err := lib.SearchIDs(ctx, Card, q); err != nil || !slices.Equal(ids, []int64{tc.want}) {
			t.Fatalf("%s: SearchIDs = %v, %v", name, ids, err)
		}
	}
}

func TestCursorStableWhileInserting(t *testing.T) {
	ctx := context.Background()
	type card struct{ slug, data string }
	for name, tc := range map[string]struct {
		q             Query
		cards         []card
		before, after []card // inserted after page 1, sorting before and after its last hit
	}{
		"created": {
			q:      Query{},
			cards:  []card{{"a", `{"name": "A"}`}, {"b", `{"name": "B"}`}, {"c", `{"name": "C"}`}, {"d", `{"name": "D"}`}, {"e", `{"name": "E"}`}},
			before: []card{{"new", `{"name": "New"}`}},
		},
		"updated": {
			q:      Query{Sort: "updated"},
			cards:  []card{{"a", `{"name": "A"}`}, {"b", `{"name": "B"}`}, {"c", `{"name": "C"}`}, {"d", `{"name": "D"}`}, {"e", `{"name": "E"}`}},
			before: []card{{"new", `{"name": "New"}`}},
		},
		"name, nameless last": {
			q:      Query{Sort: "name"},
			cards:  []card{{"b", `{"name": "Brakka"}`}, {"c", `{"name": "carrow"}`}, {"d", `{"name": "Dellin"}`}, {"e", `{"name": "Erra"}`}, {"x", `{"description": "No name."}`}},
			before: []card{{"aaron", `{"name": "Aaron"}`}},
			after:  []card{{"zed", `{"name": "Zed"}`}},
		},
		"relevance": {
			q: Query{Text: "dwarf"},
			cards: []card{{"a", `{"name": "A", "description": "A dwarf."}`}, {"b", `{"name": "B", "description": "A dwarf who fights."}`},
				{"c", `{"name": "C", "description": "A dwarf, a dwarf."}`}, {"d", `{"name": "D", "description": "A tall dwarf."}`}, {"e", `{"name": "E", "description": "Dwarf."}`}},
			before: []card{{"top", `{"name": "Dwarf", "description": "A dwarf, a dwarf, a dwarf."}`}},
			after:  []card{{"low", `{"name": "Low", "first_mes": "I once met a dwarf somewhere far away from here, a long time ago."}`}},
		},
		"family": {
			q:      Query{Group: "family"},
			cards:  []card{{"a", `{"name": "A"}`}, {"b", `{"name": "B"}`}, {"c", `{"name": "C"}`}, {"d", `{"name": "D"}`}, {"e", `{"name": "E"}`}},
			before: []card{{"new", `{"name": "New"}`}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			lib := newLibrary(t)
			ids := map[string]int64{}
			add := func(cs []card) {
				for _, c := range cs {
					ids[c.slug], _ = newCard(t, lib, c.slug, c.data)
				}
			}
			add(tc.cards)
			q := tc.q
			q.Page.Size = 2
			page, err := lib.Search(ctx, Card, q)
			if err != nil || page.NextCursor == nil {
				t.Fatalf("page 1 = %+v, %v", page, err)
			}
			got := hitIDs(page)
			add(tc.before)
			add(tc.after)
			for page.NextCursor != nil {
				q.Page.Cursor = page.NextCursor
				if page, err = lib.Search(ctx, Card, q); err != nil {
					t.Fatal(err)
				}
				got = append(got, hitIDs(page)...)
			}
			// Paging sees everything a fresh search does except what sorts before page 1's end.
			all, err := lib.Search(ctx, Card, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			end := slices.Index(hitIDs(all), got[1])
			wantAll := []int64{}
			for i, id := range hitIDs(all) {
				isBefore := slices.ContainsFunc(tc.before, func(c card) bool { return ids[c.slug] == id })
				if isBefore && i >= end {
					t.Fatalf("%d doesn't sort before page 1's end: %v", id, hitIDs(all))
				}
				if !isBefore {
					wantAll = append(wantAll, id)
				}
			}
			if !slices.Equal(got, wantAll) {
				t.Fatalf("paged = %v, want %v (ids %v)", got, wantAll, ids)
			}
		})
	}
}

func TestCursorRejectsOtherSort(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	seedSearch(t, lib)
	page, err := lib.Search(ctx, Card, Query{Sort: "name", Page: Page{Size: 1}})
	if err != nil || page.NextCursor == nil {
		t.Fatalf("page = %+v, %v", page, err)
	}
	if _, err := lib.Search(ctx, Card, Query{Page: Page{Cursor: page.NextCursor}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("name cursor on created sort: %v", err)
	}
}

func TestCursorPagesEnrichmentSort(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const score = 7
	ids := make([]int64, 5)
	var values []NewEnrichment
	for i, v := range []string{"90", "40", "40", "", ""} {
		var rev int64
		ids[i], rev = newCard(t, lib, fmt.Sprintf("c%d", i), fmt.Sprintf(`{"name": "C%d"}`, i))
		if v != "" {
			values = append(values, NewEnrichment{CardID: ids[i], RevisionID: rev, Key: "quality", Value: json.RawMessage(v)})
		}
	}
	if err := lib.AddEnrichments(ctx, score, values); err != nil {
		t.Fatal(err)
	}
	q := Query{Sort: "enrichment:quality", ScoreInstance: score, Page: Page{Size: 2}}
	got := []int64{}
	for {
		page, err := lib.Search(ctx, Card, q)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, hitIDs(page)...)
		if page.NextCursor == nil {
			break
		}
		q.Page.Cursor = page.NextCursor
	}
	// Ties and cards without a value go newest first.
	if want := []int64{ids[0], ids[2], ids[1], ids[4], ids[3]}; !slices.Equal(got, want) {
		t.Fatalf("paged = %v, want %v", got, want)
	}
}

// bulkCards inserts n cards in library 1 with SQL, far faster than Create.
// data is a SQL expression for each card's data, over the series value g.
func bulkCards(t testing.TB, lib *Library, n int, data string) {
	t.Helper()
	ctx := context.Background()
	if _, err := lib.pool.Exec(ctx, `WITH c AS (
			INSERT INTO card (slug, provenance) SELECT 'bulk-' || g, 'upload' FROM generate_series(1, $1) g RETURNING id, substr(slug, 6)::int AS g
		)
		INSERT INTO card_revision (card_id, data, content_hash, source) SELECT id, `+data+`, md5(id::text), 'ui' FROM c`, n); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.pool.Exec(ctx, `UPDATE card c SET head_revision_id = r.id FROM card_revision r
		WHERE c.head_revision_id IS NULL AND r.card_id = c.id`); err != nil {
		t.Fatal(err)
	}
}

func TestFacetCountsPastAThousandAreExact(t *testing.T) {
	lib := newLibrary(t)
	bulkCards(t, lib, 1005, `'{"name": "Bulk", "tags": ["bulk"]}'::jsonb`)
	res, err := lib.Search(context.Background(), Card, Query{Facets: []string{"tags"}})
	if err != nil || len(res.Facets.Tags) != 1 || res.Facets.Tags[0].Cards != 1005 || res.Total != 1005 {
		t.Fatalf("results = total %d, facets %+v, %v", res.Total, res.Facets, err)
	}
}

func TestCursorRejectsOutOfRangeRank(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	seedSearch(t, lib)
	for _, rank := range []string{"1e-50", "1e39"} {
		cursor := base64.RawURLEncoding.EncodeToString([]byte(`[` + rank + `, 5]`))
		if _, err := lib.Search(ctx, Card, Query{Text: "dwarf", Page: Page{Cursor: &cursor}}); !errors.Is(err, ErrInvalid) {
			t.Errorf("rank %s: %v", rank, err)
		}
	}
	cursor := base64.RawURLEncoding.EncodeToString([]byte(`[1e-45, 5]`))
	if _, err := lib.Search(ctx, Card, Query{Text: "dwarf", Page: Page{Cursor: &cursor}}); err != nil {
		t.Errorf("smallest real: %v", err)
	}
}

// Search keeps only the start of very long text, so a card or lorebook of
// several megabytes still saves.
func TestHugeCardAndLorebookStillSave(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	var words strings.Builder
	for i := range 250_000 {
		fmt.Fprintf(&words, "word%d ", i)
	}
	text, _ := json.Marshal(words.String())
	if _, _, err := lib.Cards.Create(ctx, DefaultLibrary, "huge", "upload",
		edit(fmt.Sprintf(`{"name": "Huge", "description": %s, "first_mes": %s}`, text, text))); err != nil {
		t.Fatalf("create a card of %d bytes: %v", 2*len(text), err)
	}
	if _, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "huge", "upload",
		edit(fmt.Sprintf(`{"name": "Huge", "entries": [{"keys": ["k"], "content": %s}]}`, text))); err != nil {
		t.Fatalf("create a lorebook of %d bytes: %v", len(text), err)
	}
}

func TestSemanticSearchPagesByCursor(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	near, _ := newCard(t, lib, "near", `{"name": "Brakka"}`)
	mid, _ := newCard(t, lib, "mid", `{"name": "Dorna"}`)
	far, _ := newCard(t, lib, "far", `{"name": "Lia"}`)
	newCard(t, lib, "unembedded", `{"name": "Grum"}`)
	embedCards(t, lib, inst, map[int64][]float32{near: {1, 0}, mid: {0.7, 0.7}, far: {0, 1}})

	q := Query{Text: "a dwarf smith", Semantic: true, Embedding: []float32{1, 0.1}, EmbedInstance: inst, Page: Page{Size: 2}}
	first, err := lib.Search(ctx, Card, q)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(hitIDs(first), []int64{near, mid}) || first.Total != 3 || first.NextCursor == nil {
		t.Fatalf("first page = %+v", first)
	}
	q.Page.Cursor = first.NextCursor
	second, err := lib.Search(ctx, Card, q)
	if err != nil || !slices.Equal(hitIDs(second), []int64{far}) || second.NextCursor != nil {
		t.Fatalf("second page = %+v, %v", second, err)
	}
}

func TestSemanticSearchSkipsVectorsOfAnotherLength(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	two, _ := newCard(t, lib, "two", `{"name": "Brakka"}`)
	three, _ := newCard(t, lib, "three", `{"name": "Dorna"}`)
	embedCards(t, lib, inst, map[int64][]float32{two: {1, 0}, three: {1, 0, 0}})

	res, err := lib.Search(ctx, Card, Query{Text: "smith", Semantic: true, Embedding: []float32{1, 0}, EmbedInstance: inst})
	if err != nil || !slices.Equal(hitIDs(res), []int64{two}) {
		t.Fatalf("results = %+v, %v", res, err)
	}
}

func TestSemanticSearchPagesPastVectorsOfAnotherLength(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	vectors := map[int64][]float32{}
	want := map[int64]bool{}
	for i := range 4 {
		two, _ := newCard(t, lib, fmt.Sprintf("two%d", i), `{"name": "Brakka"}`)
		three, _ := newCard(t, lib, fmt.Sprintf("three%d", i), `{"name": "Dorna"}`)
		vectors[two] = []float32{1, float32(i)}
		vectors[three] = []float32{1, 0, float32(i)}
		want[two] = true
	}
	embedCards(t, lib, inst, vectors)

	q := Query{Text: "smith", Semantic: true, Embedding: []float32{1, 0}, EmbedInstance: inst, Page: Page{Size: 1}}
	got := 0
	for {
		res, err := lib.Search(ctx, Card, q)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range hitIDs(res) {
			if !want[id] {
				t.Fatalf("card %d has a vector of another length", id)
			}
			got++
		}
		if res.NextCursor == nil {
			break
		}
		q.Page.Cursor = res.NextCursor
	}
	if got != len(want) {
		t.Fatalf("paged %d cards, want %d", got, len(want))
	}
}

func TestSemanticSearchGroupsFamilies(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	root, _ := newCard(t, lib, "root", `{"name": "Brakka"}`)
	variant, _ := newCard(t, lib, "variant", `{"name": "Brakka, older"}`)
	other, _ := newCard(t, lib, "other", `{"name": "Lia"}`)
	if err := lib.Cards.SetVariantOf(ctx, variant, &root); err != nil {
		t.Fatal(err)
	}
	embedCards(t, lib, inst, map[int64][]float32{root: {0, 1}, variant: {1, 0}, other: {0.5, 0.5}})

	res, err := lib.Search(ctx, Card, Query{Text: "smith", Semantic: true, Embedding: []float32{1, 0}, EmbedInstance: inst, Group: "family"})
	if err != nil || !slices.Equal(hitIDs(res), []int64{root, other}) || res.Total != 2 {
		t.Fatalf("results = %+v, %v", res, err)
	}
}

func TestSemanticSearchCantSelectCards(t *testing.T) {
	lib := newLibrary(t)
	q := Query{Text: "smith", Semantic: true}
	if _, err := lib.SearchIDs(context.Background(), Card, q); !errors.Is(err, ErrInvalid) {
		t.Fatalf("SearchIDs: got %v, want ErrInvalid", err)
	}
}
