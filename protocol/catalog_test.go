package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func goodCatalog() CatalogInfo {
	return CatalogInfo{
		Kinds: []string{KindCard, KindLorebook},
		Filters: []CatalogFilter{
			{Name: "site", Label: "Site", Type: FilterEnum, Options: []CatalogOption{{Value: "chub", Label: "Chub"}}},
			{Name: "tag", Label: "Tag", Type: FilterText},
			{Name: "added", Label: "Date added", Type: FilterDateRange},
			{Name: "rating", Label: "Rating", Type: FilterNumberRange},
		},
		Sorts: []CatalogOption{{Value: "newest", Label: "Newest"}, {Value: "name", Label: "Name"}},
	}
}

func TestCatalogInfoValidate(t *testing.T) {
	if err := goodCatalog().Validate(); err != nil {
		t.Fatalf("good catalog: %v", err)
	}
	tests := []struct {
		name    string
		change  func(*CatalogInfo)
		wantErr string
	}{
		{"no kinds", func(c *CatalogInfo) { c.Kinds = nil }, "kinds is empty"},
		{"bad kind", func(c *CatalogInfo) { c.Kinds = []string{"Card"} }, `kind "Card" must be lowercase snake_case`},
		{"repeated kind", func(c *CatalogInfo) { c.Kinds = []string{"card", "card"} }, `kind "card" listed twice`},
		{"unknown filter type", func(c *CatalogInfo) { c.Filters[1].Type = "regex" }, `type "regex" must be one of`},
		{"enum without options", func(c *CatalogInfo) { c.Filters[0].Options = nil }, "only an enum has them"},
		{"options on text", func(c *CatalogInfo) { c.Filters[1].Options = []CatalogOption{{Value: "a", Label: "A"}} }, "only an enum has them"},
		{"filter without label", func(c *CatalogInfo) { c.Filters[1].Label = "" }, `filter "tag" has no label`},
		{"repeated option", func(c *CatalogInfo) { c.Filters[0].Options = append(c.Filters[0].Options, c.Filters[0].Options[0]) }, `option "chub" listed twice`},
		{"repeated filter", func(c *CatalogInfo) { c.Filters[1].Name = "site" }, `filter "site" listed twice`},
		{"no sorts", func(c *CatalogInfo) { c.Sorts = nil }, "sorts is empty"},
		{"sort without label", func(c *CatalogInfo) { c.Sorts[0].Label = "" }, "needs a value and a label"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := goodCatalog()
			tt.change(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func filters(kv ...string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for i := 0; i < len(kv); i += 2 {
		out[kv[i]] = json.RawMessage(kv[i+1])
	}
	return out
}

func TestCheckSearch(t *testing.T) {
	ok := CatalogSearchParams{
		Q: "dwarf", Kind: KindCard, Sort: "name", Page: CatalogPage{Size: MaxCatalogPage},
		Filters: filters("site", `"chub"`, "tag", `"smith"`, "added", `{"from": "2024-01-01"}`, "rating", `{"min": 1, "max": 5}`),
	}
	if err := goodCatalog().CheckSearch(ok); err != nil {
		t.Fatalf("good search: %v", err)
	}
	if err := goodCatalog().CheckSearch(CatalogSearchParams{}); err != nil {
		t.Fatalf("empty search: %v", err)
	}
	tests := []struct {
		name    string
		p       CatalogSearchParams
		wantErr string
	}{
		{"undeclared kind", CatalogSearchParams{Kind: "preset"}, `kind "preset" is not declared`},
		{"undeclared sort", CatalogSearchParams{Sort: "random"}, `sort "random" is not declared`},
		{"page too big", CatalogSearchParams{Page: CatalogPage{Size: MaxCatalogPage + 1}}, "page size"},
		{"negative page", CatalogSearchParams{Page: CatalogPage{Size: -1}}, "page size"},
		{"undeclared filter", CatalogSearchParams{Filters: filters("color", `"red"`)}, `filter "color" is not declared`},
		{"enum value not an option", CatalogSearchParams{Filters: filters("site", `"janitor"`)}, "not one of its options"},
		{"text not a string", CatalogSearchParams{Filters: filters("tag", `3`)}, "want a non-empty string"},
		{"null text", CatalogSearchParams{Filters: filters("tag", `null`)}, "want a non-empty string"},
		{"enum not a string", CatalogSearchParams{Filters: filters("site", `1`)}, "want a non-empty string"},
		{"null date range", CatalogSearchParams{Filters: filters("added", `null`)}, "at least one date"},
		{"empty text", CatalogSearchParams{Filters: filters("tag", `""`)}, "want a non-empty string"},
		{"empty date range", CatalogSearchParams{Filters: filters("added", `{}`)}, "at least one date"},
		{"bad date", CatalogSearchParams{Filters: filters("added", `{"to": "May 1"}`)}, "must be YYYY-MM-DD"},
		{"backward dates", CatalogSearchParams{Filters: filters("added", `{"from": "2025-01-02", "to": "2025-01-01"}`)}, "from is after to"},
		{"empty number range", CatalogSearchParams{Filters: filters("rating", `{}`)}, "at least one number"},
		{"min not a number", CatalogSearchParams{Filters: filters("rating", `{"min": "1"}`)}, "at least one number"},
		{"backward numbers", CatalogSearchParams{Filters: filters("rating", `{"min": 5, "max": 1}`)}, "min is above max"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := goodCatalog().CheckSearch(tt.p)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestCatalogItemValidate(t *testing.T) {
	kinds := []string{KindCard}
	good := CatalogItem{ID: "chub:1", Kind: KindCard, Name: "Brakka", LatestVersion: "3",
		Hashes: CatalogHashes{ContentHash: strings.Repeat("ab", 32)}}
	if err := good.Validate(kinds); err != nil {
		t.Fatalf("good item: %v", err)
	}
	tests := []struct {
		name    string
		change  func(*CatalogItem)
		wantErr string
	}{
		{"no id", func(it *CatalogItem) { it.ID = "" }, "id is empty"},
		{"undeclared kind", func(it *CatalogItem) { it.Kind = KindLorebook }, `kind "lorebook" is not declared`},
		{"no name", func(it *CatalogItem) { it.Name = "" }, "name is empty"},
		{"no version", func(it *CatalogItem) { it.LatestVersion = "" }, "latest_version is empty"},
		{"uppercase hash", func(it *CatalogItem) { it.Hashes.ContentHash = strings.Repeat("AB", 32) }, "64 lowercase hex digits"},
		{"big thumbnail", func(it *CatalogItem) { it.Thumbnail = make([]byte, MaxThumbnail+1) }, "thumbnail is"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			it := good
			tt.change(&it)
			err := it.Validate(kinds)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want %q", err, tt.wantErr)
			}
		})
	}
}
