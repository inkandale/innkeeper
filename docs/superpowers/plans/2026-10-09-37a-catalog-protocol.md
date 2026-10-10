# 37a: Catalog protocol — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plugins can declare the `catalog` capability, compute library hashes with a public `cardhash` package, and be checked by `innkeeper plugin check`.

**Architecture:** `internal/cardhash` moves to `cardhash/` so plugins can import it. `protocol/catalog.go` adds the catalog methods, types, and the validation that both core and plugins use. The fake plugin gains a three-item catalog, and the contract kit gains catalog checks that run against it. Core routes, adopt, and the plugins come in later plans.

**Tech Stack:** Go.

Spec: `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, build step 1. Steps 2 to 5 each get their own plan (37b to 37e) once the previous one merges.

**Changes from the spec.** Task 6 updates the spec to match:

- `catalog.get` returns the item as CCv3 (`data`) and, when `avatar` is set, its image as a file in `blob_tmp`, the same way `target.get` does. It doesn't return raw file bytes, so adopt has no decode step and no 422.
- `catalog.similar` takes a `kind`.
- `catalog.ingest` takes a `run` token, like `import.run`.
- `thumbnail` is an inline JPEG of at most 32 KiB.
- Unknown IDs return `-32004`, the code targets already use for a missing `remote_id`.
- `innkeeper plugin check` reads `INNKEEPER_PLUGIN_CONFIG` so a plugin's CI can point it at test data.

**Do not** `git checkout` other branches or move HEAD; work on `inn-37-archive-spec`.

## Files

| File | Change |
|---|---|
| `internal/cardhash/` → `cardhash/` | Moved, with its tests and `testdata` |
| 15 importers of `internal/cardhash` | Import path |
| `protocol/manifest.go` | `catalog` capability |
| `protocol/catalog.go`, `catalog_test.go` (new) | Methods, types, `Validate`, `CheckSearch` |
| `protocol/target.go` | `CodeRemoteNotFound` comment covers catalogs |
| `internal/fakeplugin/catalog.go` (new), `main.go` | Fake catalog and its modes |
| `internal/contract/contract.go`, `contract_test.go` | Catalog checks, `Config` |
| `internal/contract/catalog.go` (new) | The catalog check functions |
| `cmd/innkeeper/main.go` | `INNKEEPER_PLUGIN_CONFIG` |
| `docs/protocol.md` | `catalog` section, check list |
| `docs/superpowers/specs/2026-10-09-archive-catalog-design.md` | The changes above |

---

### Task 1: Public `cardhash`

**Files:** Move `internal/cardhash/` to `cardhash/`; modify every file that imports it.

- [ ] **Step 1: Move the package**

```bash
git mv internal/cardhash cardhash
grep -rl 'innkeeper/internal/cardhash' --include='*.go' . | xargs sed -i '' 's|innkeeper/internal/cardhash|innkeeper/cardhash|'
gofmt -w $(grep -rl 'innkeeper/cardhash' --include='*.go' .)
sed -i '' 's|filepath.Join("..", "cardhash", "testdata"|filepath.Join("..", "..", "cardhash", "testdata"|' internal/library/blobs_test.go internal/library/thumbs_test.go internal/library/organize_test.go internal/api/organize_test.go
```

`gofmt -w` re-sorts the changed import within its group. The four tests in the `sed` line open `cardhash/testdata/avatar.png` by relative path. They're database tests, so without `INNKEEPER_TEST_DATABASE_URL` they skip and look green.

- [ ] **Step 2: Reword the package comment**

In `cardhash/cardhash.go`, replace the first three lines:

```go
// Package cardhash computes the duplicate-detection fingerprints for cards
// and lorebooks. Catalog plugins import it so their hashes match the
// library's; the test vectors in this package pin each algorithm.
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./cardhash ./internal/library/... ./internal/targets/... ./internal/corpus/...`
Expected: PASS. `grep -rn internal/cardhash --include='*.go' .` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add -A cardhash internal
git commit -m "refactor: make cardhash a public package for catalog plugins"
```

---

### Task 2: Catalog protocol types

**Files:**
- Create: `protocol/catalog.go`, `protocol/catalog_test.go`
- Modify: `protocol/manifest.go:16`, `protocol/target.go:26`

- [ ] **Step 1: Write the failing tests**

`protocol/catalog_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to see them fail**

Run: `go test ./protocol -run 'Catalog|CheckSearch'`
Expected: FAIL, `undefined: CatalogInfo`.

- [ ] **Step 3: Add the capability**

In `protocol/manifest.go:16`:

```go
var Capabilities = []string{"import", "search", "push", "pull", "action", "enrich", "codec", "auth", "mcp", "theme", "catalog"}
```

In `protocol/target.go`, replace the `CodeRemoteNotFound` comment:

```go
// CodeRemoteNotFound is the error for an ID the plugin doesn't have: a
// remote_id the app no longer has, or a catalog item or version.
```

- [ ] **Step 4: Write `protocol/catalog.go`**

```go
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"
)

// The catalog capability serves a collection of cards and lorebooks the
// owner searches and adopts from. catalog.similar and catalog.ingest are
// optional; catalog.describe says whether the plugin answers them.
const (
	MethodCatalogDescribe = "catalog.describe"
	MethodCatalogSearch   = "catalog.search"
	MethodCatalogGet      = "catalog.get"
	MethodCatalogSimilar  = "catalog.similar"
	MethodCatalogIngest   = "catalog.ingest"
)

// Item kinds the core can adopt. A catalog may declare others, which a later
// core may adopt.
const (
	KindCard     = "card"
	KindLorebook = "lorebook"
)

// Filter types.
const (
	FilterEnum        = "enum"
	FilterText        = "text"
	FilterDateRange   = "date_range"
	FilterNumberRange = "number_range"
)

var filterTypes = []string{FilterEnum, FilterText, FilterDateRange, FilterNumberRange}

const (
	// MaxCatalogPage is the largest page size catalog.search accepts.
	MaxCatalogPage = 100
	// MaxThumbnail is the largest thumbnail in bytes.
	MaxThumbnail = 32 << 10
)

var contentHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// CatalogInfo is the reply to catalog.describe.
type CatalogInfo struct {
	Kinds   []string        `json:"kinds"`
	Filters []CatalogFilter `json:"filters"`
	// Sorts are the orders search accepts. The first is the default.
	Sorts   []CatalogOption `json:"sorts"`
	Ingest  bool            `json:"ingest"`
	Similar bool            `json:"similar"`
}

// CatalogFilter is one filter search accepts. Only an enum has Options.
type CatalogFilter struct {
	Name    string          `json:"name"`
	Label   string          `json:"label"`
	Type    string          `json:"type"`
	Options []CatalogOption `json:"options,omitempty"`
}

// CatalogOption is an enum filter's value or a sort order.
type CatalogOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Validate reports every problem with the description at once.
func (c CatalogInfo) Validate() error {
	var errs []error
	if len(c.Kinds) == 0 {
		errs = append(errs, errors.New("kinds is empty"))
	}
	errs = append(errs, uniqueKeys("kind", c.Kinds)...)
	names := make([]string, len(c.Filters))
	for i, f := range c.Filters {
		names[i] = f.Name
		if f.Label == "" {
			errs = append(errs, fmt.Errorf("filter %q has no label", f.Name))
		}
		if !slices.Contains(filterTypes, f.Type) {
			errs = append(errs, fmt.Errorf("filter %q: type %q must be one of %v", f.Name, f.Type, filterTypes))
		}
		if (f.Type == FilterEnum) != (len(f.Options) > 0) {
			errs = append(errs, fmt.Errorf("filter %q: an enum needs options, and only an enum has them", f.Name))
		}
		errs = append(errs, checkOptions("filter "+f.Name+" option", f.Options)...)
	}
	errs = append(errs, uniqueKeys("filter", names)...)
	if len(c.Sorts) == 0 {
		errs = append(errs, errors.New("sorts is empty"))
	}
	errs = append(errs, checkOptions("sort", c.Sorts)...)
	return errors.Join(errs...)
}

// uniqueKeys wants each key in lowercase snake_case and listed once.
func uniqueKeys(what string, keys []string) []error {
	var errs []error
	seen := map[string]bool{}
	for _, k := range keys {
		if !keyPattern.MatchString(k) {
			errs = append(errs, fmt.Errorf("%s %q must be lowercase snake_case", what, k))
		}
		if seen[k] {
			errs = append(errs, fmt.Errorf("%s %q listed twice", what, k))
		}
		seen[k] = true
	}
	return errs
}

func checkOptions(what string, opts []CatalogOption) []error {
	var errs []error
	seen := map[string]bool{}
	for _, o := range opts {
		if o.Value == "" || o.Label == "" {
			errs = append(errs, fmt.Errorf("%s %q needs a value and a label", what, o.Value))
		}
		if seen[o.Value] {
			errs = append(errs, fmt.Errorf("%s %q listed twice", what, o.Value))
		}
		seen[o.Value] = true
	}
	return errs
}

// CatalogSearchParams asks for one page of items. Every field is optional:
// an empty Q matches everything, an empty Kind every kind, and an empty Sort
// means the first declared sort.
type CatalogSearchParams struct {
	Q       string                     `json:"q,omitempty"`
	Kind    string                     `json:"kind,omitempty"`
	Filters map[string]json.RawMessage `json:"filters,omitempty"`
	Sort    string                     `json:"sort,omitempty"`
	Page    CatalogPage                `json:"page"`
}

// CatalogPage is a page size, 0 for the catalog's default, and the previous
// page's Next cursor, empty for the first page.
type CatalogPage struct {
	Size   int    `json:"size,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// DateRange is a date_range filter's value. Dates are YYYY-MM-DD, both
// inclusive; either may be empty, not both.
type DateRange struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
}

// NumberRange is a number_range filter's value, both ends inclusive; either
// may be absent, not both.
type NumberRange struct {
	Min *float64 `json:"min,omitempty"`
	Max *float64 `json:"max,omitempty"`
}

// CheckSearch reports why a catalog must refuse p with -32602: a kind, sort,
// or filter it didn't declare, a filter value of the wrong shape, or a page
// size out of range.
func (c CatalogInfo) CheckSearch(p CatalogSearchParams) error {
	var errs []error
	if p.Kind != "" && !slices.Contains(c.Kinds, p.Kind) {
		errs = append(errs, fmt.Errorf("kind %q is not declared", p.Kind))
	}
	if p.Sort != "" && !slices.ContainsFunc(c.Sorts, func(o CatalogOption) bool { return o.Value == p.Sort }) {
		errs = append(errs, fmt.Errorf("sort %q is not declared", p.Sort))
	}
	if p.Page.Size < 0 || p.Page.Size > MaxCatalogPage {
		errs = append(errs, fmt.Errorf("page size %d must be 0 to %d", p.Page.Size, MaxCatalogPage))
	}
	for _, name := range slices.Sorted(maps.Keys(p.Filters)) {
		raw := p.Filters[name]
		i := slices.IndexFunc(c.Filters, func(f CatalogFilter) bool { return f.Name == name })
		if i < 0 {
			errs = append(errs, fmt.Errorf("filter %q is not declared", name))
		} else if err := c.Filters[i].check(raw); err != nil {
			errs = append(errs, fmt.Errorf("filter %q: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

func (f CatalogFilter) check(raw json.RawMessage) error {
	switch f.Type {
	case FilterEnum, FilterText:
		var s string
		if err := json.Unmarshal(raw, &s); err != nil || s == "" {
			return errors.New("want a non-empty string")
		}
		if f.Type == FilterEnum && !slices.ContainsFunc(f.Options, func(o CatalogOption) bool { return o.Value == s }) {
			return fmt.Errorf("%q is not one of its options", s)
		}
	case FilterDateRange:
		var r DateRange
		if err := json.Unmarshal(raw, &r); err != nil || r == (DateRange{}) {
			return errors.New(`want {"from", "to"} with at least one date`)
		}
		from, errFrom := parseDate(r.From)
		to, errTo := parseDate(r.To)
		if err := errors.Join(errFrom, errTo); err != nil {
			return err
		}
		if r.From != "" && r.To != "" && from.After(to) {
			return errors.New("from is after to")
		}
	case FilterNumberRange:
		var r NumberRange
		if err := json.Unmarshal(raw, &r); err != nil || r.Min == nil && r.Max == nil {
			return errors.New(`want {"min", "max"} with at least one number`)
		}
		if r.Min != nil && r.Max != nil && *r.Min > *r.Max {
			return errors.New("min is above max")
		}
	}
	return nil
}

func parseDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return t, fmt.Errorf("date %q must be YYYY-MM-DD", s)
	}
	return t, nil
}

// CatalogItem is one item as search lists it.
type CatalogItem struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Author  string   `json:"author,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Summary string   `json:"summary,omitempty"`
	// LatestVersion names the newest version. Hashes are that version's.
	LatestVersion string        `json:"latest_version"`
	Hashes        CatalogHashes `json:"hashes"`
	// Thumbnail is a JPEG at most 160 pixels wide and MaxThumbnail bytes.
	Thumbnail []byte `json:"thumbnail,omitempty"`
}

// CatalogHashes are an item's cardhash fingerprints, computed as the library
// does: over a card's data object, or over the whole lorebook. The simhash
// and phash are the 64 bits cardhash returns, read as a signed int64.
type CatalogHashes struct {
	ContentHash string `json:"content_hash"`
	TextSimhash *int64 `json:"text_simhash,omitempty"`
	ImagePhash  *int64 `json:"image_phash,omitempty"`
}

// Validate checks the item against the catalog's declared kinds.
func (it CatalogItem) Validate(kinds []string) error {
	var errs []error
	if it.ID == "" {
		errs = append(errs, errors.New("id is empty"))
	}
	if !slices.Contains(kinds, it.Kind) {
		errs = append(errs, fmt.Errorf("kind %q is not declared", it.Kind))
	}
	if it.Name == "" {
		errs = append(errs, errors.New("name is empty"))
	}
	if it.LatestVersion == "" {
		errs = append(errs, errors.New("latest_version is empty"))
	}
	if !contentHashPattern.MatchString(it.Hashes.ContentHash) {
		errs = append(errs, fmt.Errorf("content_hash %q must be 64 lowercase hex digits", it.Hashes.ContentHash))
	}
	if len(it.Thumbnail) > MaxThumbnail {
		errs = append(errs, fmt.Errorf("thumbnail is %d bytes, over %d", len(it.Thumbnail), MaxThumbnail))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("item %q: %w", it.ID, err)
	}
	return nil
}

// CatalogSearchResult is one page. Next is the cursor for the following
// page, or empty on the last.
type CatalogSearchResult struct {
	Items []CatalogItem `json:"items"`
	Next  string        `json:"next,omitempty"`
}

// CatalogGetParams asks for one version of an item, the latest if Version is
// empty. Avatar asks for its image too; only adopt sets it.
type CatalogGetParams struct {
	ID      string `json:"id"`
	Version string `json:"version,omitempty"`
	Avatar  bool   `json:"avatar,omitempty"`
}

// CatalogGetResult is one version of an item. Data is a CCv3 card for kind
// card, or a CCv3 character_book for kind lorebook. Avatar is a file in
// blob_tmp, or empty.
type CatalogGetResult struct {
	Item     CatalogItem      `json:"item"`
	Version  string           `json:"version"`
	Versions []CatalogVersion `json:"versions"`
	Data     json.RawMessage  `json:"data"`
	Avatar   string           `json:"avatar,omitempty"`
}

// CatalogVersion is one version of an item; versions list newest first.
// Created is RFC 3339, or empty if unknown.
type CatalogVersion struct {
	Version string `json:"version"`
	Created string `json:"created,omitempty"`
}

// CatalogSimilarParams asks for items of Kind with the same content_hash, or
// within MaxDistance bits of either other hash. Limit is 1 to MaxCatalogPage.
type CatalogSimilarParams struct {
	Kind        string        `json:"kind"`
	Hashes      CatalogHashes `json:"hashes"`
	MaxDistance int           `json:"max_distance"`
	Limit       int           `json:"limit"`
}

// CatalogSimilarResult lists at most Limit matches, closest first.
type CatalogSimilarResult struct {
	Items []CatalogItem `json:"items"`
}

// CatalogIngestParams starts an ingest. Run and Checkpoint work as in
// ImportRunParams, and so do the progress and checkpoint notifications.
type CatalogIngestParams struct {
	Run        string          `json:"run"`
	Checkpoint json.RawMessage `json:"checkpoint"`
}

// CatalogIngestResult counts what the ingest changed.
type CatalogIngestResult struct {
	Added   int64 `json:"added"`
	Updated int64 `json:"updated"`
	Removed int64 `json:"removed"`
	Skipped int64 `json:"skipped"`
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./protocol`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add protocol
git commit -m "feat(protocol): add the catalog capability"
```

---

### Task 3: Fake catalog

The contract tests in Task 4 and the API tests in 37b run against it. It serves three fixed items, so it needs no config.

**Files:**
- Create: `internal/fakeplugin/catalog.go`
- Modify: `internal/fakeplugin/main.go` (the `-mode` usage string and the handler switch)

- [ ] **Step 1: Write `internal/fakeplugin/catalog.go`**

```go
package main

import (
	"cmp"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/davidgibbons/innkeeper/cardhash"
	"github.com/davidgibbons/innkeeper/protocol"
)

// The fake catalog. Each mode breaks one rule the contract kit checks:
// bad-catalog describes nothing, catalog-repeat serves the first page for
// every cursor, catalog-lax accepts any filter, catalog-no-notfound answers
// unknown IDs with -32000, and catalog-wrong-hash reports a content_hash
// that doesn't match the data.
var catalogInfo = protocol.CatalogInfo{
	Kinds:   []string{protocol.KindCard, protocol.KindLorebook},
	Filters: []protocol.CatalogFilter{{Name: "tag", Label: "Tag", Type: protocol.FilterText}},
	Sorts:   []protocol.CatalogOption{{Value: "name", Label: "Name"}},
	Similar: true,
}

type catalogEntry struct {
	id, kind string
	tags     []string
	// versions holds each version's data, oldest first; version n is
	// versions[n-1].
	versions []string
}

// Sorted by name, the only sort.
var catalogEntries = []catalogEntry{
	{"brakka", protocol.KindCard, []string{"dwarf"}, []string{
		`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka", "description": "A dwarf blacksmith."}}`,
		`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka", "description": "A grumpy dwarf blacksmith who runs the forge at the edge of town."}}`,
	}},
	{"carrow", protocol.KindCard, []string{"elf"}, []string{
		`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Carrow", "description": "A cheerful elven bard who sings of the sea."}}`,
	}},
	{"forge-lore", protocol.KindLorebook, []string{"dwarf"}, []string{
		`{"name": "Forge Lore", "entries": [{"keys": ["forge"], "content": "The forge at the edge of town never cools."}]}`,
	}},
}

// hashed is the part of an item's data the library hashes: a card's data
// object, or the whole lorebook.
func hashed(kind, raw string) json.RawMessage {
	if kind != protocol.KindCard {
		return json.RawMessage(raw)
	}
	var c struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal([]byte(raw), &c)
	return c.Data
}

func (e catalogEntry) item(mode string) protocol.CatalogItem {
	data := hashed(e.kind, e.versions[len(e.versions)-1])
	if mode == "catalog-wrong-hash" {
		data = json.RawMessage(`{"name": "Wrong"}`)
	}
	var named struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &named)
	hash, _ := cardhash.ContentHash(data)
	it := protocol.CatalogItem{ID: e.id, Kind: e.kind, Name: cmp.Or(named.Name, e.id), Tags: e.tags,
		LatestVersion: strconv.Itoa(len(e.versions)), Hashes: protocol.CatalogHashes{ContentHash: hash}}
	simhash := cardhash.CardSimhash
	if e.kind == protocol.KindLorebook {
		simhash = cardhash.LorebookSimhash
	}
	if h, ok, _ := simhash(data); ok {
		it.Hashes.TextSimhash = &h
	}
	return it
}

func catalogSearch(params json.RawMessage, mode string) (any, error) {
	var p protocol.CatalogSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	if mode != "catalog-lax" {
		if err := catalogInfo.CheckSearch(p); err != nil {
			return nil, invalidParams(err)
		}
	}
	var tag string
	_ = json.Unmarshal(p.Filters["tag"], &tag)
	items := []protocol.CatalogItem{}
	for _, e := range catalogEntries {
		it := e.item(mode)
		if (p.Kind == "" || p.Kind == e.kind) && (tag == "" || slices.Contains(e.tags, tag)) &&
			strings.Contains(strings.ToLower(it.Name), strings.ToLower(p.Q)) {
			items = append(items, it)
		}
	}
	start, _ := strconv.Atoi(p.Page.Cursor)
	if mode == "catalog-repeat" {
		start = 0
	}
	start = min(max(start, 0), len(items))
	end := min(start+cmp.Or(p.Page.Size, protocol.MaxCatalogPage), len(items))
	res := protocol.CatalogSearchResult{Items: items[start:end]}
	if end < len(items) {
		res.Next = strconv.Itoa(end)
	}
	return res, nil
}

func catalogGet(blobTmp string, params json.RawMessage, mode string) (any, error) {
	var p protocol.CatalogGetParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	i := slices.IndexFunc(catalogEntries, func(e catalogEntry) bool { return e.id == p.ID })
	if i < 0 {
		code := protocol.CodeRemoteNotFound
		if mode == "catalog-no-notfound" {
			code = -32000
		}
		return nil, protocol.NewError(code, "no item "+p.ID, false)
	}
	e := catalogEntries[i]
	n := len(e.versions)
	if p.Version != "" {
		var err error
		if n, err = strconv.Atoi(p.Version); err != nil || n < 1 || n > len(e.versions) {
			return nil, protocol.NewError(protocol.CodeRemoteNotFound, "no version "+p.Version, false)
		}
	}
	res := protocol.CatalogGetResult{Item: e.item(mode), Version: strconv.Itoa(n), Data: json.RawMessage(e.versions[n-1])}
	for v := len(e.versions); v >= 1; v-- {
		res.Versions = append(res.Versions, protocol.CatalogVersion{Version: strconv.Itoa(v)})
	}
	if p.Avatar && e.kind == protocol.KindCard {
		// The core moves the file, so each call writes a new one.
		f, err := os.CreateTemp(blobTmp, "catalog-*.png")
		if err != nil {
			return nil, err
		}
		defer f.Close()
		if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			return nil, err
		}
		res.Avatar = f.Name()
	}
	return res, nil
}

func catalogSimilar(params json.RawMessage, mode string) (any, error) {
	var p protocol.CatalogSimilarParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	near := func(a, b *int64) bool { return a != nil && b != nil && cardhash.Distance(*a, *b) <= p.MaxDistance }
	res := protocol.CatalogSimilarResult{Items: []protocol.CatalogItem{}}
	for _, e := range catalogEntries {
		it := e.item(mode)
		if e.kind == p.Kind && len(res.Items) < p.Limit && (it.Hashes.ContentHash == p.Hashes.ContentHash ||
			near(it.Hashes.TextSimhash, p.Hashes.TextSimhash) || near(it.Hashes.ImagePhash, p.Hashes.ImagePhash)) {
			res.Items = append(res.Items, it)
		}
	}
	return res, nil
}
```

`fakeplugin` doesn't sort matches by distance. Its items are far enough apart that a test never gets more than one.

- [ ] **Step 2: Wire it into the handler**

In `internal/fakeplugin/main.go`, add the new modes to the end of the `-mode` usage string:

```go
	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | slow-init | bad-errors | silent | crash-unknown | no-shutdown | bad-describe | bad-tools | bad-codec | codec-hard-link | codec-no-avatar | codec-wrong-code | database | bad-list | bad-setup | bad-catalog | catalog-repeat | catalog-lax | catalog-no-notfound | catalog-wrong-hash")
```

and add these cases to the handler switch, after `case protocol.MethodCodecDecode:`:

```go
		case protocol.MethodCatalogDescribe:
			if *mode == "bad-catalog" {
				return protocol.CatalogInfo{}, nil
			}
			return catalogInfo, nil
		case protocol.MethodCatalogSearch:
			return catalogSearch(params, *mode)
		case protocol.MethodCatalogGet:
			return catalogGet(blobTmp, params, *mode)
		case protocol.MethodCatalogSimilar:
			return catalogSimilar(params, *mode)
```

- [ ] **Step 3: Build**

Run: `go build -o /dev/null ./internal/fakeplugin && go vet ./internal/fakeplugin`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/fakeplugin
git commit -m "test: give the fake plugin a catalog"
```

---

### Task 4: Contract checks

**Files:**
- Create: `internal/contract/catalog.go`
- Modify: `internal/contract/contract.go`, `internal/contract/contract_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/contract/contract_test.go`:

```go
func TestCatalogChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"catalog"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	// initialize, unknown method, six catalog checks, and two for shutdown.
	if len(results) != 10 {
		t.Fatalf("ran %d checks, want 10: %v", len(results), results)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	tests := []struct{ mode, check, wantErr string }{
		{"bad-catalog", catalogDescribeName, "kinds is empty"},
		{"catalog-repeat", catalogPagesName, "came back on a later page"},
		{"catalog-lax", catalogFiltersName, "got success"},
		{"catalog-no-notfound", catalogNotFoundName, "expected error -32004, got -32000"},
		{"catalog-wrong-hash", catalogGetName, "hash with the cardhash package"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"catalog"}, "-mode="+tt.mode), quiet)
			if err != nil {
				t.Fatal(err)
			}
			if got := failed(results)[tt.check]; got == nil || !strings.Contains(got.Error(), tt.wantErr) {
				t.Fatalf("check %q: got %v, want %q", tt.check, got, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/contract -run TestCatalogChecks`
Expected: FAIL, `undefined: catalogDescribeName`.

- [ ] **Step 3: Write `internal/contract/catalog.go`**

```go
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/davidgibbons/innkeeper/cardhash"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

const (
	catalogDescribeName = "catalog.describe returns a valid description"
	catalogPagesName    = "catalog.search pages without repeating items"
	catalogFiltersName  = "catalog.search refuses an undeclared filter with -32602"
	catalogNotFoundName = "catalog.get returns -32004 for an unknown ID"
	catalogGetName      = "catalog.get returns a search result's data and avatar"
	catalogSimilarName  = "catalog.similar finds an item by its own hashes"
)

// catalogPages is how many one-item pages the paging check walks.
const catalogPages = 3

// checkCatalog runs the catalog checks, stopping if the plugin dies.
func checkCatalog(ctx context.Context, p *pluginhost.Process, tmp string) []Result {
	var info protocol.CatalogInfo
	err := call(ctx, p, protocol.MethodCatalogDescribe, nil, &info)
	if err == nil {
		err = info.Validate()
	}
	results := []Result{{catalogDescribeName, err}}
	if err != nil {
		return results
	}
	var first *protocol.CatalogItem
	checks := []struct {
		name string
		run  func(context.Context) error
	}{
		{catalogPagesName, func(ctx context.Context) (err error) { first, err = checkCatalogPages(ctx, p, info); return err }},
		{catalogFiltersName, func(ctx context.Context) error { return checkCatalogFilters(ctx, p) }},
		{catalogNotFoundName, func(ctx context.Context) error { return checkCatalogNotFound(ctx, p) }},
		{catalogGetName, func(ctx context.Context) error { return checkCatalogGet(ctx, p, first, tmp) }},
	}
	if info.Similar {
		checks = append(checks, struct {
			name string
			run  func(context.Context) error
		}{catalogSimilarName, func(ctx context.Context) error { return checkCatalogSimilar(ctx, p, info, first) }})
	}
	for _, c := range checks {
		err := c.run(ctx)
		results = append(results, Result{c.name, err})
		if errors.Is(err, protocol.ErrClosed) || !p.Alive() {
			break
		}
	}
	return results
}

// answered reports whether err is a reply other than -32601. A catalog
// checked without its source may refuse a search that way.
func answered(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound
}

// checkCatalogPages walks one-item pages and returns the first item. Every
// item must be valid and none may come back on a later page.
func checkCatalogPages(ctx context.Context, p *pluginhost.Process, info protocol.CatalogInfo) (*protocol.CatalogItem, error) {
	var first *protocol.CatalogItem
	seen := map[string]bool{}
	cursor := ""
	for range catalogPages {
		var res protocol.CatalogSearchResult
		params := protocol.CatalogSearchParams{Page: protocol.CatalogPage{Size: 1, Cursor: cursor}}
		err := call(ctx, p, protocol.MethodCatalogSearch, params, &res)
		if cursor == "" && answered(err) {
			return nil, nil
		}
		if err != nil {
			return first, err
		}
		if len(res.Items) > 1 {
			return first, fmt.Errorf("asked for 1 item, got %d", len(res.Items))
		}
		for _, it := range res.Items {
			if err := it.Validate(info.Kinds); err != nil {
				return first, err
			}
			if seen[it.ID] {
				return first, fmt.Errorf("item %q came back on a later page", it.ID)
			}
			seen[it.ID] = true
			if first == nil {
				first = &it
			}
		}
		if res.Next == "" {
			break
		}
		cursor = res.Next
	}
	return first, nil
}

func checkCatalogFilters(ctx context.Context, p *pluginhost.Process) error {
	params := protocol.CatalogSearchParams{Filters: map[string]json.RawMessage{"innkeeper_contract_check": json.RawMessage(`"x"`)}}
	return expectCode(call(ctx, p, protocol.MethodCatalogSearch, params, nil), protocol.CodeInvalidParams)
}

func checkCatalogNotFound(ctx context.Context, p *pluginhost.Process) error {
	params := protocol.CatalogGetParams{ID: "innkeeper-contract-check-missing"}
	return expectCode(call(ctx, p, protocol.MethodCatalogGet, params, nil), protocol.CodeRemoteNotFound)
}

// checkCatalogGet fetches the first search result with its avatar and wants
// its latest data to hash to the content_hash search reported. It passes
// when search returned nothing.
func checkCatalogGet(ctx context.Context, p *pluginhost.Process, it *protocol.CatalogItem, tmp string) error {
	if it == nil {
		return nil
	}
	var res protocol.CatalogGetResult
	if err := call(ctx, p, protocol.MethodCatalogGet, protocol.CatalogGetParams{ID: it.ID, Avatar: true}, &res); err != nil {
		return err
	}
	if res.Item.ID != it.ID {
		return fmt.Errorf("asked for %q, got %q", it.ID, res.Item.ID)
	}
	if res.Version != it.LatestVersion {
		return fmt.Errorf("version is %q, want the latest, %q", res.Version, it.LatestVersion)
	}
	if !slices.ContainsFunc(res.Versions, func(v protocol.CatalogVersion) bool { return v.Version == res.Version }) {
		return fmt.Errorf("versions don't list version %q", res.Version)
	}
	data := res.Data
	switch it.Kind {
	case protocol.KindCard:
		var c struct {
			Spec string          `json:"spec"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(res.Data, &c); err != nil || c.Spec != "chara_card_v3" {
			return errors.New("data is not a chara_card_v3 card")
		}
		data = c.Data
	case protocol.KindLorebook:
	default:
		// The library has no hash for other kinds yet.
		data = nil
	}
	if data != nil {
		hash, err := cardhash.ContentHash(data)
		if err != nil {
			return fmt.Errorf("data: %w", err)
		}
		if hash != it.Hashes.ContentHash {
			return fmt.Errorf("data hashes to %s, but search reported %s; hash with the cardhash package", hash, it.Hashes.ContentHash)
		}
	}
	if res.Avatar == "" {
		return nil
	}
	if !inside(res.Avatar, tmp) {
		return fmt.Errorf("avatar %q is not directly inside blob_tmp (%s)", res.Avatar, tmp)
	}
	if err := plainFile(res.Avatar); err != nil {
		return fmt.Errorf("avatar: %w", err)
	}
	return nil
}

// checkCatalogSimilar looks up the first search result by its own hashes
// and wants it among the matches. With no item it only wants an answer.
func checkCatalogSimilar(ctx context.Context, p *pluginhost.Process, info protocol.CatalogInfo, it *protocol.CatalogItem) error {
	params := protocol.CatalogSimilarParams{Kind: info.Kinds[0],
		Hashes: protocol.CatalogHashes{ContentHash: strings.Repeat("0", 64)}, MaxDistance: 3, Limit: 10}
	if it != nil {
		params.Kind, params.Hashes = it.Kind, it.Hashes
	}
	var res protocol.CatalogSimilarResult
	err := call(ctx, p, protocol.MethodCatalogSimilar, params, &res)
	if it == nil && answered(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(res.Items) > params.Limit {
		return fmt.Errorf("asked for at most %d items, got %d", params.Limit, len(res.Items))
	}
	for _, m := range res.Items {
		if err := m.Validate(info.Kinds); err != nil {
			return err
		}
	}
	if it != nil && !slices.ContainsFunc(res.Items, func(m protocol.CatalogItem) bool { return m.ID == it.ID }) {
		return fmt.Errorf("item %q is not among the items similar to its own hashes", it.ID)
	}
	return nil
}
```

- [ ] **Step 4: Call it from `Check`**

In `internal/contract/contract.go`, insert before the `if slices.Contains(m.Capabilities, "codec") {` block:

```go
	if slices.Contains(m.Capabilities, "catalog") {
		results = append(results, checkCatalog(ctx, p, tmp)...)
		if !p.Alive() {
			return exited(results, p), nil
		}
	}
```

- [ ] **Step 5: Run the contract tests**

Run: `go test ./internal/contract`
Expected: PASS, including the existing tests, whose check counts don't change.

- [ ] **Step 6: Commit**

```bash
git add internal/contract
git commit -m "feat(contract): check catalog plugins"
```

---

### Task 5: Check config

Without a source, a catalog plugin answers search with an error or an empty page, so the paging, get, and similar checks see no items. `INNKEEPER_PLUGIN_CONFIG` lets a plugin's CI point the check at test data.

**Files:** Modify `internal/contract/contract.go`, `cmd/innkeeper/main.go`.

- [ ] **Step 1: Add `Config`**

In `internal/contract/contract.go`, after `var DatabaseURL string`:

```go
// Config is sent to the plugin as its instance config. Nil sends an empty
// one.
var Config map[string]any
```

In `Check`, change the `params :=` line's `Config: map[string]any{}` to `Config: config`, and declare it just before:

```go
	config := Config
	if config == nil {
		config = map[string]any{}
	}
```

- [ ] **Step 2: Read the variable in `pluginCheck`**

In `cmd/innkeeper/main.go`, add `"encoding/json"` to the imports, and after the `contract.DatabaseURL =` line:

```go
	if s := os.Getenv("INNKEEPER_PLUGIN_CONFIG"); s != "" {
		if err := json.Unmarshal([]byte(s), &contract.Config); err != nil {
			return fmt.Errorf("INNKEEPER_PLUGIN_CONFIG: %w", err)
		}
	}
```

- [ ] **Step 3: Try it**

```bash
INNKEEPER_PLUGIN_CONFIG='[1]' go run ./cmd/innkeeper plugin check internal/contract
```

Expected: `innkeeper: INNKEEPER_PLUGIN_CONFIG: json: cannot unmarshal array into Go value of type map[string]interface {}`.

- [ ] **Step 4: Run the tests and commit**

Run: `go test ./internal/contract ./cmd/innkeeper`
Expected: PASS.

```bash
git add internal/contract/contract.go cmd/innkeeper/main.go
git commit -m "feat(contract): send a config from INNKEEPER_PLUGIN_CONFIG"
```

---

### Task 6: Docs

**Files:** Modify `docs/protocol.md` and the spec. Use the Edit tool for both; the doc-writing hook checks them.

- [ ] **Step 1: Manifest table**

In `docs/protocol.md`, the `capabilities` row of the manifest table: add `catalog` to the list, so it reads "Any of `import`, `search`, `push`, `pull`, `action`, `enrich`, `codec`, `auth`, `mcp`, `catalog`, or `theme`."

- [ ] **Step 2: Add a `catalog` section**

Insert before `### \`mcp\``:

````markdown
### `catalog`

A catalog serves a collection of cards and lorebooks the owner searches and
adopts from, without putting them in the library. An **ingested** catalog
copies its source into its own schema (`database = true`) and answers
`catalog.ingest`. A **pass-through** catalog forwards each call to a remote
site and stores nothing.

Every item has an `id` you assign that never changes, a `kind` (`card` or
`lorebook`), and one or more versions, each named by a string you choose.
Reply with error `-32004` for an ID or version you don't have. Check params
before reaching your source, so a bad request gets `-32602` even when the
source is down.

**`catalog.describe`** takes no params. Don't contact your source to answer.

| Field | Meaning |
|---|---|
| `kinds` | The kinds you hold, such as `["card", "lorebook"]` |
| `filters` | The filters `catalog.search` accepts. Each has `name` (lowercase `snake_case`), `label`, `type`, and, for `enum` only, `options`: a list of `{value, label}` |
| `sorts` | The orders `catalog.search` accepts, each `{value, label}`. The first is the default |
| `ingest` | `true` if you answer `catalog.ingest` |
| `similar` | `true` if you answer `catalog.similar` |

| Filter `type` | Value in a search |
|---|---|
| `enum` | One of the option values, as a string |
| `text` | A non-empty string |
| `date_range` | `{"from": "2024-01-01", "to": "2024-12-31"}`, both inclusive, either optional |
| `number_range` | `{"min": 1, "max": 5}`, both inclusive, either optional |

**`catalog.search`** returns one page of items.

| Param | Meaning |
|---|---|
| `q` | Search text, or empty for everything |
| `kind` | One of your `kinds`, or empty for all |
| `filters` | Filter values by name |
| `sort` | One of your `sorts`, or empty for the first |
| `page` | `size`, 1 to 100 or 0 for your default, and `cursor`, the `next` of the previous page or empty for the first |

Reply with `-32602` for a kind, sort, or filter you didn't declare, or a
filter value of the wrong shape. Otherwise reply with `items` and `next`, the
cursor for the following page, or empty on the last. Each item has:

| Field | Meaning |
|---|---|
| `id`, `kind`, `name` | Required |
| `author`, `tags`, `summary` | Optional |
| `latest_version` | The newest version's name |
| `hashes` | The latest version's `content_hash` and, if you have them, `text_simhash` and `image_phash`. Compute them with the Go package `github.com/davidgibbons/innkeeper/cardhash`, over a card's `data` object or the whole lorebook, so they match the library's. `text_simhash` and `image_phash` are signed 64-bit integers |
| `thumbnail` | Optional. A base64 JPEG at most 160 pixels wide and 32 KiB |

**`catalog.get`** returns one version of an item.

| Param | Meaning |
|---|---|
| `id` | The item |
| `version` | A version name, or empty for the latest |
| `avatar` | `true` to write the card's image to `blob_tmp`. Only adopt sets it |

Reply with `item` (as in search), `version`, `versions` (newest first, each
`{version, created}`, with `created` in RFC 3339 if you know it), `data` (a
CCv3 card for a card, a CCv3 `character_book` for a lorebook), and `avatar`,
the file you wrote to `blob_tmp`, or empty. The core moves the file, so don't
reuse the path.

**`catalog.similar`** finds near-duplicates of a library item.

| Param | Meaning |
|---|---|
| `kind` | The kind to search |
| `hashes` | The library item's hashes |
| `max_distance` | The most bits `text_simhash` or `image_phash` may differ by |
| `limit` | The most items to return, 1 to 100 |

Reply with `items` that share `content_hash` or fall within `max_distance`
on either other hash, closest first.

**`catalog.ingest`** copies your source into your schema. It takes `run` and
`checkpoint` and sends `progress` and `checkpoint` notifications, all as in
`import.run`. Reply with the counts `added`, `updated`, `removed`, and
`skipped`. Search keeps serving what you have while an ingest runs.

```json
{"jsonrpc": "2.0", "id": 8, "method": "catalog.search", "params": {"q": "dwarf", "kind": "card", "filters": {"site": "chub"}, "page": {"size": 1}}}
{"jsonrpc": "2.0", "id": 8, "result": {"items": [{"id": "chub:42", "kind": "card", "name": "Brakka", "latest_version": "3", "hashes": {"content_hash": "90b4…a1e0", "text_simhash": -6846279246937013872}}], "next": "1"}}
```
````

- [ ] **Step 3: Update "Checking a plugin"**

Add this bullet after the `mcp` bullet:

```markdown
- a `catalog` plugin's `catalog.describe` reply is valid; `catalog.search`
  pages one item at a time without repeats and refuses an undeclared filter
  with `-32602`; `catalog.get` returns `-32004` for an unknown ID, and for the
  first search result returns data that hashes to its `content_hash` and an
  avatar inside `blob_tmp`; and, if it claims `similar`, `catalog.similar`
  finds that item by its own hashes. Without a source, an error other than
  `-32601` from search counts as an answer, and the checks that need an item
  pass
```

Replace the last paragraph of the section with:

```markdown
It exits non-zero if any check fails. For a plugin with `database = true`,
set `INNKEEPER_PLUGIN_DATABASE_URL` to send a test database as `database_url`.
Set `INNKEEPER_PLUGIN_CONFIG` to a JSON object to send it as the instance
config, such as a catalog's test data.
```

- [ ] **Step 4: Update the spec**

In `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`:

1. In the Decisions table, change the Adopt row to: "Copies one version into the library as a new card or lorebook through the existing import path, as `emit.card` and `emit.lorebook` do. It doesn't bring the version chain or track later versions."
2. In the Protocol table, replace these rows:

```markdown
| `catalog.search` | `q`, `kind`, `filters`, `sort`, `page` (`size`, `cursor`) | Items with ID, kind, name, author, tags, summary, latest version, an inline JPEG thumbnail of at most 32 KiB, and hashes (`content_hash`, `text_simhash`, `image_phash`), plus the next cursor. |
| `catalog.get` | `id`, `version` (omitted means latest), `avatar` | The item's metadata, its version list, and the version as CCv3: a card, or a `character_book` for a lorebook. With `avatar`, the card's image as a file in `blob_tmp`, as `target.get` returns it. |
| `catalog.similar` | `kind`, `hashes`, `max_distance`, `limit` | Items whose hashes fall within `max_distance`, closest first. Optional. |
| `catalog.ingest` | `run`, `checkpoint` | Runs as a core job. Sends the existing `progress` and `checkpoint` notifications, and returns counts: added, updated, removed, skipped. Optional. |
```

3. Change "Unknown IDs return the protocol's not-found error." to "Unknown IDs and versions return `-32004`, the error targets use for a missing `remote_id`."
4. Under Core, replace the Adopt bullet with: "**Adopt** calls `catalog.get` with `avatar`, and imports the data and avatar with provenance `catalog:<instance>:<item>@<version>` and source `plugin:<name>`. Import dedupe already keys on provenance and `content_hash`, so adopting the same version twice returns the existing card."
5. Under `GET /catalogs/{id}/items/{item}`, replace "forwards to `catalog.get`, without the bytes." with "forwards to `catalog.get` without `avatar`."
6. Under Plugins, `folder`: change "reads PNG, JSON, and CharX cards and lorebook JSON with the codec's parsers" to "reads PNG, JSON, and CharX cards and lorebook JSON into CCv3 with the codec's parsers".
7. Under Errors, delete the "Adopt fails to decode" bullet.
8. Under Testing, change the contract kit bullet to: "**Contract kit:** `innkeeper plugin check` gains catalog checks: `describe` is valid, `search` pages correctly and refuses undeclared filters, `get` returns data that hashes to the search result's `content_hash`, and unknown IDs return `-32004`. Both plugins run it in CI, with `INNKEEPER_PLUGIN_CONFIG` pointing at test data."

- [ ] **Step 5: Commit**

```bash
git add docs/protocol.md docs/superpowers/specs/2026-10-09-archive-catalog-design.md
git commit -m "docs: describe the catalog protocol"
```

---

### Task 7: Full check

- [ ] **Step 1: Run everything**

```bash
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./...
go vet ./...
```

Expected: PASS. If `internal/api` prints `SKIP`, the database isn't up and the run hasn't passed.

- [ ] **Step 2: Check the plugins repo still builds against this core**

```bash
cd ../innkeeper-plugins
go mod edit -replace github.com/davidgibbons/innkeeper=../innkeeper-inn-37
go build ./... && make check
git checkout go.mod go.sum
```

Expected: every plugin passes, and none declares `catalog`, so the output gains no new checks. The `git checkout` restores go.mod; the plugins pick up this core in 37c.
