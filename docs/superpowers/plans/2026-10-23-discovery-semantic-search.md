# Semantic Search Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Card search ranks by meaning when `semantic` is set: core embeds the query with the embed instance and orders cards by cosine similarity to it.

**Architecture:** The API resolves the embed instance and embeds `q.Text` once per request, as it resolves the score instance for enrichment queries, and passes both to `library.Search` on `json:"-"` fields of `Query`. `compile` limits matches to cards with a vector from that instance, and `Search` adds a `similarity` sort key to the existing order and cursor code, so filters, family grouping, and paging work unchanged. The web Cards page gets a Semantic checkbox, shown only when an embed instance is set.

**Tech Stack:** Go, pgvector (`<=>` cosine distance), Huma, React with TanStack Query, Vitest.

Spec: `docs/superpowers/specs/2026-10-23-discovery-design.md`, section "Semantic search".

**Rules for every task** (from AGENTS.md):
- Database tests need `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
- After a Go API change, run `cd web && npm run gen`. Never hand-edit `web/openapi.json` or `web/src/api/schema.d.ts`.
- Comments say why, in one line.
- Don't checkout or move HEAD off branch `discovery-semantic`.

---

## Behavior decisions

| Case | Result |
|---|---|
| `semantic` with no embed instance | 409, `library.ErrNoEmbedInstance`, naming the setting |
| `semantic` without `text` | 400 |
| `semantic` on a lorebook search | 400 |
| `semantic` with a sort other than `relevance` | 400; semantic results sort by similarity only |
| `semantic` in a bulk selection (`SearchIDs`, `CountMatches`) | 400. These never embed the query. The web drops `semantic` and `text` from an all-matching selection instead. |
| Card without a vector from the embed instance, or with a vector of another length | Not a match |
| Facet counts on the Cards page while semantic | Counted without `text`, so the page doesn't embed the query once per facet |

---

### Task 1: Semantic ranking in `library.Search`

**Files:**
- Modify: `internal/library/search.go` (the `Query` struct, `compile`, and the sort switch in `Search`)
- Modify: `internal/library/library.go` (add the sentinel next to `ErrNotFound`)
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing tests**

In `internal/library/search_test.go`, replace the `"semantic": {Semantic: true},` row of `TestSearchRejectsBadQueries` with these rows:

```go
		"semantic without text":  {Semantic: true, Embedding: []float32{1, 0}, EmbedInstance: 1},
		"semantic without vector": {Semantic: true, Text: "smith"},
		"semantic by name":       {Semantic: true, Text: "smith", Embedding: []float32{1, 0}, EmbedInstance: 1, Sort: "name"},
```

Add these tests to the end of the file. `newInstanceRow` lives in `embeddings_test.go`, `embedCards` in `discover_test.go`, and `newCard` and `hitIDs` are existing helpers in this package.

```go
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
```

The root ranks first in the family test because its variant is the best match, and a family hit is the family's face (its root).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run 'TestSemanticSearch|TestSearchRejectsBadQueries'`
Expected: a compile failure, since `Query` has no `Embedding` or `EmbedInstance` fields.

- [ ] **Step 3: Add the sentinel**

In `internal/library/library.go`, add this to the `var (...)` block holding `ErrNotFound`:

```go
	// ErrNoEmbedInstance means embed_instance is unset, or names an instance that is gone or disabled.
	ErrNoEmbedInstance = errors.New("semantic search needs an embed instance; pick one in Settings under Plugins, Embeddings from")
```

- [ ] **Step 4: Add the `Query` fields and update the `Semantic` doc**

In `internal/library/search.go`, replace the `Semantic` field with:

```go
	Semantic bool     `json:"semantic,omitempty" doc:"Rank cards by meaning instead of matching words. Needs text and an embed instance (Settings, Plugins, Embeddings from); sorts by relevance only. Cards not embedded yet are left out."`
```

Below `ScoreInstance`, add:

```go
	// Embedding is Text's vector from EmbedInstance, for a semantic search.
	// The caller embeds it.
	Embedding     []float32 `json:"-"`
	EmbedInstance int64     `json:"-"`
```

- [ ] **Step 5: Match only embedded cards in `compile`**

Replace the `if q.Semantic { ... }` guard at the top of `compile` with nothing, and replace the text block at the end of `compile`:

```go
	text := ""
	if q.Text != "" {
		text = w.arg(q.Text)
		conds = append(conds, "r.search @@ websearch_to_tsquery('english', "+text+")")
	}
	return strings.Join(conds, " AND "), w, text, nil
```

with:

```go
	text := ""
	switch {
	case q.Semantic:
		s, err := w.semantic(q)
		if err != nil {
			return "", nil, "", err
		}
		conds = append(conds, s)
	case q.Text != "":
		text = w.arg(q.Text)
		conds = append(conds, "r.search @@ websearch_to_tsquery('english', "+text+")")
	}
	return strings.Join(conds, " AND "), w, text, nil
```

Add these after `compile`:

```go
// semantic is the condition that a card has a vector from q's embed
// instance that compares with q's, which has to be the same length.
func (w *where) semantic(q Query) (string, error) {
	switch {
	case w.k != Card:
		return "", fmt.Errorf("%w: only cards have semantic search", ErrInvalid)
	case q.Text == "":
		return "", fmt.Errorf("%w: semantic search needs text", ErrInvalid)
	case q.Embedding == nil:
		// Only Search embeds the query; bulk selections never do.
		return "", fmt.Errorf("%w: semantic search lists cards but can't select them for an action", ErrInvalid)
	}
	return `EXISTS (SELECT 1 FROM card_embedding e WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.EmbedInstance) +
		` AND vector_dims(e.embedding) = ` + w.arg(len(q.Embedding)) + `)`, nil
}

// similarity is a card's cosine similarity to q's vector, as a real so a
// cursor holds it exactly. compile's semantic condition keeps it non-null.
func (w *where) similarity(q Query) string {
	return `(SELECT (1 - (e.embedding <=> ` + w.arg(vectorText(q.Embedding)) + `::vector))::real
		FROM card_embedding e WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.EmbedInstance) + `)`
}
```

- [ ] **Step 6: Sort by similarity in `Search`**

In `Search`, replace:

```go
	o := order{desc: true}
	switch q.Sort {
	case "", "relevance":
		if text != "" {
			o.keys = []sortKey{{"ts_rank(r.search, websearch_to_tsquery('english', " + text + "))", "real"}}
		}
```

with:

```go
	o := order{desc: true}
	if q.Semantic && q.Sort != "" && q.Sort != "relevance" {
		return Results{}, fmt.Errorf("%w: semantic search sorts by relevance only", ErrInvalid)
	}
	switch q.Sort {
	case "", "relevance":
		switch {
		case q.Semantic:
			o.keys = []sortKey{{w.similarity(q), "real"}}
		case text != "":
			o.keys = []sortKey{{"ts_rank(r.search, websearch_to_tsquery('english', " + text + "))", "real"}}
		}
```

Update the `Search` doc comment's first sentence to: `best match first when q has text, by meaning when q is semantic, newest first otherwise.`

- [ ] **Step 7: Run the tests to verify they pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library`
Expected: PASS for the whole package.

- [ ] **Step 8: Commit**

```bash
git add internal/library/search.go internal/library/library.go internal/library/search_test.go
git commit -m "feat(library): rank card search by meaning when semantic"
```

---

### Task 2: Embed the query in the API, plus MCP

**Files:**
- Modify: `internal/api/search.go` (`Server.search`)
- Modify: `internal/api/server.go` (`failAs`)
- Modify: `internal/mcp/tools.go` (the `searchIn.Query` description)
- Test: `internal/api/search_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing tests**

Add to `internal/api/search_test.go`. `e.addBrain` is in `settings_test.go`. The fake plugin's `ai.embed` returns `[len(text), 1]` per text, so the query `"abc"` embeds as `[3, 1]`.

```go
func TestSemanticSearchRanksByMeaning(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	inst := e.addBrain(map[string]any{"services": []string{protocol.MethodAIEmbed}})
	e.call("PATCH", "/settings", map[string]any{"embed_instance": inst}, 200, nil)
	var near, far, bare entityResult
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "Brakka"}`)}, 201, &near)
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "Lia"}`)}, 201, &far)
	e.call("POST", "/cards", map[string]any{"data": json.RawMessage(`{"name": "Grum"}`)}, 201, &bare)
	for id, v := range map[int64][]float32{near.ID: {3, 1}, far.ID: {1, 3}} {
		c, err := e.s.Lib.Cards.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.s.Lib.SaveEmbeddings(ctx, inst, []library.EmbedText{{CardID: id, RevisionID: c.HeadRevisionID}}, [][]float32{v}); err != nil {
			t.Fatal(err)
		}
	}

	var res library.Results
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true}, 200, &res)
	if len(res.Hits) != 2 || res.Hits[0].ID != near.ID || res.Hits[1].ID != far.ID {
		t.Fatalf("hits = %+v", res.Hits)
	}
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true, "sort": "name"}, 400, nil)
	e.call("POST", "/lorebooks/search", map[string]any{"text": "abc", "semantic": true}, 400, nil)
}

func TestSemanticSearchWithoutEmbedInstanceIs409(t *testing.T) {
	e := newEnv(t)
	e.call("POST", "/cards/search", map[string]any{"text": "abc", "semantic": true}, 409, nil)
}
```

Check that the names used here (`entityResult`, `newEnv`, `e.s.Lib`, and the `/cards` create body) match what the file's other tests use, and fix any that differ. The existing line `e.call("POST", "/search", map[string]any{"semantic": true}, 400, nil)` stays: no text is still a 400.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run TestSemanticSearch`
Expected: FAIL. The first test gets 400 because the query has no vector, and the second gets 400 instead of 409.

- [ ] **Step 3: Embed the query**

In `internal/api/search.go`, add after `Server.search`:

```go
// withEmbedding embeds q's text with the embed instance when q is a
// semantic card search, for Search to rank by.
func (s *Server) withEmbedding(ctx context.Context, k *library.Kind, q *library.Query) error {
	if !q.Semantic || q.Text == "" || k != library.Card {
		return nil
	}
	inst, err := embed.Instance(ctx, s.Registry)
	if err != nil {
		return err
	}
	if inst == 0 {
		return library.ErrNoEmbedInstance
	}
	vectors, err := s.Registry.Embed(ctx, s.Plugins, inst, []string{q.Text})
	if err != nil {
		return err
	}
	q.Embedding, q.EmbedInstance = vectors[0], inst
	return nil
}
```

In `Server.search`, call it right after `withScores`:

```go
		decl, err := s.withScores(ctx, &q)
		if err != nil {
			return nil, s.fail(err)
		}
		if err := s.withEmbedding(ctx, k, &q); err != nil {
			return nil, s.fail(err)
		}
```

Add the `"github.com/inkandale/innkeeper/internal/embed"` import. The early return leaves a missing text or a lorebook search to `library.Search`, which refuses both with a 400.

- [ ] **Step 4: Map the sentinel to 409**

In `internal/api/server.go`, `failAs`, add `errors.Is(err, library.ErrNoEmbedInstance),` to the case that returns `huma.Error409Conflict(err.Error())`, after `errors.Is(err, library.ErrVariantCycle),`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api`
Expected: PASS.

- [ ] **Step 6: Tell MCP clients about `semantic`**

In `internal/mcp/tools.go`, in the `searchIn.Query` jsonschema description, change `"The search query, as the API's POST /cards/search body: text, filter, ` to `"The search query, as the API's POST /cards/search body: text, semantic (true to rank by meaning, with text; needs an embed instance), filter, `. Leave the rest of the string as it is.

- [ ] **Step 7: Regenerate the schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` change only in the `semantic` description; typecheck passes.

- [ ] **Step 8: Commit**

```bash
git add internal/api/search.go internal/api/server.go internal/api/search_test.go internal/mcp/tools.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): embed the query for a semantic card search"
```

---

### Task 3: Semantic toggle on the Cards page

**Files:**
- Modify: `web/src/cards/query.ts` (`CardQuery`, `parseQuery`, `toParams`, `toSearch`, `countSearch`, `valueCountSearch`, `stateCountSearch`)
- Modify: `web/src/cards/selection.ts` (`target`)
- Modify: `web/src/cards/Cards.tsx` (checkbox, selection key, sort select)
- Test: `web/src/cards/query.test.ts`, `web/src/cards/selection.test.ts`, `web/src/cards/Cards.test.tsx`

- [ ] **Step 1: Write the failing unit tests**

Add to `web/src/cards/query.test.ts`. Use the file's existing import of `parseQuery`, `toParams`, `toSearch`, and `countSearch`, and add any that are missing.

```ts
test("semantic round-trips through the URL and searches by relevance", () => {
  const q = parseQuery(new URLSearchParams("q=grumpy+dwarf&semantic=1&sort=name"), 1);
  expect(q.semantic).toBe(true);
  expect(toParams(q).get("semantic")).toBe("1");
  expect(toSearch(q)).toMatchObject({ text: "grumpy dwarf", semantic: true, sort: "relevance" });
  expect(parseQuery(new URLSearchParams("q=x"), 1).semantic).toBe(false);
});

test("facet counts leave the text out of a semantic search", () => {
  const q = parseQuery(new URLSearchParams("q=grumpy+dwarf&semantic=1"), 1);
  expect(countSearch(q, "tag").text).toBeUndefined();
  expect(countSearch({ ...q, semantic: false }, "tag").text).toBe("grumpy dwarf");
});
```

Add to `web/src/cards/selection.test.ts`:

```ts
test("an all-matching semantic selection keeps the filter but not the text", () => {
  const all = { ...emptySelection("k"), all: true };
  expect(target(all, { library: 1, text: "dwarf", semantic: true, filter: { tag: "smith" } })).toEqual({ library: 1, filter: { tag: "smith" } });
  expect(target(all, { library: 1, text: "dwarf", semantic: true })).toEqual({ library: 1, filter: { archived: false } });
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/cards/query.test.ts src/cards/selection.test.ts`
Expected: FAIL. `semantic` is undefined on `CardQuery`, and `target` keeps the text.

- [ ] **Step 3: Carry `semantic` through the query**

In `web/src/cards/query.ts`:

Add to `CardQuery`, after `text`:

```ts
  /** semantic ranks by meaning instead of matching words. It needs text and an embed instance. */
  semantic: boolean;
```

In `parseQuery`'s returned object, after `text,`:

```ts
    semantic: p.get("semantic") === "1",
```

In `toParams`, after the `q` line:

```ts
  if (q.semantic) p.set("semantic", "1");
```

In `toSearch`, replace the `text` and `sort` lines with:

```ts
    text: q.text || undefined,
    ...(q.semantic && { semantic: true }),
    // Semantic search sorts by similarity alone.
    sort: q.semantic ? "relevance" : q.sort,
```

Add this helper above `countSearch`:

```ts
/** countText is the text facet counts search by: none for a semantic search, which would embed it once per count. */
const countText = (q: CardQuery) => (q.semantic ? undefined : q.text || undefined);
```

In `countSearch`, `valueCountSearch`, and `stateCountSearch`, replace `text: q.text || undefined,` with `text: countText(q),`.

- [ ] **Step 4: Drop the text from an all-matching semantic selection**

In `web/src/cards/selection.ts`, replace the body of `target` after `if (!s.all) ...` with:

```ts
  // Every card with a vector matches a semantic search, so its text selects nothing; the filter does.
  const text = search.semantic ? undefined : search.text;
  if (text || search.filter) return { library, ...(text && { text }), filter: search.filter };
  // /bulk refuses a query with no filter or text, so name every unarchived card.
  return { library, filter: { archived: false } };
```

Then make sure the existing non-semantic `target` tests still pass. If one compares with `toEqual` and expects `filter: undefined` or a `text` key, keep the old object shape for the non-semantic case: `{ library, text: search.text, filter: search.filter }`.

- [ ] **Step 5: Run the unit tests to verify they pass**

Run: `cd web && npx vitest run src/cards/query.test.ts src/cards/selection.test.ts`
Expected: PASS.

- [ ] **Step 6: Write the failing page test**

In `web/src/cards/Cards.test.tsx`, follow how the file's existing tests render `Cards` and mock `/api/...` routes. Add one test that:

1. Answers `GET /api/settings` with `{ theme: "", onboarding_done: true, embed_instance: 5 }` and records each `POST /api/cards/search` body.
2. Renders the Cards page at `/cards?q=dwarf&library=1`.
3. Clicks the checkbox labelled `Semantic`.
4. Expects a recorded search body with `semantic: true`, `text: "dwarf"`, and `sort: "relevance"`.

Add a second test: with `embed_instance: null`, `screen.queryByRole("checkbox", { name: "Semantic" })` is null.

- [ ] **Step 7: Run it to verify it fails**

Run: `cd web && npx vitest run src/cards/Cards.test.tsx`
Expected: FAIL; there is no Semantic checkbox.

- [ ] **Step 8: Add the checkbox**

In `web/src/cards/Cards.tsx`:

1. Import `settingsQuery` from `../api/queries` if it isn't imported yet. It's the same query `EmbedFrom.tsx` reads.
2. After `const asked = parseQuery(params, useLibrary());`, add:

```tsx
  const settings = useQuery(settingsQuery);
  // A semantic link opened after the embed instance was cleared searches by words.
  const embeds = kind === "card" && settings.data?.embed_instance != null;
```

   Then change the `q` line to drop `semantic` when it isn't available:

```tsx
  const scoredQ = keys.data ? withScoreInstance(asked, keys.data.instance_id) : asked;
  const q = scoredQ.semantic && !embeds ? { ...scoredQ, semantic: false } : scoredQ;
```

   Check that `scoresOff` (`!scored && q !== asked`) still means what it did. If it compares `q` with `asked`, compare `scoredQ` instead.
3. Change the selection key to `JSON.stringify([q.library, q.text, q.semantic, q.terms])`.
4. Give the sort `<select>` `disabled={q.semantic}` and show `relevance` while semantic: change the option filter to `s !== "relevance" || q.text || q.sort === "relevance" || q.semantic`, and the select's `value` to `q.semantic ? "relevance" : q.sort`.
5. After the search `<form>`, add:

```tsx
          {embeds && (
            <label title="Rank cards by meaning instead of matching words. Cards not embedded yet are left out.">
              <input type="checkbox" checked={q.semantic} onChange={(e) => go({ ...q, semantic: e.target.checked, page: 1 })} />
              Semantic
            </label>
          )}
```

- [ ] **Step 9: Run the web suite and typecheck**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS. If another test builds a `CardQuery` literal and now fails typecheck, add `semantic: false` to it.

- [ ] **Step 10: Commit**

```bash
git add web/src/cards
git commit -m "feat(web): add a Semantic toggle to the card search"
```

---

### Task 4: Spec and full CI

**Files:**
- Modify: `docs/superpowers/specs/2026-10-23-discovery-design.md` (section "Semantic search")

- [ ] **Step 1: Bring the spec in line with the shipped behavior**

Add these bullets to the end of the "Semantic search" list in the spec:

```markdown
- Semantic results sort by similarity only; another `sort` is a 400, as are
  a semantic search without `text` and one on lorebooks.
- A bulk selection can't be semantic, since only search embeds the query.
  The Cards page selects all matches by their filter alone, and counts
  facets without the text.
```

- [ ] **Step 2: Run CI**

```bash
go vet ./...
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: everything passes. `TestDetectSourcesSkipsFailingPlugins` and the Cards facet-click test are known flakes; rerun them alone before treating either as a failure.

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-10-23-discovery-design.md
git commit -m "docs(spec): note semantic search's sort and bulk limits"
```
