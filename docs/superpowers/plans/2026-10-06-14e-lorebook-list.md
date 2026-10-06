# Web UI Lorebook List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** List, search, filter, and page lorebooks on `/lorebooks`, and archive or push many at once.

**Architecture:** The card search runs over a `searchKind` that names the entity and revision tables, so `POST /lorebooks/search` reuses the same query, filters, sorting, paging, and facets. A generated `search` column on `lorebook_revision` covers names, descriptions, entry keys, and entry content. `/bulk` and `/pushes` take `kind: "lorebook"`. The UI's Cards page takes a `kind` prop and renders lorebooks as a table.

**Tech Stack:** Go 1.27, Huma v2, PostgreSQL, React, React Router, TanStack Query, `openapi-fetch`, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-05-web-ui-design.md`, "Lorebooks". Build order phase 5, first half; plan 14f covers the lorebook page.

---

## Design notes

- **One search, two kinds.** `library.Query` is the same type for both. Lorebooks have no tags or collections, so `tag` and `collection` filters, the `tags` and `collections` facets, and the `tags` and `lorebooks` includes return `ErrInvalid` for them. Lorebooks get an include of their own, `cards`, for the IDs of linked cards. Card searches refuse `cards`.
- **Entry count on every lorebook hit.** `Hit.Entries` is set for lorebooks only, counted in SQL, so the list doesn't fetch every entry.
- **Facet counts keep the field name `cards`.** It counts matching items of either kind. Renaming it would break the card UI and MCP for no gain.
- **Bulk for lorebooks is Archive and Push.** `kind` is optional on `/bulk` and `/pushes`, and an empty value means `card`, so existing clients don't change. A lorebook bulk job runs one `lorebook.archive` child per lorebook. `/bulk` refuses other actions for lorebooks.
- **No `default` on `kind`.** Huma marks a field with a default as required in the generated schema, which would force every card caller to send it.
- **The UI reuses the Cards page.** `<Cards kind="lorebook" />` swaps the endpoint, the search body, the nouns, and the results view: a table with no grid toggle. Each route element has its own `key`, so moving between Cards and Lorebooks remounts the page and drops the selection. Linked-card avatars come from one extra `/search` by `ids` per page.
- **Out of this plan:** the lorebook page itself (plan 14f), the CLI and MCP (the API is enough), and lorebook tags.

## Files

| File | Change |
|---|---|
| `internal/db/migrations/0012_lorebook_search.sql` | New: `lorebook_revision.search` |
| `internal/library/search.go`, `search_test.go` | `searchKind`, `SearchLorebooks`, `SearchLorebookIDs` |
| `internal/api/search.go`, `search_test.go` | `POST /lorebooks/search` |
| `internal/bulk/bulk.go`, `bulk_test.go` | `lorebook.archive` |
| `cmd/innkeeper/worker.go` | Register `lorebook.archive` |
| `internal/targets/jobs.go`, `jobs_test.go` | `EnqueueBulkLorebookPush` |
| `internal/api/jobs.go`, `targets.go`, `jobs_test.go` | `kind` on `/bulk` and `/pushes` |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `web/src/cards/query.ts`, `query.test.ts` | `toLorebookSearch` |
| `web/src/cards/SelectionBar.tsx` | `kind` prop |
| `web/src/cards/Cards.tsx`, `Tile.tsx`, `Cards.css`, `Lorebooks.test.tsx` | The lorebook list |
| `web/src/App.tsx` | Route |
| `docs/superpowers/specs/2026-10-05-web-ui-design.md` | Match the spec |

Go tests need PostgreSQL. Run them with:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

Without it, the database tests skip and a run passes without testing anything. Every `go test` command below assumes it's set.

macOS here is case-insensitive: never create a file whose name differs from an existing one only by case.

---

### Task 1: Search lorebooks in the library

**Files:**
- Create: `internal/db/migrations/0012_lorebook_search.sql`
- Modify: `internal/library/search.go`
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/library/search_test.go` (add `"errors"` to its imports if it's missing):

```go
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
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/library -run TestSearchLorebooks`
Expected: FAIL to compile, `lib.SearchLorebooks undefined`.

- [ ] **Step 3: Add the search column**

Create `internal/db/migrations/0012_lorebook_search.sql`:

```sql
-- Search matches the head revision's row. Names rank above descriptions and
-- entry keys, which rank above entry content.
ALTER TABLE lorebook_revision ADD COLUMN search tsvector GENERATED ALWAYS AS (
    setweight(to_tsvector('english', coalesce(data->>'name', '')), 'A') ||
    setweight(to_tsvector('english',
        coalesce(data->>'description', '') || ' ' ||
        coalesce(jsonb_path_query_array(data, '$.entries[*].keys[*]')::text, '')), 'B') ||
    setweight(to_tsvector('english', coalesce(jsonb_path_query_array(data, '$.entries[*].content')::text, '')), 'C')
) STORED;
CREATE INDEX lorebook_revision_search ON lorebook_revision USING gin (search);
```

Check how the other migrations are picked up (embedded by file name in `internal/db`). A new file needs no registration if they're embedded by glob.

- [ ] **Step 4: Run the search by kind**

In `internal/library/search.go`:

Add two fields to `Hit` after `Targets`:

```go
	Cards      []int64        `json:"cards,omitempty" doc:"Lorebooks with include cards: IDs of linked cards."`
	Entries    *int           `json:"entries,omitempty" doc:"Lorebooks only: how many entries it has."`
```

Change these doc strings:
- `Query.Include`: `doc:"Extra data per hit: tags, lorebooks, targets. Lorebooks take cards and targets."`
- `Query.Facets`: `doc:"Counts to return over every match: tags, collections, targets, sources. Lorebooks take targets and sources."`
- `Results.Total`: `doc:"How many match."`

Replace everything from the line `// facetSQL selects each facet's rows for the card IDs in match.` to the end of the file with:

```go
// searchKind is what a search runs over: cards or lorebooks. Every query
// calls the entity c and its head revision r.
type searchKind struct {
	name, table, revTable, sourceTable string
	// col is the entity's ID column in sources and binding_status.
	col string
}

var (
	cardSearch     = searchKind{"card", "card", "card_revision", "card_source", "card_id"}
	lorebookSearch = searchKind{"lorebook", "lorebook", "lorebook_revision", "lorebook_source", "lorebook_id"}
)

func (k searchKind) isCard() bool { return k.name == cardSearch.name }

func (k searchKind) from() string {
	return k.table + ` c JOIN ` + k.revTable + ` r ON r.id = c.head_revision_id`
}

// cardOnly names the facets and includes that need card tags, collections,
// or linked lorebooks.
func cardOnly(name string) bool { return name == "tags" || name == "collections" || name == "lorebooks" }

// facetSQL selects each facet's rows for the IDs in match.
var facetSQL = map[string]func(k searchKind, match string) string{
	"tags": func(_ searchKind, m string) string {
		return `SELECT t.name, count(*)::int FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
			WHERE ct.card_id IN (` + m + `) GROUP BY t.id ORDER BY count(*) DESC, lower(t.name) LIMIT 50`
	},
	"collections": func(_ searchKind, m string) string {
		return `SELECT co.id, co.name, count(*)::int FROM collection_card cc JOIN collection co ON co.id = cc.collection_id
			WHERE cc.card_id IN (` + m + `) GROUP BY co.id ORDER BY lower(co.name)`
	},
	"targets": func(k searchKind, m string) string {
		return `SELECT instance_id, status, count(*)::int FROM binding_status
			WHERE ` + k.col + ` IN (` + m + `) GROUP BY 1, 2 ORDER BY 1, 2`
	},
	"sources": func(k searchKind, m string) string {
		return `SELECT kind, count(DISTINCT ` + k.col + `)::int FROM ` + k.sourceTable + `
			WHERE removed_at IS NULL AND ` + k.col + ` IN (` + m + `) GROUP BY kind ORDER BY kind`
	},
}

// facets counts the matches of cond for each facet in names.
func (l *Library) facets(ctx context.Context, k searchKind, names []string, cond string, args []any) (*Facets, error) {
	match := `SELECT c.id FROM ` + k.from() + ` WHERE ` + cond
	f := &Facets{}
	for _, name := range names {
		rows, err := l.pool.Query(ctx, facetSQL[name](k, match), args...)
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

// where collects SQL conditions and their arguments.
type where struct {
	k    searchKind
	args []any
	n    int
}

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) filter(f Filter) (string, error) {
	// Bounds the SQL size, so a huge filter is a 400 instead of a Postgres error.
	if w.n++; w.n > 200 {
		return "", fmt.Errorf("%w: filter has more than 200 conditions", ErrInvalid)
	}
	set := 0
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.IDs != nil, f.Tag != nil, f.Collection != nil, f.Source != nil, f.Archived != nil, f.TargetState != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return "", fmt.Errorf("%w: each filter needs exactly one condition", ErrInvalid)
	}
	if !w.k.isCard() && (f.Tag != nil || f.Collection != nil) {
		return "", fmt.Errorf("%w: lorebooks have no tags or collections", ErrInvalid)
	}
	switch {
	case f.And != nil:
		return w.join(f.And, " AND ", "TRUE")
	case f.Or != nil:
		return w.join(f.Or, " OR ", "FALSE")
	case f.Not != nil:
		s, err := w.filter(*f.Not)
		return "NOT (" + s + ")", err
	case f.IDs != nil:
		return `c.id = ANY(` + w.arg(f.IDs) + `)`, nil
	case f.Tag != nil:
		return `EXISTS (SELECT 1 FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
			WHERE ct.card_id = c.id AND lower(t.name) = lower(` + w.arg(*f.Tag) + `))`, nil
	case f.Collection != nil:
		return `EXISTS (SELECT 1 FROM collection_card cc
			WHERE cc.card_id = c.id AND cc.collection_id = ` + w.arg(*f.Collection) + `)`, nil
	case f.Source != nil:
		return `EXISTS (SELECT 1 FROM ` + w.k.sourceTable + ` cs
			WHERE cs.` + w.k.col + ` = c.id AND cs.kind = ` + w.arg(*f.Source) + ` AND cs.removed_at IS NULL)`, nil
	case f.TargetState != nil:
		if !slices.Contains(targetStatuses, f.TargetState.Is) {
			return "", fmt.Errorf("%w: target_state.is must be one of %v", ErrInvalid, targetStatuses)
		}
		return `EXISTS (SELECT 1 FROM binding_status bs WHERE bs.` + w.k.col + ` = c.id
			AND bs.instance_id = ` + w.arg(f.TargetState.Instance) + ` AND bs.status = ` + w.arg(f.TargetState.Is) + `)`, nil
	}
	return `((c.archived_at IS NOT NULL) = ` + w.arg(*f.Archived) + `)`, nil
}

func (w *where) join(fs []Filter, op, empty string) (string, error) {
	if len(fs) == 0 {
		return empty, nil
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		s, err := w.filter(f)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, op) + ")", nil
}

func mentionsArchived(f *Filter) bool {
	if f == nil {
		return false
	}
	if f.Archived != nil || mentionsArchived(f.Not) {
		return true
	}
	return slices.ContainsFunc(f.And, func(g Filter) bool { return mentionsArchived(&g) }) ||
		slices.ContainsFunc(f.Or, func(g Filter) bool { return mentionsArchived(&g) })
}

// compile returns the WHERE condition for q over k's entity c joined to its
// head revision r, and the placeholder holding the text query, if any.
func compile(k searchKind, q Query) (string, *where, string, error) {
	if q.Semantic {
		return "", nil, "", fmt.Errorf("%w: semantic search needs an embedding enricher, and none is installed", ErrInvalid)
	}
	for _, s := range q.Sources {
		if s != "library" {
			return "", nil, "", fmt.Errorf("%w: unknown source %q", ErrInvalid, s)
		}
	}
	w := &where{k: k}
	conds := []string{"TRUE"}
	if q.Filter != nil {
		s, err := w.filter(*q.Filter)
		if err != nil {
			return "", nil, "", err
		}
		conds = append(conds, s)
	}
	if !mentionsArchived(q.Filter) {
		conds = append(conds, "c.archived_at IS NULL")
	}
	text := ""
	if q.Text != "" {
		text = w.arg(q.Text)
		conds = append(conds, "r.search @@ websearch_to_tsquery('english', "+text+")")
	}
	return strings.Join(conds, " AND "), w, text, nil
}

// Search returns one page of the library's cards that match q, best match
// first when q has text, newest first otherwise.
func (l *Library) Search(ctx context.Context, q Query) (Results, error) {
	return l.search(ctx, cardSearch, q)
}

// SearchLorebooks is Search over lorebooks.
func (l *Library) SearchLorebooks(ctx context.Context, q Query) (Results, error) {
	return l.search(ctx, lorebookSearch, q)
}

// ponytail: the cursor is an offset, so a page can repeat or skip an item
// that was added or archived since the last page; switch to keyset paging
// if that matters.
func (l *Library) search(ctx context.Context, k searchKind, q Query) (Results, error) {
	cond, w, text, err := compile(k, q)
	if err != nil {
		return Results{}, err
	}
	size := q.Page.Size
	if size == 0 {
		size = 50
	}
	if size < 0 || size > 500 {
		return Results{}, fmt.Errorf("%w: page size must be 0 to 500", ErrInvalid)
	}
	offset := 0
	switch {
	case q.Page.Cursor != nil && q.Page.Number != 0:
		return Results{}, fmt.Errorf("%w: send a page cursor or a page number, not both", ErrInvalid)
	case q.Page.Cursor != nil:
		if offset, err = strconv.Atoi(*q.Page.Cursor); err != nil || offset < 0 {
			return Results{}, fmt.Errorf("%w: bad cursor", ErrInvalid)
		}
	case q.Page.Number < 0 || q.Page.Number > 1_000_000:
		return Results{}, fmt.Errorf("%w: page number must be 1 to 1000000", ErrInvalid)
	case q.Page.Number > 0:
		offset = (q.Page.Number - 1) * size
	}
	fields := q.Fields
	if len(fields) == 0 {
		fields = []string{"name"}
	}
	tags, books, targets, cards, entries := `NULL::text[]`, `NULL::bigint[]`, `NULL::jsonb`, `NULL::bigint[]`, `NULL::int`
	if !k.isCard() {
		entries = `CASE jsonb_typeof(r.data->'entries') WHEN 'array' THEN jsonb_array_length(r.data->'entries') ELSE 0 END`
	}
	for _, inc := range q.Include {
		if !k.isCard() && cardOnly(inc) || k.isCard() && inc == "cards" {
			return Results{}, fmt.Errorf("%w: a %s search can't include %q", ErrInvalid, k.name, inc)
		}
		switch inc {
		case "tags":
			tags = `array(SELECT t.name FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
				WHERE ct.card_id = c.id ORDER BY lower(t.name))`
		case "lorebooks":
			books = `array(SELECT lorebook_id FROM card_lorebook WHERE card_id = c.id ORDER BY lorebook_id)`
		case "cards":
			cards = `array(SELECT card_id FROM card_lorebook WHERE lorebook_id = c.id ORDER BY card_id)`
		case "targets":
			targets = `(SELECT jsonb_agg(jsonb_build_object('instance', instance_id, 'is', status) ORDER BY instance_id)
				FROM binding_status WHERE ` + k.col + ` = c.id)`
		default:
			return Results{}, fmt.Errorf("%w: unknown include %q", ErrInvalid, inc)
		}
	}
	for _, name := range q.Facets {
		if facetSQL[name] == nil || !k.isCard() && cardOnly(name) {
			return Results{}, fmt.Errorf("%w: unknown facet %q", ErrInvalid, name)
		}
	}
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
	res := Results{Hits: []Hit{}}
	if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM `+k.from()+` WHERE `+cond, w.args...).Scan(&res.Total); err != nil {
		return Results{}, err
	}
	if len(q.Facets) > 0 {
		if res.Facets, err = l.facets(ctx, k, q.Facets, cond, w.args); err != nil {
			return Results{}, err
		}
	}
	sql := `SELECT c.id, c.slug, r.id, c.archived_at, r.data, r.avatar_blob, ` + tags + `, ` + books + `, ` + targets + `, ` + cards + `, ` + entries + `
		FROM ` + k.from() + `
		WHERE ` + cond + ` ORDER BY ` + order + ` LIMIT ` + w.arg(size+1) + ` OFFSET ` + w.arg(offset)
	rows, err := l.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return Results{}, err
	}
	var h Hit
	var data []byte
	var avatar *string
	_, err = pgx.ForEachRow(rows, []any{&h.ID, &h.Slug, &h.RevisionID, &h.ArchivedAt, &data, &avatar, &h.Tags, &h.Lorebooks, &h.Targets, &h.Cards, &h.Entries},
		func() error {
			var all map[string]any
			if err := json.Unmarshal(data, &all); err != nil {
				return err
			}
			h.Fields = map[string]any{}
			for _, f := range fields {
				if f == "avatar" {
					// The avatar is the blob only, never a same-named data key.
					if avatar != nil {
						h.Fields[f] = *avatar
					}
				} else if v, ok := all[f]; ok {
					h.Fields[f] = v
				}
			}
			res.Hits = append(res.Hits, h)
			h = Hit{}
			return nil
		})
	if err != nil {
		return Results{}, err
	}
	if len(res.Hits) > size {
		res.Hits = res.Hits[:size]
		next := strconv.Itoa(offset + size)
		res.NextCursor = &next
	}
	return res, nil
}

// SearchIDs returns the IDs of every card q matches, ignoring its paging and
// output options.
func (l *Library) SearchIDs(ctx context.Context, q Query) ([]int64, error) {
	return l.searchIDs(ctx, cardSearch, q)
}

// SearchLorebookIDs is SearchIDs over lorebooks.
func (l *Library) SearchLorebookIDs(ctx context.Context, q Query) ([]int64, error) {
	return l.searchIDs(ctx, lorebookSearch, q)
}

func (l *Library) searchIDs(ctx context.Context, k searchKind, q Query) ([]int64, error) {
	cond, w, _, err := compile(k, q)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM `+k.from()+` WHERE `+cond+` ORDER BY c.id`, w.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
```

Check that `lorebook_revision` has an `avatar_blob` column (migration 0002 indexes it). If it doesn't, select `NULL::text` in its place for lorebooks.

- [ ] **Step 5: Run the library tests**

Run: `go test ./internal/library`
Expected: PASS, including the existing `TestSearch*` tests unchanged.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0012_lorebook_search.sql internal/library/search.go internal/library/search_test.go
git commit -m "feat(search): search lorebooks"
```

---

### Task 2: `POST /lorebooks/search`

**Files:**
- Modify: `internal/api/search.go`
- Test: `internal/api/search_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/search_test.go`:

```go
func TestSearchLorebooksRoute(t *testing.T) {
	e := newEnv(t)
	var forge, sea entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`)}, 201, &forge)
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Sea", "entries": []}`)}, 201, &sea)

	var res struct {
		Total int `json:"total"`
		Hits  []struct {
			ID      int64 `json:"id"`
			Entries int   `json:"entries"`
		} `json:"hits"`
	}
	e.call("POST", "/lorebooks/search", map[string]any{"text": "anvil"}, 200, &res)
	if res.Total != 1 || res.Hits[0].ID != forge.ID || res.Hits[0].Entries != 1 {
		t.Fatalf("search = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("all = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{"facets": []string{"tags"}}, 400, nil)
}
```

Add `"encoding/json"` to the test file's imports if it's missing.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api -run TestSearchLorebooksRoute`
Expected: FAIL; `POST /lorebooks/search` isn't routed.

- [ ] **Step 3: Add the route**

In `internal/api/search.go`, add inside `searchRoutes` after the `search-cards` route:

```go
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "search-lorebooks", Method: http.MethodPost, Path: "/lorebooks/search",
		Summary: "Search the library's lorebooks",
		Description: "Takes the same query as POST /search. Text matches names, descriptions, entry keys, and entry content. " +
			"Lorebooks have no tags or collections. Send {} to list every lorebook that isn't archived, newest first.",
		Tags: []string{"Search"}},
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			res, err := s.Lib.SearchLorebooks(ctx, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return &searchOut{Body: res}, nil
		})
```

- [ ] **Step 4: Run the API tests**

Run: `go test ./internal/api`
Expected: PASS. If the route clashes with `/lorebooks/{id}`, check how Huma's router orders a literal segment against a parameter, and report it.

- [ ] **Step 5: Commit**

```bash
git add internal/api/search.go internal/api/search_test.go
git commit -m "feat(api): search lorebooks"
```

---

### Task 3: Archive lorebooks in bulk

**Files:**
- Modify: `internal/bulk/bulk.go`, `cmd/innkeeper/worker.go`
- Test: `internal/bulk/bulk_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/bulk/bulk_test.go`:

```go
func TestArchiveLorebooks(t *testing.T) {
	ctx := context.Background()
	lib, q, _ := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{LorebookArchiveKind: ArchiveLorebook(lib)}
	})
	var ids []int64
	for _, slug := range []string{"a", "b"} {
		l, _, err := lib.Lorebooks.Create(ctx, slug, "upload", library.Edit{Data: []byte(`{"name": "x", "entries": []}`), Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, l.ID)
	}
	parent, err := EnqueueLorebookArchive(ctx, q, ids, true)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, parent.ID)
	for _, id := range ids {
		if l, _ := lib.Lorebooks.Get(ctx, id); l.ArchivedAt == nil {
			t.Errorf("lorebook %d not archived", id)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/bulk -run TestArchiveLorebooks`
Expected: FAIL to compile, `undefined: LorebookArchiveKind`.

- [ ] **Step 3: Add the job kind**

In `internal/bulk/bulk.go`, add to the `const` block:

```go
	LorebookArchiveKind = "lorebook.archive"
```

Add after `archivePayload`:

```go
type lorebookArchivePayload struct {
	LorebookID int64 `json:"lorebook_id"`
	Archived   bool  `json:"archived"`
}
```

Add after `EnqueueArchive`:

```go
// EnqueueLorebookArchive archives or unarchives lorebooks, one child job per
// lorebook.
func EnqueueLorebookArchive(ctx context.Context, q *jobs.Queue, ids []int64, archived bool) (jobs.Job, error) {
	return enqueue(ctx, q, LorebookArchiveKind, map[string]any{"archived": archived}, ids,
		func(id int64) any { return lorebookArchivePayload{LorebookID: id, Archived: archived} })
}
```

Add after `Archive`:

```go
// ArchiveLorebook is the jobs.Handler for LorebookArchiveKind.
func ArchiveLorebook(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p lorebookArchivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return lib.Lorebooks.SetArchived(ctx, p.LorebookID, p.Archived)
	}
}
```

In `cmd/innkeeper/worker.go`, add to the handler map after `bulk.ArchiveKind`:

```go
			bulk.LorebookArchiveKind: bulk.ArchiveLorebook(svc.lib),
```

Grep for `card.archive` across `internal/` and `web/src/`. If anything lists job kinds (an enum, a label map, a filter), add `lorebook.archive` there too.

- [ ] **Step 4: Run the tests**

Run: `go build ./... && go test ./internal/bulk`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bulk/bulk.go internal/bulk/bulk_test.go cmd/innkeeper/worker.go
git commit -m "feat(bulk): archive lorebooks in bulk"
```

---

### Task 4: Push lorebooks in bulk

**Files:**
- Modify: `internal/targets/jobs.go`
- Test: `internal/targets/jobs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/targets/jobs_test.go`:

```go
func TestBulkLorebookPush(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	a, b := e.newLorebook("Forge lore"), e.newLorebook("Sea lore")
	q := jobs.New(e.pool)
	parent, err := e.s.EnqueueBulkLorebookPush(e.ctx, q, e.inst, []int64{a, b, a}, false)
	if err != nil {
		t.Fatal(err)
	}
	if j := e.startWorker(q)(parent.ID); j.Status != jobs.StatusSucceeded {
		t.Fatalf("bulk push %s: %v", j.Status, j.Error)
	}
	if e.lorebookStatus(a) != "in_sync" || e.lorebookStatus(b) != "in_sync" {
		t.Fatal("bulk push didn't push both lorebooks")
	}
}
```

If `newLorebook` builds the slug from the name, the two names give two lorebooks. If it doesn't, give them distinct slugs as the helper allows.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/targets -run TestBulkLorebookPush`
Expected: FAIL to compile, `e.s.EnqueueBulkLorebookPush undefined`.

- [ ] **Step 3: Push by kind**

In `internal/targets/jobs.go`, replace `EnqueueBulkPush` with:

```go
// EnqueueBulkPush queues a push of each card, as one bulk job.
func (s *Service) EnqueueBulkPush(ctx context.Context, q *jobs.Queue, instanceID int64, cardIDs []int64, newCopy bool) (jobs.Job, error) {
	return s.enqueueBulkPush(ctx, q, cardKind, instanceID, cardIDs, newCopy)
}

// EnqueueBulkLorebookPush queues a push of each lorebook, as one bulk job.
func (s *Service) EnqueueBulkLorebookPush(ctx context.Context, q *jobs.Queue, instanceID int64, lorebookIDs []int64, newCopy bool) (jobs.Job, error) {
	return s.enqueueBulkPush(ctx, q, lorebookKind, instanceID, lorebookIDs, newCopy)
}

func (s *Service) enqueueBulkPush(ctx context.Context, q *jobs.Queue, k kind, instanceID int64, ids []int64, newCopy bool) (jobs.Job, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	rows, err := s.pool.Query(ctx, `SELECT b.`+k.col+`, max(p.id) FROM binding b JOIN push p ON p.binding_id = b.id
		WHERE b.instance_id = $1 AND b.`+k.col+` = ANY($2) GROUP BY b.`+k.col, instanceID, ids)
	if err != nil {
		return jobs.Job{}, err
	}
	lastPush := map[int64]int64{}
	var id, last int64
	if _, err := pgx.ForEachRow(rows, []any{&id, &last}, func() error {
		lastPush[id] = last
		return nil
	}); err != nil {
		return jobs.Job{}, err
	}
	children := make([]jobs.Spec, len(ids))
	for i, id := range ids {
		p := pushPayload{NewCopy: newCopy}
		if k.isLorebook() {
			p.LorebookID = id
		} else {
			p.CardID = id
		}
		if v, ok := lastPush[id]; ok {
			p.LastPush = &v
		}
		children[i] = jobs.Spec{Kind: PushKind, InstanceID: &instanceID, Payload: p}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: bulk.Kind, InstanceID: &instanceID,
		Payload: map[string]any{"action": PushKind, "new_copy": newCopy}}, children)
}
```

- [ ] **Step 4: Run the targets tests**

Run: `go test ./internal/targets`
Expected: PASS, including `TestJobs` and `TestBulkPushDedupes`.

- [ ] **Step 5: Commit**

```bash
git add internal/targets/jobs.go internal/targets/jobs_test.go
git commit -m "feat(sync): push lorebooks in bulk"
```

---

### Task 5: `kind` on `/bulk` and `/pushes`

**Files:**
- Modify: `internal/api/jobs.go`, `internal/api/targets.go`
- Test: `internal/api/jobs_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/jobs_test.go`:

```go
func TestBulkLorebooks(t *testing.T) {
	e := newEnv(t)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.LorebookArchiveKind: bulk.ArchiveLorebook(e.s.Lib)}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	var forge entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": []}`)}, 201, &forge)
	pick := map[string]any{"filter": map[string]any{"ids": []int64{forge.ID}}}

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"kind": "lorebook", "query": pick, "action": "archive"}, 202, &j)
	if last, _ := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("bulk archive = %+v", last)
	}
	var got entityResult
	e.call("GET", fmt.Sprintf("/lorebooks/%d", forge.ID), nil, 200, &got)
	if got.ArchivedAt == nil {
		t.Fatal("lorebook not archived")
	}

	e.call("POST", "/bulk", map[string]any{"kind": "lorebook", "query": pick, "action": "tag", "tags": []string{"x"}}, 400, nil)
	e.call("POST", "/bulk", map[string]any{"kind": "spell", "query": pick, "action": "archive"}, 422, nil)
}
```

Use the field names `jobOut` and `entityResult` actually have (check `Status` and `ArchivedAt`). If `watchJob` doesn't end on success, wait for the job the way `TestBulkArchiveAndWatch` does. Add any missing imports (`encoding/json`, `internal/bulk`, `internal/jobs`).

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api -run TestBulkLorebooks`
Expected: FAIL. `kind` is an unknown field, or archive runs as a card job.

- [ ] **Step 3: Take `kind` on `/bulk`**

In `internal/api/jobs.go`, add a field to `bulkIn.Body` after `Query`:

```go
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card. Lorebooks take archive and unarchive only."`
```

Change the operation's `Summary` to `"Archive, tag, collect, or export many cards, or archive many lorebooks"`.

In the handler, after the `q.Filter == nil && q.Text == ""` check, add:

```go
			lorebooks := b.Kind == "lorebook"
			if lorebooks && b.Action != "archive" && b.Action != "unarchive" {
				return nil, huma.Error400BadRequest("lorebooks can only be archived or unarchived in bulk; push them with POST /pushes")
			}
```

Replace `ids, err := s.Lib.SearchIDs(ctx, q)` with:

```go
			search := s.Lib.SearchIDs
			if lorebooks {
				search = s.Lib.SearchLorebookIDs
			}
			ids, err := search(ctx, q)
```

Replace the `case "archive", "unarchive":` branch of the enqueue switch with:

```go
			case "archive", "unarchive":
				if lorebooks {
					p, err = bulk.EnqueueLorebookArchive(ctx, s.Queue, ids, b.Action == "archive")
				} else {
					p, err = bulk.EnqueueArchive(ctx, s.Queue, ids, b.Action == "archive")
				}
```

- [ ] **Step 4: Take `kind` on `/pushes`**

In `internal/api/targets.go`, add a field to `bulkPushIn.Body` after `Query`:

```go
		Kind       string        `json:"kind,omitempty" enum:"card,lorebook" doc:"What the query selects; default card."`
```

Change the `push-cards` operation's `Summary` to `"Push many cards or lorebooks to a target"`. In its handler, replace from `ids, err := s.Lib.SearchIDs(ctx, q)` through the `EnqueueBulkPush` call with:

```go
			search, enqueue := s.Lib.SearchIDs, s.Targets.EnqueueBulkPush
			if in.Body.Kind == "lorebook" {
				search, enqueue = s.Lib.SearchLorebookIDs, s.Targets.EnqueueBulkLorebookPush
			}
			ids, err := search(ctx, q)
			if err != nil {
				return nil, s.fail(err)
			}
			p, err := enqueue(ctx, s.Queue, in.Body.InstanceID, ids, in.Body.NewCopy)
```

- [ ] **Step 5: Run the API tests**

Run: `go test ./internal/api`
Expected: PASS.

- [ ] **Step 6: Regenerate the client**

Run: `cd web && npm run gen && grep -n '"/lorebooks/search"' src/api/schema.d.ts && npm run typecheck`
Expected: the grep prints a line, and typecheck passes. Check in `schema.d.ts` that `kind` is optional (`kind?:`) on both bulk bodies.

- [ ] **Step 7: Commit**

```bash
git add internal/api/jobs.go internal/api/targets.go internal/api/jobs_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): archive and push lorebooks in bulk"
```

---

### Task 6: The lorebook search body

**Files:**
- Modify: `web/src/cards/query.ts`
- Test: `web/src/cards/query.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `web/src/cards/query.test.ts`, adding `toLorebookSearch` to its import from `./query`:

```ts
test("toLorebookSearch asks for linked cards, sync, and lorebook facets", () => {
  const q = parseQuery(new URLSearchParams("q=anvil&f=source:chub&page=2"));
  expect(toLorebookSearch(q)).toEqual({
    text: "anvil",
    filter: { and: [{ source: "chub" }] },
    sort: "relevance",
    fields: ["name", "description"],
    include: ["cards", "targets"],
    facets: ["targets", "sources"],
    page: { size: 48, number: 2 },
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/cards/query.test.ts`
Expected: FAIL, `toLorebookSearch` isn't exported.

- [ ] **Step 3: Add it**

Append to `web/src/cards/query.ts`:

```ts
/** toLorebookSearch is the POST /api/lorebooks/search body for the state. */
export function toLorebookSearch(q: CardQuery): Search {
  return { ...toSearch(q), fields: ["name", "description"], include: ["cards", "targets"], facets: ["targets", "sources"] };
}
```

- [ ] **Step 4: Run it to see it pass**

Run: `cd web && npx vitest run src/cards/query.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/cards/query.ts web/src/cards/query.test.ts
git commit -m "feat(web): build a lorebook search"
```

---

### Task 7: The lorebook list

**Files:**
- Create: `web/src/cards/Lorebooks.test.tsx`
- Modify: `web/src/cards/Cards.tsx`, `web/src/cards/SelectionBar.tsx`, `web/src/cards/Tile.tsx`, `web/src/cards/Cards.css`, `web/src/App.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/cards/Lorebooks.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Cards } from "./Cards";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const results = {
  total: 1,
  hits: [{ id: 3, slug: "forge", revision_id: 1, fields: { name: "Forge" }, entries: 2, cards: [7], targets: [{ instance: 2, is: "ahead" }] }],
  facets: { targets: [{ instance: 2, is: "ahead", cards: 1 }], sources: [{ kind: "chub", cards: 1 }] },
};

let searches: { path: string; body: unknown }[];
let posts: { path: string; body: unknown }[];
beforeEach(() => {
  searches = [];
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/lorebooks/search") {
        searches.push({ path, body: await req.json() });
        return json(results);
      }
      if (path === "/api/search") {
        searches.push({ path, body: await req.json() });
        return json({ total: 1, hits: [{ id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka", avatar: "ab".repeat(32) } }] });
      }
      if (path === "/api/bulk" || path === "/api/pushes") {
        posts.push({ path, body: await req.json() });
        return json({ id: 5, kind: "bulk", status: "waiting" });
      }
      if (path === "/api/instances") return json([{ id: 2, name: "Lumiverse", plugin: "lumiverse" }]);
      if (path === "/api/plugins") return json([{ name: "lumiverse", capabilities: ["push"] }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter([{ path: "/lorebooks", element: <Toasts><Cards kind="lorebook" /></Toasts> }], {
    initialEntries: [entry],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("lists lorebooks with entries, linked cards, and sync", async () => {
  renderAt("/lorebooks");
  expect(await screen.findByRole("link", { name: "Forge" })).toHaveAttribute("href", "/lorebooks/3");
  expect(screen.getByText("2 entries")).toBeInTheDocument();
  expect(await screen.findByRole("link", { name: "Brakka" })).toHaveAttribute("href", "/cards/7");
  expect(searches[0]).toMatchObject({ path: "/api/lorebooks/search", body: { include: ["cards", "targets"], facets: ["targets", "sources"] } });
  expect(searches).toContainEqual({ path: "/api/search", body: { filter: { ids: [7] }, fields: ["name", "avatar"], page: { size: 500 } } });
  expect(screen.getByRole("searchbox")).toHaveAttribute("placeholder", "Search lorebooks");
  expect(screen.queryByRole("button", { name: "List view" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /chub/ })).toBeInTheDocument();
});

test("bulk archive and push send kind lorebook", async () => {
  renderAt("/lorebooks");
  await userEvent.click(await screen.findByRole("checkbox", { name: "Select Forge" }));
  expect(screen.queryByRole("button", { name: "Tag" })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Archive" }));
  await userEvent.click(screen.getByRole("button", { name: "Archive 1 lorebook" }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ path: "/api/bulk", body: { kind: "lorebook", action: "archive", query: { filter: { ids: [3] } } } });

  await userEvent.click(await screen.findByRole("checkbox", { name: "Select Forge" }));
  await userEvent.click(screen.getByRole("button", { name: "Push" }));
  await userEvent.click(await screen.findByRole("button", { name: "Push 1 lorebook" }));
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts[1]).toMatchObject({ path: "/api/pushes", body: { kind: "lorebook", instance_id: 2 } });
});
```

If the selection's query for picked IDs isn't exactly `{ filter: { ids: [3] } }`, match what `target()` in `selection.ts` builds.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/cards/Lorebooks.test.tsx`
Expected: FAIL. `Cards` ignores `kind` and calls `/api/search`.

- [ ] **Step 3: Give the selection bar a kind**

In `web/src/cards/SelectionBar.tsx`:

Remove the module-level `plural` const. Add `kind?: "card" | "lorebook";` to the props type, and `kind = "card"` to the destructuring. Inside the component, after the destructuring:

```tsx
  const noun = kind === "card" ? "card" : "lorebook";
  const plural = (n: number) => `${n} ${noun}${n === 1 ? "" : "s"}`;
  // Lorebooks have no tags, collections, or bulk export.
  const actions = kind === "card" ? (["tag", "collection", "push", "archive", "export"] as const) : (["push", "archive"] as const);
  // Card calls leave kind out, as they did before it existed.
  const kindField = kind === "lorebook" ? { kind } : {};
```

Change the heading to:

```tsx
      <strong>{all ? `All ${total} matching ${noun}s` : `${count} selected`}</strong>
```

Change `bulk` to send `kindField`:

```tsx
  const bulk = (body: Omit<Bulk, "query">) =>
    start.mutate(async () => must(await api.POST("/bulk", { body: { query, ...kindField, ...body } })));
```

Change the push call's body to `{ instance_id: instanceId, query, ...kindField }`, and the action buttons' list from the literal array to `actions.map(...)`.

- [ ] **Step 4: Add the lorebook row**

In `web/src/cards/Tile.tsx`, append:

```tsx
/** LorebookRow is one lorebook in the lorebook list. cards maps linked card IDs to their hits. */
export function LorebookRow({ hit, names, cards, pick }: { hit: Hit; names: Names; cards: Map<number, Hit>; pick: PickProps }) {
  const entries = hit.entries ?? 0;
  return (
    <tr>
      <td>
        <SelectBox hit={hit} pick={pick} />
      </td>
      <td>
        <Link to={`/lorebooks/${hit.id}`}>{str(hit.fields.name)}</Link>
      </td>
      <td>
        {entries} {entries === 1 ? "entry" : "entries"}
      </td>
      <td className="linked">
        {(hit.cards ?? []).map((id) => {
          const card = cards.get(id);
          const name = card ? str(card.fields.name) : `Card ${id}`;
          return (
            <Link key={id} to={`/cards/${id}`} aria-label={name} title={name}>
              {card ? <Avatar hit={card} w={160} /> : <span className="avatar empty">#</span>}
            </Link>
          );
        })}
      </td>
      <td>
        <Badges hit={hit} names={names} />
      </td>
    </tr>
  );
}
```

- [ ] **Step 5: Give the page a kind**

In `web/src/cards/Cards.tsx`:

Change the query import to include `toLorebookSearch`, and the Tile import to `import { LorebookRow, Row, Tile, type Names } from "./Tile";`.

Change the component signature and its first lines:

```tsx
export function Cards({ kind = "card" }: { kind?: "card" | "lorebook" }) {
  const noun = kind === "card" ? "cards" : "lorebooks";
  const Noun = kind === "card" ? "Cards" : "Lorebooks";
```

Replace the `body` and `res` lines with:

```tsx
  const body = kind === "card" ? toSearch(q) : toLorebookSearch(q);
  const res = useQuery({
    queryKey: ["search", kind, body],
    queryFn: async () => must(kind === "card" ? await api.POST("/search", { body }) : await api.POST("/lorebooks/search", { body })),
    placeholderData: keepPreviousData,
  });
```

After `const hits = res.data?.hits ?? [];`, add:

```tsx
  // Linked cards' names and avatars for the lorebook rows, in one search.
  const linkedIds = kind === "lorebook" ? [...new Set(hits.flatMap((h) => h.cards ?? []))].slice(0, 500) : [];
  const linkedBody = { filter: { ids: linkedIds }, fields: ["name", "avatar"], page: { size: 500 } };
  const linked = useQuery({
    queryKey: ["search", "card", linkedBody],
    enabled: linkedIds.length > 0,
    queryFn: async () => must(await api.POST("/search", { body: linkedBody })),
  });
  const linkedCards = new Map((linked.data?.hits ?? []).map((h) => [h.id, h]));
```

Pass `kind={kind}` to `<SelectionBar … />`.

In the toolbar, change the search placeholder to `` placeholder={`Search ${noun}`} ``, and render the grid/list toggle only when `kind === "card"`.

In the results, change `{total} cards` to `{total} {noun}`, `No cards on this page.` to `No {noun} on this page.`, and the page-size picker's `aria-label` to `` `${Noun} per page` ``. Replace the grid/table block with:

```tsx
        {kind === "lorebook" ? (
          <table className="list">
            <tbody>
              {hits.map((h) => (
                <LorebookRow key={h.id} hit={h} names={names} cards={linkedCards} pick={pickFor(h.id)} />
              ))}
            </tbody>
          </table>
        ) : q.view === "grid" ? (
          <div className="grid">
            {hits.map((h) => (
              <Tile key={h.id} hit={h} names={names} pick={pickFor(h.id)} />
            ))}
          </div>
        ) : (
          <table className="list">
            <tbody>
              {hits.map((h) => (
                <Row key={h.id} hit={h} names={names} pick={pickFor(h.id)} />
              ))}
            </tbody>
          </table>
        )}
```

All hooks stay above any early return.

- [ ] **Step 6: Style the linked avatars**

Append to `web/src/cards/Cards.css`:

```css
.list .linked a {
  display: inline-block;
  margin-right: calc(var(--space) * -0.5);
}

.list .linked .avatar {
  width: 24px;
  height: 36px;
  border-radius: calc(var(--radius) * 0.5);
  border: 1px solid var(--bg);
}
```

- [ ] **Step 7: Add the route**

In `web/src/App.tsx`, give each list its own key, so moving between them remounts the page and drops the selection:

```tsx
          { path: "cards", element: <Cards key="cards" /> },
```

```tsx
          { path: "lorebooks", element: <Cards key="lorebooks" kind="lorebook" /> },
```

- [ ] **Step 8: Run the tests**

Run: `cd web && npm test && npm run typecheck`
Expected: PASS, with `Cards.test.tsx` unchanged.

- [ ] **Step 9: Commit**

```bash
git add web/src/cards web/src/App.tsx
git commit -m "feat(web): list, filter, and bulk-act on lorebooks"
```

---

### Task 8: Match the spec

**Files:**
- Modify: `docs/superpowers/specs/2026-10-05-web-ui-design.md`

- [ ] **Step 1: Update the lorebook list**

In "Lorebooks", replace the **List** paragraph with:

```markdown
**List**: lorebooks have no image, tags, or collections, so it's a table of
name, entry count, linked cards as small avatars, and sync badges. It has the
same search, facets (sync and sources), paging, and selection as Cards. The
selection bar offers Push and Archive.
```

- [ ] **Step 2: Add the API rows**

Add rows to the "API additions" table:

```markdown
| `POST /api/lorebooks/search`: the `/search` query over lorebooks, with a `cards` include and an entry count per hit | Lorebook list |
| `kind: "lorebook"` on `/bulk` (archive and unarchive) and `/pushes` | Lorebook bulk actions |
```

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-10-05-web-ui-design.md
git commit -m "docs: match the web UI spec to the lorebook list"
```

---

### Task 9: Check it end to end

- [ ] **Step 1: Run every check CI runs**

```bash
go build ./... && go vet ./... && go test ./...
cd web && npm test && npm run build && npm run gen && git diff --exit-code openapi.json src/api/schema.d.ts
```

Expected: all pass, and regenerating the schema changes nothing.

- [ ] **Step 2: Try it in a browser**

Against a local `innkeeper api` and worker with a few lorebooks, some linked to cards:

1. Open Lorebooks. The table shows names, entry counts, linked card avatars, and sync badges.
2. Search an entry's key word. The lorebook that has it is the only result.
3. Filter by a source facet, then shift-click it to exclude it.
4. Select two lorebooks and archive them. The toast links to the job, and they leave the list.
5. Go to Cards and back. The selection is empty.
