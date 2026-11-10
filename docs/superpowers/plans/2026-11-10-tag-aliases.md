# Tag Aliases Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Treat a library's many spellings of one tag as one tag: spellings that differ only in case, spaces, `-`, or `_` match automatically, and the owner merges the rest, and picks each tag's spelling, on a Tags page that suggests likely duplicates. Cards don't change.

**Architecture:** A migration makes `tag_keys` return tag keys and adds `tag_alias` and `tag_distinct`. Two STABLE SQL functions, `primary_tag_keys` and `tag_match_keys`, resolve aliases inside the SQL that the tag filter, the tag facet, and similar cards already run. The GIN index still serves the filter, because Postgres evaluates a STABLE argument once, when the scan starts. Merges, splits, the tag list, and suggestions live in `internal/library/tags.go`. Owner edits (the bulk Tag job, and an editor save with source `ui` that changes tags) write primary names. Imports, pulls from apps, plugin writes, and MCP writes keep their tags as they come.

**Tech Stack:** Go (Huma, pgx), PostgreSQL, React 19, TypeScript, TanStack Query, react-router, Vitest with Testing Library.

Spec: `docs/superpowers/specs/2026-11-10-tag-aliases-design.md`.

## Files

| File | Responsibility |
|---|---|
| `internal/db/migrations/0058_tag_aliases.sql` (new) | `tag_key`, `tag_keys` and its index, `tag_alias`, `tag_distinct`, `primary_tag_keys`, `tag_match_keys` |
| `internal/library/tags.go` (new) | Tag key, merge, split, not the same, list, suggestions, primary names for owner edits |
| `internal/library/tags_test.go` (new) | Its tests |
| `internal/library/search.go` | Tag filter and facet read aliases; `primaryTagsOf` replaces `tagsOf` |
| `internal/library/discover.go` | Similar cards compare primary keys |
| `internal/library/organize.go` | `EditCardTags` writes primary names; `editTags` matches by key; `Tags` goes |
| `internal/library/scale_test.go` | Times tag queries on 200k cards |
| `internal/api/tags.go` (new) | The endpoints; `tagsChanged` |
| `internal/api/tags_test.go` (new) | Their tests |
| `internal/api/organize.go` | The old per-library `GET /tags` goes |
| `internal/api/facetcache.go` | `clear`, and a generation so a count from before a clear isn't cached |
| `internal/api/facetcache_test.go` (new) | Its test |
| `internal/api/entities.go` | The editor's save writes primary names for changed tags |
| `internal/api/server.go` | Registers `tagRoutes` |
| `internal/backup/backup.go` | Backs up `tag_alias` and `tag_distinct` |
| `web/src/tags/Tags.tsx`, `Tags.css`, `Tags.test.tsx` (new) | The page |
| `web/src/App.tsx`, `web/src/shell/Shell.tsx`, `web/src/shell/Icon.tsx` | Route, nav entry, icon |
| `docs/using.md` | Tags |

Checks: Go DB tests with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres` on the same command line, and `go vet ./...`; web from `web/` with `npm run typecheck && npm test`. Never run Prettier; the repo doesn't use it. Regenerate the API schema with `npm run gen` in `web/`, never by hand.

## Design choices

| Question | Choice | Why |
|---|---|---|
| How the tag filter gets alias keys | `tag_keys(r.tags) && tag_match_keys(primary_tag_keys(ARRAY[$n::text]))` | `where.filter` builds SQL without a context or a connection, and rules, saved searches, and previews all run that SQL. A STABLE function in an index qual runs once when the scan starts, so the GIN index still serves `&&`; Task 3 proves it with `EXPLAIN`. |
| Tag key | `translate(lower(t), ' -_', '')` in SQL, `tagKey` in Go, a test that they agree | Keys that are stored or compared in SQL are always computed in SQL. Go's `tagKey` only matches tags in memory and reads `q`, so a locale where the two `lower`s differ can't corrupt stored aliases. |
| `tag_alias` columns | `key`, `primary_tag`, `created_at`, plus `primary_tag_key`, generated from `primary_tag` | `primary` is reserved in SQL, so the column is `primary_tag`. Facets, filters, and merges look aliases up by primary key; a generated column keeps it indexed and in step. |
| A tag's chosen spelling | A self row in `tag_alias`: the key → that spelling, e.g. `scifi` → `Sci-Fi`. Merging two spellings of one key writes it. | Facets, the list, filters, and backups already read `tag_alias`, so the name needs no new table or column. |
| `tag_distinct` ordering | Columns `COLLATE "C"`, `CHECK (a < b)` | Go orders the pair by bytes, which is `"C"` order, so Go's lookup map and the check agree. |
| Facet grouping and name | One row per card and primary key (`primaryTagsOf`), then grouped by key. Name: the primary name on the key's `tag_alias` rows, self row included, else `min` of the spellings. | Counts each card once without `count(DISTINCT)`; `min` is what the facet shows today. |
| Merge | One transaction under `LOCK TABLE tag_alias IN EXCLUSIVE MODE`. Into an alias, or a key with a self row, goes to its primary name. Aliases of `from` move. Merging a tag into its own alias swaps them. Two spellings of one key write a self row. An empty key is `ErrInvalid` (400). | Keeps one level, and the lock stops two merges from each making the other's tag an alias. |
| Editor save | In the `PUT /cards/{id}` handler, for source `ui`, and only when the tags differ from the base revision's | `prepare` has no connection and no base revision, and source `ui` there also covers create, restore, and every library test. The handler is the editor's save (`useEditor.ts`); MCP sends `mcp` through it. Imports and pulls write through `ImportItem`, `ImportCardTx`, and reconcile's `UpdateTx`, which never reach the handler. An avatar change (`Card.tsx`) sends unchanged tags, so it keeps them. |
| Facet cache | `tagsChanged` clears it on merge and split. A generation counter drops a recount that started before the clear. | Otherwise the cache serves pre-merge counts as fresh for up to a minute. |
| Rules | `tagsChanged` reruns the hide job for every enabled rule | A merge changes what a tag rule matches, as a saved-search edit does. |
| Suggestions | Plural pass, then a one-edit pass over a deletion-neighbourhood map, with these limits: plural stem ≥ 3 runes, both keys ≥ 5 runes, no edit that touches a digit, at most 100 | The map makes the pass linear in tags × key length rather than O(n²). Numbered tags (`season1`, `season2`) are different tags. |

---

### Task 1: Tag keys and alias tables

**Files:**
- Create: `internal/db/migrations/0058_tag_aliases.sql`
- Create: `internal/library/tags.go`
- Test: `internal/library/tags_test.go`

- [ ] **Step 1: Write the failing test**

`internal/library/tags_test.go`:

```go
package library

import (
	"context"
	"slices"
	"testing"
)

func TestTagKeyAgreesWithSQL(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	for _, tag := range []string{"Sci-Fi", "sci fi", "sci_fi", "SCIFI", "  Big  Bad - Wolf_ ", "M/F", "o'neil", "Tab\there",
		"Élan", "ÜBER-cool", "日本語", "---", ""} {
		var want string
		if err := lib.pool.QueryRow(ctx, `SELECT tag_key($1)`, tag).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if got := tagKey(tag); got != want {
			t.Errorf("tagKey(%q) = %q, SQL tag_key = %q", tag, got, want)
		}
	}
	var keys []string
	if err := lib.pool.QueryRow(ctx, `SELECT tag_keys(ARRAY['Sci-Fi', 'sci fi', 'Dwarf'])`).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"dwarf", "scifi"}) {
		t.Fatalf("tag_keys = %v, want [dwarf scifi]", keys)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run TestTagKeyAgreesWithSQL`
Expected: FAIL to build, `undefined: tagKey`.

- [ ] **Step 3: Write the migration**

The newest migration is `0057_archived_vectors.sql`. `internal/db/migrations/0058_tag_aliases.sql`:

```sql
-- A tag's key: lower-cased, without spaces, hyphens, or underscores, so
-- Sci-Fi, sci fi, and scifi are one tag. library.tagKey computes the same.
CREATE FUNCTION tag_key(tag text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT translate(lower(tag), ' -_', '')
$$;

-- tag_keys now returns keys, so its index is rebuilt. CREATE INDEX runs with
-- search_path pg_catalog, so tag_key is named with its schema.
DROP INDEX card_revision_tag_keys_of_tags;
CREATE OR REPLACE FUNCTION tag_keys(tags text[]) RETURNS text[]
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT coalesce(array_agg(DISTINCT public.tag_key(t)), '{}') FROM unnest(tags) t
$$;
CREATE INDEX card_revision_tag_keys_of_tags ON card_revision USING gin (tag_keys(tags));

-- An alias: tag key `key` stands for the primary tag named primary_tag. A self
-- row (key = primary_tag_key) instead names its own key's tag. One level: a
-- primary tag is never an alias of another key.
CREATE TABLE tag_alias (
    key text PRIMARY KEY,
    primary_tag text NOT NULL,
    primary_tag_key text NOT NULL GENERATED ALWAYS AS (tag_key(primary_tag)) STORED,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (primary_tag_key <> '')
);
CREATE INDEX ON tag_alias (primary_tag_key);

-- Pairs of tag keys the owner said aren't one tag. "C" ordering is Go's, so
-- library code orders a pair the way the check does.
CREATE TABLE tag_distinct (
    a text COLLATE "C",
    b text COLLATE "C",
    PRIMARY KEY (a, b),
    CHECK (a < b)
);

-- tags' keys, each alias replaced by its primary tag's key.
CREATE FUNCTION primary_tag_keys(tags text[]) RETURNS text[]
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT coalesce(array_agg(DISTINCT coalesce(a.primary_tag_key, k)), '{}')
    FROM unnest(tag_keys(tags)) k LEFT JOIN tag_alias a ON a.key = k
$$;

-- keys, which are primary, and every alias key that stands for one of them.
-- A self row repeats its key, which && ignores.
CREATE FUNCTION tag_match_keys(keys text[]) RETURNS text[]
LANGUAGE sql STABLE PARALLEL SAFE AS $$
    SELECT keys || coalesce(array_agg(a.key), '{}') FROM tag_alias a WHERE a.primary_tag_key = ANY(keys)
$$;
```

- [ ] **Step 4: Write `tagKey`**

`internal/library/tags.go`:

```go
package library

import "strings"

// tagKey is tag lower-cased, without spaces, hyphens, or underscores: the
// key that makes two spellings one tag. SQL's tag_key computes the same.
func tagKey(tag string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == '_' {
			return -1
		}
		return r
	}, strings.ToLower(tag))
}
```

- [ ] **Step 5: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/library ./internal/db`
Expected: PASS. If only `Élan` or `ÜBER-cool` disagree, the test database uses the `C` locale; use the `pgvector/pgvector:pg18` container from CONTRIBUTING.md, whose default locale is `en_US.utf8`.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0058_tag_aliases.sql internal/library/tags.go internal/library/tags_test.go
git commit -m "feat(library): tag keys ignore case, spaces, hyphens, and underscores"
```

---

### Task 2: Merge, split, and not the same

**Files:**
- Modify: `internal/library/tags.go`
- Test: `internal/library/tags_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/library/tags_test.go`; its imports become `context`, `errors`, `maps`, `slices`, `testing`:

```go
// aliases is every tag_alias row as key → primary tag.
func aliases(t *testing.T, lib *Library) map[string]string {
	t.Helper()
	rows, err := lib.pool.Query(context.Background(), `SELECT key, primary_tag FROM tag_alias`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var key, primary string
		if err := rows.Scan(&key, &primary); err != nil {
			t.Fatal(err)
		}
		got[key] = primary
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

// mergeTag merges from into into, then checks every tag_alias row against want.
func mergeTag(t *testing.T, lib *Library, from, into string, want map[string]string) {
	t.Helper()
	if err := lib.MergeTag(context.Background(), from, into); err != nil {
		t.Fatalf("merge %q into %q: %v", from, into, err)
	}
	if got := aliases(t, lib); !maps.Equal(got, want) {
		t.Fatalf("after merging %q into %q: aliases = %v, want %v", from, into, got, want)
	}
}

func TestMergeTagKeepsOneLevel(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	mergeTag(t, lib, "fantasy world", "fantasy", map[string]string{"fantasyworld": "fantasy"})
	// Into an alias: into its primary tag.
	mergeTag(t, lib, "high fantasy", "Fantasy World", map[string]string{"fantasyworld": "fantasy", "highfantasy": "fantasy"})
	// A tag with aliases: they move with it.
	mergeTag(t, lib, "fantasy", "Fantasy Fiction", map[string]string{"fantasyworld": "Fantasy Fiction", "highfantasy": "Fantasy Fiction", "fantasy": "Fantasy Fiction"})
	// A tag into its own alias: they swap.
	mergeTag(t, lib, "fantasy fiction", "fantasy", map[string]string{"fantasyworld": "fantasy", "highfantasy": "fantasy", "fantasyfiction": "fantasy"})

	if err := lib.MergeTag(ctx, " - ", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("merging an empty key: %v, want ErrInvalid", err)
	}

	if err := lib.SplitTag(ctx, "High-Fantasy"); err != nil {
		t.Fatal(err)
	}
	if got := aliases(t, lib); !maps.Equal(got, map[string]string{"fantasyworld": "fantasy", "fantasyfiction": "fantasy"}) {
		t.Fatalf("after split: aliases = %v", got)
	}
	if err := lib.SplitTag(ctx, "high fantasy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("splitting a tag that isn't an alias: %v, want ErrNotFound", err)
	}
}

func TestMergeTagIntoItsOwnKeyChoosesItsSpelling(t *testing.T) {
	lib := newLibrary(t)
	// Two spellings of one key: a self row names the tag.
	mergeTag(t, lib, "Sci-Fi", "sci fi", map[string]string{"scifi": "sci fi"})
	// Into a key with a self row: into its chosen spelling.
	mergeTag(t, lib, "science fiction", "SCIFI", map[string]string{"scifi": "sci fi", "sciencefiction": "sci fi"})
	// Another spelling of a key with a self row and aliases: they follow it.
	mergeTag(t, lib, "sci fi", "SciFi", map[string]string{"scifi": "SciFi", "sciencefiction": "SciFi"})
	// A key with a self row and aliases into another tag: the self row becomes an ordinary alias.
	mergeTag(t, lib, "SCIFI", "speculative", map[string]string{"scifi": "speculative", "sciencefiction": "speculative"})

	// A tag with a self row into its own alias: they swap, and the self row becomes an alias.
	mergeTag(t, lib, "fantasy world", "fantasy", map[string]string{"scifi": "speculative", "sciencefiction": "speculative",
		"fantasyworld": "fantasy"})
	mergeTag(t, lib, "fantasy", "Fantasy", map[string]string{"scifi": "speculative", "sciencefiction": "speculative",
		"fantasyworld": "Fantasy", "fantasy": "Fantasy"})
	mergeTag(t, lib, "Fantasy", "Fantasy World", map[string]string{"scifi": "speculative", "sciencefiction": "speculative",
		"fantasy": "Fantasy World"})
}

func TestMarkTagsDistinctStoresOneOrderedPair(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	if err := lib.MarkTagsDistinct(ctx, "Pirates", "pirate"); err != nil {
		t.Fatal(err)
	}
	if err := lib.MarkTagsDistinct(ctx, "pirate", "PIRATES"); err != nil {
		t.Fatalf("marking a pair again: %v", err)
	}
	if n := lib.countRows(t, `SELECT count(*) FROM tag_distinct WHERE a = 'pirate' AND b = 'pirates'`); n != 1 {
		t.Fatalf("%d rows for pirate/pirates, want 1", n)
	}
	if err := lib.MarkTagsDistinct(ctx, "Sci-Fi", "scifi"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("one key: %v, want ErrInvalid", err)
	}
}
```

`countRows` is in `import_test.go`.

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run 'TestMergeTag|TestMarkTagsDistinct'`
Expected: FAIL to build, `lib.MergeTag undefined`.

- [ ] **Step 3: Implement**

In `internal/library/tags.go`, imports become `context`, `fmt`, `strings`, `github.com/jackc/pgx/v5`. Add:

```go
// MergeTag makes from an alias of into, keeping one level: an alias given as
// into stands for its primary tag, and from's own aliases move to into.
// Into a tag no card has, it renames from. Two spellings of one key write a
// self row, which makes into the tag's name. No card changes.
func (l *Library) MergeTag(ctx context.Context, from, into string) error {
	from, into = strings.TrimSpace(from), strings.TrimSpace(into)
	return pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		// One merge at a time, so two can't each make the other's tag an alias.
		if _, err := tx.Exec(ctx, `LOCK TABLE tag_alias IN EXCLUSIVE MODE`); err != nil {
			return err
		}
		var fromKey, intoKey string
		var intoPrimary, intoPrimaryKey *string
		if err := tx.QueryRow(ctx, `SELECT tag_key($1), tag_key($2), a.primary_tag, a.primary_tag_key
			FROM (SELECT 1) one LEFT JOIN tag_alias a ON a.key = tag_key($2)`, from, into).
			Scan(&fromKey, &intoKey, &intoPrimary, &intoPrimaryKey); err != nil {
			return err
		}
		switch {
		case fromKey == "" || intoKey == "":
			return fmt.Errorf("%w: a tag needs a character other than spaces, - and _", ErrInvalid)
		case intoPrimaryKey != nil && *intoPrimaryKey == fromKey:
			// into is an alias of from, or from's own self row: into becomes the primary tag.
			if _, err := tx.Exec(ctx, `DELETE FROM tag_alias WHERE key = $1`, intoKey); err != nil {
				return err
			}
		case intoPrimaryKey != nil:
			// into is an alias, or a key with a self row: use its primary name.
			into, intoKey = *intoPrimary, *intoPrimaryKey
		}
		// Every alias of either tag names into, so a key's rows agree on its name.
		if _, err := tx.Exec(ctx, `UPDATE tag_alias SET primary_tag = $1 WHERE primary_tag_key IN ($2, $3)`, into, fromKey, intoKey); err != nil {
			return err
		}
		// When fromKey is intoKey, this is the self row that names the tag.
		_, err := tx.Exec(ctx, `INSERT INTO tag_alias (key, primary_tag) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET primary_tag = excluded.primary_tag, created_at = now()`, fromKey, into)
		return err
	})
}

// SplitTag deletes the row with tag's key: an alias becomes a tag of its own
// again. On a self row, a tag without aliases goes back to its lowest-sorting
// spelling; its aliases' rows still carry the chosen name.
func (l *Library) SplitTag(ctx context.Context, tag string) error {
	res, err := l.pool.Exec(ctx, `DELETE FROM tag_alias WHERE key = tag_key($1)`, tag)
	if err != nil {
		return err
	}
	if res.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkTagsDistinct records that a and b aren't one tag, so TagSuggestions
// leaves the pair out.
func (l *Library) MarkTagsDistinct(ctx context.Context, a, b string) error {
	var x, y string
	if err := l.pool.QueryRow(ctx, `SELECT tag_key($1), tag_key($2)`, a, b).Scan(&x, &y); err != nil {
		return err
	}
	if x == "" || y == "" || x == y {
		return fmt.Errorf("%w: %q and %q are one tag", ErrInvalid, a, b)
	}
	if x > y {
		x, y = y, x
	}
	_, err := l.pool.Exec(ctx, `INSERT INTO tag_distinct (a, b) VALUES ($1, $2) ON CONFLICT DO NOTHING`, x, y)
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/library -run 'TestTagKey|TestMergeTag|TestMarkTagsDistinct' && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/tags.go internal/library/tags_test.go
git commit -m "feat(library): merge, split, and mark tags as not the same"
```

---

### Task 3: The tag filter and facet read aliases

**Files:**
- Modify: `internal/library/search.go` (`Filter.Tag` near line 77; `tagsOf` near line 241; `facetSQL["tags"]` near line 390; `case f.Tag != nil` near line 635)
- Test: `internal/library/tags_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing tests**

Add to `internal/library/tags_test.go`; its imports gain `strings` and `github.com/jackc/pgx/v5`:

```go
// sortedHits is r's hit IDs, ascending.
func sortedHits(r Results) []int64 {
	ids := hitIDs(r)
	slices.Sort(ids)
	return ids
}

func TestTagSpellingsAreOneTagInSearchAndCounts(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A", "tags": ["Sci-Fi"]}`)
	b, _ := newCard(t, lib, "b", `{"name": "B", "tags": ["sci fi"]}`)
	c, _ := newCard(t, lib, "c", `{"name": "C", "tags": ["scifi", "sci_fi"]}`)
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Tag: ptr("SCI FI")}, Facets: []string{"tags"}})
	if err != nil {
		t.Fatal(err)
	}
	if ids := sortedHits(got); !slices.Equal(ids, []int64{a, b, c}) {
		t.Fatalf("hits = %v, want %v", ids, []int64{a, b, c})
	}
	if tags := got.Facets.Tags; len(tags) != 1 || tags[0].Cards != 3 || tagKey(tags[0].Name) != "scifi" {
		t.Fatalf("facet = %+v, want one tag on 3 cards", tags)
	}
}

func TestTagFilterAndFacetReadAliases(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	plain, _ := newCard(t, lib, "plain", `{"name": "P", "tags": ["fantasy"]}`)
	world, _ := newCard(t, lib, "world", `{"name": "W", "tags": ["Fantasy World"]}`)
	both, _ := newCard(t, lib, "both", `{"name": "B", "tags": ["fantasy", "fantasy world"]}`)
	newCard(t, lib, "other", `{"name": "O", "tags": ["pirate"]}`)
	if err := lib.MergeTag(ctx, "fantasy world", "fantasy"); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"fantasy", "fantasy-world"} {
		got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Tag: ptr(tag)}})
		if err != nil {
			t.Fatal(err)
		}
		if ids := sortedHits(got); !slices.Equal(ids, []int64{plain, world, both}) {
			t.Fatalf("tag %q: hits = %v, want %v", tag, ids, []int64{plain, world, both})
		}
	}
	got, err := lib.Search(ctx, Card, Query{Facets: []string{"tags"}, Page: Page{Size: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if want := []TagCount{{"fantasy", 3}, {"pirate", 1}}; !slices.Equal(got.Facets.Tags, want) {
		t.Fatalf("facet = %+v, want %+v", got.Facets.Tags, want)
	}
}

func TestTagFilterUsesTheTagIndex(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "w", `{"name": "W", "tags": ["fantasy world"]}`)
	if err := lib.MergeTag(ctx, "fantasy world", "fantasy"); err != nil {
		t.Fatal(err)
	}
	tx, err := lib.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// The table is tiny, so without this the planner would rightly scan it.
	if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query(ctx, `EXPLAIN SELECT 1 FROM card_revision r WHERE `+tagFilter("$1"), "fantasy")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if text := strings.Join(plan, "\n"); !strings.Contains(text, "card_revision_tag_keys_of_tags") {
		t.Fatalf("the tag filter doesn't use its index:\n%s", text)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run 'TestTagSpellings|TestTagFilter'`
Expected: FAIL to build, `undefined: tagFilter`. After Step 3's `tagFilter` alone, the first two tests would still fail: `SCI FI` matches only `sci fi`, and the facet lists four tags.

- [ ] **Step 3: Implement**

In `Filter`, replace the `Tag` field's doc:

```go
	Tag           *string       `json:"tag,omitempty" doc:"Has this tag: any spelling that differs only in case, spaces, - or _, or a tag merged with it."`
```

Replace `tagsOf` and its comment with:

```go
// primaryTagsOf is r's tags as rows x, one per primary tag key (x.key),
// with the card's lowest-sorting spelling of it (x.name).
const primaryTagsOf = `LATERAL (SELECT coalesce(a.primary_tag_key, tag_key(t)) AS key, min(t) AS name
	FROM unnest(r.tags) t LEFT JOIN tag_alias a ON a.key = tag_key(t) GROUP BY 1) x`

// tagFilter matches revisions r with the tag in arg, any spelling of its key,
// or a tag merged with it. STABLE functions run once per scan, so the
// card_revision_tag_keys_of_tags index serves it.
func tagFilter(arg string) string {
	return `tag_keys(r.tags) && tag_match_keys(primary_tag_keys(ARRAY[` + arg + `::text]))`
}
```

Replace the `"tags"` entry of `facetSQL`, with its comment, by:

```go
	// primaryTagsOf has one row per card and primary tag, so count(*) counts
	// cards. A merge gives all of a key's rows, self row included, one
	// primary name, so any row names the tag.
	"tags": func(k *Kind, cond string) string {
		return `SELECT coalesce(n.primary_tag, g.name), g.cards FROM (
				SELECT x.key, min(x.name) AS name, count(*)::int AS cards FROM ` + from(k) + `, ` + primaryTagsOf + `
				WHERE ` + cond + ` GROUP BY x.key) g
			LEFT JOIN LATERAL (SELECT primary_tag FROM tag_alias WHERE primary_tag_key = g.key LIMIT 1) n ON TRUE
			ORDER BY g.cards DESC, g.key`
	},
```

Replace the tag case in `where.filter`:

```go
	case f.Tag != nil:
		return tagFilter(w.arg(*f.Tag)), nil
```

`organize.go`'s `Tags` still uses `tagsOf`; Task 8 deletes it. Until then, change its query's `tagsOf` to the old literal so it builds:

```go
	rows, err := l.pool.Query(ctx, `SELECT min(t.t), count(DISTINCT c.id)::int FROM `+from(Card)+`, LATERAL unnest(r.tags) t(t)
		WHERE c.library_id = $1 AND c.archived_at IS NULL GROUP BY lower(t.t) ORDER BY lower(t.t)`, libraryID)
```

- [ ] **Step 4: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/library ./internal/api && go vet ./...`
Expected: PASS, including the existing search, facet, and rule tests.

- [ ] **Step 5: Regenerate the schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: the `tag` filter's description changes in `openapi.json` and `schema.d.ts`; typecheck clean.

- [ ] **Step 6: Commit**

```bash
git add internal/library/search.go internal/library/organize.go internal/library/tags_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(library): tag filter and facet read tag keys and aliases"
```

---

### Task 4: Similar cards compare primary keys

**Files:**
- Modify: `internal/library/discover.go` (`discoverWith` near line 70; the tags query near line 113)
- Test: `internal/library/discover_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/library/discover_test.go`, after `TestSimilarFallsBackToTagsWithoutEmbedInstance`:

```go
func TestSimilarByTagsReadsAliases(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "tags": ["fantasy world", "smith"]}`)
	other, _ := newCard(t, lib, "other", `{"name": "Dorna", "tags": ["Fantasy", "smith"]}`)
	if got, err := lib.SimilarCards(ctx, me, SimilarOptions{}); err != nil || len(got.Items) != 0 {
		t.Fatalf("before the merge: %+v, %v; want none, sharing one tag", got, err)
	}
	if err := lib.MergeTag(ctx, "fantasy world", "fantasy"); err != nil {
		t.Fatal(err)
	}
	got, err := lib.SimilarCards(ctx, me, SimilarOptions{})
	if err != nil || !slices.Equal(ids(got.Items), []int64{other}) || got.Items[0].Score != 1 {
		t.Fatalf("after the merge: %+v, %v; want other with score 1", got, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run TestSimilarByTagsReadsAliases`
Expected: FAIL, "after the merge: … want other with score 1": the cards share only `smith`.

- [ ] **Step 3: Implement**

Replace `discoverWith`'s comment and `self`:

```go
// discoverWith is the WITH clause discovery queries share: fam holds every
// card in $1's variant family, and self is $1's head, materialized so its
// primary tag keys (tags) and the keys that match them (match) are
// computed once rather than per candidate.
const discoverWith = `WITH RECURSIVE up AS (
		SELECT id, variant_of_id FROM card WHERE id = $1
		UNION SELECT p.id, p.variant_of_id FROM card p JOIN up ON p.id = up.variant_of_id
	), fam AS (
		SELECT id FROM up WHERE variant_of_id IS NULL
		UNION SELECT x.id FROM card x JOIN fam ON x.variant_of_id = fam.id
	), self AS MATERIALIZED (
		SELECT c.library_id, r.content_hash, k.tags, tag_match_keys(k.tags) AS match, lower(btrim(r.data->>'creator')) AS creator
		FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		CROSS JOIN LATERAL (SELECT primary_tag_keys(r.tags) AS tags) k
		WHERE c.id = $1
	)`
```

In the `basis == "tags"` query, replace the join and the lateral count:

```go
		JOIN card_revision r ON tag_keys(r.tags) && self.match
		JOIN card c ON c.head_revision_id = r.id
		CROSS JOIN LATERAL (SELECT count(*) FILTER (WHERE t = ANY(self.tags)) AS shared, count(*) AS n
			FROM unnest(primary_tag_keys(r.tags)) t) x
```

- [ ] **Step 4: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/library -run 'TestSimilar|TestRelated|TestDiscover'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/discover.go internal/library/discover_test.go
git commit -m "feat(library): similar cards match tags through aliases"
```

---

### Task 5: Bulk tag edits write primary names

**Files:**
- Modify: `internal/library/organize.go` (`EditCardTags` near line 27; `editTags` and `containsFold` near line 76)
- Modify: `internal/library/tags.go`
- Test: `internal/library/organize_test.go`

`internal/bulk/bulk.go`'s `Tag` handler calls `EditCardTags`, so it needs no change.

- [ ] **Step 1: Write the failing test**

Add to `internal/library/organize_test.go`, after `TestEditCardTagsRejectsNonStringTags`:

```go
func TestEditCardTagsWritesPrimaryNamesAndImportsKeepRawTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	world, _ := newCard(t, lib, "world", `{"name": "W", "tags": ["Fantasy World", "pirate"]}`)
	plain, _ := newCard(t, lib, "plain", `{"name": "P", "tags": ["pirate", "Sci-Fi"]}`)
	if err := lib.MergeTag(ctx, "fantasy world", "fantasy"); err != nil {
		t.Fatal(err)
	}

	// Removing the primary tag takes out its alias too.
	if err := lib.EditCardTags(ctx, world, nil, []string{"fantasy"}); err != nil {
		t.Fatal(err)
	}
	if got := headTags(t, lib, world); !slices.Equal(got, []string{"pirate"}) {
		t.Fatalf("after removing fantasy: %v, want [pirate]", got)
	}
	// Adding an alias writes its primary name; a spelling of a key already there adds nothing.
	if err := lib.EditCardTags(ctx, plain, []string{"fantasy world", "scifi"}, nil); err != nil {
		t.Fatal(err)
	}
	if got := headTags(t, lib, plain); !slices.Equal(got, []string{"pirate", "Sci-Fi", "fantasy"}) {
		t.Fatalf("after adding: %v, want [pirate Sci-Fi fantasy]", got)
	}

	_, imp := importer(t, lib)
	got := imp("x.png", `{"name": "X", "tags": ["fantasy world"]}`)
	if tags := headTags(t, lib, got.ID); !slices.Equal(tags, []string{"fantasy world"}) {
		t.Fatalf("imported tags = %v, want them as they came", tags)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run TestEditCardTagsWritesPrimaryNames`
Expected: FAIL, "after removing fantasy: [Fantasy World pirate], want [pirate]".

- [ ] **Step 3: Add primary names to `tags.go`**

Imports become `context`, `fmt`, `slices`, `strings`, `github.com/jackc/pgx/v5`. Add:

```go
// tagRename is a tag a card holds and the primary name it merged into.
type tagRename struct{ Name, Primary string }

// primaryNames maps each of names whose key has a tag_alias row, an alias's
// or a self row, to its primary tag's name.
func (l *Library) primaryNames(ctx context.Context, names []string) (map[string]string, error) {
	rows, err := l.pool.Query(ctx, `SELECT t, a.primary_tag FROM unnest($1::text[]) t JOIN tag_alias a ON a.key = tag_key(t)`, names)
	if err != nil {
		return nil, err
	}
	renames, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tagRename])
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(renames))
	for _, r := range renames {
		out[r.Name] = r.Primary
	}
	return out, nil
}

// primaryTags replaces each tag in tags that primary maps with its primary
// name, keeping the first spelling of each key.
func primaryTags(tags []string, primary map[string]string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if p, ok := primary[t]; ok {
			t = p
		}
		if !containsKey(out, t) {
			out = append(out, t)
		}
	}
	return out
}
```

- [ ] **Step 4: Change `EditCardTags` and `editTags`**

Replace `EditCardTags`'s doc comment:

```go
// EditCardTags adds and removes names in a card's data.tags as a new
// revision. Names are trimmed and match by tag key. The card's tags and the
// names are first replaced by their primary names, so removing a tag also
// removes the aliases merged into it. An edit that changes nothing adds no
// revision.
```

Replace `tags := editTags(old, add, remove)` with:

```go
		primary, err := l.primaryNames(ctx, slices.Concat(old, add, remove))
		if err != nil {
			return err
		}
		tags := editTags(primaryTags(old, primary), primaryTags(add, primary), primaryTags(remove, primary))
```

Replace `editTags` and `containsFold` with:

```go
// editTags drops remove from tags, then appends each of add not already
// there. Tags with one key match.
func editTags(tags, add, remove []string) []string {
	out := slices.DeleteFunc(slices.Clone(tags), func(t string) bool { return containsKey(remove, t) })
	for _, a := range add {
		if !containsKey(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func containsKey(list []string, s string) bool {
	key := tagKey(s)
	return slices.ContainsFunc(list, func(x string) bool { return tagKey(x) == key })
}
```

- [ ] **Step 5: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/library ./internal/bulk && go vet ./...`
Expected: PASS, including `TestEditCardTagsAddsARevision` and `TestReimportingACardWithTagsRepeatedInAnotherCaseChangesNothing`.

- [ ] **Step 6: Commit**

```bash
git add internal/library/organize.go internal/library/organize_test.go internal/library/tags.go
git commit -m "feat(library): bulk tag edits write merged tags under their primary names"
```

---

### Task 6: Tag list and suggestions

**Files:**
- Modify: `internal/library/tags.go`
- Test: `internal/library/tags_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/library/tags_test.go`; its imports gain `reflect`:

```go
func TestTagListGroupsAliasesUnderTheirPrimaryTag(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	newCard(t, lib, "a", `{"name": "A", "tags": ["fantasy", "fantasy world"]}`)
	newCard(t, lib, "b", `{"name": "B", "tags": ["fantasy"]}`)
	newCard(t, lib, "c", `{"name": "C", "tags": ["pirate"]}`)
	gone, _ := newCard(t, lib, "d", `{"name": "D", "tags": ["pirate"]}`)
	if err := lib.Cards.SetArchived(ctx, gone, true); err != nil {
		t.Fatal(err)
	}
	if err := lib.MergeTag(ctx, "fantasy world", "fantasy"); err != nil {
		t.Fatal(err)
	}
	fantasy := Tag{Name: "fantasy", Key: "fantasy", Cards: 2, Spellings: []string{"fantasy"},
		Aliases: []TagAlias{{Key: "fantasyworld", Spellings: []string{"fantasy world"}}}}
	pirate := Tag{Name: "pirate", Key: "pirate", Cards: 1, Spellings: []string{"pirate"}, Aliases: []TagAlias{}}
	for _, c := range []struct {
		q    string
		page int
		want TagPage
	}{
		{"", 0, TagPage{Tags: []Tag{fantasy, pirate}, Total: 2, Pages: 1}},
		{"Fantasy-W", 1, TagPage{Tags: []Tag{fantasy}, Total: 1, Pages: 1}},
		{"pir", 1, TagPage{Tags: []Tag{pirate}, Total: 1, Pages: 1}},
		{"", 2, TagPage{Tags: []Tag{}, Total: 2, Pages: 1}},
	} {
		got, err := lib.TagList(ctx, c.q, c.page)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("TagList(%q, %d) = %+v, want %+v", c.q, c.page, got, c.want)
		}
	}
}

func TestMergingASpellingIntoItsOwnKeyNamesTheTag(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A", "tags": ["Sci-Fi"]}`)
	b, _ := newCard(t, lib, "b", `{"name": "B", "tags": ["Sci-Fi"]}`)
	// check expects one tag, key scifi, named name: in the facet, in the list,
	// and on hits, which a filter on scifi finds.
	check := func(name string, hits []int64, wantAliases []TagAlias) {
		t.Helper()
		got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Tag: ptr("scifi")}, Facets: []string{"tags"}})
		if err != nil {
			t.Fatal(err)
		}
		if ids := sortedHits(got); !slices.Equal(ids, hits) {
			t.Fatalf("hits = %v, want %v", ids, hits)
		}
		if want := []TagCount{{name, len(hits)}}; !slices.Equal(got.Facets.Tags, want) {
			t.Fatalf("facet = %+v, want %+v", got.Facets.Tags, want)
		}
		page, err := lib.TagList(ctx, "", 1)
		if err != nil {
			t.Fatal(err)
		}
		want := []Tag{{Name: name, Key: "scifi", Cards: len(hits), Spellings: []string{"Sci-Fi"}, Aliases: wantAliases}}
		if !reflect.DeepEqual(page.Tags, want) {
			t.Fatalf("tags = %+v, want %+v", page.Tags, want)
		}
	}

	mergeTag(t, lib, "Sci-Fi", "sci fi", map[string]string{"scifi": "sci fi"})
	check("sci fi", []int64{a, b}, []TagAlias{})

	// The key's aliases follow its new spelling.
	c, _ := newCard(t, lib, "c", `{"name": "C", "tags": ["science fiction"]}`)
	mergeTag(t, lib, "science fiction", "scifi", map[string]string{"scifi": "sci fi", "sciencefiction": "sci fi"})
	mergeTag(t, lib, "sci fi", "SciFi", map[string]string{"scifi": "SciFi", "sciencefiction": "SciFi"})
	check("SciFi", []int64{a, b, c}, []TagAlias{{Key: "sciencefiction", Spellings: []string{"science fiction"}}})
}

func TestSuggestTagsPairsPluralsAndNearSpellings(t *testing.T) {
	tag := func(key string, cards int) Tag { return Tag{Name: key, Key: key, Cards: cards} }
	tags := []Tag{tag("dragon", 9), tag("wizard", 7), tag("season1", 5), tag("season2", 4), tag("boxes", 3), tag("elf", 3),
		tag("box", 2), tag("wizzard", 2), tag("pirate", 2), tag("dragons", 1), tag("elfs", 1), tag("pirates", 1), tag("swords", 1),
		tag("words", 1), tag("as", 1), tag("a", 1)}
	var got []string
	for _, s := range suggestTags(tags, map[[2]string]bool{{"pirate", "pirates"}: true}) {
		got = append(got, s.From.Key+" into "+s.Into.Key)
	}
	// season1 and season2 differ by a digit, and "a" is too short a stem.
	want := []string{"dragons into dragon", "wizzard into wizard", "box into boxes", "elfs into elf", "words into swords"}
	if !slices.Equal(got, want) {
		t.Fatalf("suggestions = %q, want %q", got, want)
	}
}
```

On a tie in cards, `into` is the singular, or the tag earlier in the list: `swords` comes before `words`. Without Step 3's self-row check in `allTags`, `TestMergingASpellingIntoItsOwnKeyNamesTheTag` fails: the list shows `scifi` as an alias of itself.

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run 'TestTagList|TestSuggestTags|TestMergingASpelling'`
Expected: FAIL to build, `undefined: Tag`.

- [ ] **Step 3: Implement**

In `internal/library/tags.go`, imports become `cmp`, `context`, `fmt`, `slices`, `strings`, `unicode`, `unicode/utf8`, `github.com/jackc/pgx/v5`. Add:

```go
const (
	tagPageSize = 100
	// minNearLen is the shortest key the near-spelling pass compares; shorter
	// keys one letter apart are mostly different words.
	minNearLen = 5
	// minPluralStem is the shortest singular the plural pass pairs, so "as"
	// isn't the plural of "a".
	minPluralStem     = 3
	maxTagSuggestions = 100
)

// Tag is a primary tag across all libraries.
type Tag struct {
	Name      string     `json:"name" doc:"The name it was merged or renamed to, else its lowest-sorting spelling."`
	Key       string     `json:"key"`
	Cards     int        `json:"cards" doc:"Active cards with it or one of its aliases, each counted once."`
	Spellings []string   `json:"spellings" doc:"How cards spell its own key."`
	Aliases   []TagAlias `json:"aliases"`
}

// TagAlias is a tag key merged into a primary tag.
type TagAlias struct {
	Key       string   `json:"key"`
	Spellings []string `json:"spellings" doc:"How active cards spell it; empty when none has it."`
}

// TagPage is one page of tags, most cards first.
type TagPage struct {
	Tags  []Tag `json:"tags"`
	Total int   `json:"total"`
	Pages int   `json:"pages"`
}

// TagRef is one side of a TagSuggestion.
type TagRef struct {
	Name  string `json:"name"`
	Key   string `json:"key"`
	Cards int    `json:"cards"`
}

// TagSuggestion is two tags that may be one: a plural, or a near spelling.
// Into has more cards.
type TagSuggestion struct {
	From TagRef `json:"from"`
	Into TagRef `json:"into"`
}

type tagKeyCount struct {
	Key   string
	Cards int
}

type tagSpellings struct {
	Key       string
	Spellings []string
}

type tagAliasRow struct{ Key, PrimaryTag, PrimaryTagKey string }

// allTags returns every primary tag on an active card, most cards first.
//
// ponytail: three scans of every active card per call; cache the result if the Tags page gets slow.
func (l *Library) allTags(ctx context.Context) ([]Tag, error) {
	rows, err := l.pool.Query(ctx, `SELECT x.key, count(*)::int FROM `+from(Card)+`, `+primaryTagsOf+`
		WHERE c.archived_at IS NULL GROUP BY x.key`)
	if err != nil {
		return nil, err
	}
	counts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tagKeyCount])
	if err != nil {
		return nil, err
	}
	rows, err = l.pool.Query(ctx, `SELECT tag_key(t), array_agg(DISTINCT t ORDER BY t) FROM `+from(Card)+`, unnest(r.tags) t
		WHERE c.archived_at IS NULL GROUP BY 1`)
	if err != nil {
		return nil, err
	}
	spelled, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tagSpellings])
	if err != nil {
		return nil, err
	}
	rows, err = l.pool.Query(ctx, `SELECT key, primary_tag, primary_tag_key FROM tag_alias ORDER BY key`)
	if err != nil {
		return nil, err
	}
	aliases, err := pgx.CollectRows(rows, pgx.RowToStructByPos[tagAliasRow])
	if err != nil {
		return nil, err
	}

	spellings := make(map[string][]string, len(spelled))
	for _, s := range spelled {
		spellings[s.Key] = s.Spellings
	}
	tags := make([]Tag, len(counts))
	at := make(map[string]int, len(counts))
	for i, c := range counts {
		tags[i] = Tag{Key: c.Key, Cards: c.Cards, Spellings: append([]string{}, spellings[c.Key]...), Aliases: []TagAlias{}}
		at[c.Key] = i
	}
	for _, a := range aliases {
		i, ok := at[a.PrimaryTagKey]
		if !ok {
			continue // No active card has the tag or any of its aliases.
		}
		tags[i].Name = a.PrimaryTag
		// A self row names its tag; it isn't an alias of it.
		if a.Key != a.PrimaryTagKey {
			tags[i].Aliases = append(tags[i].Aliases, TagAlias{Key: a.Key, Spellings: append([]string{}, spellings[a.Key]...)})
		}
	}
	for i := range tags {
		// A tag without alias rows comes from a card's own spelling.
		if tags[i].Name == "" {
			tags[i].Name = tags[i].Spellings[0]
		}
	}
	slices.SortFunc(tags, func(a, b Tag) int { return cmp.Or(b.Cards-a.Cards, strings.Compare(a.Key, b.Key)) })
	return tags, nil
}

// TagList returns page (1-based) of the primary tags whose key, or one of
// whose aliases' keys, contains q's key; most cards first.
func (l *Library) TagList(ctx context.Context, q string, page int) (TagPage, error) {
	tags, err := l.allTags(ctx)
	if err != nil {
		return TagPage{}, err
	}
	if key := tagKey(strings.TrimSpace(q)); key != "" {
		tags = slices.DeleteFunc(tags, func(t Tag) bool {
			return !strings.Contains(t.Key, key) && !slices.ContainsFunc(t.Aliases, func(a TagAlias) bool { return strings.Contains(a.Key, key) })
		})
	}
	lo := min((max(page, 1)-1)*tagPageSize, len(tags))
	return TagPage{Tags: tags[lo:min(lo+tagPageSize, len(tags))], Total: len(tags), Pages: (len(tags) + tagPageSize - 1) / tagPageSize}, nil
}

// TagSuggestions returns pairs of primary tags that may be one, leaving out
// pairs marked distinct; the pairs with most cards first.
func (l *Library) TagSuggestions(ctx context.Context) ([]TagSuggestion, error) {
	tags, err := l.allTags(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT a, b FROM tag_distinct`)
	if err != nil {
		return nil, err
	}
	pairs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) ([2]string, error) {
		var p [2]string
		return p, row.Scan(&p[0], &p[1])
	})
	if err != nil {
		return nil, err
	}
	distinct := make(map[[2]string]bool, len(pairs))
	for _, p := range pairs {
		distinct[p] = true
	}
	return suggestTags(tags, distinct), nil
}

// suggestTags pairs tags whose keys are equal after dropping a trailing es or
// s, and keys of at least minNearLen runes one edit apart. distinct holds
// pairs to leave out, as (lower key, higher key).
func suggestTags(tags []Tag, distinct map[[2]string]bool) []TagSuggestion {
	at := make(map[string]int, len(tags))
	for i, t := range tags {
		at[t.Key] = i
	}
	seen := map[[2]string]bool{}
	out := []TagSuggestion{}
	pair := func(i, j int) {
		p := [2]string{tags[i].Key, tags[j].Key}
		if p[0] > p[1] {
			p[0], p[1] = p[1], p[0]
		}
		if p[0] == p[1] || seen[p] || distinct[p] {
			return
		}
		seen[p] = true
		into, from := tags[i], tags[j]
		if from.Cards > into.Cards {
			into, from = from, into
		}
		out = append(out, TagSuggestion{From: TagRef{from.Name, from.Key, from.Cards}, Into: TagRef{into.Name, into.Key, into.Cards}})
	}
	for i, t := range tags {
		for _, suffix := range []string{"es", "s"} {
			stem, ok := strings.CutSuffix(t.Key, suffix)
			if j, found := at[stem]; ok && found && utf8.RuneCountInString(stem) >= minPluralStem {
				pair(j, i)
			}
		}
	}
	// Keys one edit apart share a deletion, so only keys that share one are compared.
	near := map[string][]int{}
	for i, t := range tags {
		if utf8.RuneCountInString(t.Key) >= minNearLen {
			for _, d := range deletions(t.Key) {
				near[d] = append(near[d], i)
			}
		}
	}
	for _, group := range near {
		for x, i := range group {
			for _, j := range group[x+1:] {
				if oneEdit(tags[i].Key, tags[j].Key) {
					pair(i, j)
				}
			}
		}
	}
	slices.SortFunc(out, func(a, b TagSuggestion) int {
		return cmp.Or(b.Into.Cards+b.From.Cards-a.Into.Cards-a.From.Cards,
			strings.Compare(a.Into.Key, b.Into.Key), strings.Compare(a.From.Key, b.From.Key))
	})
	return out[:min(len(out), maxTagSuggestions)]
}

// deletions is key and each way of deleting one rune from it.
func deletions(key string) []string {
	r := []rune(key)
	out := []string{key}
	for i := range r {
		out = append(out, string(r[:i])+string(r[i+1:]))
	}
	return out
}

// oneEdit reports whether a and b differ by one inserted, deleted, or replaced
// rune that isn't a digit: season1 and season2 are different tags.
func oneEdit(a, b string) bool {
	x, y := []rune(a), []rune(b)
	if len(x) < len(y) {
		x, y = y, x
	}
	if len(x)-len(y) > 1 {
		return false
	}
	i := 0
	for i < len(y) && x[i] == y[i] {
		i++
	}
	switch {
	case i == len(y):
		return len(x) > len(y) && !unicode.IsDigit(x[i])
	case len(x) == len(y):
		return string(x[i+1:]) == string(y[i+1:]) && !unicode.IsDigit(x[i]) && !unicode.IsDigit(y[i])
	default:
		return string(x[i+1:]) == string(y[i:]) && !unicode.IsDigit(x[i])
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/library -run 'TestTag|TestSuggestTags|TestMerge' && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/tags.go internal/library/tags_test.go
git commit -m "feat(library): list primary tags and suggest likely duplicates"
```

---

### Task 7: Measure tag queries at scale

**Files:**
- Modify: `internal/library/scale_test.go`

The live instance has about 23,000 distinct tags, most on one card. This test builds 200k cards with a long tail of about that many spellings, 8% of them spelled `Tag-<n>` beside `tag<n>`, and 1,000 aliases. It times each query by the median of five runs, against the facet as it was (grouped by `lower`) on the same data.

| Query | Must stay under |
|---|---|
| Tag facet, exact | 2× the old facet, and 3 s |
| Tag facet, sampled (the sidebar's first answer) | 500 ms |
| Tag filter through an alias, one page | 200 ms |
| `TagList` | 3 s |
| `TagSuggestions` | 3 s |

- [ ] **Step 1: Add the test**

In `internal/library/scale_test.go`, imports gain `slices` and `time`. Add after `TestScaleExplain`:

```go
// TestScaleTagQueries times the tag facet, an aliased tag filter, the tag
// list, and suggestions on 200k cards with about 23k tag spellings in a long
// tail and 1,000 aliases. It takes minutes, so CI never runs it.
func TestScaleTagQueries(t *testing.T) {
	if os.Getenv("INNKEEPER_SCALE_TEST") != "1" {
		t.Skip("set INNKEEPER_SCALE_TEST=1 to build a 200k-card library and time tag queries")
	}
	pool := testdb.New(t)
	ctx := context.Background()
	generateScaleLibrary(t, pool, 200_000)
	for _, sql := range []string{
		// WHERE id > 0 correlates the subquery, so each revision draws its own tags.
		`UPDATE card_revision SET data = jsonb_set(data, '{tags}', (
			SELECT coalesce(jsonb_agg(DISTINCT CASE WHEN random() < 0.08 THEN 'Tag-' || n ELSE 'tag' || n END), '[]')
			FROM (SELECT floor(power(random(), 4) * 23000)::int AS n FROM generate_series(1, 3 + id % 8) WHERE id > 0) x))`,
		`INSERT INTO tag_alias (key, primary_tag) SELECT 'tag' || (2 * g + 1), 'tag' || (2 * g) FROM generate_series(0, 999) g`,
		`ANALYZE`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", strings.Fields(sql)[:3], err)
		}
	}
	var spellings int
	if err := pool.QueryRow(ctx, `SELECT count(DISTINCT t) FROM card_revision, unnest(tags) t`).Scan(&spellings); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d distinct tag spellings", spellings)

	lib := New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()})
	median := func(run func() error) time.Duration {
		t.Helper()
		var took []time.Duration
		for range 5 {
			start := time.Now()
			if err := run(); err != nil {
				t.Fatal(err)
			}
			took = append(took, time.Since(start))
		}
		slices.Sort(took)
		return took[2]
	}
	const archive = 2
	facet := func(sampleOver int) func() error {
		return func() error {
			_, err := lib.Search(ctx, Card, Query{Library: archive, Facets: []string{"tags"}, FacetsOnly: true, SampleOver: sampleOver})
			return err
		}
	}
	old := median(func() error {
		_, err := pool.Exec(ctx, `SELECT min(t.t), count(*)::int FROM card c JOIN card_revision r ON r.id = c.head_revision_id,
			LATERAL unnest(r.tags) t(t) WHERE c.library_id = $1 AND c.archived_at IS NULL
			GROUP BY lower(t.t) ORDER BY count(*) DESC, lower(t.t)`, archive)
		return err
	})
	for _, m := range []struct {
		name  string
		run   func() error
		under time.Duration
	}{
		{"tag facet, exact", facet(0), min(2*old, 3*time.Second)},
		{"tag facet, sampled", facet(20_000), 500 * time.Millisecond},
		{"tag filter through an alias", func() error {
			_, err := lib.Search(ctx, Card, Query{Library: archive, Filter: &Filter{Tag: ptr("tag1")}, Page: Page{Size: 50}})
			return err
		}, 200 * time.Millisecond},
		{"tag list", func() error { _, err := lib.TagList(ctx, "", 1); return err }, 3 * time.Second},
		{"tag suggestions", func() error { _, err := lib.TagSuggestions(ctx); return err }, 3 * time.Second},
	} {
		took := median(m.run)
		t.Logf("%s: %v (old facet %v; must stay under %v)", m.name, took, old, m.under)
		if took > m.under {
			t.Errorf("%s took %v, over %v", m.name, took, m.under)
		}
	}
}
```

- [ ] **Step 2: Run it**

Run: `INNKEEPER_SCALE_TEST=1 INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run TestScaleTagQueries -v -timeout 60m`
Expected: PASS, logging about 20,000 or more spellings and each time. If the exact facet is over budget, run `TestScaleExplain` (also with `INNKEEPER_SCALE_TEST=1`) and read the tags facet's plan before changing the query; don't raise the budget.

- [ ] **Step 3: Check that CI skips it**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run TestScaleTagQueries -v`
Expected: `--- SKIP`.

- [ ] **Step 4: Commit with the times**

Put Step 2's logged times in the commit body:

```bash
git add internal/library/scale_test.go
git commit -m "test(library): time tag queries on 200k cards with 23k tags" -m "<paste the five logged times and the old facet's>"
```

---

### Task 8: The Tags API

**Files:**
- Create: `internal/api/tags.go`
- Test: `internal/api/tags_test.go`
- Modify: `internal/api/organize.go` (`tagCountsOut` near line 19; `list-tags` near line 59)
- Modify: `internal/api/server.go` (route list near line 179)
- Modify: `internal/library/organize.go` (delete `Tags`, near line 171)
- Modify: `internal/api/organize_test.go`, `internal/api/libraries_test.go`, `internal/library/libraries_test.go`, `internal/library/search_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

`GET /tags` exists already as `list-tags`: one library's tags, alphabetically. No web page, MCP tool, or `ink` command calls it; only tests do. The new list replaces it, across all libraries, so `?library=` goes.

Mutating endpoints need the write tier. A merge or split can be undone by the other, like a revision, so none needs destructive, and `TestOperationTiers` needs no new exception.

- [ ] **Step 1: Write the failing test**

`internal/api/tags_test.go`:

```go
package api

import (
	"reflect"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestTagRoutes(t *testing.T) {
	e := newEnv(t)
	for _, tags := range []string{`["dragon"]`, `["dragon"]`, `["Dragons"]`, `["M/F"]`, `["pirate", "pirates"]`} {
		e.createCard(`{"name": "C", "tags": ` + tags + `}`)
	}
	var suggestions []library.TagSuggestion
	e.call("GET", "/tags/suggestions", nil, 200, &suggestions)
	if len(suggestions) != 2 || suggestions[0].From.Key != "dragons" || suggestions[0].Into.Key != "dragon" ||
		suggestions[1].From.Key != "pirates" || suggestions[1].Into.Key != "pirate" {
		t.Fatalf("suggestions = %+v", suggestions)
	}

	e.call("POST", "/tags/distinct", map[string]string{"a": "Pirates", "b": "pirate"}, 204, nil)
	e.call("POST", "/tags/distinct", map[string]string{"a": "pirate", "b": "PIRATE"}, 400, nil)
	e.call("POST", "/tags/merge", map[string]string{"from": "Dragons", "into": "dragon"}, 204, nil)
	e.call("POST", "/tags/merge", map[string]string{"from": " - ", "into": "dragon"}, 400, nil)
	e.call("GET", "/tags/suggestions", nil, 200, &suggestions)
	if len(suggestions) != 0 {
		t.Fatalf("after merging and marking distinct, suggestions = %+v", suggestions)
	}

	var page library.TagPage
	e.call("GET", "/tags?q=drag", nil, 200, &page)
	want := library.TagPage{Total: 1, Pages: 1, Tags: []library.Tag{{Name: "dragon", Key: "dragon", Cards: 3,
		Spellings: []string{"dragon"}, Aliases: []library.TagAlias{{Key: "dragons", Spellings: []string{"Dragons"}}}}}}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("tags = %+v, want %+v", page, want)
	}

	// A key with a slash splits through its escaped path.
	e.call("POST", "/tags/merge", map[string]string{"from": "M/F", "into": "male female"}, 204, nil)
	e.call("DELETE", "/tags/aliases/m%2Ff", nil, 204, nil)
	e.call("DELETE", "/tags/aliases/dragons", nil, 204, nil)
	e.call("DELETE", "/tags/aliases/dragons", nil, 404, nil)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run TestTagRoutes`
Expected: FAIL, `GET /tags/suggestions` answers 404.

- [ ] **Step 3: Delete the old list**

- `internal/api/organize.go`: delete `type tagCountsOut struct{ Body []library.TagCount }` and the `huma.Register` call for `list-tags`.
- `internal/library/organize.go`: delete `Tags` and its comment. Keep `TagCount`; facets use it.
- `internal/api/organize_test.go`, `TestTagsAndCollections`: replace the `GET /tags` block with:

```go
	var tags library.TagPage
	e.call("GET", "/tags", nil, 200, &tags)
	if tags.Total != 1 || tags.Tags[0].Name != "fantasy" || tags.Tags[0].Cards != 1 {
		t.Fatalf("tags = %+v", tags)
	}
```

- `internal/api/libraries_test.go`: delete the `GET /tags?library=…` call and its check in the first test (near line 103), and the `{"GET", "/tags?library=999", nil},` row (near line 162).
- `internal/library/libraries_test.go`: delete the `lib.Tags(ctx, archive.ID)` check (near line 234).
- `internal/library/search_test.go`, `TestSearchIgnoresNonStringTags`: replace the `lib.Tags` call and its check with:

```go
	page, err := lib.TagList(ctx, "", 1)
	if err != nil || page.Total != 1 || page.Tags[0].Name != "fantasy" {
		t.Fatalf("TagList = %+v, %v", page, err)
	}
```

- [ ] **Step 4: Add the routes**

`internal/api/tags.go`:

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
)

type tagListIn struct {
	Q    string `query:"q" doc:"Only tags whose key, or one of whose aliases' keys, contains this text's key."`
	Page int    `query:"page" minimum:"0" doc:"1-based, 100 tags a page; default 1."`
}

type tagPageOut struct{ Body library.TagPage }

type tagSuggestionsOut struct{ Body []library.TagSuggestion }

type mergeTagsIn struct {
	Body struct {
		From string `json:"from" minLength:"1" doc:"The tag to merge, in any spelling"`
		Into string `json:"into" minLength:"1" doc:"The tag it becomes. A name no card has renames from."`
	}
}

type tagAliasIn struct {
	Key string `path:"key" doc:"The alias, in any spelling"`
}

type distinctTagsIn struct {
	Body struct {
		A string `json:"a" minLength:"1"`
		B string `json:"b" minLength:"1"`
	}
}

func (s *Server) tagRoutes(api huma.API) {
	tags := []string{"Tags"}
	huma.Register(api, huma.Operation{OperationID: "list-tags", Method: http.MethodGet, Path: "/tags",
		Summary: "List tags", Description: "Primary tags across all libraries, most cards first, each with its card count and aliases. Archived cards aren't counted.", Tags: tags},
		func(ctx context.Context, in *tagListIn) (*tagPageOut, error) {
			page, err := s.Lib.TagList(ctx, in.Q, in.Page)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tagPageOut{Body: page}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-tag-suggestions", Method: http.MethodGet, Path: "/tags/suggestions",
		Summary: "Suggest tags to merge", Description: "Up to 100 pairs of tags that may be one: plurals, and keys of 5 or more characters one letter apart. " +
			"into has more cards. Pairs marked not the same are left out.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*tagSuggestionsOut, error) {
			out, err := s.Lib.TagSuggestions(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tagSuggestionsOut{Body: out}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "merge-tags", Method: http.MethodPost, Path: "/tags/merge",
		Summary: "Merge a tag into another", Description: "Makes from an alias of into; no card changes. An alias given as into stands for its primary tag, " +
			"and from's aliases move to into. When from and into differ only in case, spaces, - or _, into becomes the tag's name.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *mergeTagsIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.MergeTag(ctx, in.Body.From, in.Body.Into))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "split-tag", Method: http.MethodDelete, Path: "/tags/aliases/{key}",
		Summary: "Split an alias out of its tag", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *tagAliasIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.SplitTag(ctx, in.Key))
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "mark-tags-distinct", Method: http.MethodPost, Path: "/tags/distinct",
		Summary: "Mark two tags as not the same", Description: "Suggestions leave the pair out.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *distinctTagsIn) (*struct{}, error) {
			return nil, s.fail(s.Lib.MarkTagsDistinct(ctx, in.Body.A, in.Body.B))
		})
}
```

In `internal/api/server.go`, after `s.organizeRoutes(api)`, add `s.tagRoutes(api)`.

- [ ] **Step 5: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api ./internal/library && go vet ./...`
Expected: PASS, including `TestOperationTiers`, `TestTagsAndCollections`, and the library tests.

- [ ] **Step 6: Regenerate the schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `schema.d.ts` gains `Tag`, `TagAlias`, `TagPage`, `TagRef`, and `TagSuggestion`, and the five `/tags` operations; typecheck clean.

- [ ] **Step 7: Commit**

```bash
git add internal/api/tags.go internal/api/tags_test.go internal/api/organize.go internal/api/organize_test.go internal/api/libraries_test.go internal/api/server.go \
  internal/library/organize.go internal/library/libraries_test.go internal/library/search_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): list, merge, split, and suggest tags across all libraries" -m "GET /tags lists primary tags across all libraries and no longer takes library."
```

---

### Task 9: Merges refresh facet counts and rules

**Files:**
- Modify: `internal/api/facetcache.go`
- Modify: `internal/api/tags.go`
- Test: `internal/api/facetcache_test.go` (new), `internal/api/tags_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/api/facetcache_test.go`:

```go
package api

import (
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestFacetCacheDropsACountThatStartedBeforeAClear(t *testing.T) {
	var c facetCache
	gen, ok := c.start("k")
	if !ok {
		t.Fatal("couldn't start a count")
	}
	c.clear()
	c.finish("k", gen, &library.Facets{}, true)
	if _, _, ok := c.get("k"); ok {
		t.Fatal("cached a count that started before the clear")
	}
	gen, _ = c.start("k")
	c.finish("k", gen, &library.Facets{}, true)
	if _, _, ok := c.get("k"); !ok {
		t.Fatal("didn't cache a count that started after the clear")
	}
}
```

Add to `internal/api/tags_test.go`; its imports don't change:

```go
func TestMergingTagsDropsCachedFacetCounts(t *testing.T) {
	e := newEnv(t)
	for range 20 {
		e.createCard(`{"name": "C", "tags": ["fantasy"]}`)
	}
	for range 10 {
		e.createCard(`{"name": "C", "tags": ["fantasy world"]}`)
	}
	e.s.facets.sampleOver = 5
	body := map[string]any{"facets": []string{"tags"}, "facets_only": true}
	var res library.Results
	eventually(t, "counted exactly", func() bool {
		res = library.Results{}
		e.call("POST", "/cards/search", body, 200, &res)
		return !res.Facets.Approximate
	})
	if len(res.Facets.Tags) != 2 {
		t.Fatalf("before the merge = %+v, want two tags", res.Facets.Tags)
	}
	e.call("POST", "/tags/merge", map[string]string{"from": "fantasy world", "into": "fantasy"}, 204, nil)
	res = library.Results{}
	e.call("POST", "/cards/search", body, 200, &res)
	if len(res.Facets.Tags) != 1 {
		t.Fatalf("after the merge = %+v, want one tag", res.Facets.Tags)
	}
}

func TestMergingTagsReappliesRules(t *testing.T) {
	e := newEnv(t)
	startHide(e)
	junk := e.createCard(`{"name": "A", "tags": ["junk"]}`)
	rubbish := e.createCard(`{"name": "B", "tags": ["rubbish"]}`)
	r := e.putRule(e.newRule("Hide junk", map[string]any{"tag": "junk"}, map[string]any{"action": "archive", "all_libraries": true}), enable)
	eventually(t, "archived the junk card", func() bool { return e.archivedReason(junk.ID) == r.Reason() })
	e.call("POST", "/tags/merge", map[string]string{"from": "rubbish", "into": "junk"}, 204, nil)
	eventually(t, "archived the merged tag's card", func() bool { return e.archivedReason(rubbish.ID) == r.Reason() })
	e.call("DELETE", "/tags/aliases/rubbish", nil, 204, nil)
	eventually(t, "gave the split tag's card back", func() bool { return e.archivedReason(rubbish.ID) == "-" })
}
```

`eventually`, `startHide`, `newRule`, `putRule`, `enable`, and `archivedReason` are in `rules_test.go`.

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run 'TestFacetCache|TestMergingTags'`
Expected: FAIL to build, `c.clear undefined` and `assignment mismatch` on `c.start`.

- [ ] **Step 3: Give the cache a generation**

In `internal/api/facetcache.go`, add to `facetCache`, after `counting`:

```go
	// gen counts clears, so a count that started before one isn't cached.
	gen int
```

Replace `start` and `finish`, and add `clear`:

```go
// start marks key as counting and returns the cache's generation, or false
// when key already was counting.
func (c *facetCache) start(key string) (gen int, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counting[key] {
		return 0, false
	}
	if c.counting == nil {
		c.counting = map[string]bool{}
	}
	c.counting[key] = true
	return c.gen, true
}

// finish ends key's count, caching f when the count succeeded and no clear
// came since it started at gen, and drops counts nobody has refreshed in
// facetKeep.
func (c *facetCache) finish(key string, gen int, f *library.Facets, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.counting, key)
	if c.entries == nil {
		c.entries = map[string]facetEntry{}
	}
	if ok && gen == c.gen {
		c.entries[key] = facetEntry{f, time.Now()}
	}
	for k, e := range c.entries {
		if time.Since(e.counted) > facetKeep {
			delete(c.entries, k)
		}
	}
}

// clear drops every cached count, for a change that alters them all.
func (c *facetCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = nil
	c.gen++
}
```

In `recountFacets`, replace the `start` check and the `finish` call:

```go
	gen, ok := s.facets.start(key)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), facetRecount)
		defer cancel()
		res, err := s.Lib.Search(ctx, k, q)
		if err != nil {
			s.Log.Warn("recount facets", "err", err)
		}
		s.facets.finish(key, gen, res.Facets, err == nil)
	}()
```

- [ ] **Step 4: Call `tagsChanged` after a merge or split**

In `internal/api/tags.go`, add:

```go
// tagsChanged drops cached facet counts and reruns the hide job for enabled
// rules, since a merge or split changes what tag filters and counts read.
func (s *Server) tagsChanged(ctx context.Context) error {
	s.facets.clear()
	rules, err := s.Lib.Rules(ctx)
	if err != nil {
		return err
	}
	return s.recheckRules(ctx, rules...)
}
```

Change the merge and split handlers' bodies to:

```go
		func(ctx context.Context, in *mergeTagsIn) (*struct{}, error) {
			if err := s.Lib.MergeTag(ctx, in.Body.From, in.Body.Into); err != nil {
				return nil, s.fail(err)
			}
			return nil, s.fail(s.tagsChanged(ctx))
		})
```

```go
		func(ctx context.Context, in *tagAliasIn) (*struct{}, error) {
			if err := s.Lib.SplitTag(ctx, in.Key); err != nil {
				return nil, s.fail(err)
			}
			return nil, s.fail(s.tagsChanged(ctx))
		})
```

Add `" Cached facet counts are dropped, and the hide job reruns for enabled rules."` to the merge operation's `Description`, and `Description: "Cached facet counts are dropped, and the hide job reruns for enabled rules."` to the split operation.

- [ ] **Step 5: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api && go vet ./...`
Expected: PASS, including `TestFacetSearchAnswersFromASampleThenFromExactCounts`.

- [ ] **Step 6: Regenerate the schema and commit**

Run: `cd web && npm run gen && npm run typecheck`
Expected: only the two descriptions change; typecheck clean.

```bash
git add internal/api/facetcache.go internal/api/facetcache_test.go internal/api/tags.go internal/api/tags_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): drop cached facet counts and rerun rules after a tag merge or split"
```

---

### Task 10: The editor's save writes primary tags

**Files:**
- Modify: `internal/library/tags.go`
- Modify: `internal/api/entities.go` (the `update-` handler near line 294)
- Test: `internal/api/tags_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

The editor shows a card's raw tags as chips, so its save replaces aliases in the list it sends with their primary names rather than replaying a remove the way the bulk Tag job does. Removing `fantasy world` while `fantasy` stays keeps the tag. A save whose tags equal the base revision's keeps them as they are, so an avatar change doesn't rewrite them.

- [ ] **Step 1: Write the failing test**

Add to `internal/api/tags_test.go`; its imports gain `encoding/json`, `fmt`, and `slices`:

```go
func TestEditorSaveWritesPrimaryTagsAndMCPDoesNot(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "A", "tags": ["pirate"]}`)
	e.call("POST", "/tags/merge", map[string]string{"from": "fantasy world", "into": "fantasy"}, 204, nil)
	put := func(base int64, source, name string, tags ...string) (int64, []string) {
		t.Helper()
		data, _ := json.Marshal(map[string]any{"name": name, "tags": tags})
		var out entityResult
		e.call("PUT", fmt.Sprintf("/cards/%d", c.ID), map[string]any{"base_revision_id": base, "data": json.RawMessage(data), "source": source}, 200, &out)
		var saved struct{ Tags []string }
		if err := json.Unmarshal(out.Revision.Data, &saved); err != nil {
			t.Fatal(err)
		}
		return out.Revision.ID, saved.Tags
	}
	head, tags := put(c.Revision.ID, "mcp", "A", "pirate", "fantasy world")
	if !slices.Equal(tags, []string{"pirate", "fantasy world"}) {
		t.Fatalf("mcp save = %v, want the tags as sent", tags)
	}
	head, tags = put(head, "ui", "B", "pirate", "fantasy world")
	if !slices.Equal(tags, []string{"pirate", "fantasy world"}) {
		t.Fatalf("editor save without a tag change = %v, want them kept", tags)
	}
	_, tags = put(head, "ui", "B", "pirate", "fantasy world", "Fantasy", "smith")
	if !slices.Equal(tags, []string{"pirate", "fantasy", "smith"}) {
		t.Fatalf("editor save with a tag change = %v, want [pirate fantasy smith]", tags)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run TestEditorSaveWritesPrimaryTags`
Expected: FAIL, "editor save with a tag change = [pirate fantasy world Fantasy smith]".

- [ ] **Step 3: Add `WithPrimaryTags`**

In `internal/library/tags.go`, imports gain `encoding/json`. Add:

```go
// WithPrimaryTags returns edited with each alias in its tags replaced by its
// primary name, when its tags differ from base's. Data whose tags aren't a
// list of strings comes back as is, for prepare to store.
func (l *Library) WithPrimaryTags(ctx context.Context, base, edited json.RawMessage) (json.RawMessage, error) {
	before, _ := stringTags(base)
	after, ok := stringTags(edited)
	if !ok || slices.Equal(before, after) {
		return edited, nil
	}
	primary, err := l.primaryNames(ctx, after)
	if err != nil {
		return nil, err
	}
	tags := primaryTags(after, primary)
	if slices.Equal(tags, after) {
		return edited, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(edited, &top); err != nil {
		return nil, fmt.Errorf("%w: data must be a JSON object", ErrInvalid)
	}
	if top["tags"], err = json.Marshal(tags); err != nil {
		return nil, err
	}
	return json.Marshal(top)
}

// stringTags is data's tags, and false unless data is an object whose tags
// are missing or a list of strings.
func stringTags(data json.RawMessage) ([]string, bool) {
	var d struct {
		Tags []*string `json:"tags"`
	}
	// *string so a null element is seen; []string would turn it into "".
	if json.Unmarshal(data, &d) != nil || slices.Contains(d.Tags, nil) {
		return nil, false
	}
	out := make([]string, len(d.Tags))
	for i, t := range d.Tags {
		out[i] = *t
	}
	return out, true
}
```

- [ ] **Step 4: Call it from the editor's save**

In `internal/api/entities.go`, in the `update-` handler, after the `e := library.Edit{…}` line:

```go
			if k == library.Card && e.Source == "ui" {
				// The owner's save writes merged tags under their primary names; mcp keeps what it sends.
				if e.Data, err = s.Lib.WithPrimaryTags(ctx, base.Data, e.Data); err != nil {
					return nil, s.fail(err)
				}
			}
```

Change the operation's `Description` to `"Adds a revision on top of base_revision_id. With source ui, a card whose tags changed is saved with merged tags under their primary names."`.

- [ ] **Step 5: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api ./internal/library && go vet ./...`
Expected: PASS, including `TestCardLifecycle`.

- [ ] **Step 6: Regenerate the schema and commit**

Run: `cd web && npm run gen && npm run typecheck`
Expected: only the update operations' description changes; typecheck clean.

```bash
git add internal/library/tags.go internal/api/entities.go internal/api/tags_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): the card editor saves merged tags under their primary names"
```

---

### Task 11: Back up aliases

**Files:**
- Modify: `internal/backup/backup.go` (`tables` near line 35)
- Test: `internal/backup/backup_test.go` (`TestWriteAssets` near line 102)

Merges are the owner's work, like collections and rules, so an assets backup keeps them, self rows included as ordinary rows. `copyTable` already leaves out generated columns such as `primary_tag_key`.

- [ ] **Step 1: Write the failing test**

In `TestWriteAssets`, add `"tag_alias": 0, "tag_distinct": 0,` to `want` after `"enrichment": 0,`.

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/backup -run TestWriteAssets`
Expected: FAIL, `tables = map[…]` without `tag_alias`.

- [ ] **Step 3: Implement**

Append `"tag_alias", "tag_distinct"` to the `Assets` list in `tables`, after `"enrichment"`.

- [ ] **Step 4: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/backup`
Expected: PASS, including the restore tests.

- [ ] **Step 5: Commit**

```bash
git add internal/backup/backup.go internal/backup/backup_test.go
git commit -m "feat(backup): keep tag aliases and not-the-same pairs in assets backups"
```

---

### Task 12: The Tags page

**Files:**
- Create: `web/src/tags/Tags.tsx`, `web/src/tags/Tags.css`, `web/src/tags/Tags.test.tsx`

- [ ] **Step 1: Write the failing tests**

`web/src/tags/Tags.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Tags } from "./Tags";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const fantasy = {
  name: "fantasy",
  key: "fantasy",
  cards: 12,
  spellings: ["fantasy", "Fantasy"],
  aliases: [{ key: "fantasyworld", spellings: ["fantasy world"] }],
};
let gets: string[];
let calls: { method: string; path: string; body: unknown }[];
beforeEach(() => {
  gets = [];
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (url.pathname === "/api/libraries") return json([{ id: 1, name: "Main", cards: 0, lorebooks: 0, created_at: "" }]);
      if (req.method !== "GET") {
        calls.push({ method: req.method, path: url.pathname, body: req.method === "DELETE" ? undefined : await req.json() });
        return new Response(null, { status: 204 });
      }
      if (url.pathname === "/api/tags/suggestions")
        return json([{ into: { name: "dragon", key: "dragon", cards: 9 }, from: { name: "dragons", key: "dragons", cards: 2 } }]);
      if (url.pathname === "/api/tags") {
        gets.push(url.search);
        return json({ tags: [fantasy], total: 101, pages: 2 });
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPage() {
  const router = createMemoryRouter([{ path: "/tags", element: <Toasts><Tags /></Toasts> }], { initialEntries: ["/tags"] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("each tag shows its count, other spellings, and aliases, links to its cards, and pages", async () => {
  renderPage();
  const user = userEvent.setup();
  expect(await screen.findByRole("link", { name: "fantasy" })).toHaveAttribute("href", "/cards?f=tag%3Afantasy&library=1");
  expect(screen.getByText("12 cards")).toBeInTheDocument();
  expect(screen.getByText("Fantasy")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Split fantasy world from fantasy" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Next page" }));
  await waitFor(() => expect(gets).toContain("?page=2"));
  await user.type(screen.getByRole("searchbox", { name: "Search tags" }), "drag{Enter}");
  await waitFor(() => expect(gets).toContain("?q=drag&page=1"));
});

test("rename and merge into send a merge, and split deletes the alias", async () => {
  renderPage();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Rename fantasy" }));
  const input = screen.getByRole("textbox", { name: "New name for fantasy" });
  await user.clear(input);
  await user.type(input, "Epic fantasy{Enter}");
  // A merge closes its form when it succeeds, so wait for that before opening the next one.
  await waitFor(() => expect(screen.queryByRole("textbox", { name: "New name for fantasy" })).not.toBeInTheDocument());
  await user.click(screen.getByRole("button", { name: "Merge fantasy into…" }));
  await user.type(screen.getByRole("textbox", { name: "Merge fantasy into" }), "magic{Enter}");
  await waitFor(() => expect(screen.queryByRole("textbox", { name: "Merge fantasy into" })).not.toBeInTheDocument());
  await user.click(screen.getByRole("button", { name: "Split fantasy world from fantasy" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/tags/merge", body: { from: "fantasy", into: "Epic fantasy" } },
      { method: "POST", path: "/api/tags/merge", body: { from: "fantasy", into: "magic" } },
      { method: "DELETE", path: "/api/tags/aliases/fantasyworld", body: undefined },
    ]),
  );
});

test("make primary merges the tag into another spelling or into an alias", async () => {
  renderPage();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Make Fantasy primary" }));
  await user.click(screen.getByRole("button", { name: "Make fantasy world primary" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/tags/merge", body: { from: "fantasy", into: "Fantasy" } },
      { method: "POST", path: "/api/tags/merge", body: { from: "fantasy", into: "fantasy world" } },
    ]),
  );
});

test("a suggestion merges into the tag with more cards, the other way after Swap, or is dismissed", async () => {
  renderPage();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Merge dragons into dragon" }));
  await user.click(screen.getByRole("button", { name: "Swap dragons and dragon" }));
  await user.click(screen.getByRole("button", { name: "Merge dragon into dragons" }));
  await user.click(screen.getByRole("button", { name: "dragon and dragons are not the same" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/tags/merge", body: { from: "dragons", into: "dragon" } },
      { method: "POST", path: "/api/tags/merge", body: { from: "dragon", into: "dragons" } },
      { method: "POST", path: "/api/tags/distinct", body: { a: "dragon", b: "dragons" } },
    ]),
  );
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/tags/Tags.test.tsx`
Expected: FAIL, `./Tags` doesn't exist.

- [ ] **Step 3: Implement `web/src/tags/Tags.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { api, must, mustOk } from "../api/client";
import type { components } from "../api/schema";
import { count } from "../shell/count";
import { useLibrary, withLibrary } from "../shell/library";
import "./Tags.css";

type Tag = components["schemas"]["Tag"];

/** cardsWith is the Cards page of library filtered to tag. */
const cardsWith = (tag: string, library: number) => withLibrary(`/cards?${new URLSearchParams({ f: `tag:${tag}` })}`, library);

/** Tags lists the primary tags across all libraries, with their aliases, and suggests likely duplicates to merge. */
export function Tags() {
  const qc = useQueryClient();
  const library = useLibrary();
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [editing, setEditing] = useState<{ key: string; mode: "rename" | "merge" } | null>(null);
  const [swapped, setSwapped] = useState<Record<string, boolean>>({});
  const list = useQuery({
    queryKey: ["tags", q, page],
    queryFn: async () => must(await api.GET("/tags", { params: { query: { q: q || undefined, page } } })),
    placeholderData: (previous) => previous,
  });
  const suggestions = useQuery({ queryKey: ["tag-suggestions"], queryFn: async () => must(await api.GET("/tags/suggestions")) });
  // A merge or split changes tag counts and filters on every page.
  const changed = () => qc.invalidateQueries();
  const merge = useMutation({
    mutationFn: async (v: { from: string; into: string }) => mustOk(await api.POST("/tags/merge", { body: v })),
    onSuccess: (_, v) => {
      setEditing(null);
      toast.success(`Merged ${v.from} into ${v.into}`);
      return changed();
    },
  });
  const split = useMutation({
    mutationFn: async (key: string) => mustOk(await api.DELETE("/tags/aliases/{key}", { params: { path: { key } } })),
    onSuccess: changed,
  });
  const distinct = useMutation({
    mutationFn: async (v: { a: string; b: string }) => mustOk(await api.POST("/tags/distinct", { body: v })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tag-suggestions"] }),
  });
  const submit = (t: Tag) => (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const into = String(new FormData(e.currentTarget).get("into") ?? "").trim();
    if (into && into !== t.name) merge.mutate({ from: t.name, into });
    else setEditing(null);
  };
  const pages = list.data?.pages ?? 0;

  return (
    <section className="page">
      <h1>Tags</h1>
      <p className="muted">
        Every tag across all libraries, most used first. Spellings that differ only in case, spaces, - or _ are already one tag.
        Merging tags changes no card: search, counts, and rules read them as one.
      </p>
      {(suggestions.data?.length ?? 0) > 0 && (
        <>
          <h2>Possible duplicates</h2>
          <ul className="tag-list">
            {(suggestions.data ?? []).map((s) => {
              const id = `${s.from.key}|${s.into.key}`;
              const [from, into] = swapped[id] ? [s.into, s.from] : [s.from, s.into];
              return (
                <li key={id} className="tag-row">
                  <span>
                    {from.name} <span className="muted">({count(from.cards, "card")})</span>
                  </span>
                  <span aria-hidden="true">→</span>
                  <span>
                    {into.name} <span className="muted">({count(into.cards, "card")})</span>
                  </span>
                  <span className="tag-actions">
                    <button className="button primary" aria-label={`Merge ${from.name} into ${into.name}`}
                      onClick={() => merge.mutate({ from: from.name, into: into.name })}>
                      Merge
                    </button>{" "}
                    <button className="button" aria-label={`Swap ${from.name} and ${into.name}`}
                      onClick={() => setSwapped({ ...swapped, [id]: !swapped[id] })}>
                      Swap
                    </button>{" "}
                    <button className="button" aria-label={`${from.name} and ${into.name} are not the same`}
                      onClick={() => distinct.mutate({ a: from.key, b: into.key })}>
                      Not the same
                    </button>
                  </span>
                </li>
              );
            })}
          </ul>
          <h2>All tags</h2>
        </>
      )}
      <form
        role="search"
        className="tag-search"
        onSubmit={(e) => {
          e.preventDefault();
          setQ(String(new FormData(e.currentTarget).get("q") ?? "").trim());
          setPage(1);
        }}
      >
        <input className="input" name="q" type="search" aria-label="Search tags" placeholder="Search tags" />
        <button className="button">Search</button>
      </form>
      {list.error && <p className="error">{list.error.message}</p>}
      {list.data?.total === 0 && <p>{q ? "No tag matches." : "No tags yet."}</p>}
      <ul className="tag-list">
        {(list.data?.tags ?? []).map((t) => (
          <li key={t.key} className="tag-row">
            {editing?.key === t.key ? (
              <form onSubmit={submit(t)}>
                <input
                  className="input"
                  name="into"
                  autoFocus
                  aria-label={editing.mode === "rename" ? `New name for ${t.name}` : `Merge ${t.name} into`}
                  defaultValue={editing.mode === "rename" ? t.name : ""}
                  onKeyDown={(e) => e.key === "Escape" && setEditing(null)}
                  onBlur={() => setEditing(null)}
                />
              </form>
            ) : (
              <Link className="tag-name" to={cardsWith(t.name, library)}>
                {t.name}
              </Link>
            )}
            <span className="muted">{count(t.cards, "card")}</span>
            {t.spellings
              .filter((s) => s !== t.name)
              .map((s) => (
                <span key={s} className="chip">
                  {s}{" "}
                  <button className="button" aria-label={`Make ${s} primary`} onClick={() => merge.mutate({ from: t.name, into: s })}>
                    Make primary
                  </button>
                </span>
              ))}
            {t.aliases.map((a) => {
              const name = a.spellings.join(", ") || a.key;
              const spelling = a.spellings[0] ?? a.key;
              return (
                <span key={a.key} className="chip">
                  {name}{" "}
                  {/* Merging a tag into its own alias swaps them. */}
                  <button className="button" aria-label={`Make ${spelling} primary`}
                    onClick={() => merge.mutate({ from: t.name, into: spelling })}>
                    Make primary
                  </button>{" "}
                  <button className="button" aria-label={`Split ${name} from ${t.name}`} onClick={() => split.mutate(a.key)}>
                    Split
                  </button>
                </span>
              );
            })}
            <span className="tag-actions">
              <button className="button" aria-label={`Rename ${t.name}`} onClick={() => setEditing({ key: t.key, mode: "rename" })}>
                Rename
              </button>{" "}
              <button className="button" aria-label={`Merge ${t.name} into…`} onClick={() => setEditing({ key: t.key, mode: "merge" })}>
                Merge into…
              </button>
            </span>
          </li>
        ))}
      </ul>
      {pages > 1 && (
        <nav className="tag-pager" aria-label="Pages">
          <button className="button" disabled={page <= 1} onClick={() => setPage(page - 1)}>
            Previous page
          </button>
          <span>
            Page {page} of {pages}
          </span>
          <button className="button" disabled={page >= pages} onClick={() => setPage(page + 1)}>
            Next page
          </button>
        </nav>
      )}
    </section>
  );
}
```

- [ ] **Step 4: Add `web/src/tags/Tags.css`**

```css
.tag-search {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin: var(--space) 0;
}

.tag-search .input {
  width: auto;
  flex: 1 1 12rem;
  max-width: 24rem;
}

.tag-list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 0.5rem;
}

.tag-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.tag-name {
  font-weight: 600;
  overflow-wrap: anywhere;
}

.tag-row form .input {
  width: auto;
  min-width: 10rem;
}

.tag-actions {
  margin-left: auto;
}

.tag-pager {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin-top: var(--space);
}
```

- [ ] **Step 5: Run the tests**

Run: `cd web && npm run typecheck && npx vitest run src/tags/Tags.test.tsx`
Expected: typecheck clean; four tests pass.

- [ ] **Step 6: Commit**

```bash
git add web/src/tags
git commit -m "feat(web): Tags page lists, merges, splits, and suggests tags"
```

---

### Task 13: Route and nav entry

**Files:**
- Modify: `web/src/App.tsx` (imports; `collections` route near line 82)
- Modify: `web/src/shell/Shell.tsx` (`sections` near line 14)
- Modify: `web/src/shell/Icon.tsx` (`paths`)
- Test: `web/src/shell/Shell.test.tsx`

- [ ] **Step 1: Write the failing test**

In `web/src/shell/Shell.test.tsx`, in "the rail links every section and marks the current one", change the list to:

```tsx
  for (const name of ["Cards", "Lorebooks", "Collections", "Tags", "Saved searches", "Rules", "Sync", "Settings"]) {
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/shell/Shell.test.tsx`
Expected: FAIL, no link named Tags.

- [ ] **Step 3: Implement**

`web/src/shell/Icon.tsx`, in `paths` after `collections`:

```tsx
  tags: <path d="M3 12V4a1 1 0 0 1 1-1h8l9 9-9 9zM7.5 7.5h.01" />,
```

`web/src/shell/Shell.tsx`, in `sections` after Collections:

```tsx
  { to: "/tags", label: "Tags", icon: "tags" },
```

The Tags page spans libraries, so it stays out of `listPages`.

`web/src/App.tsx`: add `import { Tags } from "./tags/Tags";` after the `Sync` import, and after the `collections` route:

```tsx
          { path: "tags", element: <Tags /> },
```

- [ ] **Step 4: Run the tests**

Run: `cd web && npm run typecheck && npm test`
Expected: typecheck clean; every test passes.

- [ ] **Step 5: Commit**

```bash
git add web/src/App.tsx web/src/shell/Shell.tsx web/src/shell/Icon.tsx web/src/shell/Shell.test.tsx
git commit -m "feat(web): link the Tags page beside Collections"
```

---

### Task 14: Docs

**Files:**
- Modify: `docs/using.md`

- [ ] **Step 1: Add a Tags section**

In `docs/using.md`, before `## Saved searches and rules`, add:

```markdown
## Tags

Spellings of a tag that differ only in case, spaces, `-`, or `_` are one
tag: `Sci-Fi`, `sci fi`, and `scifi` filter and count together.

The Tags page lists every tag across all libraries, most used first. Merge
into… makes one tag an alias of another; Rename merges a tag into a name no
card has yet, or into another spelling of it, such as `Sci-Fi` for `scifi`;
Make primary on an alias or a spelling makes it the tag's name; Split undoes
a merge for one alias. A merge changes no card:
search, counts, rules, and similar cards read the two as one, and apps and
exports still get each card's tags as the card holds them.

Above the list, the page suggests likely duplicates: plurals, and spellings
one letter apart. Merge one into the other, Swap to merge the other way, or
choose Not the same so the pair isn't suggested again.

A card's tags change to their merged names only when you edit them: a bulk
Tag action, or saving the card editor with changed tags. A bulk remove of a
tag also removes its aliases. Imports, updates from apps, plugins, and MCP
writes keep the tags they bring. See `/tags`.
```

- [ ] **Step 2: Commit**

```bash
git add docs/using.md
git commit -m "docs: merge and split tags on the Tags page"
```

---

### Task 15: Final verification

**Files:** none

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: vet clean; every Go and web test passes; no test skipped for want of a database.

- [ ] **Step 2: Check the generated schema is current**

Run: `cd web && npm run gen && git status --short openapi.json src/api/schema.d.ts`
Expected: no output.

- [ ] **Step 3: Check the page in a browser**

Start the stack from the checkout with `docker compose up -d --build`, which builds the local source. Then, in `web/`, run `npm run dev`. Tag a few cards `Dragon`, `dragon`, `dragons`, and `fantasy world` so the page has content. Open `http://localhost:5173/tags`:

1. At 1200 px wide: the suggestions list shows dragons and dragon; Merge, Swap, and Not the same work; the tag list shows counts, spelling chips with Make primary, alias chips with Make primary and Split, and Rename and Merge into… on each row. The tag shows as `Dragon` with a `dragon` chip; Make primary on the chip renames the tag `dragon`.
2. At 375 px wide: rows wrap, buttons stay reachable, and `document.documentElement.scrollWidth <= window.innerWidth` is `true` in the console.
3. Merge `fantasy world` into `fantasy`, then open Cards: the Tags facet shows `fantasy` once, and filtering on it shows both cards.

- [ ] **Step 4: Read the diff**

Run: `git diff develop --stat` and read `git diff develop`.
Expected: no dead code, no leftover `tagsOf`, `containsFold`, or `Library.Tags`, and nothing the plan didn't ask for.
