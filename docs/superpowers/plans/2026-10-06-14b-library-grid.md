# Web UI Library Grid Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Browse the library in the web UI: a Cards page with facets, filters, sort, paging, and thumbnails, and a Collections page.

**Architecture:** `POST /search` gains sort, page numbers, a total, facet counts, per-target sync state, and a `source` filter. `GET /blobs/{sha256}?w=` serves a resized JPEG, cached next to the blob store. The Cards page keeps its whole query in the URL and turns it into one `/search` call. The Collections list returns four member avatars for each mosaic.

**Tech Stack:** Go 1.27, Huma v2, PostgreSQL, `golang.org/x/image/draw`, React, React Router, TanStack Query, `openapi-fetch`, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-05-web-ui-design.md`, "Cards page" and "Collections". Build order phase 2.

---

## Design notes

- **Sort.** `sort` is `relevance`, `updated`, `name`, or `created`. Unset means relevance with text and newest first without, as today, so the CLI and MCP don't change. `updated` orders by `head_revision_id`: revision IDs only grow, so the newest head is the latest edit.
- **Page numbers.** `page.number` is 1-based and replaces the cursor. Sending both is a 400. The cursor stays for the CLI and MCP.
- **Total.** Every search returns `total`, from a separate `count(*)`. A window count would read 0 on a page past the end.
- **Facets** are counted over every card the query matches, including the facet's own terms. Tags are capped at the 50 most used.
- **Targets.** `include: ["targets"]` adds each hit's `{instance, is}` per bound target, from `binding_status`. The facet counts the same pairs.
- **Source filter.** `{"source": "chub"}` matches a card with a live `card_source` of that kind. It's unrelated to the query's `sources` list, which picks a catalog.
- **Thumbnails** are cached at `<store root>/thumbs/<w>/<aa>/<sha>.jpg`, not as blob rows, so garbage collection doesn't delete them. `Store.Remove` deletes a blob's thumbnails with it. Widths are limited to 160, 320, and 640, so a client can't fill the disk with sizes. Transparent pixels become white, since JPEG has no alpha.
- **URL state.** `q` is the text, each `f` is one filter term (`tag:fantasy`, `-tag:pirate`, `collection:3`, `target:2:ahead`, `source:chub`), and `sort`, `page`, `size`, and `view` hold the rest. Defaults are left out of the URL. Every change except paging resets the page to 1.
- **Facet clicks.** Clicking a value adds it; shift-click adds its negation. Clicking an active value the same way removes it, and the other way flips it.
- **Sync badges.** `in_sync` is green, `ahead` blue, `remote_changed`, `diverged`, and `missing` orange, and `cant_check` gray.
- **Out of this phase:** the tile's select box and the selection bar (phase 3), the card page (phase 4; `/cards/:id` is a placeholder), catalog sources (the archive spec), toasts and the offline bar (built with the first page that writes from a dialog), and Playwright (its own plan, once phases 3 and 4 give it flows to cover).

## Files

| File | Change |
|---|---|
| `internal/library/search.go`, `search_test.go` | `sort`, `page.number`, `total`, `source` filter, facets, `include: targets` |
| `internal/targets/filter_test.go` | Target include and facet against real bindings |
| `internal/cardhash/phash.go` | Export `DecodeImage` |
| `internal/blobstore/blobstore.go`, `blobstore_test.go` | `ThumbPath`, `WriteThumb`; `Remove` deletes thumbnails |
| `internal/library/thumbs.go`, `thumbs_test.go` | New: `OpenThumb` |
| `internal/api/organize.go`, `organize_test.go` | `?w=` on `get-blob` |
| `internal/library/organize.go`, `organize_test.go` | `Collection.Avatars` |
| `internal/api/search_test.go` | New fields over HTTP |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `web/src/api/client.ts`, `client.test.ts` | `thumb`, `mustOk` |
| `web/src/theme/tokens.css` | `--facets-width`, `--tile-width` |
| `web/src/cards/query.ts`, `query.test.ts` | New: URL ⇄ query state |
| `web/src/cards/pages.ts`, `pages.test.ts` | New: page number list |
| `web/src/cards/Tile.tsx`, `Facets.tsx`, `Cards.tsx`, `Cards.css`, `Cards.test.tsx` | New: Cards page |
| `web/src/collections/Collections.tsx`, `Collections.css`, `Collections.test.tsx` | New: Collections page |
| `web/src/App.tsx` | Routes |

Go tests need PostgreSQL. Run them with:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

Without it, the database tests skip and a run passes without testing anything. Every `go test` command below assumes it's set.

---

### Task 1: Sort, page numbers, and total

**Files:**
- Modify: `internal/library/search.go`
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/library/search_test.go`:

```go
func TestSearchSort(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	// Editing Brakka makes it the most recently updated card.
	c, err := lib.Cards.Get(ctx, s.brakka)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Update(ctx, s.brakka, c.HeadRevisionID, edit(`{"name": "Zed Brakka"}`)); err != nil {
		t.Fatal(err)
	}
	for sort, want := range map[string][]int64{
		"":          {s.carrow, s.brakka},
		"relevance": {s.carrow, s.brakka},
		"created":   {s.carrow, s.brakka},
		"updated":   {s.brakka, s.carrow},
		"name":      {s.carrow, s.brakka},
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
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/library -run 'TestSearchSort|TestSearchPageNumberAndTotal'`
Expected: build failure, `unknown field Sort in struct literal` and `unknown field Number`.

- [ ] **Step 3: Implement**

In `internal/library/search.go`, add to `Query` after `Fields`:

```go
	Sort     string   `json:"sort,omitempty" enum:"relevance,updated,name,created" doc:"Default: relevance with text, newest first without."`
```

Replace `Page`:

```go
// Page selects one page of results, by cursor or by number.
type Page struct {
	Size   int     `json:"size,omitempty" minimum:"0" maximum:"500" doc:"Default 50."`
	Cursor *string `json:"cursor,omitempty" nullable:"true" doc:"next_cursor from the previous page."`
	Number int     `json:"number,omitempty" minimum:"0" doc:"1-based page number, instead of cursor."`
}
```

Add `Total` to `Results`:

```go
// Results is one page of hits.
type Results struct {
	Hits       []Hit   `json:"hits"`
	NextCursor *string `json:"next_cursor,omitempty"`
	Total      int     `json:"total" doc:"How many cards match."`
}
```

In `Search`, replace the offset block:

```go
	offset := 0
	switch {
	case q.Page.Cursor != nil && q.Page.Number != 0:
		return Results{}, fmt.Errorf("%w: send a page cursor or a page number, not both", ErrInvalid)
	case q.Page.Cursor != nil:
		if offset, err = strconv.Atoi(*q.Page.Cursor); err != nil || offset < 0 {
			return Results{}, fmt.Errorf("%w: bad cursor", ErrInvalid)
		}
	case q.Page.Number < 0:
		return Results{}, fmt.Errorf("%w: page number must be 1 or more", ErrInvalid)
	case q.Page.Number > 0:
		offset = (q.Page.Number - 1) * size
	}
```

Replace the `order` block:

```go
	order := "c.id DESC"
	switch q.Sort {
	case "", "relevance":
		if text != "" {
			order = "ts_rank(r.search, websearch_to_tsquery('english', " + text + ")) DESC, c.id DESC"
		}
	case "updated":
		order = "c.head_revision_id DESC, c.id DESC"
	case "name":
		order = "lower(r.data->>'name'), c.id"
	case "created":
	default:
		return Results{}, fmt.Errorf("%w: unknown sort %q", ErrInvalid, q.Sort)
	}
```

Count the matches just before `sql :=`. Only `LIMIT` and `OFFSET` add arguments after that, so `w.args` still holds just the filter's:

```go
	res := Results{Hits: []Hit{}}
	if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE `+cond, w.args...).Scan(&res.Total); err != nil {
		return Results{}, err
	}
```

Then delete the later `res := Results{Hits: []Hit{}}` line, since `res` now exists.

- [ ] **Step 4: Run the library tests**

Run: `go test ./internal/library`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go
git commit -m "feat(search): sort, page numbers, and a total"
```

---

### Task 2: Source filter and facets

**Files:**
- Modify: `internal/library/search.go`
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/library/search_test.go`:

```go
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
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/library -run 'TestSearchSourceFilter|TestSearchFacets'`
Expected: build failure, `unknown field Source in struct literal` and `undefined: CollectionCount`.

- [ ] **Step 3: Add the source filter**

In `Filter`, after `Collection`:

```go
	Source      *string      `json:"source,omitempty" doc:"Has a source of this kind, such as chub."`
```

In `where.filter`, add `f.Source != nil` to the list counted into `set`, and a case after `f.Collection`:

```go
	case f.Source != nil:
		return `EXISTS (SELECT 1 FROM card_source cs
			WHERE cs.card_id = c.id AND cs.kind = ` + w.arg(*f.Source) + ` AND cs.removed_at IS NULL)`, nil
```

- [ ] **Step 4: Add facets**

In `Query`, after `Sort`:

```go
	Facets   []string `json:"facets,omitempty" doc:"Counts to return over every match: tags, collections, targets, sources."`
```

In `Results`, after `Total`:

```go
	Facets     *Facets `json:"facets,omitempty"`
```

Add the types and the query after `Results`:

```go
// Facets counts the matching cards per value. Only the facets asked for are set.
type Facets struct {
	Tags        []TagCount        `json:"tags,omitempty" doc:"The 50 most used."`
	Collections []CollectionCount `json:"collections,omitempty"`
	Targets     []TargetCount     `json:"targets,omitempty"`
	Sources     []SourceCount     `json:"sources,omitempty"`
}

// CollectionCount is a collection and how many matching cards it has.
type CollectionCount struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Cards int    `json:"cards"`
}

// TargetCount is how many matching cards stand one way on one target.
type TargetCount struct {
	Instance int64  `json:"instance"`
	Is       string `json:"is"`
	Cards    int    `json:"cards"`
}

// SourceCount is a source kind and how many matching cards have it.
type SourceCount struct {
	Kind  string `json:"kind"`
	Cards int    `json:"cards"`
}

// facetSQL selects each facet's rows for the card IDs in match.
var facetSQL = map[string]func(match string) string{
	"tags": func(m string) string {
		return `SELECT t.name, count(*)::int FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
			WHERE ct.card_id IN (` + m + `) GROUP BY t.id ORDER BY count(*) DESC, lower(t.name) LIMIT 50`
	},
	"collections": func(m string) string {
		return `SELECT co.id, co.name, count(*)::int FROM collection_card cc JOIN collection co ON co.id = cc.collection_id
			WHERE cc.card_id IN (` + m + `) GROUP BY co.id ORDER BY lower(co.name)`
	},
	"targets": func(m string) string {
		return `SELECT instance_id, status, count(*)::int FROM binding_status
			WHERE card_id IN (` + m + `) GROUP BY 1, 2 ORDER BY 1, 2`
	},
	"sources": func(m string) string {
		return `SELECT kind, count(DISTINCT card_id)::int FROM card_source
			WHERE removed_at IS NULL AND card_id IN (` + m + `) GROUP BY kind ORDER BY kind`
	},
}

// facets counts the cards matching cond for each facet in names.
func (l *Library) facets(ctx context.Context, names []string, cond string, args []any) (*Facets, error) {
	match := `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id WHERE ` + cond
	f := &Facets{}
	for _, name := range names {
		rows, err := l.pool.Query(ctx, facetSQL[name](match), args...)
		if err != nil {
			return nil, err
		}
		switch name {
		case "tags":
			f.Tags, err = pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
		case "collections":
			f.Collections, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CollectionCount])
		case "targets":
			f.Targets, err = pgx.CollectRows(rows, pgx.RowToStructByPos[TargetCount])
		case "sources":
			f.Sources, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SourceCount])
		}
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}
```

In `Search`, validate the names next to the `include` loop:

```go
	for _, name := range q.Facets {
		if facetSQL[name] == nil {
			return Results{}, fmt.Errorf("%w: unknown facet %q", ErrInvalid, name)
		}
	}
```

Then compute them right after the total, before `sql :=`:

```go
	if len(q.Facets) > 0 {
		if res.Facets, err = l.facets(ctx, q.Facets, cond, w.args); err != nil {
			return Results{}, err
		}
	}
```

- [ ] **Step 5: Run the library tests**

Run: `go test ./internal/library`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go
git commit -m "feat(search): a source filter and facet counts"
```

---

### Task 3: Sync state per hit and the targets facet

**Files:**
- Modify: `internal/library/search.go`
- Test: `internal/targets/filter_test.go`

`binding_status` rows only exist after a push, so this test lives with the push helpers in `internal/targets`.

- [ ] **Step 1: Write the failing test**

Append to `internal/targets/filter_test.go`:

```go
func TestSearchTargets(t *testing.T) {
	e := setup(t, nil)
	edited := e.newCard("Carrow")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	got, err := e.lib.Search(e.ctx, library.Query{Include: []string{"targets"}, Facets: []string{"targets"}})
	if err != nil {
		t.Fatal(err)
	}
	states := map[int64][]library.TargetState{}
	for _, h := range got.Hits {
		states[h.ID] = h.Targets
	}
	if !slices.Equal(states[e.card], []library.TargetState{{Instance: e.inst, Is: "in_sync"}}) ||
		!slices.Equal(states[edited], []library.TargetState{{Instance: e.inst, Is: "ahead"}}) {
		t.Fatalf("targets = %+v", states)
	}
	want := []library.TargetCount{{Instance: e.inst, Is: "ahead", Cards: 1}, {Instance: e.inst, Is: "in_sync", Cards: 1}}
	if !slices.Equal(got.Facets.Targets, want) {
		t.Fatalf("targets facet = %+v", got.Facets.Targets)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/targets -run TestSearchTargets`
Expected: FAIL with `unknown include "targets"` (a build failure on `h.Targets` first).

- [ ] **Step 3: Implement**

In `Hit`, after `Lorebooks`:

```go
	Targets    []TargetState  `json:"targets,omitempty" doc:"With include targets: how the card stands on each target it's pushed to."`
```

Update the `Include` doc to `"Extra data per hit: tags, lorebooks, targets."`.

In `Search`, start the include variables with a third one and add a case:

```go
	tags, books, targets := `NULL::text[]`, `NULL::bigint[]`, `NULL::jsonb`
```

```go
		case "targets":
			targets = `(SELECT jsonb_agg(jsonb_build_object('instance', instance_id, 'is', status) ORDER BY instance_id)
				FROM binding_status WHERE card_id = c.id)`
```

Select it after `books` and scan it after `&h.Lorebooks`:

```go
	sql := `SELECT c.id, c.slug, r.id, c.archived_at, r.data, r.avatar_blob, ` + tags + `, ` + books + `, ` + targets + `
```

```go
	_, err = pgx.ForEachRow(rows, []any{&h.ID, &h.Slug, &h.RevisionID, &h.ArchivedAt, &data, &avatar, &h.Tags, &h.Lorebooks, &h.Targets},
```

pgx decodes `jsonb` into `[]TargetState` with `encoding/json`, and SQL `NULL` into a nil slice.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/library ./internal/targets`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/search.go internal/targets/filter_test.go
git commit -m "feat(search): each hit's sync state per target"
```

---

### Task 4: Search additions over HTTP

**Files:**
- Test: `internal/api/search_test.go`

- [ ] **Step 1: Write the test**

Append to `TestSearchRoute` in `internal/api/search_test.go`, before the error cases:

```go
	res = library.Results{}
	e.call("POST", "/search", map[string]any{"sort": "name", "page": map[string]any{"size": 1, "number": 2},
		"facets": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].Fields["name"] != "Carrow" || res.Total != 2 ||
		res.Facets == nil || len(res.Facets.Tags) != 1 {
		t.Fatalf("numbered page = %+v", res)
	}
	e.call("POST", "/search", map[string]any{"sort": "size"}, 422, nil)
```

`sort` has an `enum` tag, so Huma rejects a bad value with `422` before the handler runs.

- [ ] **Step 2: Run it**

Run: `go test ./internal/api -run TestSearchRoute`
Expected: PASS. Tasks 1 to 3 already did the work; this pins the JSON names.

- [ ] **Step 3: Commit**

```bash
git add internal/api/search_test.go
git commit -m "test(api): cover sort, page numbers, and facets"
```

---

### Task 5: Thumbnail storage

**Files:**
- Modify: `internal/cardhash/phash.go`
- Modify: `internal/blobstore/blobstore.go`
- Test: `internal/blobstore/blobstore_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/blobstore/blobstore_test.go` (add `io`, `os`, and `strings` to its imports if they're missing):

```go
func TestThumbs(t *testing.T) {
	s := &Store{Name: "library", Root: t.TempDir()}
	sha, _, err := s.Put(strings.NewReader("image bytes"))
	if err != nil {
		t.Fatal(err)
	}
	dst, err := s.ThumbPath(sha, 320)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteThumb(dst, func(w io.Writer) error { _, err := io.WriteString(w, "jpeg"); return err }); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(dst); err != nil || string(b) != "jpeg" {
		t.Fatalf("thumb = %q, %v", b, err)
	}
	if err := s.Remove(sha); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("thumb outlived its blob: %v", err)
	}
	if _, err := s.ThumbPath("../etc", 320); err == nil {
		t.Fatal("bad hash accepted")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/blobstore -run TestThumbs`
Expected: build failure, `s.ThumbPath undefined`.

- [ ] **Step 3: Implement the store side**

In `internal/blobstore/blobstore.go`, add after `Path` (add `strconv` to the imports):

```go
// ThumbPath returns where the w-pixel-wide thumbnail of a blob is cached.
func (s *Store) ThumbPath(sha string, w int) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("invalid blob hash %q", sha)
	}
	return filepath.Join(s.Root, "thumbs", strconv.Itoa(w), sha[0:2], sha+".jpg"), nil
}

// WriteThumb writes a thumbnail to dst, a ThumbPath, all at once, so a
// concurrent reader never sees half a file.
func (s *Store) WriteThumb(dst string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = write(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), dst)
}
```

Replace `Remove`:

```go
// Remove deletes a blob's file and its thumbnails. A file that is already
// gone is not an error.
func (s *Store) Remove(sha string) error {
	path, err := s.Path(sha)
	if err != nil {
		return err
	}
	thumbs, _ := filepath.Glob(filepath.Join(s.Root, "thumbs", "*", sha[0:2], sha+".jpg"))
	for _, p := range append(thumbs, path) {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}
```

`filepath.Glob` only fails on a bad pattern, and `sha` already matched `shaPattern`, so its error is safe to drop.

- [ ] **Step 4: Export the image decoder**

In `internal/cardhash/phash.go`, move the open, size check, and decode out of `ImagePhash` into:

```go
// DecodeImage decodes the image at path, refusing one whose decoded size
// could exhaust memory.
func DecodeImage(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, err
	}
	if cfg.Width*cfg.Height > maxPixels {
		return nil, fmt.Errorf("image is %d×%d, over the %d pixel limit", cfg.Width, cfg.Height, maxPixels)
	}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	img, _, err := image.Decode(f)
	return img, err
}
```

and start `ImagePhash` with:

```go
func ImagePhash(path string) (int64, error) {
	img, err := DecodeImage(path)
	if err != nil {
		return 0, err
	}
	const n = 32
```

Move the "Images are untrusted" comment from `maxPixels` only if it no longer reads right there; it still does.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/blobstore ./internal/cardhash ./internal/library`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/blobstore internal/cardhash/phash.go
git commit -m "feat(blobstore): cache thumbnails beside their blobs"
```

---

### Task 6: `OpenThumb` and `?w=`

**Files:**
- Create: `internal/library/thumbs.go`
- Create: `internal/library/thumbs_test.go`
- Modify: `internal/api/organize.go:53-56,130-150`
- Test: `internal/api/organize_test.go`

- [ ] **Step 1: Write the failing library test**

Create `internal/library/thumbs_test.go`:

```go
package library

import (
	"context"
	"errors"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenThumb(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	png, err := os.Open(filepath.Join("..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer png.Close()
	b, err := lib.PutBlob(ctx, png)
	if err != nil {
		t.Fatal(err)
	}

	// The fixture is 256×320.
	for w, want := range map[int][2]int{160: {160, 200}, 320: {256, 320}} {
		f, err := lib.OpenThumb(ctx, b.SHA256, w)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := jpeg.DecodeConfig(f)
		f.Close()
		if err != nil || cfg.Width != want[0] || cfg.Height != want[1] {
			t.Fatalf("w=%d: %d×%d, %v; want %v", w, cfg.Width, cfg.Height, err, want)
		}
	}
	// The second request reads the cached file.
	f, err := lib.OpenThumb(ctx, b.SHA256, 160)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := lib.OpenThumb(ctx, b.SHA256, 300); !errors.Is(err, ErrInvalid) {
		t.Fatalf("odd width: %v", err)
	}
	text, err := lib.PutBlob(ctx, strings.NewReader("not an image"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lib.OpenThumb(ctx, text.SHA256, 160); !errors.Is(err, ErrInvalid) {
		t.Fatalf("text blob: %v", err)
	}
	if _, err := lib.OpenThumb(ctx, strings.Repeat("0", 64), 160); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing blob: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/library -run TestOpenThumb`
Expected: build failure, `lib.OpenThumb undefined`.

- [ ] **Step 3: Implement**

Create `internal/library/thumbs.go`:

```go
package library

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"os"
	"slices"
	"strings"

	"golang.org/x/image/draw"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

// ThumbWidths are the thumbnail widths OpenThumb makes. Few, so the cache
// stays small.
var ThumbWidths = []int{160, 320, 640}

// OpenThumb returns a JPEG of an image blob, w pixels wide or its own width
// if narrower, making and caching it on first use. The caller closes the file.
func (l *Library) OpenThumb(ctx context.Context, sha string, w int) (*os.File, error) {
	if !slices.Contains(ThumbWidths, w) {
		return nil, fmt.Errorf("%w: thumbnail width must be one of %v", ErrInvalid, ThumbWidths)
	}
	b, err := l.Blob(ctx, sha)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(b.MIME, "image/") {
		return nil, fmt.Errorf("%w: blob is %s, not an image", ErrInvalid, b.MIME)
	}
	dst, err := l.blobs.ThumbPath(sha, w)
	if err != nil {
		return nil, err
	}
	if f, err := os.Open(dst); err == nil {
		return f, nil
	}
	src, err := l.blobs.Path(sha)
	if err != nil {
		return nil, err
	}
	img, err := cardhash.DecodeImage(src)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	r := img.Bounds()
	tw, th := r.Dx(), r.Dy()
	if tw > w {
		tw, th = w, max(1, th*w/tw)
	}
	// JPEG has no alpha, so transparent pixels land on white.
	out := image.NewRGBA(image.Rect(0, 0, tw, th))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.CatmullRom.Scale(out, out.Bounds(), img, r, draw.Over, nil)
	if err := l.blobs.WriteThumb(dst, func(f io.Writer) error {
		return jpeg.Encode(f, out, &jpeg.Options{Quality: 82})
	}); err != nil {
		return nil, err
	}
	return os.Open(dst)
}
```

- [ ] **Step 4: Run it**

Run: `go test ./internal/library -run TestOpenThumb`
Expected: PASS.

- [ ] **Step 5: Write the failing API test**

Append to `TestBlobs` in `internal/api/organize_test.go`, before its last line (add `image/jpeg`, `os`, and `path/filepath` to the imports if missing):

```go
	avatar, err := os.ReadFile(filepath.Join("..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	b = library.Blob{}
	e.call("POST", "/blobs", avatar, 201, &b)
	r = e.call("GET", "/blobs/"+b.SHA256+"?w=160", nil, 200, nil)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(r.Body))
	if err != nil || cfg.Width != 160 || r.Header.Get("Content-Type") != "image/jpeg" ||
		r.Header.Get("Cache-Control") != "private, max-age=31536000, immutable" {
		t.Fatalf("thumb: %v, %v, %v", cfg, err, r.Header)
	}
	e.call("GET", "/blobs/"+b.SHA256+"?w=300", nil, 400, nil)
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/api -run TestBlobs`
Expected: FAIL. Without a `w` parameter the route ignores the query and returns the PNG, so `jpeg.DecodeConfig` errors.

- [ ] **Step 7: Implement the route**

In `internal/api/organize.go`, give `blobIn` the parameter:

```go
type blobIn struct {
	SHA256 string `path:"sha256" pattern:"^[0-9a-f]{64}$"`
	W      int    `query:"w" doc:"Return a JPEG of an image this many pixels wide: 160, 320, or 640."`
}
```

Replace the `get-blob` handler body (add `os` to the imports):

```go
		func(ctx context.Context, in *blobIn) (*huma.StreamResponse, error) {
			var f *os.File
			var mime string
			var err error
			if in.W != 0 {
				f, err = s.Lib.OpenThumb(ctx, in.SHA256, in.W)
				mime = "image/jpeg"
			} else {
				var b library.Blob
				b, f, err = s.Lib.OpenBlob(ctx, in.SHA256)
				mime = b.MIME
			}
			if err != nil {
				return nil, s.fail(err)
			}
			st, err := f.Stat()
			if err != nil {
				f.Close()
				return nil, s.fail(err)
			}
			return &huma.StreamResponse{Body: func(hc huma.Context) {
				defer f.Close()
				// Only images display inline. Anything else downloads, so an
				// uploaded HTML file can't run as this site.
				if !strings.HasPrefix(mime, "image/") {
					mime = "application/octet-stream"
				}
				hc.SetHeader("Content-Type", mime)
				hc.SetHeader("Content-Length", strconv.FormatInt(st.Size(), 10))
				hc.SetHeader("X-Content-Type-Options", "nosniff")
				hc.SetHeader("Content-Security-Policy", "sandbox")
				hc.SetHeader("Cache-Control", "private, max-age=31536000, immutable")
				if _, err := io.Copy(hc.BodyWriter(), f); err != nil {
					s.Log.Warn("send blob", "sha256", in.SHA256, "err", err)
				}
			}}, nil
		})
```

`s.fail` maps `library.ErrInvalid` to `400`, so an unlisted width or a non-image blob is a `400`.

- [ ] **Step 8: Run the API tests**

Run: `go test ./internal/api`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/library/thumbs.go internal/library/thumbs_test.go internal/api/organize.go internal/api/organize_test.go
git commit -m "feat(api): serve resized JPEG thumbnails with ?w="
```

---

### Task 7: Collection mosaics

**Files:**
- Modify: `internal/library/organize.go:55-62,108-117`
- Test: `internal/library/organize_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/library/organize_test.go` (add `os`, `path/filepath`, and `slices` to its imports if missing):

```go
func TestCollectionAvatars(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	png, err := os.Open(filepath.Join("..", "cardhash", "testdata", "avatar.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer png.Close()
	img, err := lib.PutBlob(ctx, png)
	if err != nil {
		t.Fatal(err)
	}
	c, err := lib.CreateCollection(ctx, "Crew", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		ed := edit(`{"name": "Sailor"}`)
		if i > 0 {
			ed.AvatarBlob = img.SHA256
		}
		card, _, err := lib.Cards.Create(ctx, fmt.Sprintf("sailor-%d", i), "upload", ed)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.AddToCollection(ctx, c.ID, card.ID); err != nil {
			t.Fatal(err)
		}
	}
	list, err := lib.Collections(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// The card without an avatar is skipped, and the mosaic stops at four.
	want := slices.Repeat([]string{img.SHA256}, 4)
	if len(list) != 1 || list[0].Cards != 6 || !slices.Equal(list[0].Avatars, want) {
		t.Fatalf("collections = %+v", list)
	}
}
```

Add `fmt` to the imports if it's missing.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/library -run TestCollectionAvatars`
Expected: build failure, `list[0].Avatars undefined`.

- [ ] **Step 3: Implement**

Add to `Collection`, after `Cards`:

```go
	// Avatars are the first four members' avatar blobs, for a mosaic. Only
	// Collections sets it.
	Avatars []string `json:"avatars,omitempty"`
```

Replace the query in `Collections`:

```go
	rows, err := l.pool.Query(ctx, `SELECT c.id, c.name, c.description, c.created_at, count(cc.card_id)::int,
			array(SELECT r.avatar_blob FROM collection_card m
				JOIN card k ON k.id = m.card_id JOIN card_revision r ON r.id = k.head_revision_id
				WHERE m.collection_id = c.id AND r.avatar_blob IS NOT NULL AND k.archived_at IS NULL
				ORDER BY m.added_at, m.card_id LIMIT 4)
		FROM collection c LEFT JOIN collection_card cc ON cc.collection_id = c.id
		GROUP BY c.id ORDER BY lower(c.name)`)
```

Update the doc comment to `// Collections returns every collection with its card count and mosaic avatars, by name.`

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/library ./internal/api`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/organize.go internal/library/organize_test.go
git commit -m "feat(collections): list four member avatars for a mosaic"
```

---

### Task 8: Regenerate the client and add helpers

**Files:**
- Modify: `web/openapi.json`, `web/src/api/schema.d.ts` (generated)
- Modify: `web/src/api/client.ts`
- Test: `web/src/api/client.test.ts`
- Modify: `web/src/theme/tokens.css`

- [ ] **Step 1: Regenerate**

Run: `cd web && npm run gen && grep -n "CollectionCount\|TargetCount\|avatars" src/api/schema.d.ts`
Expected: the new types and the `avatars` field appear.

- [ ] **Step 2: Write the failing test**

Append to `web/src/api/client.test.ts` (merge the import with the existing one from `./client`):

```ts
import { ApiError, mustOk, thumb } from "./client";

test("thumb asks for a resized image", () => {
  expect(thumb("ab".repeat(32), 160)).toBe("/api/blobs/" + "ab".repeat(32) + "?w=160");
});

test("mustOk throws the problem detail on an error status", () => {
  const ok = { error: undefined, response: new Response(null, { status: 204 }) };
  expect(() => mustOk(ok)).not.toThrow();
  const bad = { error: { detail: "nope" }, response: new Response(null, { status: 409 }) };
  expect(() => mustOk(bad)).toThrow(new ApiError(409, "nope"));
});
```

- [ ] **Step 3: Run it to verify it fails**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL, `mustOk` and `thumb` aren't exported.

- [ ] **Step 4: Implement**

Append to `web/src/api/client.ts`:

```ts
/** mustOk throws a failed response's error as an ApiError, for calls with no body, like a 204. */
export function mustOk(res: { error?: unknown; response: Response }): void {
  if (res.error !== undefined || !res.response.ok) {
    const detail = (res.error as { detail?: string } | undefined)?.detail ?? res.response.statusText;
    throw new ApiError(res.response.status, detail);
  }
}

/** thumb is the URL of a blob's image resized to w pixels wide. */
export function thumb(sha: string, w: 160 | 320 | 640 = 320): string {
  return `/api/blobs/${sha}?w=${w}`;
}
```

Add to `:root` in `web/src/theme/tokens.css`, after `--rail-width-collapsed`:

```css
  --facets-width: 220px;
  --tile-width: 160px;
```

- [ ] **Step 5: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/openapi.json web/src/api web/src/theme/tokens.css
git commit -m "chore(web): regenerate the API client and add thumbnail helpers"
```

---

### Task 9: Query state in the URL

**Files:**
- Create: `web/src/cards/query.ts`
- Create: `web/src/cards/query.test.ts`

- [ ] **Step 1: Write the failing tests**

Create `web/src/cards/query.test.ts`:

```ts
import { parseQuery, toParams, toSearch, toggleTerm, type CardQuery } from "./query";

const base: CardQuery = { text: "", terms: [], sort: "updated", page: 1, size: 48, view: "grid" };

test("an empty URL is the default query", () => {
  expect(parseQuery(new URLSearchParams())).toEqual(base);
});

test("a query round-trips through the URL", () => {
  const q: CardQuery = {
    text: "dwarf smith",
    terms: [
      { kind: "tag", value: "fantasy", not: false },
      { kind: "tag", value: "a:b", not: true },
      { kind: "target", value: "2:ahead", not: false },
    ],
    sort: "name",
    page: 3,
    size: 96,
    view: "list",
  };
  expect(parseQuery(toParams(q))).toEqual(q);
});

test("defaults stay out of the URL", () => {
  expect(toParams(base).toString()).toBe("");
  expect(toParams({ ...base, text: "x", sort: "relevance" }).toString()).toBe("q=x");
});

test("text searches default to relevance", () => {
  expect(parseQuery(new URLSearchParams("q=x")).sort).toBe("relevance");
  expect(parseQuery(new URLSearchParams("q=x&sort=name")).sort).toBe("name");
});

test("bad values fall back to defaults", () => {
  const q = parseQuery(new URLSearchParams("page=-2&size=7&sort=size&view=x&f=color:red&f=nocolon"));
  expect(q).toEqual(base);
});

test("toSearch builds the API body", () => {
  const body = toSearch({
    ...base,
    text: "dwarf",
    terms: [
      { kind: "tag", value: "fantasy", not: false },
      { kind: "collection", value: "3", not: true },
      { kind: "source", value: "chub", not: false },
      { kind: "target", value: "2:ahead", not: false },
      { kind: "collection", value: "x", not: false },
    ],
    page: 2,
  });
  expect(body).toMatchObject({
    text: "dwarf",
    sort: "updated",
    filter: {
      and: [
        { tag: "fantasy" },
        { not: { collection: 3 } },
        { source: "chub" },
        { target_state: { instance: 2, is: "ahead" } },
      ],
    },
    page: { size: 48, number: 2 },
  });
  expect(toSearch(base).filter).toBeUndefined();
});

test("toggleTerm adds, flips, and removes, and resets the page", () => {
  const tag = { kind: "tag", value: "fantasy", not: false } as const;
  const added = toggleTerm({ ...base, page: 4 }, tag);
  expect(added.terms).toEqual([tag]);
  expect(added.page).toBe(1);
  expect(toggleTerm(added, { ...tag, not: true }).terms).toEqual([{ ...tag, not: true }]);
  expect(toggleTerm(added, tag).terms).toEqual([]);
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/cards/query.test.ts`
Expected: FAIL, `Cannot find module './query'`.

- [ ] **Step 3: Implement**

Create `web/src/cards/query.ts`:

```ts
import type { components } from "../api/schema";

type Filter = components["schemas"]["Filter"];
type Search = components["schemas"]["Query"];
type TargetIs = components["schemas"]["TargetState"]["is"];

const kinds = ["tag", "collection", "source", "target"] as const;
const sorts = ["relevance", "updated", "name", "created"] as const;
export const sizes = [24, 48, 96];

/** Term is one filter: a facet value, or its negation. */
export type Term = { kind: (typeof kinds)[number]; value: string; not: boolean };
export type Sort = (typeof sorts)[number];
export type CardQuery = { text: string; terms: Term[]; sort: Sort; page: number; size: number; view: "grid" | "list" };

export const defaultSort = (text: string): Sort => (text ? "relevance" : "updated");

/** parseQuery reads the Cards page state from its URL, ignoring anything malformed. */
export function parseQuery(p: URLSearchParams): CardQuery {
  const terms: Term[] = [];
  for (const raw of p.getAll("f")) {
    const not = raw.startsWith("-");
    const s = not ? raw.slice(1) : raw;
    const i = s.indexOf(":");
    const kind = s.slice(0, i) as Term["kind"];
    if (i > 0 && kinds.includes(kind)) terms.push({ kind, value: s.slice(i + 1), not });
  }
  const text = p.get("q") ?? "";
  const sort = p.get("sort") as Sort;
  const page = Number(p.get("page"));
  const size = Number(p.get("size"));
  return {
    text,
    terms,
    sort: sorts.includes(sort) ? sort : defaultSort(text),
    page: Number.isInteger(page) && page > 0 ? page : 1,
    size: sizes.includes(size) ? size : 48,
    view: p.get("view") === "list" ? "list" : "grid",
  };
}

/** toParams writes the state back, leaving defaults out. */
export function toParams(q: CardQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (q.text) p.set("q", q.text);
  for (const t of q.terms) p.append("f", `${t.not ? "-" : ""}${t.kind}:${t.value}`);
  if (q.sort !== defaultSort(q.text)) p.set("sort", q.sort);
  if (q.page !== 1) p.set("page", String(q.page));
  if (q.size !== 48) p.set("size", String(q.size));
  if (q.view !== "grid") p.set("view", q.view);
  return p;
}

function termFilter(t: Term): Filter | null {
  let f: Filter;
  if (t.kind === "tag") f = { tag: t.value };
  else if (t.kind === "source") f = { source: t.value };
  else if (t.kind === "collection") {
    const id = Number(t.value);
    if (!Number.isInteger(id)) return null;
    f = { collection: id };
  } else {
    const [instance, is] = t.value.split(":");
    if (!Number.isInteger(Number(instance)) || !is) return null;
    f = { target_state: { instance: Number(instance), is: is as TargetIs } };
  }
  return t.not ? { not: f } : f;
}

/** toSearch is the POST /api/search body for the state. */
export function toSearch(q: CardQuery): Search {
  const filters = q.terms.map(termFilter).filter((f): f is Filter => f !== null);
  return {
    text: q.text || undefined,
    filter: filters.length ? { and: filters } : undefined,
    sort: q.sort,
    fields: ["name", "description", "avatar"],
    include: ["tags", "lorebooks", "targets"],
    facets: ["tags", "collections", "targets", "sources"],
    page: { size: q.size, number: q.page },
  };
}

/** toggleTerm adds t, flips its negation, or removes it if it's already there as given. */
export function toggleTerm(q: CardQuery, t: Term): CardQuery {
  const i = q.terms.findIndex((x) => x.kind === t.kind && x.value === t.value);
  const terms = q.terms.filter((_, j) => j !== i);
  if (i < 0 || q.terms[i].not !== t.not) terms.push(t);
  return { ...q, terms, page: 1 };
}
```

If `tsc` rejects the `Filter` literals because the generated type marks every key as required or `null`-able, look at `Filter` in `src/api/schema.d.ts` and adjust the literals; don't cast the whole object.

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/cards/query.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/cards/query.ts web/src/cards/query.test.ts
git commit -m "feat(web): keep the card query in the URL"
```

---

### Task 10: Page links

**Files:**
- Create: `web/src/cards/pages.ts`
- Create: `web/src/cards/pages.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/cards/pages.test.ts`:

```ts
import { pageLinks } from "./pages";

test("short ranges list every page", () => {
  expect(pageLinks(2, 5)).toEqual([1, 2, 3, 4, 5]);
});

test("long ranges keep the ends and two either side of the current page", () => {
  expect(pageLinks(10, 20)).toEqual([1, "…", 8, 9, 10, 11, 12, "…", 20]);
  expect(pageLinks(1, 20)).toEqual([1, 2, 3, "…", 20]);
  expect(pageLinks(4, 20)).toEqual([1, 2, 3, 4, 5, 6, "…", 20]);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/cards/pages.test.ts`
Expected: FAIL, `Cannot find module './pages'`.

- [ ] **Step 3: Implement**

Create `web/src/cards/pages.ts`:

```ts
/** pageLinks lists the page numbers to show, with "…" where pages are skipped. */
export function pageLinks(current: number, last: number): (number | "…")[] {
  const out: (number | "…")[] = [];
  for (let p = 1; p <= last; p++) {
    if (p === 1 || p === last || Math.abs(p - current) <= 2) out.push(p);
    else if (out[out.length - 1] !== "…") out.push("…");
  }
  return out;
}
```

- [ ] **Step 4: Run it**

Run: `cd web && npx vitest run src/cards/pages.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/cards/pages.ts web/src/cards/pages.test.ts
git commit -m "feat(web): list page links with gaps"
```

---

### Task 11: The Cards page

**Files:**
- Create: `web/src/cards/Tile.tsx`
- Create: `web/src/cards/Facets.tsx`
- Create: `web/src/cards/Cards.tsx`
- Create: `web/src/cards/Cards.css`
- Create: `web/src/cards/Cards.test.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/cards/Cards.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Cards } from "./Cards";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const results = {
  total: 49,
  hits: [
    {
      id: 7,
      slug: "brakka",
      revision_id: 1,
      fields: { name: "Brakka", description: "A grumpy dwarf.", avatar: "ab".repeat(32) },
      tags: ["fantasy", "dwarf", "smith", "grumpy"],
      lorebooks: [3],
      targets: [{ instance: 2, is: "ahead" }],
    },
  ],
  facets: {
    tags: [
      { name: "fantasy", cards: 40 },
      { name: "pirate", cards: 9 },
    ],
    targets: [{ instance: 2, is: "ahead", cards: 1 }],
  },
};

let searches: unknown[];
beforeEach(() => {
  searches = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/search") {
        searches.push(await req.json());
        return json(results);
      }
      if (path === "/api/instances") return json([{ id: 2, name: "Lumiverse", plugin: "lumiverse" }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderCards(entry: string) {
  const router = createMemoryRouter([{ path: "/cards", element: <Cards /> }], { initialEntries: [entry] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("the URL becomes the search, and hits become tiles", async () => {
  renderCards("/cards?f=tag:fantasy&page=2");
  expect(await screen.findByText("Brakka")).toBeInTheDocument();
  expect(searches[0]).toMatchObject({ filter: { and: [{ tag: "fantasy" }] }, page: { size: 48, number: 2 } });
  expect(screen.getByText("+1")).toBeInTheDocument();
  expect(screen.getByTitle("Lumiverse: ahead")).toBeInTheDocument();
  expect(screen.getByText("49 cards")).toBeInTheDocument();
  expect(screen.getByRole("img", { name: "Brakka" })).toHaveAttribute("src", `/api/blobs/${"ab".repeat(32)}?w=320`);
});

test("a facet click filters and shift-click excludes", async () => {
  const router = renderCards("/cards?page=3");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /pirate/ }));
  expect(router.state.location.search).toBe("?f=tag%3Apirate");
  await user.keyboard("{Shift>}");
  await user.click(screen.getByRole("button", { name: /fantasy/ }));
  await user.keyboard("{/Shift}");
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["tag:pirate", "-tag:fantasy"]);
});

test("a chip removes its filter", async () => {
  const router = renderCards("/cards?f=tag:fantasy");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Remove tag: fantasy" }));
  expect(router.state.location.search).toBe("");
});

test("the search box sets the text", async () => {
  const router = renderCards("/cards");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("searchbox"), "dwarf{Enter}");
  expect(router.state.location.search).toBe("?q=dwarf");
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/cards/Cards.test.tsx`
Expected: FAIL, `Cannot find module './Cards'`.

- [ ] **Step 3: Write the tile**

Create `web/src/cards/Tile.tsx`:

```tsx
import { Link } from "react-router";
import { thumb } from "../api/client";
import type { components } from "../api/schema";

export type Hit = components["schemas"]["Hit"];
export type Names = { instances: Map<number, string>; collections: Map<number, string> };

const badgeClass: Record<string, string> = {
  in_sync: "ok",
  ahead: "ahead",
  remote_changed: "drift",
  diverged: "drift",
  missing: "drift",
  cant_check: "unknown",
};

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

export function Badges({ hit, names }: { hit: Hit; names: Names }) {
  return (
    <span className="badges">
      {(hit.targets ?? []).map((t) => (
        <span
          key={t.instance}
          className={"badge " + badgeClass[t.is]}
          title={`${names.instances.get(t.instance) ?? "#" + t.instance}: ${t.is.replace("_", " ")}`}
        />
      ))}
    </span>
  );
}

function Avatar({ hit, w }: { hit: Hit; w: 160 | 320 }) {
  const name = str(hit.fields.name);
  const avatar = str(hit.fields.avatar);
  if (!avatar) return <span className="avatar empty">{name.slice(0, 1)}</span>;
  return <img className="avatar" src={thumb(avatar, w)} alt={name} loading="lazy" />;
}

export function Tile({ hit, names }: { hit: Hit; names: Names }) {
  const tags = hit.tags ?? [];
  const books = hit.lorebooks?.length ?? 0;
  return (
    <article className="tile">
      <Link to={`/cards/${hit.id}`} className="tile-image">
        <Avatar hit={hit} w={320} />
        <Badges hit={hit} names={names} />
        {books > 0 && <span className="lorebooks" title={`${books} lorebooks`}>📖 {books}</span>}
      </Link>
      <Link to={`/cards/${hit.id}`} className="tile-name">
        {str(hit.fields.name)}
      </Link>
      <p className="tile-description">{str(hit.fields.description)}</p>
      <p className="tile-tags">
        {tags.slice(0, 3).map((t) => (
          <span key={t} className="tag">
            {t}
          </span>
        ))}
        {tags.length > 3 && <span className="tag more">+{tags.length - 3}</span>}
      </p>
    </article>
  );
}

export function Row({ hit, names }: { hit: Hit; names: Names }) {
  return (
    <tr>
      <td>
        <Avatar hit={hit} w={160} />
      </td>
      <td>
        <Link to={`/cards/${hit.id}`}>{str(hit.fields.name)}</Link>
      </td>
      <td>{(hit.tags ?? []).join(", ")}</td>
      <td>{hit.lorebooks?.length ?? 0}</td>
      <td>
        <Badges hit={hit} names={names} />
      </td>
    </tr>
  );
}
```

- [ ] **Step 4: Write the facet panel**

Create `web/src/cards/Facets.tsx`:

```tsx
import type { MouseEvent } from "react";
import type { components } from "../api/schema";
import type { CardQuery, Term } from "./query";
import type { Names } from "./Tile";

type FacetData = components["schemas"]["Facets"];
type Value = { term: Omit<Term, "not">; label: string; cards: number };

function Section({ title, values, q, onToggle }: { title: string; values: Value[]; q: CardQuery; onToggle: (t: Term) => void }) {
  if (values.length === 0) return null;
  return (
    <section className="facet-section">
      <h2>{title}</h2>
      {values.map((v) => {
        const active = q.terms.find((t) => t.kind === v.term.kind && t.value === v.term.value);
        return (
          <button
            key={v.term.value}
            className={"facet" + (active ? (active.not ? " excluded" : " included") : "")}
            onClick={(e: MouseEvent) => onToggle({ ...v.term, not: e.shiftKey })}
          >
            <span>{v.label}</span>
            <span className="count">{v.cards}</span>
          </button>
        );
      })}
    </section>
  );
}

export function Facets({ facets, names, q, onToggle }: { facets?: FacetData; names: Names; q: CardQuery; onToggle: (t: Term) => void }) {
  const status = (s: string) => s.replace("_", " ");
  return (
    <aside className="facets" aria-label="Filters">
      <Section
        title="Sync"
        q={q}
        onToggle={onToggle}
        values={(facets?.targets ?? []).map((t) => ({
          term: { kind: "target", value: `${t.instance}:${t.is}` },
          label: `${names.instances.get(t.instance) ?? "#" + t.instance}: ${status(t.is)}`,
          cards: t.cards,
        }))}
      />
      <Section
        title="Tags"
        q={q}
        onToggle={onToggle}
        values={(facets?.tags ?? []).map((t) => ({ term: { kind: "tag", value: t.name }, label: t.name, cards: t.cards }))}
      />
      <Section
        title="Collections"
        q={q}
        onToggle={onToggle}
        values={(facets?.collections ?? []).map((c) => ({
          term: { kind: "collection", value: String(c.id) },
          label: c.name,
          cards: c.cards,
        }))}
      />
      <Section
        title="Sources"
        q={q}
        onToggle={onToggle}
        values={(facets?.sources ?? []).map((s) => ({ term: { kind: "source", value: s.kind }, label: s.kind, cards: s.cards }))}
      />
    </aside>
  );
}
```

- [ ] **Step 5: Write the page**

Create `web/src/cards/Cards.tsx`:

```tsx
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { Facets } from "./Facets";
import { pageLinks } from "./pages";
import { defaultSort, parseQuery, sizes, toggleTerm, toParams, toSearch, type CardQuery, type Sort, type Term } from "./query";
import { Row, Tile, type Names } from "./Tile";
import "./Cards.css";

const sortLabels: Record<Sort, string> = { relevance: "Best match", updated: "Updated", name: "Name", created: "Created" };

function termLabel(t: Term, names: Names): string {
  let value = t.value;
  if (t.kind === "collection") value = names.collections.get(Number(t.value)) ?? "#" + t.value;
  if (t.kind === "target") {
    const [id, is] = t.value.split(":");
    value = `${names.instances.get(Number(id)) ?? "#" + id} ${is?.replace("_", " ")}`;
  }
  return `${t.not ? "not " : ""}${t.kind}: ${value}`;
}

export function Cards() {
  const [params, setParams] = useSearchParams();
  const q = parseQuery(params);
  const [facetsOpen, setFacetsOpen] = useState(true);
  const go = (next: CardQuery) => setParams(toParams(next));

  const body = toSearch(q);
  const res = useQuery({
    queryKey: ["search", body],
    queryFn: async () => must(await api.POST("/search", { body })),
    placeholderData: keepPreviousData,
  });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const collections = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.name])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
  };

  const search = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const text = String(new FormData(e.currentTarget).get("q") ?? "").trim();
    go({ ...q, text, sort: q.sort === defaultSort(q.text) ? defaultSort(text) : q.sort, page: 1 });
  };

  const total = res.data?.total ?? 0;
  const last = Math.max(1, Math.ceil(total / q.size));
  const hits = res.data?.hits ?? [];

  return (
    <div className={facetsOpen ? "cards" : "cards no-facets"}>
      {facetsOpen && <Facets facets={res.data?.facets} names={names} q={q} onToggle={(t) => go(toggleTerm(q, t))} />}
      <div className="toolbar">
        <button className="button" onClick={() => setFacetsOpen(!facetsOpen)} aria-expanded={facetsOpen}>
          Filters
        </button>
        <form role="search" onSubmit={search}>
          <input key={q.text} className="input" type="search" name="q" defaultValue={q.text} placeholder="Search cards" />
        </form>
        <select
          className="input"
          aria-label="Sort"
          value={q.sort}
          onChange={(e) => go({ ...q, sort: e.target.value as Sort, page: 1 })}
        >
          {(Object.keys(sortLabels) as Sort[])
            .filter((s) => s !== "relevance" || q.text)
            .map((s) => (
              <option key={s} value={s}>
                {sortLabels[s]}
              </option>
            ))}
        </select>
        <button className="button" onClick={() => go({ ...q, view: q.view === "grid" ? "list" : "grid" })}>
          {q.view === "grid" ? "List view" : "Grid view"}
        </button>
        <div className="chips">
          {q.terms.map((t) => (
            <button
              key={t.kind + t.value}
              className={t.not ? "chip excluded" : "chip"}
              aria-label={`Remove ${termLabel(t, names)}`}
              onClick={() => go(toggleTerm(q, t))}
            >
              {termLabel(t, names)} ×
            </button>
          ))}
        </div>
      </div>
      <div className="results">
        {res.isError && <p className="error">{res.error.message}</p>}
        {res.data && <p className="total">{total} cards</p>}
        {q.view === "grid" ? (
          <div className="grid">
            {hits.map((h) => (
              <Tile key={h.id} hit={h} names={names} />
            ))}
          </div>
        ) : (
          <table className="list">
            <tbody>
              {hits.map((h) => (
                <Row key={h.id} hit={h} names={names} />
              ))}
            </tbody>
          </table>
        )}
        <nav className="pager" aria-label="Pages">
          {pageLinks(q.page, last).map((p, i) =>
            p === "…" ? (
              <span key={"gap" + i}>…</span>
            ) : (
              <button
                key={p}
                className={p === q.page ? "button primary" : "button"}
                aria-current={p === q.page ? "page" : undefined}
                onClick={() => go({ ...q, page: p })}
              >
                {p}
              </button>
            ),
          )}
          <select
            className="input"
            aria-label="Cards per page"
            value={q.size}
            onChange={(e) => go({ ...q, size: Number(e.target.value), page: 1 })}
          >
            {sizes.map((s) => (
              <option key={s} value={s}>
                {s} per page
              </option>
            ))}
          </select>
        </nav>
      </div>
    </div>
  );
}
```

A sort the owner picked survives a new search. A default sort follows the text: relevance with text, updated without.

- [ ] **Step 6: Style it**

Create `web/src/cards/Cards.css`:

```css
.cards {
  display: grid;
  grid-template-areas: "facets toolbar" "facets main";
  grid-template-columns: var(--facets-width) 1fr;
  grid-template-rows: auto 1fr;
  min-height: 100%;
}

.cards.no-facets {
  grid-template-areas: "toolbar" "main";
  grid-template-columns: 1fr;
}

.facets {
  grid-area: facets;
  padding: var(--space);
  border-right: 1px solid var(--border);
  overflow: auto;
}

.facet-section h2 {
  font-size: 0.85em;
  text-transform: uppercase;
  color: var(--text-muted);
  margin: calc(var(--space) * 1.5) var(--space) calc(var(--space) * 0.5);
}

.facet {
  display: flex;
  justify-content: space-between;
  width: 100%;
  background: none;
  border: none;
  border-radius: calc(var(--radius) * 0.5);
  padding: calc(var(--space) * 0.25 * var(--density)) var(--space);
  cursor: pointer;
  text-align: left;
}

.facet:hover {
  background: var(--surface-2);
}

.facet.included {
  color: var(--accent);
}

.facet.excluded {
  color: var(--danger);
  text-decoration: line-through;
}

.facet .count {
  color: var(--text-muted);
}

.toolbar {
  grid-area: toolbar;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space);
  align-items: center;
  padding: var(--space) calc(var(--space) * 2);
  border-bottom: 1px solid var(--border);
}

.toolbar form {
  flex: 1 1 200px;
}

.toolbar select {
  width: auto;
}

.chips {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--space) * 0.5);
  flex-basis: 100%;
}

.chips:empty {
  display: none;
}

.chip {
  background: var(--surface-2);
  border: 1px solid var(--accent);
  border-radius: 999px;
  padding: 0 var(--space);
  cursor: pointer;
}

.chip.excluded {
  border-color: var(--danger);
}

.results {
  grid-area: main;
  padding: calc(var(--space) * 2);
}

.total {
  color: var(--text-muted);
  margin: 0 0 var(--space);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(var(--tile-width), 1fr));
  gap: calc(var(--space) * 2);
}

.tile {
  min-width: 0;
}

.tile-image {
  position: relative;
  display: block;
}

.tile .avatar {
  display: block;
  width: 100%;
  aspect-ratio: var(--tile-aspect);
  object-fit: cover;
  border-radius: var(--radius);
  background: var(--surface-2);
}

.avatar.empty {
  display: grid;
  place-items: center;
  font-size: 2em;
  color: var(--text-muted);
}

.badges {
  position: absolute;
  top: var(--space);
  right: var(--space);
  display: flex;
  gap: 4px;
}

.badge {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  box-shadow: 0 0 0 2px var(--bg);
}

.badge.ok { background: var(--ok); }
.badge.ahead { background: var(--ahead); }
.badge.drift { background: var(--drift); }
.badge.unknown { background: var(--unknown); }

.lorebooks {
  position: absolute;
  right: var(--space);
  bottom: var(--space);
  background: var(--bg);
  border-radius: calc(var(--radius) * 0.5);
  padding: 0 4px;
  font-size: 0.85em;
}

.tile-name {
  display: block;
  margin-top: var(--space);
  font-weight: 600;
  text-decoration: none;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.tile-description {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  margin: 2px 0;
  color: var(--text-muted);
  font-size: 0.9em;
}

.tile-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  margin: 0;
}

.tag {
  background: var(--surface-2);
  border-radius: calc(var(--radius) * 0.5);
  padding: 0 6px;
  font-size: 0.8em;
}

.list {
  width: 100%;
  border-collapse: collapse;
}

.list td {
  padding: calc(var(--space) * 0.5);
  border-bottom: 1px solid var(--border);
}

.list .avatar {
  width: 32px;
  height: 32px;
  object-fit: cover;
  border-radius: 50%;
}

.list .badges {
  position: static;
}

.pager {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--space) * 0.5);
  align-items: center;
  margin-top: calc(var(--space) * 2);
}

.pager select {
  width: auto;
  margin-left: auto;
}

@media (max-width: 640px) {
  .cards {
    grid-template-areas: "toolbar" "facets" "main";
    grid-template-columns: 1fr;
    grid-template-rows: auto auto 1fr;
  }

  .facets {
    border-right: none;
    border-bottom: 1px solid var(--border);
    max-height: 40dvh;
  }
}
```

- [ ] **Step 7: Route it**

In `web/src/App.tsx`, import `Cards` and replace the cards route, adding a placeholder for the card page:

```tsx
import { Cards } from "./cards/Cards";
```

```tsx
          { path: "cards", element: <Cards /> },
          { path: "cards/:id", element: <Page title="Card" /> },
```

- [ ] **Step 8: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS. If `getByTitle("Lumiverse: ahead")` misses because the instances query resolves after the tiles render, change it to `await screen.findByTitle(...)`.

- [ ] **Step 9: Commit**

```bash
git add web/src/cards web/src/App.tsx
git commit -m "feat(web): add the Cards page"
```

---

### Task 12: The Collections page

**Files:**
- Create: `web/src/collections/Collections.tsx`
- Create: `web/src/collections/Collections.css`
- Create: `web/src/collections/Collections.test.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/collections/Collections.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Collections } from "./Collections";

let list: { id: number; name: string; description: string; created_at: string; cards: number; avatars?: string[] }[];
let calls: string[];
beforeEach(() => {
  list = [{ id: 3, name: "Crew", description: "", created_at: "2026-10-06T00:00:00Z", cards: 5, avatars: ["ab".repeat(32)] }];
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method !== "GET") calls.push(`${req.method} ${path} ${await req.text()}`);
      if (req.method === "DELETE") return new Response(null, { status: 204 });
      return new Response(JSON.stringify(req.method === "GET" ? list : list[0]), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  vi.stubGlobal("confirm", () => true);
});
afterEach(() => vi.unstubAllGlobals());

function renderCollections() {
  const router = createMemoryRouter([{ path: "/collections", element: <Collections /> }], {
    initialEntries: ["/collections"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("each collection links to its cards with a mosaic", async () => {
  renderCollections();
  const link = await screen.findByRole("link", { name: /Crew/ });
  expect(link).toHaveAttribute("href", "/cards?f=collection%3A3");
  expect(within(link).getByText("5 cards")).toBeInTheDocument();
  expect(link.querySelector("img")).toHaveAttribute("src", `/api/blobs/${"ab".repeat(32)}?w=160`);
});

test("create, rename, and delete", async () => {
  renderCollections();
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "New collection" }), "Villains{Enter}");
  await user.click(screen.getByRole("button", { name: "Rename Crew" }));
  const name = screen.getByRole("textbox", { name: "Name" });
  await user.clear(name);
  await user.type(name, "Sailors{Enter}");
  await user.click(screen.getByRole("button", { name: "Delete Crew" }));
  expect(calls).toEqual([
    'POST /api/collections {"name":"Villains"}',
    'PATCH /api/collections/3 {"name":"Sailors"}',
    "DELETE /api/collections/3 ",
  ]);
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/collections`
Expected: FAIL, `Cannot find module './Collections'`.

- [ ] **Step 3: Implement**

Create `web/src/collections/Collections.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api, must, mustOk, thumb } from "../api/client";
import "./Collections.css";

function nameFrom(e: FormEvent<HTMLFormElement>): string {
  e.preventDefault();
  return String(new FormData(e.currentTarget).get("name") ?? "").trim();
}

export function Collections() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const [renaming, setRenaming] = useState<number | null>(null);
  const onSuccess = () => qc.invalidateQueries({ queryKey: ["collections"] });

  const create = useMutation({
    mutationFn: async (name: string) => must(await api.POST("/collections", { body: { name } })),
    onSuccess,
  });
  const rename = useMutation({
    mutationFn: async ({ id, name }: { id: number; name: string }) =>
      must(await api.PATCH("/collections/{id}", { params: { path: { id } }, body: { name } })),
    onSuccess: () => {
      setRenaming(null);
      return onSuccess();
    },
  });
  const remove = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/collections/{id}", { params: { path: { id } } })),
    onSuccess,
  });
  const failed = create.error ?? rename.error ?? remove.error ?? list.error;

  return (
    <section className="page">
      <h1>Collections</h1>
      <form
        className="collection-new"
        onSubmit={(e) => {
          const name = nameFrom(e);
          if (name) create.mutate(name, { onSuccess: () => (e.target as HTMLFormElement).reset() });
        }}
      >
        <input className="input" name="name" aria-label="New collection" placeholder="New collection" />
        <button className="button primary">Create</button>
      </form>
      {failed && <p className="error">{failed.message}</p>}
      <div className="collections">
        {(list.data ?? []).map((c) => (
          <article key={c.id} className="collection">
            <Link to={`/cards?f=${encodeURIComponent(`collection:${c.id}`)}`}>
              <span className="mosaic">
                {[0, 1, 2, 3].map((i) =>
                  c.avatars?.[i] ? <img key={i} src={thumb(c.avatars[i], 160)} alt="" loading="lazy" /> : <span key={i} />,
                )}
              </span>
              <span className="collection-name">{c.name}</span>
              <span className="collection-count">{c.cards} cards</span>
            </Link>
            {renaming === c.id ? (
              <form onSubmit={(e) => rename.mutate({ id: c.id, name: nameFrom(e) })}>
                <input className="input" name="name" aria-label="Name" defaultValue={c.name} autoFocus />
              </form>
            ) : (
              <div className="collection-actions">
                <button className="button" aria-label={`Rename ${c.name}`} onClick={() => setRenaming(c.id)}>
                  Rename
                </button>
                <button
                  className="button"
                  aria-label={`Delete ${c.name}`}
                  onClick={() => confirm(`Delete ${c.name}? Its cards stay in the library.`) && remove.mutate(c.id)}
                >
                  Delete
                </button>
              </div>
            )}
          </article>
        ))}
      </div>
    </section>
  );
}
```

Create `web/src/collections/Collections.css`:

```css
.collection-new {
  display: flex;
  gap: var(--space);
  max-width: 400px;
  margin-bottom: calc(var(--space) * 2);
}

.collections {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(var(--tile-width), 1fr));
  gap: calc(var(--space) * 2);
}

.collection a {
  display: block;
  text-decoration: none;
}

.mosaic {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 2px;
  aspect-ratio: 1;
  border-radius: var(--radius);
  overflow: hidden;
  background: var(--surface-2);
}

.mosaic img,
.mosaic span {
  width: 100%;
  height: 100%;
  object-fit: cover;
  background: var(--surface);
}

.collection-name {
  display: block;
  margin-top: var(--space);
  font-weight: 600;
}

.collection-count {
  color: var(--text-muted);
  font-size: 0.9em;
}

.collection-actions {
  display: flex;
  gap: calc(var(--space) * 0.5);
  margin-top: calc(var(--space) * 0.5);
}
```

In `web/src/App.tsx`, import it and replace the collections route:

```tsx
import { Collections } from "./collections/Collections";
```

```tsx
          { path: "collections", element: <Collections /> },
```

- [ ] **Step 4: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/collections web/src/App.tsx
git commit -m "feat(web): add the Collections page"
```

---

### Task 13: Check it end to end

- [ ] **Step 1: Run every check CI runs**

```bash
go vet ./... && go test ./...
cd web && npm run gen && git diff --exit-code -- openapi.json src/api/schema.d.ts && npm run typecheck && npm test && npm run build
```

Expected: all pass, and `git diff` prints nothing.

- [ ] **Step 2: Look at it**

Start `innkeeper api` against a database with some cards on a free port (18080 is taken locally), then `cd web && INNKEEPER_URL=http://localhost:<port> npm run dev`. Log in and check:

- Tiles show thumbnails, badges, lorebook counts, and tags with `+N`.
- A facet click and a shift-click filter the grid, and the URL changes.
- Back, refresh, and a pasted URL restore the same page.
- Paging and the page-size picker work, and the narrow-screen layout stacks.
- A collection opens the Cards page filtered to it.

Fix anything broken with a failing test first, then commit.
