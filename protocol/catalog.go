package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
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
	for name, raw := range p.Filters {
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
// does: over a card's data object, or over the whole lorebook.
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
// within MaxDistance bits of either other hash.
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
