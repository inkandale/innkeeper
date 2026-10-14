# One kind of tag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a card's `data.tags` its only tags, and remove library tags (`tag`, `card_tag`).

**Architecture:** Search, the tag facet, `include: tags`, and `GET /tags` read `r.data->'tags'` through one SQL expression. Bulk tagging writes a new revision per changed card. The card page edits tags as a draft field, saved with the rest of the revision. Spec: `docs/superpowers/specs/2026-10-13-filter-sidebar-design.md`, section "PR 1".

**Tech Stack:** Go, PostgreSQL (pgx), huma; React, TypeScript, Vitest.

---

## Before you start

Database tests skip silently without a database. Export this in every shell, including subagent shells:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

A Go test run that prints `ok` in well under a second for `internal/library` probably skipped. Check with `-v` and look for `--- SKIP`.

Work in `~/git/innkeeper-filter-sidebar-spec` (branch `filter-sidebar-spec`). Never run git commands in `~/git/innkeeper`.

Commit messages are conventional (`feat:`, `fix:`, `refactor:`). Never use `!` or a `BREAKING CHANGE` footer.

## Files

| File | Change |
|---|---|
| `internal/library/organize.go` | `EditCardTags` writes a revision; delete `SetCardTags`, `CardTags`; `Tags` reads `data.tags` |
| `internal/library/search.go` | `tagsOf` expression; tag filter, facet, include read it; no 50 limit |
| `internal/db/migrations/0021_drop_library_tags.sql` | Create: drop `card_tag`, `tag` |
| `internal/backup/backup.go` | Drop `tag`, `card_tag` from the assets table list |
| `internal/api/entities.go` | Delete `PUT /cards/{id}/tags`, `tagsIn`, `tagsOut`, `entityOut.Tags` |
| `internal/mcp/tools.go` | `get_card` description |
| `web/src/card/ChipInput.tsx` | Moved from `web/src/lorebook/ChipInput.tsx` |
| `web/src/card/Panel.tsx` | Tags chip input on the draft |
| `web/src/card/Organize.tsx` | Delete `Tags` |
| `README.md` | Backup table row |
| Tests | `internal/library/{organize,search,api}_test.go`, `internal/bulk/bulk_test.go`, `internal/api/{entities,organize,search,jobs}_test.go`, `internal/backup/{backup,restore}_test.go`, `web/src/card/Card.test.tsx` |

---

### Task 1: Bulk tag edits write a revision

`EditCardTags` keeps its signature, so `bulk.Tag` doesn't change. It now edits the head revision's `data.tags`.

**Files:**
- Modify: `internal/library/organize.go:50-75`
- Test: `internal/library/organize_test.go:156-174`, `internal/bulk/bulk_test.go:76-98`

- [ ] **Step 1: Write the failing library test**

Replace `TestEditCardTags` in `internal/library/organize_test.go`:

```go
// headTags returns the tags in a card's head revision data.
func headTags(t *testing.T, lib *Library, id int64) []string {
	t.Helper()
	ctx := context.Background()
	c, err := lib.Cards.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Tags []string }
	if err := json.Unmarshal(rev.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data.Tags
}

func TestEditCardTagsAddsARevision(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	avatar := putBlob(t, lib, "avatar")
	c, _, err := lib.Cards.Create(ctx, "brakka", "upload",
		Edit{Data: json.RawMessage(`{"name": "Brakka", "tags": ["Dwarf", "smith"]}`), AvatarBlob: avatar, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.EditCardTags(ctx, c.ID, []string{" grumpy ", "dwarf"}, []string{"SMITH", "absent"}); err != nil {
		t.Fatal(err)
	}
	if got := headTags(t, lib, c.ID); !slices.Equal(got, []string{"Dwarf", "grumpy"}) {
		t.Fatalf("tags = %v", got)
	}
	head, _ := lib.Cards.Get(ctx, c.ID)
	rev, _ := lib.Cards.Revision(ctx, head.HeadRevisionID)
	if rev.AvatarBlob == nil || *rev.AvatarBlob != avatar || rev.Message != "Edit tags" {
		t.Fatalf("revision = %+v", rev)
	}
}

func TestEditCardTagsWithNoChangeAddsNoRevision(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, first := newCard(t, lib, "brakka", `{"name": "Brakka", "tags": ["dwarf"]}`)
	if err := lib.EditCardTags(ctx, id, []string{"DWARF"}, []string{"absent"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, id); c.HeadRevisionID != first {
		t.Fatalf("head = %d, want %d", c.HeadRevisionID, first)
	}
}

func TestEditCardTagsOnMissingCard(t *testing.T) {
	lib := newLibrary(t)
	if err := lib.EditCardTags(context.Background(), 99999, []string{"x"}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestEditCardTagsRejectsNonListTags(t *testing.T) {
	lib := newLibrary(t)
	id, _ := newCard(t, lib, "brakka", `{"name": "Brakka", "tags": "dwarf"}`)
	if err := lib.EditCardTags(context.Background(), id, []string{"x"}, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}
```

Add `"encoding/json"` to the imports. Look for an existing blob helper with `grep -n "func putBlob\|PutBlob(" internal/library/*_test.go`. If none exists, add this to `organize_test.go`:

```go
func putBlob(t *testing.T, lib *Library, content string) string {
	t.Helper()
	b, err := lib.PutBlob(context.Background(), strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	return b.SHA256
}
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test ./internal/library -run 'TestEditCardTags' -v`
Expected: FAIL. Tags come back `nil` because the old code writes `card_tag`.

- [ ] **Step 3: Implement**

In `internal/library/organize.go`, replace `EditCardTags` and add two helpers below it. Add `encoding/json` and `slices` to the imports.

```go
// EditCardTags adds and removes names in a card's data.tags as a new
// revision. Names are trimmed and match case-insensitively; an edit that
// changes nothing adds no revision.
func (l *Library) EditCardTags(ctx context.Context, cardID int64, add, remove []string) error {
	add, remove = cleanTags(add), cleanTags(remove)
	return pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		var head int64
		if err := tx.QueryRow(ctx, `SELECT head_revision_id FROM card WHERE id = $1 FOR UPDATE`, cardID).Scan(&head); err != nil {
			return mapErr(err)
		}
		rev, err := scanRevision(tx.QueryRow(ctx, `SELECT `+l.Cards.revCols()+` FROM card_revision WHERE id = $1`, head))
		if err != nil {
			return err
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(rev.Data, &data); err != nil {
			return fmt.Errorf("card %d data: %w", cardID, err)
		}
		var old []string
		if raw, ok := data["tags"]; ok {
			if err := json.Unmarshal(raw, &old); err != nil {
				return fmt.Errorf("%w: card %d tags aren't a list of strings", ErrInvalid, cardID)
			}
		}
		tags := editTags(old, add, remove)
		if slices.Equal(tags, old) {
			return nil
		}
		if data["tags"], err = json.Marshal(tags); err != nil {
			return err
		}
		body, err := json.Marshal(data)
		if err != nil {
			return err
		}
		e := Edit{Data: body, Overrides: rev.Overrides, Message: "Edit tags", Source: "ui"}
		if rev.AvatarBlob != nil {
			e.AvatarBlob = *rev.AvatarBlob
		}
		_, err = l.Cards.UpdateTx(ctx, tx, cardID, head, e)
		return err
	})
}

// editTags drops remove from tags, then appends each of add not already
// there. Matching ignores case.
func editTags(tags, add, remove []string) []string {
	out := slices.DeleteFunc(slices.Clone(tags), func(t string) bool { return containsFold(remove, t) })
	for _, a := range add {
		if !containsFold(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}
```

`l.Cards` is the card `*Entities`; check with `grep -n "Cards" internal/library/library.go`. `UpdateTx` takes the row lock again inside the same transaction, which is harmless.

- [ ] **Step 4: Run it and watch it pass**

Run: `go test ./internal/library -run 'TestEditCardTags' -v`
Expected: four PASS lines, no SKIP.

- [ ] **Step 5: Update the bulk test**

In `internal/bulk/bulk_test.go` `TestTagAndCollect`, card a starts with tags in its data instead of `SetCardTags`. Replace the `SetCardTags` block with:

```go
	head, _ := lib.Cards.Get(ctx, ids[0])
	if _, err := lib.Cards.Update(ctx, ids[0], head.HeadRevisionID,
		library.Edit{Data: []byte(`{"name": "x", "tags": ["old", "keep"]}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
```

Replace the tag check loop with:

```go
	for i, want := range [][]string{{"keep", "new"}, {"new"}} {
		c, _ := lib.Cards.Get(ctx, ids[i])
		rev, _ := lib.Cards.Revision(ctx, c.HeadRevisionID)
		var data struct{ Tags []string }
		if err := json.Unmarshal(rev.Data, &data); err != nil || !slices.Equal(data.Tags, want) {
			t.Errorf("card %d tags = %v, want %v (%v)", ids[i], data.Tags, want, err)
		}
	}
```

Add `"encoding/json"` to the imports if it's missing.

- [ ] **Step 6: Run the bulk test**

Run: `go test ./internal/bulk -run TestTagAndCollect -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/library/organize.go internal/library/organize_test.go internal/bulk/bulk_test.go
git commit -m "feat(library): bulk tagging edits data.tags as a revision"
```

---

### Task 2: Search reads data.tags

**Files:**
- Modify: `internal/library/search.go` (tag facet at `:123-126`, tag filter at `:206-208`, include at `:327-328`)
- Modify: `internal/library/organize.go` (`Tags`, `:158-173`)
- Test: `internal/library/search_test.go`, `internal/library/api_test.go:40-58`, `internal/api/search_test.go`, `internal/api/organize_test.go`, `internal/api/jobs_test.go:53-60`

- [ ] **Step 1: Change the library tests to tag through data**

In `internal/library/search_test.go`:

1. In `seedSearch`, put the tags in the data and delete both `SetCardTags` blocks:

```go
	s.brakka, _ = newCard(t, lib, "brakka", `{"name": "Brakka", "description": "A grumpy dwarf blacksmith.", "tags": ["fantasy"]}`)
	s.carrow, _ = newCard(t, lib, "carrow", `{"name": "Carrow", "description": "A sailor who owes Brakka money.", "tags": ["pirate"]}`)
```

2. In `TestSearchQuotedTagAndBareAvatar`, create the card with `{"name": "O", "avatar": "from-data", "tags": ["o'brien"]}` and delete the `SetCardTags` block.

3. In `TestSearchFacets`, replace `lib.SetCardTags(ctx, s.carrow, []string{"pirate", "fantasy"})` with `lib.EditCardTags(ctx, s.carrow, []string{"fantasy"}, nil)`.

4. Add a test that the facet has no 50-tag cap and that a card listing a tag twice in different case counts once:

```go
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
```

Add `"encoding/json"` and `"fmt"` to the imports.

5. In `internal/library/api_test.go` `TestTags`, create the cards with tags and delete the `SetCardTags` blocks:

```go
	newCard(t, lib, "a", `{"name": "A", "tags": ["fantasy", "Dwarf"]}`)
	newCard(t, lib, "b", `{"name": "B", "tags": ["fantasy"]}`)
```

The expected `[]TagCount{{"Dwarf", 1}, {"fantasy", 2}}` stays. Use one spelling per tag here: which spelling `min()` picks between `fantasy` and `Fantasy` depends on the database collation.

- [ ] **Step 2: Run them and watch them fail**

Run: `go test ./internal/library -run 'TestSearch|TestTags' -v`
Expected: FAIL on the tag cases. Search still reads `card_tag`.

- [ ] **Step 3: Implement**

In `internal/library/search.go`, add above `from`:

```go
// tagsOf is r's data.tags as rows of text, none when tags is missing or
// not a list.
const tagsOf = `jsonb_array_elements_text(CASE jsonb_typeof(r.data->'tags') WHEN 'array' THEN r.data->'tags' ELSE '[]' END)`
```

Replace the `"tags"` facet:

```go
	"tags": func(k *Kind, m string) string {
		return `SELECT min(t), count(DISTINCT c.id)::int FROM ` + from(k) + `, ` + tagsOf + ` t
			WHERE c.id IN (` + m + `) GROUP BY lower(t) ORDER BY count(DISTINCT c.id) DESC, lower(t)`
	},
```

Replace the `f.Tag` case:

```go
	case f.Tag != nil:
		return `EXISTS (SELECT 1 FROM ` + tagsOf + ` t WHERE lower(t) = lower(` + w.arg(*f.Tag) + `))`, nil
```

Replace the `"tags"` include:

```go
		case "tags":
			tags = `array(SELECT t FROM ` + tagsOf + ` t)`
```

Update the `Facets.Tags` doc tag from `"The 50 most used."` to `"Most used first."`.

In `internal/library/organize.go`, replace `Tags`:

```go
// Tags returns every tag on an active card with its card count,
// alphabetically. Spellings that differ only in case count as one.
func (l *Library) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := l.pool.Query(ctx, `SELECT min(t), count(DISTINCT c.id)::int FROM `+from(Card)+`, `+tagsOf+` t
		WHERE c.archived_at IS NULL GROUP BY lower(t) ORDER BY lower(t)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
}
```

- [ ] **Step 4: Run them and watch them pass**

Run: `go test ./internal/library -v 2>&1 | grep -E '^(--- |FAIL|ok)'`
Expected: no FAIL, no SKIP. `TestCardTags` in `organize_test.go` still passes because it uses the old functions; Task 3 deletes it.

- [ ] **Step 5: Change the API tests to tag through data**

These call `PUT /cards/{id}/tags`, which writes `card_tag` that search no longer reads.

- `internal/api/search_test.go` `TestSearchRoute`: create Brakka with `{"name": "Brakka", "description": "A dwarf blacksmith.", "tags": ["fantasy"]}` and delete the `PUT .../tags` line.
- `internal/api/organize_test.go` `TestTagsAndCollections`: create the card with `{"name": "Brakka", "tags": ["fantasy"]}` and delete the `PUT .../tags` line.
- `internal/api/jobs_test.go` `TestBulkArchiveAndWatch`: replace the create-and-tag loop with:

```go
	for _, name := range []string{"A", "B", "C"} {
		tags := `[]`
		if name != "C" {
			tags = `["old"]`
		}
		e.createCard(fmt.Sprintf(`{"name": %q, "tags": %s}`, name, tags))
	}
```

- [ ] **Step 6: Run the API tests**

Run: `go test ./internal/api -run 'TestSearchRoute|TestTagsAndCollections|TestBulk' -v 2>&1 | grep -E '^(--- |FAIL|ok)'`
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/library internal/api/search_test.go internal/api/organize_test.go internal/api/jobs_test.go
git commit -m "feat(search): tag filter, facet, and list read the card's own tags"
```

---

### Task 3: Remove library tags

**Files:**
- Create: `internal/db/migrations/0021_drop_library_tags.sql`
- Modify: `internal/library/organize.go`, `internal/api/entities.go`, `internal/backup/backup.go`, `internal/mcp/tools.go`, `README.md`
- Test: `internal/library/organize_test.go`, `internal/api/entities_test.go`, `internal/backup/backup_test.go`, `internal/backup/restore_test.go`

- [ ] **Step 1: Write the migration**

Check the latest number with `ls internal/db/migrations | tail -1`. If it isn't `0020_source_detect.sql`, use the next free number.

```sql
-- A card's tags live in its data.tags.
DROP TABLE card_tag;
DROP TABLE tag;
```

- [ ] **Step 2: Delete the Go code**

- `internal/library/organize.go`: delete `SetCardTags` and `CardTags`. Keep `cleanTags`, which `EditCardTags` uses.
- `internal/api/entities.go`:
  - delete the `Tags` field from `entityOut`
  - delete `tagsIn` and `tagsOut`
  - delete the `CardTags` block in `entity()`, and change its doc comment to "a card's lorebooks and collections"
  - delete the `set-card-tags` route in `cardRoutes`
- `internal/backup/backup.go`: remove `"tag", "card_tag",` from `tables[Assets]`.
- `internal/mcp/tools.go:187`: change the `get_card` description to `"Get a card: its head revision, linked lorebooks, and its status on each target app."`

Run: `go build ./... && go vet ./...`
Expected: errors only in `_test.go` files, which the next step fixes.

- [ ] **Step 3: Fix the tests**

- `internal/library/organize_test.go`: delete `TestCardTags`.
- `internal/api/entities_test.go`:
  - remove `Tags` from `entityResult`
  - rename `TestCardTagsLinksAndExport` to `TestCardLinksAndExport`
  - delete its `PUT path+"/tags"` block
  - change the lorebook check to `if book.Slug != "forge" {`
  - change the card check to `if len(got.Lorebooks) != 1 || got.Lorebooks[0] != book.ID {`
- `internal/backup/backup_test.go`: in `seed`, delete the `SetCardTags` block. In the table count map, delete `"tag": 1, "card_tag": 1,`.
- `internal/backup/restore_test.go`:
  - in `TestRestoreAssets`, delete the `card_tag` count query
  - in `TestRestoreReportsMissingRows`, replace the comment and rebuild with the collection pair:

```go
	// collection_card still points at the collection.
	noCollections := rebuild(t, assets, func(b map[string]string) { b["tables/collection.copy"] = "" })
	_, err := newTarget(t).restore(noCollections, nil, false)
```

- [ ] **Step 4: Run the Go suite**

Run: `go vet ./... && go test -race ./... 2>&1 | grep -E '^(FAIL|ok|---)' | grep -v '^ok'`
Expected: no output, meaning every package is `ok`.

- [ ] **Step 5: Regenerate the API schema**

Run: `cd web && npm run gen`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` lose `set-card-tags` and the entity `tags` field. Don't edit either by hand.

Run: `cd web && npm run typecheck`
Expected: errors in `web/src/card/Organize.tsx` and `Panel.tsx` (they still use the removed route and field). Task 4 fixes them.

- [ ] **Step 6: Update the README**

In `README.md`'s backup table, change the `assets` row to `Cards, lorebooks, and their revisions; avatars; collections, sources, and lorebook links`. Tags are inside revisions now.

- [ ] **Step 7: Commit**

```bash
git add internal web/openapi.json web/src/api/schema.d.ts README.md
git commit -m "refactor: remove library tags; a card's tags are its data.tags"
```

---

### Task 4: Edit tags on the card draft

**Files:**
- Move: `web/src/lorebook/ChipInput.tsx` → `web/src/card/ChipInput.tsx`
- Modify: `web/src/lorebook/EntryEditor.tsx` (import), `web/src/lorebook/Lorebook.css` → `web/src/card/Card.css` (`.chip-input` rules)
- Modify: `web/src/card/Panel.tsx`, `web/src/card/Organize.tsx`
- Test: `web/src/card/Card.test.tsx`

- [ ] **Step 1: Write the failing test**

In `web/src/card/Card.test.tsx`:

1. Move the tags into the revision data and off the entity:

```ts
const data = { name: "Brakka", creator: "dg", description: "A grumpy dwarf.", tags: ["dwarf"], alternate_greetings: ["Hail."], extensions: { depth: 4 } };
const entity = {
  id: 7, slug: "brakka", provenance: "api", created_at: now, lorebooks: [3], collections: [1],
  revision: { id: 11, content_hash: "x", message: "", source: "ui", created_at: now, avatar: "ab".repeat(32), data },
};
```

2. Delete the `if (path === "/api/cards/7/tags") ...` line in the fetch stub.

3. Replace the test `"tags and collections save as they change"` with two tests:

```ts
test("tags are part of the draft and save with it", async () => {
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Add to Tags"), "smith{Enter}");
  expect(puts).toHaveLength(0);
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ path: "/api/cards/7", body: { base_revision_id: 11, data: { tags: ["dwarf", "smith"] } } });
});

test("collections save as they change", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("checkbox", { name: "Pirates" }));
  await waitFor(() => expect(puts.map((p) => p.path)).toContain("/api/collections/2/cards/7"));
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd web && npx vitest run src/card/Card.test.tsx`
Expected: FAIL. There's no "Add to Tags" input.

- [ ] **Step 3: Move ChipInput**

```bash
git mv web/src/lorebook/ChipInput.tsx web/src/card/ChipInput.tsx
```

In `web/src/lorebook/EntryEditor.tsx`, change the import to `import { ChipInput } from "../card/ChipInput";`. Move the `.chip-input` and `.chip-input .input` rules from `web/src/lorebook/Lorebook.css` to `web/src/card/Card.css`, next to `.chips`. The lorebook page already imports `Card.css`.

- [ ] **Step 4: Add tags to the panel and drop the old editor**

In `web/src/card/Panel.tsx`:
- change the Organize import to `import { Collections, Sources } from "./Organize";`
- add `import { ChipInput } from "./ChipInput";`
- add this after the Creator input:

```tsx
      <ChipInput label="Tags" values={Array.isArray(data.tags) ? data.tags.map(text) : []} onChange={(v) => edit("tags", v)} />
```

- delete the `<Tags id={card.id} tags={card.tags ?? []} />` line

In `web/src/card/Organize.tsx`:
- delete the `Tags` component
- change the top comment to `// These save on change: collections and sources aren't part of a revision.`

Remove any imports this leaves unused. `typecheck` reports them.

- [ ] **Step 5: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: both pass.

- [ ] **Step 6: Commit**

```bash
git add web
git commit -m "feat(web): edit tags on the card draft"
```

---

### Task 5: Final check

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass, with no `SKIP` in `go test -v ./internal/library ./internal/api ./internal/bulk ./internal/backup`.

- [ ] **Step 2: Grep for leftovers**

Run: `git grep -n "card_tag\|SetCardTags\|CardTags\|set-card-tags\|/tags\"" -- ':!docs' ':!internal/db/migrations'`
Expected: only `GET /tags` in `internal/api/organize.go` and `README.md`.

- [ ] **Step 3: Read the diff once more**

Run: `git diff origin/main --stat` and `git diff origin/main -- '*.go' '*.tsx' '*.ts' ':!web/src/api/schema.d.ts'`. Look for dead code, stale comments that mention library tags, and unrelated edits.
