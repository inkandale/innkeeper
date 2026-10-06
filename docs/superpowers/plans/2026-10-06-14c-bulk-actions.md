# Web UI Bulk Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Select cards on the Cards page and tag, collect, push, archive, or export them in one job, then follow that job in Settings → Jobs.

**Architecture:** A new `ids` search filter lets every query-based bulk endpoint take picked cards as well as a search. `/bulk` gains tag, untag, collect, uncollect, and export. Tag and collect run one child job per card, as archive does. Export runs as one job that writes a zip blob. The Cards page gets select boxes, a selection bar, and toasts. Settings gets a Jobs tab with a list and a detail view that follows a running job over its event stream.

**Tech Stack:** Go 1.27, Huma v2, PostgreSQL, `archive/zip`, React, React Router, TanStack Query, `openapi-fetch`, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-05-web-ui-design.md`, "Selection", "Settings → Jobs", and "API additions". Build order phase 3.

---

## Design notes

- **Picked cards are a filter.** `{"ids": [1, 2]}` is a filter term like `tag`. `/bulk` and `/pushes` already take a query, so "card IDs or a search query" needs no second input. Archived cards stay hidden unless the filter names `archived`, as for any search.
- **Push stays on `/pushes`.** The spec's `card.push` kind already exists as `target.push` children under a `bulk` parent. The selection bar calls `/pushes` with the selection's query.
- **Tags change, not replace.** `card.tag` children add and remove names with `EditCardTags`, so a card keeps the tags the action doesn't name.
- **Export is one job, not a bulk parent.** A zip is one file, so children would have to hand their files to a last step. `card.export` loops the cards, reports `done` and `total`, and sets `blob` when the zip is stored. Nothing references the blob, so blob cleanup deletes it after a day. A card deleted since the job started is skipped.
- **Encoding moves out of the API.** `s.encode` becomes `internal/export`, so the worker can write PNGs too. `GET /cards/{id}/export` calls the same code.
- **Selection state.** Picked IDs or "all matching", keyed by the search text and filter terms, so a new search clears it and paging keeps it. Shift-click selects a range within the current page. In "all matching" mode the tile boxes show checked and are disabled; Clear leaves the mode.
- **"All matching" with no filter or text** sends `{"archived": false}`, because `/bulk` refuses a query with neither.
- **Action panels, not dialogs.** Each action opens a small form under the selection bar, so there's no dialog library and no `<dialog>`, which jsdom doesn't fully support. Push and Archive need a second click on a button that names the count. The full push preview is phase 7.
- **Toasts.** A provider in the shell. Info toasts close after 8 seconds; error toasts stay until dismissed. A started job's toast links to `/settings/jobs/{id}`.
- **Live jobs.** The detail view follows `/api/jobs/{id}/events`. The list polls every 2 seconds while any listed job is unfinished: a stream per row would use up the browser's six connections per host.
- **Out of this phase:** the CLI's `bulk` command and MCP keep archive only (the API is enough for now); the push preview (phase 7); the other Settings tabs (phase 6).

## Files

| File | Change |
|---|---|
| `internal/library/search.go`, `search_test.go` | `ids` filter |
| `internal/library/organize.go`, `organize_test.go` | `EditCardTags` |
| `internal/export/export.go`, `export_test.go` | New: `Exporter.Card`, moved from `internal/api/export.go` |
| `internal/api/export.go`, `entities.go`, `server.go` | Use `internal/export` |
| `internal/bulk/bulk.go`, `bulk_test.go` | `card.tag`, `card.collect` |
| `internal/bulk/export.go`, `export_test.go` | New: `card.export` |
| `internal/api/jobs.go`, `jobs_test.go` | New `/bulk` actions |
| `cmd/innkeeper/worker.go` | Register the new kinds |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `web/src/shell/Toasts.tsx`, `Toasts.test.tsx`, `Shell.tsx`, `Shell.css` | New: toasts |
| `web/src/cards/selection.ts`, `selection.test.ts` | New: selection state |
| `web/src/cards/SelectionBar.tsx`, `Tile.tsx`, `Cards.tsx`, `Cards.css`, `Cards.test.tsx` | Select boxes and the selection bar |
| `web/src/settings/jobs.ts`, `jobs.test.ts`, `Settings.tsx`, `Jobs.tsx`, `Job.tsx`, `Settings.css`, `Jobs.test.tsx` | New: Settings → Jobs |
| `web/src/App.tsx` | Routes |

Go tests need PostgreSQL. Run them with:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

Without it, the database tests skip and a run passes without testing anything. Every `go test` command below assumes it's set.

---

### Task 1: The `ids` filter

**Files:**
- Modify: `internal/library/search.go`
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/library/search_test.go`:

```go
func TestSearchIDs(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	// Dellin is archived, so an ids filter alone still hides it.
	got, err := lib.Search(ctx, Query{Filter: &Filter{IDs: []int64{s.carrow, s.dellin}}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.carrow}) {
		t.Fatalf("ids = %v, %v", hitIDs(got), err)
	}
	ids, err := lib.SearchIDs(ctx, Query{Filter: &Filter{Or: []Filter{{IDs: []int64{s.brakka}}, {Tag: ptr("pirate")}}}})
	if err != nil || !slices.Equal(ids, []int64{s.brakka, s.carrow}) {
		t.Fatalf("ids or tag = %v, %v", ids, err)
	}
	none, err := lib.SearchIDs(ctx, Query{Filter: &Filter{IDs: []int64{}}})
	if err != nil || len(none) != 0 {
		t.Fatalf("empty ids = %v, %v", none, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/library -run TestSearchIDs`
Expected: FAIL to compile, `unknown field IDs in struct literal`.

- [ ] **Step 3: Add the filter**

In `internal/library/search.go`, add the field to `Filter` after `Not`:

```go
	IDs         []int64      `json:"ids,omitempty" maxItems:"10000" doc:"Is one of these cards."`
```

Add `f.IDs != nil` to the list in `(*where).filter` that counts conditions:

```go
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.IDs != nil, f.Tag != nil, f.Collection != nil, f.Source != nil, f.Archived != nil, f.TargetState != nil} {
```

Add a case to the switch below it, before `case f.Tag != nil:`:

```go
	case f.IDs != nil:
		return `c.id = ANY(` + w.arg(f.IDs) + `)`, nil
```

- [ ] **Step 4: Run the library tests**

Run: `go test ./internal/library`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go
git commit -m "feat(search): filter on card IDs"
```

---

### Task 2: Add and remove tags without replacing them

**Files:**
- Modify: `internal/library/organize.go`
- Test: `internal/library/organize_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/library/organize_test.go`:

```go
func TestEditCardTags(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	id, _ := newCard(t, lib, "brakka", `{"name": "Brakka"}`)
	if err := lib.SetCardTags(ctx, id, []string{"Dwarf", "smith"}); err != nil {
		t.Fatal(err)
	}
	if err := lib.EditCardTags(ctx, id, []string{" grumpy ", "dwarf"}, []string{"SMITH", "absent"}); err != nil {
		t.Fatal(err)
	}
	got, err := lib.CardTags(ctx, id)
	if err != nil || !slices.Equal(got, []string{"Dwarf", "grumpy"}) {
		t.Fatalf("tags = %v, %v", got, err)
	}
	if err := lib.EditCardTags(ctx, 99999, []string{"x"}, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing card: %v", err)
	}
}
```

Add `"errors"` and `"slices"` to the file's imports if they aren't there.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/library -run TestEditCardTags`
Expected: FAIL to compile, `lib.EditCardTags undefined`.

- [ ] **Step 3: Implement**

In `internal/library/organize.go`, replace the trimming loop at the top of `SetCardTags` with a call to a new helper, and add `EditCardTags` after it:

```go
// cleanTags trims names and drops empty ones.
func cleanTags(names []string) []string {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	return clean
}

// SetCardTags replaces a card's tags. Tag names are trimmed and matched
// case-insensitively; new names create tags.
func (l *Library) SetCardTags(ctx context.Context, cardID int64, names []string) error {
	clean := cleanTags(names)
	tx, err := l.pool.Begin(ctx)
	// ... the rest of SetCardTags is unchanged
}

// EditCardTags adds and removes tags on a card and keeps the rest. Names
// match as in SetCardTags.
func (l *Library) EditCardTags(ctx context.Context, cardID int64, add, remove []string) error {
	add, remove = cleanTags(add), cleanTags(remove)
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO tag (name)
		SELECT DISTINCT ON (lower(n)) n FROM unnest($1::text[]) n
		ON CONFLICT (lower(name)) DO NOTHING`, add); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_tag (card_id, tag_id)
		SELECT $1, id FROM tag WHERE lower(name) IN (SELECT lower(n) FROM unnest($2::text[]) n)
		ON CONFLICT DO NOTHING`, cardID, add); err != nil {
		return mapErr(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM card_tag WHERE card_id = $1 AND tag_id IN
		(SELECT id FROM tag WHERE lower(name) IN (SELECT lower(n) FROM unnest($2::text[]) n))`,
		cardID, remove); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

`mapErr` turns the foreign-key violation for a missing card into `ErrNotFound`, as it does for `SetCardTags`. If the test's missing-card check fails, read `mapErr` and use the error it maps foreign-key violations to.

- [ ] **Step 4: Run the library tests**

Run: `go test ./internal/library`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/organize.go internal/library/organize_test.go
git commit -m "feat(library): add and remove a card's tags"
```

---

### Task 3: Move card encoding into `internal/export`

The worker needs to encode cards for a bulk export, and encoding lives on the API server. This task moves it without changing behavior; the API's export tests are the check.

**Files:**
- Create: `internal/export/export.go`
- Modify: `internal/api/export.go`, `internal/api/entities.go:402-426`, `internal/api/server.go:219`
- Test: `internal/export/export_test.go`, existing `internal/api/entities_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/export/export_test.go`:

```go
package export

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/testdb"
)

func TestCardJSON(t *testing.T) {
	ctx := context.Background()
	lib := library.New(testdb.New(t), &blobstore.Store{Name: "library", Root: t.TempDir()})
	c, _, err := lib.Cards.Create(ctx, "brakka", "upload", library.Edit{Data: []byte(`{"name": "Brakka"}`), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	f, err := (&Exporter{Lib: lib}).Card(ctx, c.ID, "json")
	if err != nil {
		t.Fatal(err)
	}
	var card struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if f.Name != "brakka.json" || f.Type != "application/json" || json.Unmarshal(f.Body, &card) != nil || card.Data.Name != "Brakka" {
		t.Fatalf("file = %s %s %s", f.Name, f.Type, f.Body)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/export`
Expected: FAIL, `no non-test Go files` or `undefined: Exporter`.

- [ ] **Step 3: Create the package**

Create `internal/export/export.go`. `encode` is the body of `(*Server).encode` from `internal/api/export.go`, with `s.` replaced by `e.`:

```go
// Package export writes library cards as files.
package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// ErrBadCodec means a codec plugin's reply broke the protocol.
var ErrBadCodec = errors.New("codec plugin sent a bad reply")

// maxBytes caps a codec's file, as uploads are capped.
const maxBytes = 32 << 20

// Exporter writes cards as JSON, or in a format a codec plugin writes.
type Exporter struct {
	Lib      *library.Library
	Registry *plugins.Registry
	Plugins  *pluginhost.Manager
	Log      *slog.Logger
}

// File is one exported card.
type File struct {
	Name string // slug and extension, such as brakka.png
	Type string // the type sniffed when the file was stored
	Body []byte
}

// Card exports a card's head with its linked lorebooks embedded. format is
// json, or a format a codec plugin instance writes.
func (e *Exporter) Card(ctx context.Context, id int64, format string) (File, error) {
	c, err := e.Lib.Cards.Get(ctx, id)
	if err != nil {
		return File{}, err
	}
	raw, err := e.Lib.ExportCard(ctx, id)
	if err != nil {
		return File{}, err
	}
	if format == "json" {
		return File{Name: c.Slug + ".json", Type: "application/json", Body: raw}, nil
	}
	body, ext, typ, err := e.encode(ctx, c.HeadRevisionID, raw, format)
	if err != nil {
		return File{}, err
	}
	return File{Name: c.Slug + "." + ext, Type: typ, Body: body}, nil
}

// encode writes a card in format with the oldest working codec instance that
// lists it. revID is the revision whose avatar goes in the file. It returns
// the file, its extension and the type sniffed when it was stored.
func (e *Exporter) encode(ctx context.Context, revID int64, card json.RawMessage, format string) ([]byte, string, string, error) {
	inst, err := e.Registry.CodecInstance(ctx, format)
	if err != nil {
		return nil, "", "", err
	}
	rev, err := e.Lib.Cards.Revision(ctx, revID)
	if err != nil {
		return nil, "", "", err
	}
	params := protocol.CodecEncodeParams{Card: card, Format: format}
	if rev.AvatarBlob != nil {
		if params.Avatar, err = e.Lib.BlobPath(*rev.AvatarBlob); err != nil {
			return nil, "", "", err
		}
	}
	var res protocol.CodecEncodeResult
	if err := e.Registry.Call(ctx, e.Plugins, inst, protocol.MethodCodecEncode, params, &res); err != nil {
		return nil, "", "", err
	}
	if !protocol.ValidExtension(res.Extension) {
		return nil, "", "", fmt.Errorf("%w: extension %q", ErrBadCodec, res.Extension)
	}
	// IngestBlob refuses a path outside blob_tmp. Nothing references the
	// blob, so cleanup removes it after a day.
	b, err := e.Lib.IngestBlob(ctx, res.Path)
	if errors.Is(err, blobstore.ErrInvalid) {
		e.Log.Error("codec wrote a bad file", "format", format, "err", err)
		return nil, "", "", fmt.Errorf("%w: it named a file the server can't accept", ErrBadCodec)
	}
	if err != nil {
		return nil, "", "", err
	}
	// ponytail: checked after IngestBlob stored the file, so a runaway codec
	// can fill the disk until cleanup; check the size before ingesting if
	// that matters.
	if b.Size > maxBytes {
		return nil, "", "", fmt.Errorf("%w: file is over %d MiB", ErrBadCodec, maxBytes>>20)
	}
	_, f, err := e.Lib.OpenBlob(ctx, b.SHA256)
	if err != nil {
		return nil, "", "", err
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	return body, res.Extension, b.MIME, err
}
```

- [ ] **Step 4: Point the API at it**

In `internal/api/export.go`, delete `errBadCodec` and `encode`, and remove the imports they used, leaving `strings`. Add a helper next to `exportType`:

```go
// exporter encodes cards with the server's plugins.
func (s *Server) exporter() *export.Exporter {
	return &export.Exporter{Lib: s.Lib, Registry: s.Registry, Plugins: s.Plugins, Log: s.Log}
}
```

Import `github.com/davidgibbons/innkeeper/internal/export`.

In `internal/api/entities.go`, replace the body of the `export-card` handler:

```go
		func(ctx context.Context, in *exportIn) (*exportOut, error) {
			f, err := s.exporter().Card(ctx, in.ID, in.Format)
			if err != nil {
				return nil, s.fail(err)
			}
			return &exportOut{ContentDisposition: fmt.Sprintf(`attachment; filename="%s"`, f.Name),
				ContentType: exportType(f.Type), NoSniff: "nosniff", CSP: "sandbox", Body: f.Body}, nil
		})
```

In `internal/api/server.go`, change `errors.Is(err, errBadCodec)` to `errors.Is(err, export.ErrBadCodec)` and import the package.

- [ ] **Step 5: Run the tests**

Run: `go build ./... && go test ./internal/export ./internal/api`
Expected: PASS, including the existing export tests in `internal/api`.

- [ ] **Step 6: Commit**

```bash
git add internal/export internal/api/export.go internal/api/entities.go internal/api/server.go
git commit -m "refactor(export): move card encoding out of the API server"
```

---

### Task 4: `card.tag` and `card.collect` jobs

**Files:**
- Modify: `internal/bulk/bulk.go`
- Test: `internal/bulk/bulk_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/bulk/bulk_test.go`, pull the worker and wait out of `TestArchive` into helpers, and add `TestTagAndCollect`. Replace the file with:

```go
package bulk

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/blobstore"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/testdb"
)

// setup returns a library with one card per slug, and a queue whose worker
// runs handlers until the test ends.
func setup(t *testing.T, handlers func(*library.Library) map[string]jobs.Handler, slugs ...string) (*library.Library, *jobs.Queue, []int64) {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	lib := library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()})
	var ids []int64
	for _, slug := range slugs {
		c, _, err := lib.Cards.Create(ctx, slug, "upload", library.Edit{Data: []byte(`{"name": "x"}`), Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Handlers: handlers(lib)}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() { cancel(); <-done })
	return lib, q, ids
}

// wait returns the job once it succeeds, and fails the test if it fails or
// takes over 10 seconds.
func wait(t *testing.T, q *jobs.Queue, id int64) jobs.Job {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		j, _ := q.Get(context.Background(), id)
		if j.Status == jobs.StatusSucceeded {
			return j
		}
		if j.Status == jobs.StatusFailed || time.Now().After(deadline) {
			msg := ""
			if j.Error != nil {
				msg = *j.Error
			}
			t.Fatalf("job %d is %s: %s", id, j.Status, msg)
		}
	}
}

func TestArchive(t *testing.T) {
	ctx := context.Background()
	lib, q, ids := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{ArchiveKind: Archive(lib)}
	}, "a", "b")
	parent, err := EnqueueArchive(ctx, q, ids, true)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, parent.ID)
	for _, id := range ids {
		if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt == nil {
			t.Errorf("card %d not archived", id)
		}
	}
}

func TestTagAndCollect(t *testing.T) {
	ctx := context.Background()
	lib, q, ids := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{TagKind: Tag(lib), CollectKind: Collect(lib)}
	}, "a", "b")
	if err := lib.SetCardTags(ctx, ids[0], []string{"old", "keep"}); err != nil {
		t.Fatal(err)
	}
	col, err := lib.CreateCollection(ctx, "Fantasy", "")
	if err != nil {
		t.Fatal(err)
	}

	p, err := EnqueueTag(ctx, q, ids, []string{"new"}, []string{"old"})
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	for i, want := range [][]string{{"keep", "new"}, {"new"}} {
		if got, _ := lib.CardTags(ctx, ids[i]); !slices.Equal(got, want) {
			t.Errorf("card %d tags = %v, want %v", ids[i], got, want)
		}
	}

	p, err = EnqueueCollect(ctx, q, ids, col.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	if got, _ := lib.CollectionCards(ctx, col.ID); len(got) != 2 {
		t.Fatalf("collected = %v", got)
	}
	p, err = EnqueueCollect(ctx, q, ids[:1], col.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	wait(t, q, p.ID)
	if got, _ := lib.CollectionCards(ctx, col.ID); !slices.Equal(got, ids[1:]) {
		t.Fatalf("after uncollect = %v", got)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/bulk`
Expected: FAIL to compile, `undefined: TagKind`.

- [ ] **Step 3: Implement**

Replace `internal/bulk/bulk.go`:

```go
// Package bulk holds bulk actions: a parent job with one child job per card,
// and the export job.
package bulk

import (
	"context"
	"encoding/json"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// Job kinds.
const (
	Kind        = "bulk"
	ArchiveKind = "card.archive"
	TagKind     = "card.tag"
	CollectKind = "card.collect"
)

type archivePayload struct {
	CardID   int64 `json:"card_id"`
	Archived bool  `json:"archived"`
}

type tagPayload struct {
	CardID int64    `json:"card_id"`
	Add    []string `json:"add,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

type collectPayload struct {
	CardID       int64 `json:"card_id"`
	CollectionID int64 `json:"collection_id"`
	Remove       bool  `json:"remove,omitempty"`
}

// enqueue queues a bulk parent with params and one child of kind per card.
func enqueue(ctx context.Context, q *jobs.Queue, kind string, params map[string]any, cardIDs []int64, payload func(int64) any) (jobs.Job, error) {
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		children[i] = jobs.Spec{Kind: kind, Payload: payload(id)}
	}
	params["action"] = kind
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: Kind, Payload: params}, children)
}

// EnqueueArchive archives or unarchives cards, one child job per card.
func EnqueueArchive(ctx context.Context, q *jobs.Queue, cardIDs []int64, archived bool) (jobs.Job, error) {
	return enqueue(ctx, q, ArchiveKind, map[string]any{"archived": archived}, cardIDs,
		func(id int64) any { return archivePayload{CardID: id, Archived: archived} })
}

// EnqueueTag adds and removes tags on cards, one child job per card.
func EnqueueTag(ctx context.Context, q *jobs.Queue, cardIDs []int64, add, remove []string) (jobs.Job, error) {
	return enqueue(ctx, q, TagKind, map[string]any{"add": add, "remove": remove}, cardIDs,
		func(id int64) any { return tagPayload{CardID: id, Add: add, Remove: remove} })
}

// EnqueueCollect adds cards to a collection, or removes them, one child job
// per card.
func EnqueueCollect(ctx context.Context, q *jobs.Queue, cardIDs []int64, collectionID int64, remove bool) (jobs.Job, error) {
	return enqueue(ctx, q, CollectKind, map[string]any{"collection_id": collectionID, "remove": remove}, cardIDs,
		func(id int64) any { return collectPayload{CardID: id, CollectionID: collectionID, Remove: remove} })
}

// Archive is the jobs.Handler for ArchiveKind.
func Archive(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p archivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return lib.Cards.SetArchived(ctx, p.CardID, p.Archived)
	}
}

// Tag is the jobs.Handler for TagKind.
func Tag(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p tagPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		return lib.EditCardTags(ctx, p.CardID, p.Add, p.Remove)
	}
}

// Collect is the jobs.Handler for CollectKind.
func Collect(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p collectPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		if p.Remove {
			return lib.RemoveFromCollection(ctx, p.CollectionID, p.CardID)
		}
		return lib.AddToCollection(ctx, p.CollectionID, p.CardID)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bulk ./internal/api`
Expected: PASS. The API's archive test checks the parent payload still works.

- [ ] **Step 5: Commit**

```bash
git add internal/bulk/bulk.go internal/bulk/bulk_test.go
git commit -m "feat(bulk): tag and collect many cards"
```

---

### Task 5: The `card.export` job

**Files:**
- Create: `internal/bulk/export.go`
- Test: `internal/bulk/export_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/bulk/export_test.go`:

```go
package bulk

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/export"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestExport(t *testing.T) {
	ctx := context.Background()
	lib, q, ids := setup(t, func(lib *library.Library) map[string]jobs.Handler {
		return map[string]jobs.Handler{ExportKind: Export(&export.Exporter{Lib: lib})}
	}, "a", "b")
	// A card deleted after the job was queued is skipped.
	j, err := EnqueueExport(ctx, q, append(ids, 99999), "json")
	if err != nil {
		t.Fatal(err)
	}
	j = wait(t, q, j.ID)
	var p ExportProgress
	if err := json.Unmarshal(j.Progress, &p); err != nil || p.Done != 3 || p.Total != 3 || p.Blob == "" {
		t.Fatalf("progress = %s, %v", j.Progress, err)
	}
	_, f, err := lib.OpenBlob(ctx, p.Blob)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	body, _ := io.ReadAll(f)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, zf := range zr.File {
		names = append(names, zf.Name)
	}
	if !slices.Equal(names, []string{"a.json", "b.json"}) {
		t.Fatalf("zip holds %v", names)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/bulk -run TestExport`
Expected: FAIL to compile, `undefined: ExportKind`.

- [ ] **Step 3: Implement**

Create `internal/bulk/export.go`:

```go
package bulk

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/davidgibbons/innkeeper/internal/export"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// ExportKind writes cards into one zip blob.
const ExportKind = "card.export"

type exportPayload struct {
	CardIDs []int64 `json:"card_ids"`
	Format  string  `json:"format"`
}

// ExportProgress is an export job's progress. Blob is the zip's SHA-256,
// set once the zip is stored.
type ExportProgress struct {
	Done  int    `json:"done"`
	Total int    `json:"total"`
	Blob  string `json:"blob,omitempty"`
}

// EnqueueExport queues one job that writes the cards in format to a zip.
func EnqueueExport(ctx context.Context, q *jobs.Queue, cardIDs []int64, format string) (jobs.Job, error) {
	return q.Enqueue(ctx, jobs.Spec{Kind: ExportKind, Payload: exportPayload{CardIDs: cardIDs, Format: format}})
}

// Export is the jobs.Handler for ExportKind. Nothing references the zip, so
// blob cleanup deletes it after a day.
func Export(ex *export.Exporter) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p exportPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		tmp, err := os.CreateTemp("", "innkeeper-export-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		defer tmp.Close()
		zw := zip.NewWriter(tmp)
		progress := func(pr ExportProgress) {
			b, _ := json.Marshal(pr)
			r.SetProgress(b)
		}
		for i, id := range p.CardIDs {
			if err := ctx.Err(); err != nil {
				return err
			}
			f, err := ex.Card(ctx, id, p.Format)
			if errors.Is(err, library.ErrNotFound) {
				progress(ExportProgress{Done: i + 1, Total: len(p.CardIDs)})
				continue
			}
			if err != nil {
				return fmt.Errorf("card %d: %w", id, err)
			}
			w, err := zw.Create(f.Name)
			if err != nil {
				return err
			}
			if _, err := w.Write(f.Body); err != nil {
				return err
			}
			progress(ExportProgress{Done: i + 1, Total: len(p.CardIDs)})
		}
		if err := zw.Close(); err != nil {
			return err
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return err
		}
		b, err := ex.Lib.PutBlob(ctx, tmp)
		if err != nil {
			return err
		}
		progress(ExportProgress{Done: len(p.CardIDs), Total: len(p.CardIDs), Blob: b.SHA256})
		return nil
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/bulk`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bulk/export.go internal/bulk/export_test.go
git commit -m "feat(bulk): export many cards as one zip"
```

---

### Task 6: New `/bulk` actions, and the worker runs them

**Files:**
- Modify: `internal/api/jobs.go`, `cmd/innkeeper/worker.go`
- Test: `internal/api/jobs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/jobs_test.go`:

```go
func TestBulkTagCollectExport(t *testing.T) {
	e := newEnv(t)
	a := e.createCard(`{"name": "A"}`)
	b := e.createCard(`{"name": "B"}`)
	e.createCard(`{"name": "C"}`)
	w := &jobs.Worker{Queue: e.s.Queue, Log: quiet, Poll: 50 * time.Millisecond,
		Handlers: map[string]jobs.Handler{bulk.TagKind: bulk.Tag(e.s.Lib), bulk.CollectKind: bulk.Collect(e.s.Lib),
			bulk.ExportKind: bulk.Export(e.s.exporter())}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	picked := map[string]any{"filter": map[string]any{"ids": []int64{a.ID, b.ID}}}

	var j jobOut
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag", "tags": []string{"elf"}}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" || last.Children["succeeded"] != 2 {
		t.Fatalf("tag job = %+v\n%s", last, r.Body)
	}
	var res library.Results
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"tag": "elf"}}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("tagged = %+v", res.Hits)
	}

	var col struct{ ID int64 }
	e.call("POST", "/collections", map[string]any{"name": "Elves"}, 201, &col)
	j = jobOut{}
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "collect", "collection": col.ID}, 202, &j)
	if last, _ := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("collect job = %+v", last)
	}
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"collection": col.ID}}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("collected = %+v", res.Hits)
	}

	j = jobOut{}
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "export"}, 202, &j)
	last, _ := e.watchJob(j.ID)
	var p bulk.ExportProgress
	if last.Kind != bulk.ExportKind || last.Status != "succeeded" || json.Unmarshal(last.Progress, &p) != nil || p.Blob == "" {
		t.Fatalf("export job = %+v", last)
	}
	e.call("GET", "/blobs/"+p.Blob, nil, 200, nil)

	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "tag"}, 400, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "collect", "collection": 99999}, 404, nil)
	e.call("POST", "/bulk", map[string]any{"query": picked, "action": "export", "format": "nope"}, 400, nil)
}
```

Check the collection create status against `organize_test.go`; if `create-collection` answers 200, use 200. If `e.call` decodes a non-JSON body into `out` and fails, pass `nil` as the blob call already does.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api -run TestBulkTagCollectExport`
Expected: FAIL: the `tag` action is a 422, as the enum doesn't allow it.

- [ ] **Step 3: Add the actions**

In `internal/api/jobs.go`, replace `bulkIn`:

```go
type bulkIn struct {
	Body struct {
		Query      library.Query `json:"query" doc:"Selects the cards, as in POST /search. Paging is ignored. To pick cards by ID, filter on ids."`
		Action     string        `json:"action" enum:"archive,unarchive,tag,untag,collect,uncollect,export"`
		Tags       []string      `json:"tags,omitempty" doc:"For tag and untag: the tags to add or remove."`
		Collection int64         `json:"collection,omitempty" doc:"For collect and uncollect: the collection."`
		Format     string        `json:"format,omitempty" default:"json" doc:"For export: json, or a format a codec plugin instance writes, such as png."`
	}
}
```

Replace the `start-bulk` registration:

```go
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-bulk", Method: http.MethodPost, Path: "/bulk",
		Summary: "Archive, tag, collect, or export many cards",
		Description: "Queues one child job per card the query matches. Export queues one job that writes a zip; " +
			"its progress names the zip's blob when it's done, and the blob is deleted after a day.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *bulkIn) (*jobResp, error) {
			b := in.Body
			q := b.Query
			if q.Filter == nil && q.Text == "" {
				return nil, huma.Error400BadRequest("bulk actions need a filter or text; to select every card, filter on archived")
			}
			switch b.Action {
			case "archive", "unarchive":
				// Select only cards the action changes; search would otherwise hide archived cards from unarchive.
				wasArchived := b.Action == "unarchive"
				state := library.Filter{Archived: &wasArchived}
				if q.Filter != nil {
					state = library.Filter{And: []library.Filter{*q.Filter, state}}
				}
				q.Filter = &state
			case "tag", "untag":
				if len(b.Tags) == 0 {
					return nil, huma.Error400BadRequest(b.Action + " needs tags")
				}
			case "collect", "uncollect":
				cols, err := s.Lib.Collections(ctx)
				if err != nil {
					return nil, s.fail(err)
				}
				if !slices.ContainsFunc(cols, func(c library.Collection) bool { return c.ID == b.Collection }) {
					return nil, huma.Error404NotFound("collection not found")
				}
			case "export":
				if b.Format != "json" {
					if _, err := s.Registry.CodecInstance(ctx, b.Format); err != nil {
						return nil, s.fail(err)
					}
				}
			}
			ids, err := s.Lib.SearchIDs(ctx, q)
			if err != nil {
				return nil, s.fail(err)
			}
			var p jobs.Job
			switch b.Action {
			case "archive", "unarchive":
				p, err = bulk.EnqueueArchive(ctx, s.Queue, ids, b.Action == "archive")
			case "tag":
				p, err = bulk.EnqueueTag(ctx, s.Queue, ids, b.Tags, nil)
			case "untag":
				p, err = bulk.EnqueueTag(ctx, s.Queue, ids, nil, b.Tags)
			case "collect", "uncollect":
				p, err = bulk.EnqueueCollect(ctx, s.Queue, ids, b.Collection, b.Action == "uncollect")
			case "export":
				p, err = bulk.EnqueueExport(ctx, s.Queue, ids, b.Format)
			}
			if err != nil {
				return nil, s.fail(err)
			}
			j, err := s.job(ctx, p.ID)
			if err != nil {
				return nil, err
			}
			return &jobResp{Body: j}, nil
		})
```

Add `"slices"` to the imports. If an unknown format's `CodecInstance` error doesn't map to 400 in `s.fail`, check that it wraps `plugins.ErrNoCodec`.

- [ ] **Step 4: Register the kinds in the worker**

In `cmd/innkeeper/worker.go`, after `tg := targets.New(...)`:

```go
	ex := &export.Exporter{Lib: svc.lib, Registry: svc.registry, Plugins: mgr, Log: log}
```

Add to `Handlers`, after `bulk.ArchiveKind`:

```go
			bulk.TagKind:          bulk.Tag(svc.lib),
			bulk.CollectKind:      bulk.Collect(svc.lib),
			bulk.ExportKind:       bulk.Export(ex),
```

Import `github.com/davidgibbons/innkeeper/internal/export`.

- [ ] **Step 5: Run the Go tests**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/jobs.go internal/api/jobs_test.go cmd/innkeeper/worker.go
git commit -m "feat(api): tag, collect, and export from /bulk"
```

---

### Task 7: Regenerate the API client

**Files:**
- Modify: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Regenerate**

Run: `cd web && npm run gen`

- [ ] **Step 2: Check the new types**

Run: `cd web && grep -n "BulkInBody: {" -A20 src/api/schema.d.ts && npm run typecheck`
Expected: `BulkInBody` lists `action` with the seven actions, `tags`, `collection`, and `format`; `Filter` has `ids`; typecheck passes.

- [ ] **Step 3: Commit**

```bash
git add web/openapi.json web/src/api/schema.d.ts
git commit -m "chore(web): regenerate the API client"
```

---

### Task 8: Toasts

**Files:**
- Create: `web/src/shell/Toasts.tsx`, `web/src/shell/Toasts.test.tsx`
- Modify: `web/src/shell/Shell.tsx`, `web/src/shell/Shell.css`

- [ ] **Step 1: Write the failing test**

Create `web/src/shell/Toasts.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts, useToast } from "./Toasts";

function Buttons() {
  const toast = useToast();
  return (
    <>
      <button onClick={() => toast({ text: "Job 5 started", link: { to: "/settings/jobs/5", label: "View job" } })}>info</button>
      <button onClick={() => toast({ text: "Boom", error: true })}>error</button>
    </>
  );
}

function renderToasts() {
  const router = createMemoryRouter([{ path: "/", element: <Toasts><Buttons /></Toasts> }]);
  render(<RouterProvider router={router} />);
}

test("a toast shows its text and link, and dismisses", async () => {
  renderToasts();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "info" }));
  expect(screen.getByText("Job 5 started")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/5");
  await user.click(screen.getByRole("button", { name: "Dismiss" }));
  expect(screen.queryByText("Job 5 started")).not.toBeInTheDocument();
});

test("info toasts close themselves and errors stay", () => {
  vi.useFakeTimers();
  try {
    renderToasts();
    act(() => screen.getByRole("button", { name: "info" }).click());
    act(() => screen.getByRole("button", { name: "error" }).click());
    act(() => vi.advanceTimersByTime(9000));
    expect(screen.queryByText("Job 5 started")).not.toBeInTheDocument();
    expect(screen.getByText("Boom")).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});
```

Add `act` to the `@testing-library/react` import.

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/shell/Toasts.test.tsx`
Expected: FAIL, cannot resolve `./Toasts`.

- [ ] **Step 3: Implement**

Create `web/src/shell/Toasts.tsx`:

```tsx
import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { Link } from "react-router";

export type ToastSpec = { text: string; link?: { to: string; label: string }; error?: boolean };
type Toast = ToastSpec & { id: number };

const ToastContext = createContext<(t: ToastSpec) => void>(() => {});

/** useToast returns a function that shows a toast. */
export const useToast = () => useContext(ToastContext);

let nextId = 0;

/** Toasts shows toasts for its children. Errors stay until dismissed. */
export function Toasts({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const dismiss = useCallback((id: number) => setList((l) => l.filter((t) => t.id !== id)), []);
  const show = useCallback(
    (t: ToastSpec) => {
      const id = ++nextId;
      setList((l) => [...l, { ...t, id }]);
      if (!t.error) setTimeout(() => dismiss(id), 8000);
    },
    [dismiss],
  );
  return (
    <ToastContext.Provider value={show}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className={t.error ? "toast error" : "toast"}>
            <span>{t.text}</span>
            {t.link && <Link to={t.link.to}>{t.link.label}</Link>}
            <button className="button" aria-label="Dismiss" onClick={() => dismiss(t.id)}>
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
```

In `web/src/shell/Shell.tsx`, import `Toasts` and wrap the outlet:

```tsx
      <main className="main">
        <Toasts>
          <Outlet />
        </Toasts>
      </main>
```

Append to `web/src/shell/Shell.css`:

```css
.toasts {
  position: fixed;
  right: calc(var(--space) * 2);
  bottom: calc(var(--space) * 2);
  z-index: 10;
  display: flex;
  flex-direction: column;
  gap: var(--space);
}

.toast {
  display: flex;
  align-items: center;
  gap: var(--space);
  padding: var(--space) calc(var(--space) * 1.5);
  background: var(--surface-2);
  border: 1px solid var(--border);
  border-radius: var(--radius);
}

.toast.error {
  border-color: var(--danger);
}
```

- [ ] **Step 4: Run the web tests**

Run: `cd web && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/shell
git commit -m "feat(web): add toasts"
```

---

### Task 9: Selection state

**Files:**
- Create: `web/src/cards/selection.ts`, `web/src/cards/selection.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/cards/selection.test.ts`:

```ts
import { emptySelection, pick, target } from "./selection";

const page = [1, 2, 3, 4, 5];

test("a click toggles one card", () => {
  let s = pick(emptySelection("k"), 2, false, page);
  expect(s.ids).toEqual([2]);
  s = pick(s, 2, false, page);
  expect(s.ids).toEqual([]);
});

test("shift-click selects from the last click, in page order", () => {
  let s = pick(emptySelection("k"), 4, false, page);
  s = pick(s, 2, true, page);
  expect(s.ids.sort()).toEqual([2, 3, 4]);
  // Shift-clicking a selected card clears the range from the last click.
  s = pick(s, 3, true, page);
  expect(s.ids).toEqual([4]);
});

test("shift-click with no anchor on this page toggles one card", () => {
  const s = pick({ ...emptySelection("k"), ids: [9], anchor: 9 }, 2, true, page);
  expect(s.ids).toEqual([9, 2]);
});

test("target sends picked IDs, or the search without paging", () => {
  const search = { text: "dwarf", filter: { and: [{ tag: "fantasy" }] }, page: { size: 48, number: 2 }, sort: "name" as const };
  expect(target({ ...emptySelection("k"), ids: [3, 1] }, search)).toEqual({ filter: { ids: [3, 1] } });
  expect(target({ ...emptySelection("k"), all: true }, search)).toEqual({ text: "dwarf", filter: search.filter });
  expect(target({ ...emptySelection("k"), all: true }, {})).toEqual({ filter: { archived: false } });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/cards/selection.test.ts`
Expected: FAIL, cannot resolve `./selection`.

- [ ] **Step 3: Implement**

Create `web/src/cards/selection.ts`:

```ts
import type { components } from "../api/schema";

type Search = components["schemas"]["Query"];

/** Selection is the picked cards, or every card the search matches. key is the search it belongs to. */
export type Selection = { key: string; ids: number[]; all: boolean; anchor: number | null };

export const emptySelection = (key: string): Selection => ({ key, ids: [], all: false, anchor: null });

/** pick toggles id. With shift, every card from the last click to id, in page order, takes id's new state. */
export function pick(s: Selection, id: number, shift: boolean, page: number[]): Selection {
  const on = !s.ids.includes(id);
  const from = s.anchor === null ? -1 : page.indexOf(s.anchor);
  const to = page.indexOf(id);
  const range = shift && from >= 0 && to >= 0 ? page.slice(Math.min(from, to), Math.max(from, to) + 1) : [id];
  const ids = on ? [...new Set([...s.ids, ...range])] : s.ids.filter((x) => !range.includes(x));
  return { ...s, ids, all: false, anchor: id };
}

/** target is the bulk query for a selection: the picked IDs, or the search without paging. */
export function target(s: Selection, search: Search): Search {
  if (!s.all) return { filter: { ids: s.ids } };
  if (search.text || search.filter) return { text: search.text, filter: search.filter };
  // /bulk refuses a query with no filter or text, so name every unarchived card.
  return { filter: { archived: false } };
}
```

- [ ] **Step 4: Run the test**

Run: `cd web && npx vitest run src/cards/selection.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/cards/selection.ts web/src/cards/selection.test.ts
git commit -m "feat(web): track picked cards"
```

---

### Task 10: Select boxes and the selection bar

**Files:**
- Create: `web/src/cards/SelectionBar.tsx`
- Modify: `web/src/cards/Tile.tsx`, `web/src/cards/Cards.tsx`, `web/src/cards/Cards.css`
- Test: `web/src/cards/Cards.test.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/cards/Cards.test.tsx`:

1. Add two hits after Brakka in `results.hits`:

```ts
    { id: 8, slug: "carrow", revision_id: 1, fields: { name: "Carrow" } },
    { id: 9, slug: "dellin", revision_id: 1, fields: { name: "Dellin" } },
```

2. Record writes and answer the new endpoints. Replace the `beforeEach` with:

```ts
let searches: unknown[];
let posts: { path: string; body: unknown }[];
beforeEach(() => {
  searches = [];
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/search") {
        searches.push(await req.json());
        return json(results);
      }
      if (path === "/api/bulk" || path === "/api/pushes") {
        posts.push({ path, body: await req.json() });
        return json({ id: 5, kind: "bulk", status: "waiting" });
      }
      if (path === "/api/instances")
        return json([
          { id: 2, name: "Lumiverse", plugin: "lumiverse" },
          { id: 3, name: "Cards folder", plugin: "folder" },
        ]);
      if (path === "/api/plugins")
        return json([
          { name: "lumiverse", capabilities: ["push", "pull"] },
          { name: "folder", capabilities: ["import"] },
        ]);
      if (path === "/api/collections") return json([{ id: 4, name: "Elves", cards: 0 }]);
      return json([]);
    }),
  );
});
```

3. Wrap the page in `Toasts` in `renderCards`:

```tsx
  const router = createMemoryRouter([{ path: "/cards", element: <Toasts><Cards /></Toasts> }], { initialEntries: [entry] });
```

and import `Toasts` from `"../shell/Toasts"`.

4. Append the tests:

```tsx
test("shift-click selects a range and tags it", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Brakka" }));
  await user.keyboard("{Shift>}");
  await user.click(screen.getByRole("checkbox", { name: "Select Dellin" }));
  await user.keyboard("{/Shift}");
  expect(screen.getByText("3 selected")).toBeInTheDocument();
  expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Tag" }));
  await user.type(screen.getByRole("textbox", { name: "Tags" }), "elf, sage");
  await user.click(screen.getByRole("button", { name: "Add tags" }));
  expect(posts).toEqual([{ path: "/api/bulk", body: { query: { filter: { ids: [7, 8, 9] } }, action: "tag", tags: ["elf", "sage"] } }]);
  expect(await screen.findByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/5");
  expect(screen.getByRole("searchbox")).toBeInTheDocument();
});

test("select all matching archives the search", async () => {
  renderCards("/cards?f=tag:fantasy");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Select all 49 matching" }));
  expect(screen.getByText("All 49 matching cards")).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Select Brakka" })).toBeChecked();
  await user.click(screen.getByRole("button", { name: "Archive" }));
  await user.click(screen.getByRole("button", { name: "Archive 49 cards" }));
  expect(posts[0]).toEqual({ path: "/api/bulk", body: { query: { filter: { and: [{ tag: "fantasy" }] } }, action: "archive" } });
});

test("push offers only targets that push", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Push" }));
  const target = await screen.findByRole("combobox", { name: "Target" });
  expect(Array.from((target as HTMLSelectElement).options).map((o) => o.text)).toEqual(["Lumiverse"]);
  await user.click(screen.getByRole("button", { name: "Push 1 card" }));
  expect(posts[0]).toEqual({ path: "/api/pushes", body: { instance_id: 2, query: { filter: { ids: [8] } } } });
});

test("a facet click clears the selection, and paging keeps it", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "2" }));
  expect(router.state.location.search).toBe("?page=2");
  expect(screen.getByText("1 selected")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /pirate/ }));
  expect(screen.getByRole("searchbox")).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Select Carrow" })).not.toBeChecked();
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/cards/Cards.test.tsx`
Expected: the new tests FAIL, no checkbox named "Select Brakka". The older tests pass.

- [ ] **Step 3: Add the select box to tiles and rows**

In `web/src/cards/Tile.tsx`, add a shared prop type and the box:

```tsx
export type PickProps = { selected: boolean; locked: boolean; onPick: (shift: boolean) => void };

// The change event of a checkbox comes from a click, so it carries the shift key.
function SelectBox({ hit, pick }: { hit: Hit; pick: PickProps }) {
  return (
    <input
      type="checkbox"
      className="select"
      aria-label={`Select ${str(hit.fields.name)}`}
      checked={pick.selected}
      disabled={pick.locked}
      onChange={(e) => pick.onPick((e.nativeEvent as MouseEvent).shiftKey === true)}
    />
  );
}
```

Give `Tile` and `Row` a `pick: PickProps` prop. In `Tile`, render `<SelectBox hit={hit} pick={pick} />` as the article's first child, and add `selected` to the article's class when `pick.selected`:

```tsx
export function Tile({ hit, names, pick }: { hit: Hit; names: Names; pick: PickProps }) {
  const tags = hit.tags ?? [];
  const books = hit.lorebooks?.length ?? 0;
  return (
    <article className={pick.selected ? "tile selected" : "tile"}>
      <SelectBox hit={hit} pick={pick} />
      {/* ...the rest of the tile is unchanged */}
    </article>
  );
}
```

In `Row`, add a first cell: `<td><SelectBox hit={hit} pick={pick} /></td>`.

- [ ] **Step 4: Write the selection bar**

Create `web/src/cards/SelectionBar.tsx`:

```tsx
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";

type Search = components["schemas"]["Query"];
type Bulk = components["schemas"]["BulkInBody"];
type Job = components["schemas"]["JobOut"];
type Panel = "tag" | "collection" | "push" | "archive" | "export" | null;

const labels = { tag: "Tag", collection: "Collection", push: "Push", archive: "Archive", export: "Export" };
const plural = (n: number) => `${n} ${n === 1 ? "card" : "cards"}`;

/** SelectionBar replaces the toolbar while cards are picked, and starts bulk jobs on them. */
export function SelectionBar(props: {
  count: number;
  all: boolean;
  total: number;
  query: Search;
  onAll: () => void;
  onClear: () => void;
}) {
  const { count, all, total, query, onAll, onClear } = props;
  const [panel, setPanel] = useState<Panel>(null);
  const [tags, setTags] = useState("");
  const [collection, setCollection] = useState<number>();
  const [instance, setInstance] = useState<number>();
  const [format, setFormat] = useState("json");
  const toast = useToast();
  const n = all ? total : count;

  const collections = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const plugins = useQuery({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });
  const pushers = new Set((plugins.data ?? []).filter((p) => p.capabilities?.includes("push")).map((p) => p.name));
  const targets = (instances.data ?? []).filter((i) => pushers.has(i.plugin));

  const start = useMutation({
    mutationFn: (call: () => Promise<Job>) => call(),
    onSuccess: (j) => {
      toast({ text: `Job ${j.id} started`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } });
      onClear();
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });
  const bulk = (body: Omit<Bulk, "query">) =>
    start.mutate(async () => must(await api.POST("/bulk", { body: { query, ...body } })));
  const collectionId = collection ?? collections.data?.[0]?.id ?? 0;
  const instanceId = instance ?? targets[0]?.id ?? 0;
  const tagList = () => tags.split(",").map((t) => t.trim()).filter(Boolean);

  const toggle = (p: Panel) => setPanel(panel === p ? null : p);
  return (
    <div className="toolbar selection-bar">
      <strong>{all ? `All ${total} matching cards` : `${count} selected`}</strong>
      {!all && count < total && (
        <button className="button" onClick={onAll}>
          Select all {total} matching
        </button>
      )}
      <button className="button" onClick={onClear}>
        Clear
      </button>
      <span className="actions">
        {(["tag", "collection", "push", "archive", "export"] as const).map((p) => (
          <button key={p} className={panel === p ? "button primary" : "button"} aria-expanded={panel === p} onClick={() => toggle(p)}>
            {labels[p]}
          </button>
        ))}
      </span>
      {panel && (
        <div className="bulk-panel">
          {panel === "tag" && (
            <>
              <input className="input" aria-label="Tags" placeholder="Tags, comma separated" value={tags} onChange={(e) => setTags(e.target.value)} />
              <button className="button primary" disabled={!tagList().length} onClick={() => bulk({ action: "tag", tags: tagList() })}>
                Add tags
              </button>
              <button className="button" disabled={!tagList().length} onClick={() => bulk({ action: "untag", tags: tagList() })}>
                Remove tags
              </button>
            </>
          )}
          {panel === "collection" && (
            <>
              <select className="input" aria-label="Collection" value={collectionId} onChange={(e) => setCollection(Number(e.target.value))}>
                {(collections.data ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
              <button className="button primary" onClick={() => bulk({ action: "collect", collection: collectionId })}>
                Add to collection
              </button>
              <button className="button" onClick={() => bulk({ action: "uncollect", collection: collectionId })}>
                Remove from collection
              </button>
            </>
          )}
          {panel === "push" &&
            (targets.length ? (
              <>
                <select className="input" aria-label="Target" value={instanceId} onChange={(e) => setInstance(Number(e.target.value))}>
                  {targets.map((i) => (
                    <option key={i.id} value={i.id}>
                      {i.name}
                    </option>
                  ))}
                </select>
                <button
                  className="button primary"
                  onClick={() =>
                    start.mutate(async () =>
                      must(await api.POST("/pushes", { body: { instance_id: instanceId, query } })),
                    )
                  }
                >
                  Push {plural(n)}
                </button>
              </>
            ) : (
              <span>No target can take pushes. Add one in Settings.</span>
            ))}
          {panel === "archive" && (
            <button className="button danger" onClick={() => bulk({ action: "archive" })}>
              Archive {plural(n)}
            </button>
          )}
          {panel === "export" && (
            <>
              <select className="input" aria-label="Format" value={format} onChange={(e) => setFormat(e.target.value)}>
                <option value="json">JSON</option>
                <option value="png">PNG</option>
              </select>
              <button className="button primary" onClick={() => bulk({ action: "export", format })}>
                Export {plural(n)}
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
```

If the generated body type names differ from `BulkInBody` and `JobOut`, use the names in `schema.d.ts`.

- [ ] **Step 5: Wire it into the Cards page**

In `web/src/cards/Cards.tsx`:

```tsx
import { emptySelection, pick, target, type Selection } from "./selection";
import { SelectionBar } from "./SelectionBar";
```

After `const q = parseQuery(params);`:

```tsx
  // The selection belongs to one search: paging keeps it, a new search clears it.
  const key = JSON.stringify([q.text, q.terms]);
  const [stored, setSel] = useState<Selection>(() => emptySelection(key));
  const sel = stored.key === key ? stored : emptySelection(key);
```

After `const hits = ...`:

```tsx
  const pageIds = hits.map((h) => h.id);
  const pickFor = (id: number) => ({
    selected: sel.all || sel.ids.includes(id),
    locked: sel.all,
    onPick: (shift: boolean) => setSel(pick(sel, id, shift, pageIds)),
  });
  const picking = sel.all || sel.ids.length > 0;
```

Render the bar in place of the toolbar while picking:

```tsx
      {picking ? (
        <SelectionBar
          count={sel.ids.length}
          all={sel.all}
          total={total}
          query={target(sel, body)}
          onAll={() => setSel({ ...sel, all: true })}
          onClear={() => setSel(emptySelection(key))}
        />
      ) : (
        <div className="toolbar">{/* ...the toolbar is unchanged */}</div>
      )}
```

Pass `pick={pickFor(h.id)}` to each `Tile` and `Row`.

- [ ] **Step 6: Style it**

Append to `web/src/cards/Cards.css`:

```css
.tile {
  position: relative;
}

.tile .select {
  position: absolute;
  top: calc(var(--space) * 0.75);
  left: calc(var(--space) * 0.75);
  z-index: 1;
  width: 18px;
  height: 18px;
}

.tile.selected .tile-image {
  outline: 2px solid var(--accent);
}

.selection-bar .actions {
  display: flex;
  gap: var(--space);
  margin-left: auto;
}

.bulk-panel {
  display: flex;
  flex-basis: 100%;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space);
}

.button.danger {
  background: var(--danger);
  color: var(--accent-text);
}
```

If `.toolbar` isn't `flex-wrap: wrap`, add it to `.selection-bar` so the panel drops to its own line. If `.tile` already sets `position`, keep the existing rule and drop the first block.

- [ ] **Step 7: Run the web tests and typecheck**

Run: `cd web && npm test && npm run typecheck`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/cards
git commit -m "feat(web): select cards and act on them in bulk"
```

---

### Task 11: Settings → Jobs

**Files:**
- Create: `web/src/settings/jobs.ts`, `web/src/settings/jobs.test.ts`, `web/src/settings/Settings.tsx`, `web/src/settings/Jobs.tsx`, `web/src/settings/Job.tsx`, `web/src/settings/Settings.css`, `web/src/settings/Jobs.test.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Write the failing helper tests**

Create `web/src/settings/jobs.test.ts`:

```ts
import { age, kindLabel, progressText, type Job } from "./jobs";

const job = (j: Partial<Job>): Job => ({ id: 1, kind: "bulk", status: "running", payload: {}, progress: {}, attempts: 1, max_attempts: 3, run_after: "", created_at: "", ...j }) as Job;

test("progressText shows children, then done of total", () => {
  expect(progressText(job({ children: { succeeded: 2, failed: 1, queued: 3 } }))).toBe("3 of 6");
  expect(progressText(job({ kind: "card.export", progress: { done: 4, total: 10 } }))).toBe("4 of 10");
  expect(progressText(job({ progress: {} }))).toBe("");
});

test("kindLabel names a bulk job's action", () => {
  expect(kindLabel(job({ payload: { action: "card.tag" } }))).toBe("bulk card.tag");
  expect(kindLabel(job({ kind: "target.push" }))).toBe("target.push");
});

test("age is short", () => {
  const now = Date.parse("2026-10-06T12:00:00Z");
  expect(age("2026-10-06T11:59:30Z", now)).toBe("30s");
  expect(age("2026-10-06T11:15:00Z", now)).toBe("45m");
  expect(age("2026-10-06T02:00:00Z", now)).toBe("10h");
  expect(age("2026-10-02T12:00:00Z", now)).toBe("4d");
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/settings/jobs.test.ts`
Expected: FAIL, cannot resolve `./jobs`.

- [ ] **Step 3: Implement the helpers**

Create `web/src/settings/jobs.ts`:

```ts
import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { components } from "../api/schema";

export type Job = components["schemas"]["JobOut"];

export const finished = (j: Job) => j.status === "succeeded" || j.status === "failed";

/** kindLabel is a job's kind, with the action for a bulk job. */
export function kindLabel(j: Job): string {
  const action = (j.payload as { action?: unknown } | null)?.action;
  return j.kind === "bulk" && typeof action === "string" ? `bulk ${action}` : j.kind;
}

/** progressText is "done of total" from a bulk job's children or a job's progress, or "". */
export function progressText(j: Job): string {
  if (j.children) {
    const counts = Object.values(j.children);
    const total = counts.reduce((a, b) => a + b, 0);
    return `${(j.children.succeeded ?? 0) + (j.children.failed ?? 0)} of ${total}`;
  }
  const p = j.progress as { done?: unknown; total?: unknown } | null;
  return typeof p?.done === "number" && typeof p.total === "number" ? `${p.done} of ${p.total}` : "";
}

/** age is how long ago t was: 30s, 45m, 10h, 4d. */
export function age(t: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - Date.parse(t)) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** useJobEvents keeps the ["job", id] query current from the job's event stream while live. */
export function useJobEvents(id: number, live: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!live) return;
    const es = new EventSource(`/api/jobs/${id}/events`);
    es.addEventListener("job", (e) => {
      const j = JSON.parse((e as MessageEvent<string>).data) as Job;
      qc.setQueryData(["job", id], j);
      if (finished(j)) {
        es.close();
        // Streams cut a reconcile's snapshot, so fetch the whole job once it's done.
        void qc.invalidateQueries({ queryKey: ["job", id] });
        void qc.invalidateQueries({ queryKey: ["jobs"] });
      }
    });
    return () => es.close();
  }, [id, live, qc]);
}
```

Run: `cd web && npx vitest run src/settings/jobs.test.ts`
Expected: PASS.

- [ ] **Step 4: Write the failing page tests**

Create `web/src/settings/Jobs.test.tsx`:

```tsx
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Job } from "./Job";
import { Jobs } from "./Jobs";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const bulkJob = {
  id: 5, kind: "bulk", status: "waiting", payload: { action: "card.tag" }, progress: {}, attempts: 0, max_attempts: 3,
  run_after: now, created_at: now, children: { succeeded: 1, queued: 1 },
};
const exportJob = {
  id: 6, kind: "card.export", status: "succeeded", payload: {}, progress: { done: 2, total: 2, blob: "cd".repeat(32) },
  attempts: 1, max_attempts: 3, run_after: now, created_at: now,
};

class FakeEventSource {
  static last: FakeEventSource | null = null;
  listener: ((e: { data: string }) => void) | null = null;
  closed = false;
  constructor(readonly url: string) {
    FakeEventSource.last = this;
  }
  addEventListener(_: string, f: (e: { data: string }) => void) {
    this.listener = f;
  }
  close() {
    this.closed = true;
  }
}

let requests: string[];
beforeEach(() => {
  requests = [];
  FakeEventSource.last = null;
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      requests.push(`${req.method} ${u.pathname}${u.search}`);
      if (u.pathname === "/api/jobs" && u.searchParams.get("parent") === "5")
        return json([{ ...bulkJob, id: 7, kind: "card.tag", status: "succeeded", children: undefined }]);
      if (u.pathname === "/api/jobs") return json([bulkJob, exportJob]);
      if (u.pathname === "/api/jobs/5/cancel") return json({ ...bulkJob, status: "failed" });
      if (u.pathname === "/api/jobs/5") return json(bulkJob);
      if (u.pathname === "/api/jobs/6") return json(exportJob);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/settings/jobs", element: <Jobs /> },
      { path: "/settings/jobs/:id", element: <Toasts><Job /></Toasts> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("the list shows jobs and filters by state", async () => {
  const router = renderAt("/settings/jobs");
  expect(await screen.findByText("bulk card.tag")).toBeInTheDocument();
  expect(screen.getByText("card.export")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.selectOptions(screen.getByRole("combobox", { name: "State" }), "failed");
  expect(router.state.location.search).toBe("?status=failed");
  expect(requests).toContain("GET /api/jobs?status=failed");
});

test("a bulk job shows its children, follows events, and cancels", async () => {
  renderAt("/settings/jobs/5");
  expect(await screen.findByText("1 of 2")).toBeInTheDocument();
  expect(await screen.findByText("card.tag")).toBeInTheDocument();
  expect(FakeEventSource.last?.url).toBe("/api/jobs/5/events");

  act(() => FakeEventSource.last!.listener!({ data: JSON.stringify({ ...bulkJob, status: "running", children: { succeeded: 2 } }) }));
  expect(screen.getByText("2 of 2")).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  expect(requests).toContain("POST /api/jobs/5/cancel");
  expect(await screen.findByText("failed", { selector: "dd" })).toBeInTheDocument();
});

test("a finished export links to its zip and opens no stream", async () => {
  renderAt("/settings/jobs/6");
  const link = await screen.findByRole("link", { name: "Download zip" });
  expect(link).toHaveAttribute("href", `/api/blobs/${"cd".repeat(32)}`);
  expect(FakeEventSource.last).toBeNull();
  expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
});
```

- [ ] **Step 5: Run them to see them fail**

Run: `cd web && npx vitest run src/settings/Jobs.test.tsx`
Expected: FAIL, cannot resolve `./Job`.

- [ ] **Step 6: Write the pages**

Create `web/src/settings/Settings.tsx`:

```tsx
import { NavLink, Outlet } from "react-router";
import "./Settings.css";

/** Settings holds the settings tabs. Plugins, Access, and Appearance come in a later phase. */
export function Settings() {
  return (
    <div className="settings">
      <nav className="tabs" aria-label="Settings">
        <NavLink to="/settings/jobs">Jobs</NavLink>
      </nav>
      <Outlet />
    </div>
  );
}
```

Create `web/src/settings/Jobs.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { age, finished, kindLabel, progressText, type Job } from "./jobs";

const states: Job["status"][] = ["queued", "running", "waiting", "succeeded", "failed"];

export function Jobs() {
  const [params, setParams] = useSearchParams();
  const status = states.find((s) => s === params.get("status"));
  const list = useQuery({
    queryKey: ["jobs", status],
    queryFn: async () => must(await api.GET("/jobs", { params: { query: status ? { status } : {} } })),
    // ponytail: polls while jobs run; a stream per row would use up the browser's six connections per host.
    refetchInterval: (q) => (q.state.data?.some((j) => !finished(j)) ? 2000 : false),
  });
  return (
    <section>
      <div className="toolbar">
        <select
          className="input"
          aria-label="State"
          value={status ?? ""}
          onChange={(e) => setParams(e.target.value ? { status: e.target.value } : {})}
        >
          <option value="">All states</option>
          {states.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>
      </div>
      {list.isError && <p className="error">{list.error.message}</p>}
      <table className="jobs">
        <thead>
          <tr>
            <th>Job</th>
            <th>Kind</th>
            <th>State</th>
            <th>Progress</th>
            <th>Age</th>
          </tr>
        </thead>
        <tbody>
          {(list.data ?? []).map((j) => (
            <tr key={j.id}>
              <td>
                <Link to={`/settings/jobs/${j.id}`}>#{j.id}</Link>
              </td>
              <td>{kindLabel(j)}</td>
              <td className={"state " + j.status}>{j.status}</td>
              <td>{progressText(j)}</td>
              <td>{age(j.created_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
```

Create `web/src/settings/Job.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { useToast } from "../shell/Toasts";
import { age, finished, kindLabel, progressText, useJobEvents, type Job as JobT } from "./jobs";

function Counts({ label, counts }: { label: string; counts?: Record<string, number> }) {
  if (!counts || !Object.keys(counts).length) return null;
  return (
    <p className="counts">
      {label}:{" "}
      {Object.entries(counts).map(([k, n]) => (
        <span key={k} className="chip">
          {k} {n}
        </span>
      ))}
    </p>
  );
}

export function Job() {
  const id = Number(useParams().id);
  const qc = useQueryClient();
  const toast = useToast();
  const job = useQuery({
    queryKey: ["job", id],
    queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id } } })),
  });
  const j = job.data;
  useJobEvents(id, !!j && !finished(j));

  const [childStatus, setChildStatus] = useState<JobT["status"] | "">("");
  const children = useQuery({
    enabled: j?.kind === "bulk",
    // Keyed on the counts, so the list refetches as children finish.
    queryKey: ["jobs", "children", id, childStatus, j?.children],
    queryFn: async () =>
      must(await api.GET("/jobs", { params: { query: { parent: id, ...(childStatus ? { status: childStatus } : {}) } } })),
  });
  const cancel = useMutation({
    mutationFn: async () => must(await api.POST("/jobs/{id}/cancel", { params: { path: { id } } })),
    onSuccess: (data) => qc.setQueryData(["job", id], data),
    onError: (e) => toast({ text: e.message, error: true }),
  });

  if (job.isError) return <p className="error">{job.error.message}</p>;
  if (!j) return null;
  const blob = j.kind === "card.export" ? (j.progress as { blob?: string } | null)?.blob : undefined;
  return (
    <section className="job">
      <p>
        <Link to="/settings/jobs">All jobs</Link>
      </p>
      <h1>Job #{j.id}</h1>
      <dl>
        <dt>Kind</dt>
        <dd>{kindLabel(j)}</dd>
        <dt>State</dt>
        <dd className={"state " + j.status}>{j.status}</dd>
        <dt>Progress</dt>
        <dd>{progressText(j)}</dd>
        <dt>Age</dt>
        <dd>{age(j.created_at)}</dd>
        {j.error && (
          <>
            <dt>Error</dt>
            <dd className="error">{j.error}</dd>
          </>
        )}
      </dl>
      {!finished(j) && (
        <button className="button danger" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
          Cancel
        </button>
      )}
      {blob && (
        <a className="button primary" href={`/api/blobs/${blob}`} download="innkeeper-cards.zip">
          Download zip
        </a>
      )}
      <Counts label="Children" counts={j.children} />
      <Counts label="Outcomes" counts={j.outcomes} />
      {j.kind === "bulk" && (
        <>
          <select
            className="input"
            aria-label="Child state"
            value={childStatus}
            onChange={(e) => setChildStatus(e.target.value as JobT["status"] | "")}
          >
            <option value="">All children</option>
            {["queued", "running", "succeeded", "failed"].map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          <table className="jobs">
            <tbody>
              {(children.data ?? []).map((c) => (
                <tr key={c.id}>
                  <td>#{c.id}</td>
                  <td>{c.kind}</td>
                  <td className={"state " + c.status}>{c.status}</td>
                  <td className="error">{c.error}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
```

The child list holds the job's first 50 children, the API's default page. Paging through more waits until someone needs it.

Create `web/src/settings/Settings.css`:

```css
.settings {
  padding: calc(var(--space) * 2);
}

.tabs {
  display: flex;
  gap: calc(var(--space) * 2);
  margin-bottom: calc(var(--space) * 2);
  border-bottom: 1px solid var(--border);
}

.tabs a {
  padding: var(--space) 0;
  color: var(--text-muted);
  text-decoration: none;
}

.tabs a.active {
  color: var(--text);
  border-bottom: 2px solid var(--accent);
}

.jobs {
  width: 100%;
  border-collapse: collapse;
}

.jobs th,
.jobs td {
  padding: calc(var(--space) * 0.75);
  border-bottom: 1px solid var(--border);
  text-align: left;
}

.state.succeeded { color: var(--ok); }
.state.failed { color: var(--danger); }
.state.running { color: var(--ahead); }

.job dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: calc(var(--space) * 0.5) calc(var(--space) * 2);
}

.job dd {
  margin: 0;
}
```

If `.chip` lives only in `Cards.css`, the counts still render; they just lose the chip style on this page.

- [ ] **Step 7: Add the routes**

In `web/src/App.tsx`, import `Settings`, `Jobs`, and `Job`, and replace the settings route:

```tsx
          {
            path: "settings",
            element: <Settings />,
            children: [
              { index: true, element: <Navigate to="jobs" replace /> },
              { path: "jobs", element: <Jobs /> },
              { path: "jobs/:id", element: <Job /> },
            ],
          },
```

- [ ] **Step 8: Run the web checks**

Run: `cd web && npm test && npm run build`
Expected: PASS, and the build writes `web/dist`.

- [ ] **Step 9: Commit**

```bash
git add web/src/settings web/src/App.tsx
git commit -m "feat(web): add Settings → Jobs"
```

---

### Task 12: Check it end to end

- [ ] **Step 1: Run every check CI runs**

```bash
go build ./... && go vet ./... && go test ./...
cd web && npm test && npm run build && npm run gen && git diff --exit-code openapi.json src/api/schema.d.ts
```

Expected: all pass, and regenerating the schema changes nothing.

- [ ] **Step 2: Try it in a browser**

Start `innkeeper api` and `innkeeper worker` against a local database with a few cards, and `npm run dev` in `web/`. Then:

1. Pick two cards, shift-click a third, and add a tag. The toast links to the job, and the tag shows on the tiles after a refresh.
2. Select all matching under a tag filter and export as JSON. The job page shows progress, then "Download zip", and the zip holds one file per card.
3. Start an archive of many cards and cancel it from the job page. The state turns `failed` and unfinished children stop.
4. Filter Settings → Jobs by `failed`.

- [ ] **Step 3: Fix anything found, then commit**

```bash
git add -A
git commit -m "fix(web): <what the browser check found>"
```

Skip this step if nothing needed fixing.
