# Discovery slice 2: similar and related cards

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show, on each card page and through MCP, the cards that read like this one and the cards linked to it by creator, lorebook, or collection.

**Architecture:** `library.SimilarCards` ranks cards by cosine distance between `card_embedding` vectors from the embed instance, falling back to Jaccard similarity of tags. `library.RelatedCards` lists one group of linked cards. Both exclude the card's variant family and archived cards, and page with `library.Page` using offset cursors. Two API routes expose them, two MCP tools wrap the routes, and two side-panel sections show them on the card page.

**Tech stack:** Go, pgx, PostgreSQL with pgvector, huma, MCP Go SDK, React and TanStack Query.

**Spec:** [2026-10-23-discovery-design.md](../specs/2026-10-23-discovery-design.md), sections Similar, Related, MCP, Web.

---

## Conventions

- Work in `~/git/innkeeper-discovery-similar` (branch `discovery-similar`).
  Never run git in `~/git/innkeeper`.
- Go tests need
  `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
  Without it they skip, which is not a pass.
- After any API change, run `cd web && npm run gen`. Never hand-edit
  `web/openapi.json` or `web/src/api/schema.d.ts`.
- Comments say why, in one line. Follow AGENTS.md.

## Files

| File | Change |
|---|---|
| `internal/library/discover.go` | `SimilarCards`, `RelatedCards`, their types, `Page.window` |
| `internal/library/discover_test.go` | Tests |
| `internal/api/discover.go` | `GET /cards/{id}/similar`, `GET /cards/{id}/related` |
| `internal/api/discover_test.go` | Tests |
| `internal/api/server.go` | Register the routes |
| `internal/mcp/tools.go` | `find_similar_cards`, `find_related_cards` |
| `internal/api/mcp_test.go` | MCP test |
| `web/src/card/Discover.tsx` | `Similar`, `Related`, shared `CardList` |
| `web/src/card/Discover.test.tsx` | Tests |
| `web/src/card/Panel.tsx` | Render both |

---

### Task 1: Similar cards in the library

**Files:** create `internal/library/discover.go`, `internal/library/discover_test.go`

Background: `card_embedding` (migration 0045) holds one vector per
`(instance_id, card_id)`. `card_tag_keys(data)` (migration 0044) is a card
revision's lower-cased tags, GIN-indexed. Variants (`card.variant_of_id`)
stay in one library. Existing test helpers in package `library`:
`newLibrary`, `newCard(t, lib, slug, data) (id, rev)`, `newInstanceRow`
(embeddings_test.go), `newLibraryRow` and `moveCard` (similar_test.go).
`lib.Cards.SetArchived(ctx, id, true)` archives. `lib.Cards.SetVariantOf(ctx, id, &parent)` links a variant (check the exact signature in variants.go).

- [ ] **Step 1: Write the failing tests**

```go
package library

import (
	"context"
	"testing"
)

// embedCards saves vec[i] as cards[i]'s vector from instance, from each card's head.
func embedCards(t *testing.T, lib *Library, instance int64, vecs map[int64][]float32) {
	t.Helper()
	ctx := context.Background()
	for id, v := range vecs {
		c, err := lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.SaveEmbeddings(ctx, instance, []EmbedText{{CardID: id, RevisionID: c.HeadRevisionID}}, [][]float32{v}); err != nil {
			t.Fatal(err)
		}
	}
}

func ids(cards []DiscoveredCard) []int64 {
	out := make([]int64, len(cards))
	for i, c := range cards {
		out[i] = c.CardID
	}
	return out
}

func TestSimilarRanksByEmbeddingDistance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	near, _ := newCard(t, lib, "near", `{"name": "Dorna"}`)
	far, _ := newCard(t, lib, "far", `{"name": "Lia"}`)
	embedCards(t, lib, inst, map[int64][]float32{me: {1, 0}, near: {0.9, 0.1}, far: {0, 1}})

	got, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst})
	if err != nil {
		t.Fatal(err)
	}
	if got.Basis != "embedding" || len(got.Items) != 2 || got.Items[0].CardID != near || got.Items[1].CardID != far ||
		got.Items[0].Name != "Dorna" || got.Items[0].Score <= got.Items[1].Score {
		t.Fatalf("similar = %+v", got)
	}
}

func TestSimilarFallsBackToTagsWithoutEmbedInstance(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "tags": ["Dwarf", "smith", "grumpy"]}`)
	two, _ := newCard(t, lib, "two", `{"name": "Dorna", "tags": ["dwarf", "smith"]}`)
	three, _ := newCard(t, lib, "three", `{"name": "Grum", "tags": ["dwarf", "smith", "grumpy", "old"]}`)
	newCard(t, lib, "one", `{"name": "Lia", "tags": ["dwarf", "elf"]}`)

	got, err := lib.SimilarCards(ctx, me, SimilarOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Jaccard: three shares 3 of 4 tags, two shares 2 of 3; one shares only 1.
	if got.Basis != "tags" || !slices.Equal(ids(got.Items), []int64{three, two}) || got.Items[0].Score != 0.75 {
		t.Fatalf("similar = %+v", got)
	}
}

func TestSimilarFallsBackToTagsForACardWithoutAVector(t *testing.T) {
	lib := newLibrary(t)
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "tags": ["dwarf", "smith"]}`)
	other, _ := newCard(t, lib, "other", `{"name": "Dorna", "tags": ["dwarf", "smith"]}`)
	embedCards(t, lib, inst, map[int64][]float32{other: {1, 0}})
	got, err := lib.SimilarCards(context.Background(), me, SimilarOptions{Instance: inst})
	if err != nil || got.Basis != "tags" || !slices.Equal(ids(got.Items), []int64{other}) {
		t.Fatalf("similar = %+v, %v", got, err)
	}
}

func TestSimilarExcludesVariantFamilyDuplicatesAndArchived(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	root, _ := newCard(t, lib, "root", `{"name": "Root"}`)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	sibling, _ := newCard(t, lib, "sibling", `{"name": "Sibling"}`)
	dup, _ := newCard(t, lib, "dup", `{"name": "Brakka"}`)
	archived, _ := newCard(t, lib, "archived", `{"name": "Old"}`)
	kept, _ := newCard(t, lib, "kept", `{"name": "Kept"}`)
	for _, v := range []int64{me, sibling} {
		if err := lib.Cards.SetVariantOf(ctx, v, &root); err != nil {
			t.Fatal(err)
		}
	}
	if err := lib.Cards.SetArchived(ctx, archived, true); err != nil {
		t.Fatal(err)
	}
	embedCards(t, lib, inst, map[int64][]float32{root: {1, 0}, me: {1, 0}, sibling: {1, 0}, dup: {1, 0}, archived: {1, 0}, kept: {0, 1}})

	got, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst})
	if err != nil || !slices.Equal(ids(got.Items), []int64{kept}) {
		t.Fatalf("similar = %+v, %v; want only kept", got, err)
	}
}

func TestSimilarAllScopeFindsOtherLibraries(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	away, _ := newCard(t, lib, "away", `{"name": "Dorna"}`)
	archive := newLibraryRow(t, lib, "Archive")
	moveCard(t, lib, away, archive)
	embedCards(t, lib, inst, map[int64][]float32{me: {1, 0}, away: {1, 0.1}})

	here, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst})
	if err != nil || len(here.Items) != 0 {
		t.Fatalf("library scope = %+v, %v", here, err)
	}
	all, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst, AllLibraries: true})
	if err != nil || len(all.Items) != 1 || all.Items[0].CardID != away || all.Items[0].LibraryID != archive {
		t.Fatalf("all scope = %+v, %v", all, err)
	}
}

func TestSimilarPagesByCursor(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	inst := newInstanceRow(t, lib)
	me, _ := newCard(t, lib, "me", `{"name": "Me"}`)
	vecs := map[int64][]float32{me: {1, 0}}
	var want []int64
	for i := range 5 {
		id, _ := newCard(t, lib, fmt.Sprintf("c%d", i), fmt.Sprintf(`{"name": "C%d"}`, i))
		vecs[id] = []float32{1, float32(i + 1)}
		want = append(want, id)
	}
	embedCards(t, lib, inst, vecs)

	var got []int64
	page := Page{Size: 2}
	for range 3 {
		res, err := lib.SimilarCards(ctx, me, SimilarOptions{Instance: inst, Page: page})
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ids(res.Items)...)
		if res.NextCursor == nil {
			break
		}
		page.Cursor = res.NextCursor
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged = %v, want %v", got, want)
	}
}
```

Add `"fmt"` and `"slices"` to the imports.

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./internal/library -run TestSimilar
```

Expected: a compile error, `lib.SimilarCards undefined`. (The old
`TestSimilar*` tests in similar_test.go exercise `DuplicateCards`; if a
name collides, rename the new test, never the old.)

- [ ] **Step 3: Implement**

`internal/library/discover.go`:

```go
package library

import (
	"cmp"
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5"
)

// DiscoveredCard is a card found from another: similar or related to it.
type DiscoveredCard struct {
	CardID    int64   `json:"card_id"`
	LibraryID int64   `json:"library_id"`
	Name      string  `json:"name"`
	Score     float64 `json:"score,omitempty" doc:"How alike, 0 to 1; similar cards only."`
}

// SimilarPage is one page of similar cards.
type SimilarPage struct {
	Basis      string           `json:"basis" enum:"embedding,tags" doc:"embedding ranks by meaning; tags, by shared tags, when there is no embed instance or the card has no vector yet."`
	Items      []DiscoveredCard `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

// SimilarOptions narrows SimilarCards.
type SimilarOptions struct {
	// Instance is the embed instance, or 0 to rank by tags.
	Instance     int64
	AllLibraries bool
	Page         Page
}

// discoverPageSize is the default page of similar and related cards.
const discoverPageSize = 20

// minSharedTags is how many tags a card must share to count as similar by tags.
const minSharedTags = 2

// window turns p into a limit and offset. Discovery cursors are offsets: an
// exact scan ranks every candidate, so a keyset saves nothing.
func (p Page) window() (limit, offset int, err error) {
	limit = cmp.Or(p.Size, discoverPageSize)
	switch {
	case p.Cursor != nil && p.Number != 0:
		return 0, 0, fmt.Errorf("%w: page takes a cursor or a number, not both", ErrInvalid)
	case p.Cursor != nil:
		offset, err = strconv.Atoi(*p.Cursor)
		if err != nil || offset < 0 {
			return 0, 0, fmt.Errorf("%w: bad cursor %q", ErrInvalid, *p.Cursor)
		}
	case p.Number > 0:
		offset = (p.Number - 1) * limit
	}
	return limit, offset, nil
}

// discoverWith is the WITH clause discovery queries share: fam holds every
// card in $1's variant family, and self is $1's head.
const discoverWith = `WITH RECURSIVE up AS (
		SELECT id, variant_of_id FROM card WHERE id = $1
		UNION SELECT p.id, p.variant_of_id FROM card p JOIN up ON p.id = up.variant_of_id
	), fam AS (
		SELECT id FROM up WHERE variant_of_id IS NULL
		UNION SELECT x.id FROM card x JOIN fam ON x.variant_of_id = fam.id
	), self AS (
		SELECT c.library_id, r.content_hash, card_tag_keys(r.data) AS tags, lower(btrim(r.data->>'creator')) AS creator
		FROM card c JOIN card_revision r ON r.id = c.head_revision_id WHERE c.id = $1
	)`

// discoverWhere leaves out the card, its family, and archived cards.
const discoverWhere = `c.archived_at IS NULL AND c.id NOT IN (SELECT id FROM fam)`

// SimilarCards returns cards like cardID, closest first: by embedding
// distance when o.Instance is set and the card has a vector from it, else
// by shared tags. Exact copies and the card's variants are left out; other
// panels show them.
func (l *Library) SimilarCards(ctx context.Context, cardID int64, o SimilarOptions) (SimilarPage, error) {
	limit, offset, err := o.Page.window()
	if err != nil {
		return SimilarPage{}, err
	}
	basis, err := l.similarBasis(ctx, cardID, o.Instance)
	if err != nil {
		return SimilarPage{}, err
	}
	// ponytail: exact scan over every vector; add an HNSW index when an archive makes this slow.
	sql := discoverWith + `, q AS (SELECT embedding FROM card_embedding WHERE instance_id = $2 AND card_id = $1)
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), 1 - (e.embedding <=> q.embedding)
		FROM self CROSS JOIN q
		JOIN card_embedding e ON e.instance_id = $2 AND vector_dims(e.embedding) = vector_dims(q.embedding)
		JOIN card c ON c.id = e.card_id
		JOIN card_revision r ON r.id = c.head_revision_id
		WHERE ` + discoverWhere + ` AND r.content_hash <> self.content_hash AND ($3 OR c.library_id = self.library_id)
		ORDER BY e.embedding <=> q.embedding, c.id
		LIMIT $4 OFFSET $5`
	if basis == "tags" {
		sql = discoverWith + `
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), x.shared::float8 / (cardinality(self.tags) + x.n - x.shared)
		FROM self
		JOIN card_revision r ON card_tag_keys(r.data) && self.tags
		JOIN card c ON c.head_revision_id = r.id
		CROSS JOIN LATERAL (SELECT count(*) FILTER (WHERE t = ANY(self.tags)) AS shared, count(*) AS n
			FROM unnest(card_tag_keys(r.data)) t) x
		WHERE ` + discoverWhere + ` AND r.content_hash <> self.content_hash AND ($3 OR c.library_id = self.library_id)
			AND x.shared >= ` + strconv.Itoa(minSharedTags) + ` AND $2::bigint IS NOT NULL
		ORDER BY 4 DESC, c.id
		LIMIT $4 OFFSET $5`
	}
	items, next, err := l.discoverPage(ctx, sql, limit, offset, cardID, o.Instance, o.AllLibraries, limit+1, offset)
	if err != nil {
		return SimilarPage{}, fmt.Errorf("similar cards to %d: %w", cardID, err)
	}
	return SimilarPage{Basis: basis, Items: items, NextCursor: next}, nil
}

// similarBasis is embedding when instance is set and has a vector for cardID, else tags.
func (l *Library) similarBasis(ctx context.Context, cardID, instance int64) (string, error) {
	if instance == 0 {
		return "tags", nil
	}
	var has bool
	err := l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM card_embedding WHERE instance_id = $1 AND card_id = $2)`,
		instance, cardID).Scan(&has)
	if err != nil || !has {
		return "tags", err
	}
	return "embedding", nil
}

// discoverPage runs a discovery query that selects (id, library_id, name,
// score), asked for limit+1 rows, and returns limit of them and the next
// cursor when there are more.
func (l *Library) discoverPage(ctx context.Context, sql string, limit, offset int, args ...any) ([]DiscoveredCard, *string, error) {
	rows, err := l.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, nil, err
	}
	items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[DiscoveredCard])
	if err != nil {
		return nil, nil, err
	}
	if len(items) <= limit {
		return items, nil, nil
	}
	next := strconv.Itoa(offset + limit)
	return items[:limit], &next, nil
}
```

The tags query keeps `$2` in its argument list (`$2::bigint IS NOT
NULL` is always true) so both queries take the same arguments; if you find
a cleaner way to share the argument list, use it.

`RowToStructByPos` needs four columns for `DiscoveredCard`; the related
query in Task 2 selects `0::float8` as the score.

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/library -run 'TestSimilar' -v
```

Expected: the five new tests and the existing `TestSimilar*` duplicate
tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/discover.go internal/library/discover_test.go
git commit -m "feat(library): rank cards similar to a card by embedding or tags"
```

---

### Task 2: Related cards in the library

**Files:** `internal/library/discover.go`, `internal/library/discover_test.go`

Helpers: `newLorebook(t, lib, slug)` (sources_test.go),
`lib.LinkLorebook(ctx, cardID, lorebookID)`,
`lib.CreateCollection(ctx, libraryID, name, description)`,
`lib.AddToCollection(ctx, collectionID, cardID)`.

- [ ] **Step 1: Write the failing tests**

```go
func TestRelatedGroups(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Brakka", "creator": "DG "}`)
	byCreator, _ := newCard(t, lib, "by-dg", `{"name": "Dorna", "creator": "dg"}`)
	newCard(t, lib, "by-other", `{"name": "Lia", "creator": "someone"}`)
	sharesBook, _ := newCard(t, lib, "book", `{"name": "Grum"}`)
	inCollection, _ := newCard(t, lib, "coll", `{"name": "Hild"}`)

	book := newLorebook(t, lib, "deeps")
	for _, c := range []int64{me, sharesBook} {
		if err := lib.LinkLorebook(ctx, c, book); err != nil {
			t.Fatal(err)
		}
	}
	coll, err := lib.CreateCollection(ctx, DefaultLibrary, "Dwarves", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []int64{me, inCollection} {
		if err := lib.AddToCollection(ctx, coll.ID, c); err != nil {
			t.Fatal(err)
		}
	}

	for group, want := range map[string][]int64{"creator": {byCreator}, "lorebook": {sharesBook}, "collection": {inCollection}} {
		got, err := lib.RelatedCards(ctx, me, group, Page{})
		if err != nil || !slices.Equal(ids(got.Items), want) || got.Group != group {
			t.Fatalf("%s = %+v, %v; want %v", group, got, err, want)
		}
	}
}

func TestRelatedIgnoresAnEmptyCreator(t *testing.T) {
	lib := newLibrary(t)
	me, _ := newCard(t, lib, "me", `{"name": "Brakka"}`)
	newCard(t, lib, "other", `{"name": "Dorna", "creator": ""}`)
	got, err := lib.RelatedCards(context.Background(), me, "creator", Page{})
	if err != nil || len(got.Items) != 0 {
		t.Fatalf("creator = %+v, %v", got, err)
	}
}

func TestRelatedPagesOneGroup(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	me, _ := newCard(t, lib, "me", `{"name": "Me", "creator": "dg"}`)
	var want []int64
	for i := range 3 {
		id, _ := newCard(t, lib, fmt.Sprintf("c%d", i), `{"name": "C", "creator": "dg"}`)
		want = append(want, id)
	}
	first, err := lib.RelatedCards(ctx, me, "creator", Page{Size: 2})
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil {
		t.Fatalf("first = %+v, %v", first, err)
	}
	second, err := lib.RelatedCards(ctx, me, "creator", Page{Size: 2, Cursor: first.NextCursor})
	if err != nil || second.NextCursor != nil || !slices.Equal(append(ids(first.Items), ids(second.Items)...), want) {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestRelatedRejectsAnUnknownGroup(t *testing.T) {
	lib := newLibrary(t)
	me, _ := newCard(t, lib, "me", `{"name": "Me"}`)
	if _, err := lib.RelatedCards(context.Background(), me, "colour", Page{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
```

Add `"errors"` to the imports.

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./internal/library -run TestRelated
```

Expected: a compile error, `lib.RelatedCards undefined`.

- [ ] **Step 3: Implement**

Append to `internal/library/discover.go`:

```go
// RelatedGroups are the ways a card links to others, in display order.
var RelatedGroups = []string{"creator", "lorebook", "collection"}

// relatedWhere is each group's condition on c, a candidate, against self
// and $1. Collections are per-library, so that group never leaves the card's.
var relatedWhere = map[string]string{
	"creator": `self.creator <> '' AND lower(btrim(r.data->>'creator')) = self.creator`,
	"lorebook": `c.id IN (SELECT o.card_id FROM card_lorebook o
		JOIN card_lorebook mine ON mine.lorebook_id = o.lorebook_id WHERE mine.card_id = $1)`,
	"collection": `c.id IN (SELECT o.card_id FROM collection_card o
		JOIN collection_card mine ON mine.collection_id = o.collection_id WHERE mine.card_id = $1)`,
}

// RelatedPage is one page of one group of related cards.
type RelatedPage struct {
	Group      string           `json:"group" enum:"creator,lorebook,collection"`
	Items      []DiscoveredCard `json:"items"`
	NextCursor *string          `json:"next_cursor,omitempty"`
}

// RelatedCards returns one page of the cards linked to cardID by group,
// lowest ID first, leaving out its variants and archived cards.
func (l *Library) RelatedCards(ctx context.Context, cardID int64, group string, p Page) (RelatedPage, error) {
	cond, ok := relatedWhere[group]
	if !ok {
		return RelatedPage{}, fmt.Errorf("%w: group must be one of %v", ErrInvalid, RelatedGroups)
	}
	limit, offset, err := p.window()
	if err != nil {
		return RelatedPage{}, err
	}
	// ponytail: the creator group scans every head; add an expression index if it shows up slow.
	sql := discoverWith + `
		SELECT c.id, c.library_id, coalesce(r.data->>'name', ''), 0::float8
		FROM self CROSS JOIN card c
		JOIN card_revision r ON r.id = c.head_revision_id
		WHERE ` + discoverWhere + ` AND ` + cond + `
		ORDER BY c.id
		LIMIT $2 OFFSET $3`
	items, next, err := l.discoverPage(ctx, sql, limit, offset, cardID, limit+1, offset)
	if err != nil {
		return RelatedPage{}, fmt.Errorf("%s related to card %d: %w", group, cardID, err)
	}
	return RelatedPage{Group: group, Items: items, NextCursor: next}, nil
}
```

Keep `RelatedGroups` and `relatedWhere`'s keys in step: a test that every
name in `RelatedGroups` has a condition is cheap, add it if you like.

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/library -run 'TestRelated|TestSimilar' -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/discover.go internal/library/discover_test.go
git commit -m "feat(library): list cards related by creator, lorebook, or collection"
```

---

### Task 3: The API routes

**Files:** create `internal/api/discover.go`, `internal/api/discover_test.go`;
modify `internal/api/server.go` (register next to `s.cardRoutes(api)`)

`embed.Instance(ctx, s.Registry)` (internal/embed) returns the usable
embed instance's ID, or 0. Check how `cardRoutes` checks the card exists
(`s.Lib.Cards.Get` then `s.fail`) and copy that for a 404.

- [ ] **Step 1: Write the failing tests**

`internal/api/discover_test.go`, using `newEnv`, `e.createCard(data)`, and
`e.call(method, path, body, status, out)` from api_test.go:

```go
package api

import (
	"fmt"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestSimilarRouteFallsBackToTags(t *testing.T) {
	e := newEnv(t)
	me := e.createCard(`{"name": "Brakka", "tags": ["dwarf", "smith"]}`)
	other := e.createCard(`{"name": "Dorna", "tags": ["dwarf", "smith"]}`)
	var got library.SimilarPage
	e.call("GET", fmt.Sprintf("/cards/%d/similar?scope=all", me.ID), nil, 200, &got)
	if got.Basis != "tags" || len(got.Items) != 1 || got.Items[0].CardID != other.ID || got.Items[0].Name != "Dorna" {
		t.Fatalf("similar = %+v", got)
	}
	e.call("GET", "/cards/999999/similar", nil, 404, nil)
	e.call("GET", fmt.Sprintf("/cards/%d/similar?scope=galaxy", me.ID), nil, 422, nil)
}

func TestRelatedRouteListsNonEmptyGroups(t *testing.T) {
	e := newEnv(t)
	me := e.createCard(`{"name": "Brakka", "creator": "dg"}`)
	other := e.createCard(`{"name": "Dorna", "creator": "dg"}`)
	var all struct{ Groups []library.RelatedPage }
	e.call("GET", fmt.Sprintf("/cards/%d/related", me.ID), nil, 200, &all)
	if len(all.Groups) != 1 || all.Groups[0].Group != "creator" || all.Groups[0].Items[0].CardID != other.ID {
		t.Fatalf("related = %+v", all)
	}
	var one struct{ Groups []library.RelatedPage }
	e.call("GET", fmt.Sprintf("/cards/%d/related?group=lorebook", me.ID), nil, 200, &one)
	if len(one.Groups) != 1 || one.Groups[0].Group != "lorebook" || len(one.Groups[0].Items) != 0 {
		t.Fatalf("one group = %+v", one)
	}
}
```

Check the status huma returns for a bad enum (422 is huma's default for
validation errors; confirm with an existing test such as one sending a bad
`sort`). Check `createCard`'s return type and field names in api_test.go.

- [ ] **Step 2: Run them and see them fail**

```bash
go test ./internal/api -run 'TestSimilarRoute|TestRelatedRoute'
```

Expected: FAIL (404 for the routes).

- [ ] **Step 3: Implement**

`internal/api/discover.go`:

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/embed"
	"github.com/inkandale/innkeeper/internal/library"
)

type discoverPageIn struct {
	ID     int64  `path:"id"`
	Cursor string `query:"cursor" doc:"next_cursor from the previous page."`
	Limit  int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
}

func (in discoverPageIn) page() library.Page {
	p := library.Page{Size: in.Limit}
	if in.Cursor != "" {
		p.Cursor = &in.Cursor
	}
	return p
}

type similarCardsIn struct {
	discoverPageIn
	Scope string `query:"scope" enum:"library,all" default:"library" doc:"library looks in the card's library; all, in every library."`
}

type similarCardsOut struct{ Body library.SimilarPage }

type relatedCardsIn struct {
	discoverPageIn
	Group string `query:"group" enum:"creator,lorebook,collection" doc:"One group to page through; without it, the first page of every group with results."`
}

type relatedCardsOut struct {
	Body struct {
		Groups []library.RelatedPage `json:"groups"`
	}
}

func (s *Server) discoverRoutes(api huma.API) {
	tags := []string{"Cards"}
	huma.Register(api, huma.Operation{OperationID: "list-similar-cards", Method: http.MethodGet, Path: "/cards/{id}/similar",
		Summary: "Find similar cards", Tags: tags,
		Description: "Cards that read like this one, closest first: by meaning when an embed instance is set, else by shared tags. Leaves out exact copies, this card's variants, and archived cards."},
		func(ctx context.Context, in *similarCardsIn) (*similarCardsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			inst, err := embed.Instance(ctx, s.Registry)
			if err != nil {
				return nil, s.fail(err)
			}
			res, err := s.Lib.SimilarCards(ctx, in.ID, library.SimilarOptions{Instance: inst, AllLibraries: in.Scope == "all", Page: in.page()})
			if err != nil {
				return nil, s.fail(err)
			}
			return &similarCardsOut{Body: res}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-related-cards", Method: http.MethodGet, Path: "/cards/{id}/related",
		Summary: "Find related cards", Tags: tags,
		Description: "Cards linked to this one: by the same creator, a shared lorebook, or a shared collection. Leaves out this card's variants and archived cards."},
		func(ctx context.Context, in *relatedCardsIn) (*relatedCardsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			out := &relatedCardsOut{}
			out.Body.Groups = []library.RelatedPage{}
			groups := library.RelatedGroups
			if in.Group != "" {
				groups = []string{in.Group}
			}
			for _, g := range groups {
				res, err := s.Lib.RelatedCards(ctx, in.ID, g, in.page())
				if err != nil {
					return nil, s.fail(err)
				}
				if in.Group != "" || len(res.Items) > 0 {
					out.Body.Groups = append(out.Body.Groups, res)
				}
			}
			return out, nil
		})
}
```

Make `Items` serialize as `[]`, not `null`, when empty: either initialize
it in the library (`items = []DiscoveredCard{}` when nil in
`discoverPage`) or use the repo's existing `orEmpty` helper. Prefer the
library, so MCP and API agree.

Register with `s.discoverRoutes(api)` beside `s.cardRoutes(api)` in
server.go. Check that `list-similar-cards` isn't still taken (slice 1
renamed the old one to `list-duplicate-cards`).

- [ ] **Step 4: Run, then regenerate**

```bash
go test ./internal/api -run 'TestSimilarRoute|TestRelatedRoute' && (cd web && npm run gen)
```

Expected: PASS, and schema.d.ts gains both routes.

- [ ] **Step 5: Commit**

```bash
git add internal/api/discover.go internal/api/discover_test.go internal/api/server.go internal/library/discover.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): serve similar and related cards"
```

---

### Task 4: MCP tools

**Files:** `internal/mcp/tools.go`, `internal/api/mcp_test.go`

Tools are thin wrappers over the API: `def(name, description, tier, run)`
with `c.call(ctx, method, path, body)`. Inputs use `json` and `jsonschema`
tags (see `searchIn` in tools.go).

- [ ] **Step 1: Write the failing test**

Append to `internal/api/mcp_test.go`, following `TestMCPCards`:

```go
func TestMCPFindsSimilarAndRelatedCards(t *testing.T) {
	e := newEnv(t)
	s := e.mcpClient(tier.Read)
	me := e.createCard(`{"name": "Brakka", "creator": "dg", "tags": ["dwarf", "smith"]}`)
	e.createCard(`{"name": "Dorna", "creator": "dg", "tags": ["dwarf", "smith"]}`)

	if got := tool(t, s, "find_similar_cards", map[string]any{"card_id": me.ID}, false); !strings.Contains(got, "Dorna") || !strings.Contains(got, `"basis":"tags"`) {
		t.Fatalf("find_similar_cards = %s", got)
	}
	if got := tool(t, s, "find_related_cards", map[string]any{"card_id": me.ID, "group": "creator"}, false); !strings.Contains(got, "Dorna") {
		t.Fatalf("find_related_cards = %s", got)
	}
}
```

- [ ] **Step 2: Run it and see it fail**

```bash
go test ./internal/api -run TestMCPFindsSimilarAndRelatedCards
```

Expected: FAIL, unknown tool.

- [ ] **Step 3: Implement**

In `internal/mcp/tools.go`, add input types near `searchIn`:

```go
type similarIn struct {
	CardID int64  `json:"card_id"`
	Scope  string `json:"scope,omitempty" jsonschema:"library (default) to look in the card's library, or all for every library"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page"`
}

type relatedIn struct {
	CardID int64  `json:"card_id"`
	Group  string `json:"group,omitempty" jsonschema:"creator, lorebook, or collection to page through one group; omit for the first page of each"`
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; needs group"`
}
```

and to `tools`, after `search_cards`:

```go
	def("find_similar_cards", "List cards that read like a card, closest first: by meaning when an embed instance is set, else by shared tags.", tier.Read,
		func(ctx context.Context, c caller, in similarIn) (*sdk.CallToolResult, error) {
			q := url.Values{}
			if in.Scope != "" {
				q.Set("scope", in.Scope)
			}
			if in.Cursor != "" {
				q.Set("cursor", in.Cursor)
			}
			return c.call(ctx, "GET", fmt.Sprintf("/cards/%d/similar?%s", in.CardID, q.Encode()), nil)
		}),
	def("find_related_cards", "List cards linked to a card by the same creator, a shared lorebook, or a shared collection.", tier.Read,
		func(ctx context.Context, c caller, in relatedIn) (*sdk.CallToolResult, error) {
			q := url.Values{}
			if in.Group != "" {
				q.Set("group", in.Group)
			}
			if in.Cursor != "" {
				q.Set("cursor", in.Cursor)
			}
			return c.call(ctx, "GET", fmt.Sprintf("/cards/%d/related?%s", in.CardID, q.Encode()), nil)
		}),
```

If the repo lists MCP tools anywhere else (README, a test that counts or
names every tool), update it: `grep -rn "search_cards" --include='*.go' --include='*.md' .`

- [ ] **Step 4: Run the MCP tests**

```bash
go test ./internal/api -run TestMCP
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/api/mcp_test.go
git commit -m "feat(mcp): find similar and related cards"
```

(Add any README or listing file you updated.)

---

### Task 5: The card page sections

**Files:** create `web/src/card/Discover.tsx`, `web/src/card/Discover.test.tsx`;
modify `web/src/card/Panel.tsx`

The side panel (`Panel.tsx`) is a column of short `<section>`s with an
`<h2>`, such as `OtherLibraries`. Copy its look: a `ul.plain` of links,
"in <library>" in `span.muted` only for cards in another library.
`librariesQuery` (api/queries) lists libraries. `api.GET` is the typed
client; check the generated operation for `/cards/{id}/similar` after Task
3.

- [ ] **Step 1: Write the failing tests**

`web/src/card/Discover.test.tsx`. Copy the `json` helper, fetch stub, and
router/QueryClient render from `web/src/settings/EmbedFrom.test.tsx` and
`Card.test.tsx` (links need a router: wrap in `createMemoryRouter` with one
route rendering the component). Cases:

1. Similar: fetch of `/api/cards/7/similar` returns
   `{basis: "embedding", items: [{card_id: 8, library_id: 1, name: "Dorna", score: 0.9}], next_cursor: "1"}`;
   the second call (with `cursor=1`) returns one more item and no cursor.
   Assert "Dorna" links to `/cards/8`, click "Show more", assert the
   second item appears and "Show more" is gone.
2. Similar with `basis: "tags"` shows the text "Ranked by shared tags" with
   a link to `/settings/plugins`.
3. Similar toggling "All libraries" refetches with `scope=all`, and an
   item with `library_id: 2` shows "in Archive" (mock `/api/libraries` →
   `[{id: 1, name: "Main"}, {id: 2, name: "Archive"}]`; check the real
   libraries response shape in schema.d.ts).
4. Similar with no items renders nothing.
5. Related: `/api/cards/7/related` returns one group `creator` with one
   item and `next_cursor: "1"`; assert the heading "Same creator", the
   link, and that "Show more" fetches `/api/cards/7/related?group=creator&cursor=1`.

- [ ] **Step 2: Run them and see them fail**

```bash
cd web && npx vitest run src/card/Discover.test.tsx
```

Expected: FAIL, module not found.

- [ ] **Step 3: Implement**

`web/src/card/Discover.tsx`:

```tsx
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, must } from "../api/client";
import { librariesQuery } from "../api/queries";
import type { components } from "../api/schema";

type Found = components["schemas"]["DiscoveredCard"];

const groupNames: Record<string, string> = {
  creator: "Same creator",
  lorebook: "Shares a lorebook",
  collection: "Shares a collection",
};

/** CardList links each found card, naming its library when it isn't this card's. */
function CardList({ items, library }: { items: Found[]; library: number }) {
  const libraries = useQuery(librariesQuery);
  const libraryName = (n: number) => libraries.data?.find((l) => l.id === n)?.name ?? `Library ${n}`;
  return (
    <ul className="plain">
      {items.map((c) => (
        <li key={c.card_id}>
          <Link to={`/cards/${c.card_id}`}>{c.name || `#${c.card_id}`}</Link>
          {c.library_id !== library && <span className="muted"> in {libraryName(c.library_id)}</span>}
        </li>
      ))}
    </ul>
  );
}

/** Similar lists the cards that read like this one, in its library or all of them. */
export function Similar({ id, library }: { id: number; library: number }) {
  const [scope, setScope] = useState<"library" | "all">("library");
  const pages = useInfiniteQuery({
    queryKey: ["card", id, "similar", scope],
    queryFn: async ({ pageParam }) =>
      must(await api.GET("/cards/{id}/similar", { params: { path: { id }, query: { scope, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
  });
  const items = pages.data?.pages.flatMap((p) => p.items) ?? [];
  const basis = pages.data?.pages[0]?.basis;
  if (!pages.isError && !items.length && scope === "library") return null;
  return (
    <section>
      <h2>Similar</h2>
      <label>
        <input type="checkbox" checked={scope === "all"} onChange={(e) => setScope(e.target.checked ? "all" : "library")} /> All
        libraries
      </label>
      {pages.isError && <p className="error">{pages.error.message}</p>}
      {basis === "tags" && (
        <p className="muted">
          Ranked by shared tags. <Link to="/settings/plugins">Pick an embed instance</Link> to rank by meaning.
        </p>
      )}
      <CardList items={items} library={library} />
      {pages.hasNextPage && (
        <button className="link" disabled={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>
          Show more
        </button>
      )}
    </section>
  );
}

/** RelatedGroup pages through one group of related cards, starting from the page the overview fetched. */
function RelatedGroup({ id, library, first }: { id: number; library: number; first: components["schemas"]["RelatedPage"] }) {
  const more = useInfiniteQuery({
    queryKey: ["card", id, "related", first.group],
    queryFn: async ({ pageParam }) =>
      must(await api.GET("/cards/{id}/related", { params: { path: { id }, query: { group: first.group, cursor: pageParam } } }))
        .groups[0]!,
    initialPageParam: first.next_cursor ?? undefined,
    getNextPageParam: (last) => last.next_cursor ?? undefined,
    enabled: false,
  });
  const items = [...first.items, ...(more.data?.pages.flatMap((p) => p.items) ?? [])];
  const hasMore = more.data ? more.hasNextPage : !!first.next_cursor;
  return (
    <>
      <h3>{groupNames[first.group] ?? first.group}</h3>
      <CardList items={items} library={library} />
      {hasMore && (
        <button
          className="link"
          disabled={more.isFetching}
          onClick={() => void (more.data ? more.fetchNextPage() : more.refetch())}
        >
          Show more
        </button>
      )}
    </>
  );
}

/** Related lists the cards linked to this one by creator, lorebook, or collection. */
export function Related({ id, library }: { id: number; library: number }) {
  const related = useQuery({
    queryKey: ["card", id, "related"],
    queryFn: async () => must(await api.GET("/cards/{id}/related", { params: { path: { id } } })),
  });
  const groups = related.data?.groups ?? [];
  if (!related.isError && !groups.length) return null;
  return (
    <section>
      <h2>Related</h2>
      {related.isError && <p className="error">{related.error.message}</p>}
      {groups.map((g) => (
        <RelatedGroup key={g.group} id={id} library={library} first={g} />
      ))}
    </section>
  );
}
```

The `RelatedGroup` paging above is the fiddliest part. If `enabled:
false` with `refetch` for the first extra page doesn't behave (TanStack
v5 refetch on a disabled infinite query fetches `initialPageParam`), keep
the behavior the test asserts and simplify: a `useState` list of extra
pages and a plain `useMutation`/async click handler that fetches the next
cursor and appends. Choose whichever is shorter and passes.

Check the generated schema names (`DiscoveredCard`, `RelatedPage`,
`SimilarPage`) in schema.d.ts and the exact import path of the schema
types used elsewhere (`grep -rn 'components\["schemas"\]' web/src | head`).
Use an existing button style for "Show more" (`grep -rn "Show more\|className=\"link\"" web/src`).

In `web/src/card/Panel.tsx`, render after `<OtherLibraries kind="card" id={card.id} />`:

```tsx
      <Similar id={card.id} library={card.library_id} />
      <Related id={card.id} library={card.library_id} />
```

`Card.test.tsx` mocks fetch for the whole card page. If its fallback for
unknown paths breaks on the new requests, add `/similar` and `/related`
responses there (`{basis: "tags", items: []}`, `{groups: []}`).

- [ ] **Step 4: Run the web checks**

```bash
cd web && npm run typecheck && npx vitest run src/card
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/card/Discover.tsx web/src/card/Discover.test.tsx web/src/card/Panel.tsx web/src/card/Card.test.tsx
git commit -m "feat(web): show similar and related cards on the card page"
```

---

### Task 6: Verify

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: every package `ok`, none skipped for want of a database; web
checks pass.

- [ ] **Step 2: Try it on real data**

Start nothing in production. With the local stack (`compose.yaml`, API on
a port other than 18080), create three cards sharing tags, open one card
page, and confirm the Similar section lists the others with "Ranked by
shared tags". Report what you saw.

- [ ] **Step 3: Read the whole diff**

```bash
git diff origin/develop --stat && git diff origin/develop
```

Look for dead code, names that disagree between Go, API, and UI, and
anything this plan didn't ask for.
