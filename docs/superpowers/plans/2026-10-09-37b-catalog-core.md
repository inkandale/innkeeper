# 37b: Catalog core — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The API lists, searches, and reads catalogs, adopts their items into the library, queues ingest jobs, finds a library card or lorebook in every catalog, and lets `POST /search` take `catalog:<id>` sources.

**Architecture:** A new `internal/api/catalogs.go` holds the catalog routes. One helper, `catalogCall`, sends every catalog call with a 30-second timeout and maps catalog errors to HTTP ones. `Entities.MatchHashes` in `internal/library/similar.go` badges search results with library matches in one query per kind. Ingest jobs run in the importer, which already routes `progress` and `checkpoint` notifications by run token. Adopt reuses `Library.ImportCard` and `Entities.Import`, whose dedupe on provenance and content makes a repeat adopt return the existing entry.

**Tech Stack:** Go, Huma v2, PostgreSQL (pgx).

Spec: `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, build step 2. Plan 37a added the protocol, the fake catalog, and the contract checks.

**Changes from the spec.** Task 9 updates the spec to match:

- Adopt takes `version` as a query parameter. It answers 201 with a new entry, or 200 with the one an earlier adopt of the same version made.
- `GET /catalogs/{id}/search` takes `filters` as one query parameter holding a JSON object.
- `POST /search` returns catalog results in a `catalogs` list beside the library hits, one entry per `catalog:<id>` source. A catalog matches on `text` only and returns its first page.
- Each catalog search result lists its library matches in `library`. Like `/cards/{id}/similar`, matches compare head revisions.
- A catalog takes one ingest at a time. A second request returns 409 while the first is unfinished.
- A catalog that won't start, exits, takes over 30 seconds, or returns a retryable error gives 503. A broken instance gives 409, as on every other route.

**Branch:** `inn-37b-catalog-core`, in the worktree `../innkeeper-inn-37b`. **Do not** `git checkout` other branches or move HEAD.

**Database tests** skip without `INNKEEPER_TEST_DATABASE_URL`, and a skipped run looks green. Run every test command in this plan with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres` set.

## Files

| File | Change |
|---|---|
| `internal/library/similar.go`, `similar_test.go` | `NearDistance`, `Hashes`, `HashMatch`, `Entities.MatchHashes` |
| `internal/fakeplugin/catalog.go`, `main.go` | `catalog.ingest`, and the `down` and `no_ingest` config keys |
| `internal/importer/importer.go`, `importer_test.go` | `IngestKind`, `RunIngest` |
| `cmd/innkeeper/worker.go` | Runs `IngestKind` |
| `internal/jobs/queue.go` | `ListOptions.Instance` |
| `internal/api/catalogs.go`, `catalogs_test.go` (new) | Catalog routes, adopt, catalog matches |
| `internal/api/server.go` | Registers the catalog routes |
| `internal/api/jobs.go` | `progress` doc covers ingests |
| `internal/api/search.go`, `internal/library/search.go` | `catalog:<id>` sources |
| `internal/mcp/tools.go` | The search tool mentions catalog sources |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `docs/superpowers/specs/2026-10-09-archive-catalog-design.md` | The changes above |

---

### Task 1: Library hash matches

Search results need to show which library entries match each catalog item. `SimilarCards` compares one library card with the rest. `MatchHashes` compares a page of outside hashes with the library in one query.

**Files:**
- Modify: `internal/library/similar.go`
- Test: `internal/library/similar_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/library/similar_test.go`, and add `"strings"` to its imports:

```go
func TestMatchHashes(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	const text = "Brakka is a grumpy dwarf blacksmith who runs the forge at the edge of town. " +
		"She hates elves, loves good ale, and never turns away a customer with honest coin."
	orig, rev := newCard(t, lib, "orig", `{"name": "Brakka", "description": "`+text+`"}`)
	edited, _ := newCard(t, lib, "edited", `{"name": "Brakka 2", "description": "`+text+` She has a cat."}`)
	newCard(t, lib, "elf", `{"name": "Lia", "description": "A cheerful elven bard wanders the coast singing songs of the sea."}`)
	r, err := lib.Cards.Revision(ctx, rev)
	if err != nil {
		t.Fatal(err)
	}

	got, err := lib.Cards.MatchHashes(ctx, []Hashes{
		{ContentHash: r.ContentHash, TextSimhash: r.TextSimhash},
		{ContentHash: strings.Repeat("0", 64)},
	}, 10)
	if err != nil {
		t.Fatal(err)
	}
	// orig matches by content and by text, and is listed once, by content.
	if len(got) != 2 || len(got[0]) != 2 || got[0][0] != (HashMatch{orig, "content", 0}) ||
		got[0][1].ID != edited || got[0][1].Reason != "text" || len(got[1]) != 0 {
		t.Fatalf("matches = %+v", got)
	}
	if got, err := lib.Lorebooks.MatchHashes(ctx, []Hashes{{ContentHash: r.ContentHash}}, 10); err != nil || len(got[0]) != 0 {
		t.Fatalf("lorebook matches = %+v, %v", got, err)
	}
	if got, err := lib.Cards.MatchHashes(ctx, nil, 10); err != nil || len(got) != 0 {
		t.Fatalf("no hashes = %+v, %v", got, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/library -run TestMatchHashes`
Expected: FAIL, `undefined: Hashes`.

- [ ] **Step 3: Write `MatchHashes`**

In `internal/library/similar.go`, change the imports to:

```go
import (
	"cmp"
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
)
```

and append:

```go
// NearDistance is the most differing hash bits that count as a near match.
// GET /cards/{id}/similar defaults to the same.
const NearDistance = 8

// Hashes are an outside item's fingerprints, as cardhash computes them.
type Hashes struct {
	ContentHash string
	TextSimhash *int64
	ImagePhash  *int64
}

// HashMatch is a library card or lorebook that matches an outside item.
type HashMatch struct {
	ID       int64  `json:"id"`
	Reason   string `json:"reason" enum:"content,text,avatar"`
	Distance int    `json:"distance"`
}

// MatchHashes returns, for each of hashes, the entities whose head revision
// has the same content_hash, or a text_simhash or avatar phash within
// maxDistance bits, closest first. An entity that matches more than one way
// is listed once, by its closest reason.
//
// ponytail: scans every head revision for every item, as SimilarCards does;
// index the hashes if libraries outgrow a scan.
func (s *Entities) MatchHashes(ctx context.Context, hashes []Hashes, maxDistance int) ([][]HashMatch, error) {
	out := make([][]HashMatch, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	content := make([]string, len(hashes))
	text := make([]*int64, len(hashes))
	image := make([]*int64, len(hashes))
	for i, h := range hashes {
		content[i], text[i], image[i] = h.ContentHash, h.TextSimhash, h.ImagePhash
	}
	rows, err := s.pool.Query(ctx, `
		WITH q AS (
			SELECT * FROM unnest($1::text[], $2::bigint[], $3::bigint[])
				WITH ORDINALITY AS q (content_hash, text_simhash, image_phash, i)
		),
		heads AS (
			SELECT e.id, r.content_hash, r.text_simhash, b.avatar_phash
			FROM `+s.k.table+` e
			JOIN `+s.k.revTable+` r ON r.id = e.head_revision_id
			LEFT JOIN blob b ON b.sha256 = r.avatar_blob
		)
		SELECT DISTINCT ON (i, id) i::int, id, reason, distance FROM (
			SELECT q.i, h.id, 'content' AS reason, 0 AS distance
			FROM q JOIN heads h ON h.content_hash = q.content_hash
			UNION ALL
			SELECT q.i, h.id, 'text', bit_count((h.text_simhash # q.text_simhash)::bit(64))::int
			FROM q JOIN heads h ON bit_count((h.text_simhash # q.text_simhash)::bit(64)) <= $4
			UNION ALL
			SELECT q.i, h.id, 'avatar', bit_count((h.avatar_phash # q.image_phash)::bit(64))::int
			FROM q JOIN heads h ON bit_count((h.avatar_phash # q.image_phash)::bit(64)) <= $4
		) m
		ORDER BY i, id, distance, reason <> 'content'`, content, text, image, maxDistance)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var i int
		var m HashMatch
		if err := rows.Scan(&i, &m.ID, &m.Reason, &m.Distance); err != nil {
			return nil, err
		}
		// WITH ORDINALITY counts from 1.
		out[i-1] = append(out[i-1], m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, ms := range out {
		slices.SortStableFunc(ms, func(a, b HashMatch) int { return cmp.Compare(a.Distance, b.Distance) })
	}
	return out, nil
}
```

`DISTINCT ON (i, id)` keeps the first row of each pair in `ORDER BY` order: the smallest distance, with `content` ahead of a `text` or `avatar` match at distance 0.

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/library -run 'TestMatchHashes|TestSimilarCards'`
Expected: PASS, not SKIP.

- [ ] **Step 5: Commit**

```bash
git add internal/library/similar.go internal/library/similar_test.go
git commit -m "feat(library): match outside hashes against the library"
```

---

### Task 2: Fake catalog ingest and outages

The API and importer tests need a catalog that ingests, one that is down, and one that doesn't ingest. The fake reads all three from its instance config, so each test instance gets its own process.

**Files:** Modify `internal/fakeplugin/catalog.go` and `internal/fakeplugin/main.go`.

- [ ] **Step 1: Document the config keys**

In `internal/fakeplugin/catalog.go`, append to the comment above `var catalogInfo`:

```go
// Config down fails every call but describe with a retryable error, and
// no_ingest describes a catalog that doesn't ingest.
```

- [ ] **Step 2: Add `catalogIngest`**

Append to `internal/fakeplugin/catalog.go`:

```go
// catalogIngest walks the entries, one checkpoint each, and counts them all
// as added. Config crash_ingest_at exits after that entry's checkpoint on a
// first attempt, and emit_during_ingest sends a card the core must drop.
func catalogIngest(conn *protocol.Conn, config map[string]any, params json.RawMessage) (any, error) {
	var p protocol.CatalogIngestParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalidParams(err)
	}
	var cp struct {
		Next int `json:"next"`
	}
	resumed := len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null"
	if resumed {
		_ = json.Unmarshal(p.Checkpoint, &cp)
	}
	if config["emit_during_ingest"] == true {
		_ = conn.Notify(protocol.NotifyEmitCard, protocol.EmitCard{Run: p.Run, Item: "smuggled",
			Card: json.RawMessage(catalogEntries[0].versions[0])})
	}
	var res protocol.CatalogIngestResult
	total := int64(len(catalogEntries))
	for i := cp.Next; i < len(catalogEntries); i++ {
		res.Added++
		_ = conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: int64(i + 1), Total: total})
		next, _ := json.Marshal(map[string]int{"next": i + 1})
		_ = conn.Notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: next})
		if at, ok := config["crash_ingest_at"].(float64); ok && int(at) == i && !resumed {
			os.Exit(5)
		}
	}
	return res, nil
}
```

- [ ] **Step 3: Wire it into the handler**

In `internal/fakeplugin/main.go`, insert as the first statement of the `handler` func, before `switch method {`:

```go
		if config["down"] == true && strings.HasPrefix(method, "catalog.") && method != protocol.MethodCatalogDescribe {
			return nil, protocol.NewError(-32000, "archive unavailable", true)
		}
```

Replace the `case protocol.MethodCatalogDescribe:` block with:

```go
		case protocol.MethodCatalogDescribe:
			if *mode == "bad-catalog" {
				return protocol.CatalogInfo{}, nil
			}
			info := catalogInfo
			info.Ingest = config["no_ingest"] != true
			return info, nil
		case protocol.MethodCatalogIngest:
			return catalogIngest(conn, config, params)
```

- [ ] **Step 4: Build and run the contract tests**

Run: `go vet ./internal/fakeplugin && go test ./internal/contract`
Expected: PASS. The contract kit doesn't check `ingest`, so its check counts don't change.

- [ ] **Step 5: Commit**

```bash
git add internal/fakeplugin
git commit -m "test: let the fake catalog ingest, go down, or refuse ingest"
```

---

### Task 3: Ingest jobs

`catalog.ingest` sends the same `progress` and `checkpoint` notifications as `import.run`, and the worker's plugin manager already sends those to `Importer.Handle`. So the importer runs ingests too. An ingest session drops `emit.card` and `emit.lorebook`: a catalog keeps its items, and only adopt brings one into the library.

**Files:**
- Modify: `internal/importer/importer.go`, `cmd/innkeeper/worker.go`
- Test: `internal/importer/importer_test.go`

- [ ] **Step 1: Let the test setup run a catalog**

In `internal/importer/importer_test.go`, replace the `setup` doc comment and signature line:

```go
// setup runs a worker with the importer against a fake import plugin started
// with args.
func setup(t *testing.T, args ...string) *env {
	t.Helper()
```

with:

```go
// setup runs a worker with the importer against a fake import plugin started
// with args.
func setup(t *testing.T, args ...string) *env {
	t.Helper()
	return setupCaps(t, []string{"import"}, args...)
}

// setupCaps is setup with a plugin that has caps.
func setupCaps(t *testing.T, caps []string, args ...string) *env {
	t.Helper()
```

In its body, change `testutil.FakePluginWith(t, []string{"import"}, args...)` to `testutil.FakePluginWith(t, caps, args...)`, and the worker's `Handlers: map[string]jobs.Handler{Kind: im.Run}` to `Handlers: map[string]jobs.Handler{Kind: im.Run, IngestKind: im.RunIngest}`.

Replace the body of `run` from its `for deadline` loop to the end with a call to a new `wait`, so `run` reads:

```go
// run imports with params and returns the finished job and its progress.
func (e *env) run(t *testing.T, params string) (jobs.Job, progress) {
	t.Helper()
	j, err := e.q.Enqueue(context.Background(), jobs.Spec{Kind: Kind, InstanceID: &e.inst, Payload: Payload{Params: json.RawMessage(params)}})
	if err != nil {
		t.Fatal(err)
	}
	j = e.wait(t, j.ID)
	var p progress
	if err := json.Unmarshal(j.Progress, &p); err != nil {
		t.Fatal(err)
	}
	return j, p
}

// wait returns the job once it finishes.
func (e *env) wait(t *testing.T, id int64) jobs.Job {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		j, err := e.q.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			return j
		}
	}
	t.Fatalf("job %d still running", id)
	return jobs.Job{}
}
```

- [ ] **Step 2: Write the failing test**

Append to `internal/importer/importer_test.go`:

```go
func TestCatalogIngestResumes(t *testing.T) {
	e := setupCaps(t, []string{"catalog"})
	ctx := context.Background()
	inst, err := e.reg.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: "Archive",
		Config: map[string]any{"crash_ingest_at": 1, "emit_during_ingest": true}})
	if err != nil {
		t.Fatal(err)
	}
	j, err := e.q.Enqueue(ctx, jobs.Spec{Kind: IngestKind, InstanceID: &inst})
	if err != nil {
		t.Fatal(err)
	}
	j = e.wait(t, j.ID)
	if j.Status != jobs.StatusSucceeded || j.Attempts != 2 {
		t.Fatalf("job %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
	var p ingestProgress
	if err := json.Unmarshal(j.Progress, &p); err != nil {
		t.Fatal(err)
	}
	// The retry starts after entry 1's checkpoint, so it adds only entry 2.
	if p.Done != 3 || p.Total != 3 || p.CatalogIngestResult == nil || p.Added != 1 {
		t.Fatalf("progress = %s", j.Progress)
	}
	if n := e.count(t, "card"); n != 0 {
		t.Fatalf("ingest stored %d cards", n)
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./internal/importer -run TestCatalogIngestResumes`
Expected: FAIL, `undefined: IngestKind`.

- [ ] **Step 4: Add `IngestKind` and `RunIngest`**

In `internal/importer/importer.go`, replace the package comment with:

```go
// Package importer runs import jobs: it calls an import plugin's import.run
// and stores the cards and lorebooks the plugin emits. It also runs catalog
// ingests, which send the same progress and checkpoint notifications.
```

After the `Kind` const, add:

```go
// IngestKind is the job kind for catalog ingests. The job's instance is the
// catalog plugin instance, and its payload is empty.
const IngestKind = "catalog.ingest"
```

After the `progress` type, add:

```go
// ingestProgress is a catalog ingest's progress: Done, Total, and Message
// from the plugin, and once it finishes, the counts catalog.ingest returns.
type ingestProgress struct {
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
	*protocol.CatalogIngestResult
}
```

In the `session` struct, after `plugin string`, add:

```go
	// ingest marks a catalog.ingest run, which stores no items.
	ingest bool
```

and after `p   progress`, add:

```go
	// result is what catalog.ingest returned.
	result *protocol.CatalogIngestResult
```

Replace `Run`, from its doc comment through the line `	err = im.Plugins.Call(ctx, inst, protocol.MethodImportRun,` and the `protocol.ImportRunParams{...}, nil)` line after it, with:

```go
// Run is the jobs.Handler for Kind.
func (im *Importer) Run(ctx context.Context, r *jobs.Run) error { return im.run(ctx, r, false) }

// RunIngest is the jobs.Handler for IngestKind.
func (im *Importer) RunIngest(ctx context.Context, r *jobs.Run) error { return im.run(ctx, r, true) }

// run calls import.run, or catalog.ingest if ingest is set, on the job's
// instance.
func (im *Importer) run(ctx context.Context, r *jobs.Run, ingest bool) error {
	if r.InstanceID == nil {
		return fmt.Errorf("%s job has no plugin instance", r.Kind)
	}
	inst, err := im.Registry.Instance(ctx, *r.InstanceID)
	if err != nil {
		return err
	}
	var payload Payload
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		return fmt.Errorf("%s payload: %w", r.Kind, err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	s := &session{im: im, run: r, plugin: inst.Manifest.Name, ingest: ingest, ctx: ctx, cancel: cancel}
	// ponytail: items re-sent after a resume count as skipped a second time.
	_ = json.Unmarshal(r.Progress, &s.p)

	// The token includes the attempt, so notifications from an abandoned
	// earlier attempt can't reach this one.
	token := fmt.Sprintf("%d.%d", r.ID, r.Attempts)
	im.mu.Lock()
	im.runs[token] = s
	im.mu.Unlock()
	defer func() {
		im.mu.Lock()
		delete(im.runs, token)
		im.mu.Unlock()
	}()

	// ponytail: a run abandoned on shutdown or a lost lease keeps going in
	// the plugin until it finishes or the plugin is stopped.
	if ingest {
		var res protocol.CatalogIngestResult
		err = im.Plugins.Call(ctx, inst, protocol.MethodCatalogIngest,
			protocol.CatalogIngestParams{Run: token, Checkpoint: r.Checkpoint}, &res)
		if err == nil {
			s.mu.Lock()
			s.result = &res
			s.mu.Unlock()
			s.publish()
		}
	} else {
		err = im.Plugins.Call(ctx, inst, protocol.MethodImportRun,
			protocol.ImportRunParams{Run: token, Params: payload.Params, Checkpoint: r.Checkpoint}, nil)
	}
```

The rest of the old `Run`, from `if serr := s.failure(); serr != nil {` on, stays as the end of `run`.

At the top of `session.handle`, after the `if s.ctx.Err() != nil { return }` block, add:

```go
	if s.ingest && (method == protocol.NotifyEmitCard || method == protocol.NotifyEmitLorebook) {
		s.im.Log.Warn("catalog sent an item during ingest; only adopt adds catalog items", "plugin", s.plugin, "method", method)
		return
	}
```

Replace `publish` with:

```go
func (s *session) publish() {
	s.mu.Lock()
	var raw []byte
	if s.ingest {
		raw, _ = json.Marshal(ingestProgress{Done: s.p.Done, Total: s.p.Total, Message: s.p.Message, CatalogIngestResult: s.result})
	} else {
		raw, _ = json.Marshal(s.p)
	}
	s.mu.Unlock()
	s.run.SetProgress(raw)
}
```

- [ ] **Step 5: Run the importer tests**

Run: `go test ./internal/importer`
Expected: PASS, including the existing import tests.

- [ ] **Step 6: Run ingests in the worker**

In `cmd/innkeeper/worker.go`, add after `importer.Kind:              im.Run,`:

```go
			importer.IngestKind:        im.RunIngest,
```

Run: `gofmt -l cmd internal && go build ./...`
Expected: no output.

- [ ] **Step 7: Commit**

```bash
git add internal/importer cmd/innkeeper/worker.go
git commit -m "feat(importer): run catalog ingest jobs"
```

---

### Task 4: List jobs by instance

`GET /catalogs` shows each catalog's newest ingest, and the ingest route refuses a second one while the first is unfinished. Both look up jobs by instance.

**Files:** Modify `internal/jobs/queue.go`.

- [ ] **Step 1: Add `Instance`**

In `ListOptions`, after `Kind   string`, add:

```go
	// Instance lists only that plugin instance's jobs; 0 lists every one's.
	Instance int64
```

In `List`, replace the query and its arguments with:

```go
	rows, err := q.pool.Query(ctx, `SELECT `+jobCols+` FROM job
		WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR kind = $2)
			AND parent_id IS NOT DISTINCT FROM $3::bigint AND ($4::bigint = 0 OR id < $4)
			AND ($6::bigint = 0 OR instance_id = $6)
		ORDER BY id DESC LIMIT $5`, o.Status, o.Kind, o.Parent, o.Before, o.Limit, o.Instance)
```

- [ ] **Step 2: Run the job tests**

Run: `go test ./internal/jobs`
Expected: PASS. Task 5's `GET /catalogs` test covers the filter: of two catalogs, only the one with an ingest shows one.

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/queue.go
git commit -m "feat(jobs): list one instance's jobs"
```

---

### Task 5: Catalog routes

**Files:**
- Create: `internal/api/catalogs.go`, `internal/api/catalogs_test.go`
- Modify: `internal/api/server.go`, `internal/api/jobs.go`

- [ ] **Step 1: Write the failing tests**

`internal/api/catalogs_test.go`:

```go
package api

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// catalog adds an instance of the fake plugin as a catalog, with config.
func (e *testEnv) catalog(name string, config map[string]any) int64 {
	e.t.Helper()
	if e.manifests["catalog"] == nil {
		m := *e.manifests["fake"]
		m.Name, m.Capabilities = "catalog", []string{"catalog"}
		m.Command = []string{m.Command[0], "-caps=catalog", "-name=catalog"}
		e.manifests["catalog"] = &m
	}
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "catalog", Name: name, Config: config})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func catalogIDs(items []catalogHit) string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return strings.Join(ids, ",")
}

func TestCatalogRoutes(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	down := e.catalog("Down", map[string]any{"down": true})
	broken := e.catalog("Broken", nil)
	e.markBroken(broken, "bad config")
	base := fmt.Sprintf("/catalogs/%d", inst)

	var list []catalogOut
	e.call("GET", "/catalogs", nil, 200, &list)
	got := map[int64]catalogOut{}
	for _, c := range list {
		got[c.ID] = c
	}
	if len(list) != 3 || got[inst].Name != "Archive" || got[inst].Info == nil || !got[inst].Info.Ingest || got[inst].LastIngest != nil {
		t.Fatalf("catalogs = %+v", list)
	}
	// describe doesn't reach the source, so a catalog that is down still answers it.
	if got[down].Info == nil || got[broken].Info != nil || !strings.Contains(got[broken].Error, "broken") {
		t.Fatalf("down = %+v, broken = %+v", got[down], got[broken])
	}

	var page catalogResults
	e.call("GET", base+"/search?kind=card&size=1", nil, 200, &page)
	if catalogIDs(page.Items) != "brakka" || page.Next == "" || page.CatalogID != inst || page.Items[0].Library == nil {
		t.Fatalf("first page = %+v", page)
	}
	e.call("GET", base+"/search?kind=card&size=1&cursor="+page.Next, nil, 200, &page)
	if catalogIDs(page.Items) != "carrow" || page.Next != "" {
		t.Fatalf("second page = %+v", page)
	}
	e.call("GET", base+"/search?filters="+url.QueryEscape(`{"tag": "dwarf"}`), nil, 200, &page)
	if catalogIDs(page.Items) != "brakka,forge-lore" {
		t.Fatalf("tag filter = %+v", page)
	}
	e.call("GET", base+"/search?filters="+url.QueryEscape(`{"color": "red"}`), nil, 400, nil)
	e.call("GET", base+"/search?filters=red", nil, 400, nil)
	if r := e.call("GET", fmt.Sprintf("/catalogs/%d/search", down), nil, 503, nil); !strings.Contains(string(r.Body), "catalog Down is unavailable") {
		t.Fatalf("down: %s", r)
	}
	e.call("GET", fmt.Sprintf("/catalogs/%d/search", broken), nil, 409, nil)
	e.call("GET", fmt.Sprintf("/catalogs/%d/search", e.importID), nil, 404, nil)

	var item protocol.CatalogGetResult
	e.call("GET", base+"/items/brakka", nil, 200, &item)
	if item.Version != "2" || len(item.Versions) != 2 || item.Avatar != "" {
		t.Fatalf("item = %+v", item)
	}
	e.call("GET", base+"/items/brakka?version=1", nil, 200, &item)
	if item.Version != "1" {
		t.Fatalf("version 1 = %+v", item)
	}
	// An ID with a slash travels escaped and reaches the plugin whole.
	if r := e.call("GET", base+"/items/no%2Fsuch", nil, 404, nil); !strings.Contains(string(r.Body), "no item no/such") {
		t.Fatalf("missing item: %s", r)
	}
}

func TestCatalogIngestRoute(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	static := e.catalog("Static", map[string]any{"no_ingest": true})

	var j jobOut
	e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", inst), nil, 202, &j)
	if j.Kind != importer.IngestKind || j.InstanceID == nil || *j.InstanceID != inst {
		t.Fatalf("job = %+v", j)
	}
	// Nothing runs jobs here, so the first ingest is still queued.
	e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", inst), nil, 409, nil)
	if r := e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", static), nil, 400, nil); !strings.Contains(string(r.Body), "doesn't ingest") {
		t.Fatalf("static: %s", r)
	}

	var list []catalogOut
	e.call("GET", "/catalogs", nil, 200, &list)
	last := map[int64]*jobOut{}
	for _, c := range list {
		last[c.ID] = c.LastIngest
	}
	if last[inst] == nil || last[inst].ID != j.ID || last[static] != nil {
		t.Fatalf("last ingests = %+v", last)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/api -run 'TestCatalogRoutes|TestCatalogIngestRoute'`
Expected: FAIL, `undefined: catalogOut`.

- [ ] **Step 3: Write `internal/api/catalogs.go`**

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// catalogTimeout bounds each call the API makes to a catalog.
const catalogTimeout = 30 * time.Second

type catalogOut struct {
	ID         int64                 `json:"id"`
	Name       string                `json:"name"`
	Plugin     string                `json:"plugin"`
	Info       *protocol.CatalogInfo `json:"info,omitempty" doc:"The catalog's kinds, filters, and sorts, and whether it ingests and finds similar items"`
	Error      string                `json:"error,omitempty" doc:"Why the catalog couldn't describe itself"`
	LastIngest *jobOut               `json:"last_ingest,omitempty" doc:"The newest catalog.ingest job"`
}

type catalogsOut struct{ Body []catalogOut }

type catalogSearchIn struct {
	ID      int64  `path:"id"`
	Q       string `query:"q" doc:"Search text; empty matches everything"`
	Kind    string `query:"kind" doc:"One of the catalog's kinds; empty for all"`
	Filters string `query:"filters" doc:"A JSON object of filter values by name, as the catalog declares them, such as {\"site\": \"chub\"}"`
	Sort    string `query:"sort" doc:"One of the catalog's sorts; empty for its first"`
	Size    int    `query:"size" minimum:"0" maximum:"100" doc:"Default: the catalog's"`
	Cursor  string `query:"cursor" doc:"next from the previous page"`
}

// catalogHit is a catalog item and the library entries that match it.
type catalogHit struct {
	protocol.CatalogItem
	Library []library.HashMatch `json:"library" doc:"Library entries of the item's kind that match it, closest first. content means the same content."`
}

// catalogResults is one page from one catalog.
type catalogResults struct {
	CatalogID int64        `json:"catalog_id"`
	Items     []catalogHit `json:"items"`
	Next      string       `json:"next,omitempty" doc:"Cursor for the next page, for GET /catalogs/{id}/search"`
	Error     string       `json:"error,omitempty" doc:"In POST /search: why this catalog returned nothing"`
}

type catalogResultsOut struct{ Body catalogResults }

type catalogItemIn struct {
	ID      int64  `path:"id"`
	Item    string `path:"item" doc:"The catalog's item ID, with / escaped as %2F"`
	Version string `query:"version" doc:"Default: the latest"`
}

type catalogItemOut struct{ Body protocol.CatalogGetResult }

// catalogCall sends method to catalog instance id with a timeout, and maps
// its errors to HTTP ones. It returns the instance's settings.
func (s *Server) catalogCall(ctx context.Context, id int64, method string, params, result any) (plugins.InstanceInfo, error) {
	info, err := s.Registry.GetInstance(ctx, id)
	if err != nil {
		return info, s.fail(err)
	}
	if !s.Registry.HasCapability(info.Plugin, "catalog") {
		return info, huma.Error404NotFound(fmt.Sprintf("instance %d is not a catalog", id))
	}
	inst, err := s.Registry.Instance(ctx, id)
	if err != nil {
		return info, s.fail(err)
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	err = s.Registry.Call(ctx, s.Plugins, inst, method, params, result)
	var rpcErr *protocol.Error
	var startErr *pluginhost.StartError
	switch {
	case err == nil:
		return info, nil
	case errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams:
		return info, huma.Error400BadRequest(rpcErr.Message)
	case errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeRemoteNotFound:
		return info, huma.Error404NotFound(rpcErr.Message)
	case errors.As(err, &startErr), errors.Is(err, protocol.ErrClosed), errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &rpcErr) && rpcErr.Retryable():
		s.Log.Warn("catalog unavailable", "catalog", info.Name, "err", err)
		return info, huma.Error503ServiceUnavailable(fmt.Sprintf("catalog %s is unavailable; see the server log", info.Name))
	}
	return info, s.fail(err)
}

// catalogs lists every catalog instance with its description and newest
// ingest. A catalog that can't describe itself gets an error instead.
func (s *Server) catalogs(ctx context.Context) ([]catalogOut, error) {
	list, err := s.Registry.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	out := []catalogOut{}
	for _, i := range list {
		if !s.Registry.HasCapability(i.Plugin, "catalog") {
			continue
		}
		c := catalogOut{ID: i.ID, Name: i.Name, Plugin: i.Plugin}
		var info protocol.CatalogInfo
		if _, err := s.catalogCall(ctx, i.ID, protocol.MethodCatalogDescribe, nil, &info); err != nil {
			c.Error = err.Error()
		} else {
			c.Info = &info
		}
		if c.LastIngest, err = s.lastIngest(ctx, i.ID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// lastIngest is a catalog's newest ingest job, or nil.
func (s *Server) lastIngest(ctx context.Context, id int64) (*jobOut, error) {
	list, err := s.Queue.List(ctx, jobs.ListOptions{Kind: importer.IngestKind, Instance: id, Limit: 1})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	j := toJob(list[0])
	return &j, nil
}

// kindEntities is where the library keeps kind, or nil if it doesn't.
func (s *Server) kindEntities(kind string) *library.Entities {
	switch kind {
	case protocol.KindCard:
		return s.Lib.Cards
	case protocol.KindLorebook:
		return s.Lib.Lorebooks
	}
	return nil
}

// withMatches adds each item's library matches, with one query per kind.
func (s *Server) withMatches(ctx context.Context, items []protocol.CatalogItem) ([]catalogHit, error) {
	hits := make([]catalogHit, len(items))
	byKind := map[string][]int{}
	for i, it := range items {
		hits[i] = catalogHit{CatalogItem: it, Library: []library.HashMatch{}}
		byKind[it.Kind] = append(byKind[it.Kind], i)
	}
	for kind, idx := range byKind {
		ents := s.kindEntities(kind)
		if ents == nil {
			continue
		}
		hashes := make([]library.Hashes, len(idx))
		for j, i := range idx {
			h := items[i].Hashes
			hashes[j] = library.Hashes{ContentHash: h.ContentHash, TextSimhash: h.TextSimhash, ImagePhash: h.ImagePhash}
		}
		matches, err := ents.MatchHashes(ctx, hashes, library.NearDistance)
		if err != nil {
			return nil, err
		}
		for j, i := range idx {
			hits[i].Library = orEmpty(matches[j])
		}
	}
	return hits, nil
}

// searchCatalog returns one page from a catalog with its library matches.
func (s *Server) searchCatalog(ctx context.Context, id int64, p protocol.CatalogSearchParams) (catalogResults, error) {
	var page protocol.CatalogSearchResult
	if _, err := s.catalogCall(ctx, id, protocol.MethodCatalogSearch, p, &page); err != nil {
		return catalogResults{}, err
	}
	hits, err := s.withMatches(ctx, page.Items)
	if err != nil {
		return catalogResults{}, s.fail(err)
	}
	return catalogResults{CatalogID: id, Items: hits, Next: page.Next}, nil
}

func (s *Server) catalogRoutes(api huma.API) {
	tags := []string{"Catalogs"}
	huma.Register(api, huma.Operation{OperationID: "list-catalogs", Method: http.MethodGet, Path: "/catalogs",
		Summary: "List catalogs", Description: "Every instance of a catalog plugin, with its description and newest ingest job.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*catalogsOut, error) {
			list, err := s.catalogs(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &catalogsOut{Body: list}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "search-catalog", Method: http.MethodGet, Path: "/catalogs/{id}/search",
		Summary: "Search a catalog", Description: "Returns one page of items, each with the library entries that match it.", Tags: tags},
		func(ctx context.Context, in *catalogSearchIn) (*catalogResultsOut, error) {
			p := protocol.CatalogSearchParams{Q: in.Q, Kind: in.Kind, Sort: in.Sort,
				Page: protocol.CatalogPage{Size: in.Size, Cursor: in.Cursor}}
			if in.Filters != "" {
				if err := json.Unmarshal([]byte(in.Filters), &p.Filters); err != nil {
					return nil, huma.Error400BadRequest("filters must be a JSON object: " + err.Error())
				}
			}
			res, err := s.searchCatalog(ctx, in.ID, p)
			if err != nil {
				return nil, err
			}
			return &catalogResultsOut{Body: res}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-catalog-item", Method: http.MethodGet, Path: "/catalogs/{id}/items/{item}",
		Summary: "Get a catalog item", Description: "One version as CCv3, with the item's versions, newest first.", Tags: tags},
		func(ctx context.Context, in *catalogItemIn) (*catalogItemOut, error) {
			out := &catalogItemOut{}
			params := protocol.CatalogGetParams{ID: in.Item, Version: in.Version}
			if _, err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogGet, params, &out.Body); err != nil {
				return nil, err
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "start-catalog-ingest", Method: http.MethodPost, Path: "/catalogs/{id}/ingest",
		Summary: "Ingest a catalog's source",
		Description: "Queues a catalog.ingest job. Returns 409 while an earlier ingest of the catalog is unfinished.",
		Tags: tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			var d protocol.CatalogInfo
			info, err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogDescribe, nil, &d)
			if err != nil {
				return nil, err
			}
			if !d.Ingest {
				return nil, huma.Error400BadRequest(fmt.Sprintf("catalog %s doesn't ingest; it searches its source directly", info.Name))
			}
			// ponytail: two requests at once can both queue one; the plugin's upserts make that harmless.
			last, err := s.lastIngest(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			if last != nil && last.FinishedAt == nil {
				return nil, huma.Error409Conflict("an ingest of this catalog is already queued or running",
					&huma.ErrorDetail{Location: "path.id", Value: last})
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: importer.IngestKind, InstanceID: &in.ID})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
}
```

Every route here keeps the tier its method implies: `read` for the GETs and `write` for the POSTs, so `tierExceptions` doesn't change.

- [ ] **Step 4: Register the routes and document ingest progress**

In `internal/api/server.go`, add `s.catalogRoutes(api)` to `routes`, after `s.searchRoutes(api)`.

In `internal/api/jobs.go`, in the `Progress` field's `doc` tag of `jobOut`, insert after the import sentence: `A catalog ingest's has done, total, and message, and once finished, added, updated, removed, and skipped.`

- [ ] **Step 5: Run the tests to see them pass**

Run: `go test ./internal/api -run 'TestCatalogRoutes|TestCatalogIngestRoute|TestOperationTiers'`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/catalogs.go internal/api/catalogs_test.go internal/api/server.go internal/api/jobs.go
git commit -m "feat(api): list, search, and read catalogs, and queue ingests"
```

---

### Task 6: Adopt

**Files:** Modify `internal/api/catalogs.go`, `internal/api/catalogs_test.go`.

- [ ] **Step 1: Write the failing test**

Add `"github.com/inkandale/innkeeper/internal/library"` to the imports of `internal/api/catalogs_test.go`, and append:

```go
func TestCatalogAdopt(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	base := fmt.Sprintf("/catalogs/%d", inst)

	var c entityOut
	e.call("POST", base+"/items/brakka/adopt", nil, 201, &c)
	if c.Provenance != fmt.Sprintf("catalog:%d:brakka@2", inst) || c.Revision.Source != "plugin:catalog" ||
		len(c.Lorebooks) != 1 || c.Revision.Avatar == nil {
		t.Fatalf("adopted %+v", c)
	}
	var again entityOut
	e.call("POST", base+"/items/brakka/adopt", nil, 200, &again)
	e.call("POST", base+"/items/brakka/adopt?version=2", nil, 200, &again)
	if again.ID != c.ID {
		t.Fatalf("adopted again as %d, want %d", again.ID, c.ID)
	}
	var old entityOut
	e.call("POST", base+"/items/brakka/adopt?version=1", nil, 201, &old)
	if old.ID == c.ID || old.Provenance != fmt.Sprintf("catalog:%d:brakka@1", inst) {
		t.Fatalf("version 1 = %+v", old)
	}
	e.call("POST", base+"/items/brakka/adopt?version=9", nil, 404, nil)
	e.call("POST", base+"/items/nobody/adopt", nil, 404, nil)

	var lb entityOut
	e.call("POST", base+"/items/forge-lore/adopt", nil, 201, &lb)
	e.call("GET", fmt.Sprintf("/lorebooks/%d", lb.ID), nil, 200, nil)

	// near has carrow's text but not its content.
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)
	var page catalogResults
	e.call("GET", base+"/search", nil, 200, &page)
	got := map[string][]library.HashMatch{}
	for _, it := range page.Items {
		got[it.ID] = it.Library
	}
	if m := got["brakka"]; len(m) == 0 || m[0] != (library.HashMatch{ID: c.ID, Reason: "content"}) {
		t.Fatalf("brakka matches = %+v", m)
	}
	if m := got["carrow"]; len(m) != 1 || m[0] != (library.HashMatch{ID: near.ID, Reason: "text"}) {
		t.Fatalf("carrow matches = %+v", m)
	}
	if m := got["forge-lore"]; len(m) != 1 || m[0] != (library.HashMatch{ID: lb.ID, Reason: "content"}) {
		t.Fatalf("forge-lore matches = %+v", m)
	}
}
```

The fake's Brakka version 2 has a `character_book`, so adopting it also creates a linked lorebook.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api -run TestCatalogAdopt`
Expected: FAIL, `POST /api/catalogs/…/items/brakka/adopt: got 404`.

- [ ] **Step 3: Write the adopt route**

In `internal/api/catalogs.go`, add `"cmp"` to the imports, and after the `catalogItemOut` type add:

```go
type adoptCatalogOut struct {
	Status int
	Body   entityOut
}
```

Append:

```go
// adoptCatalogItem copies one version of an item into the library. Import
// dedupes on provenance and content, so adopting a version twice finds the
// first copy.
func (s *Server) adoptCatalogItem(ctx context.Context, in *catalogItemIn) (*adoptCatalogOut, error) {
	var res protocol.CatalogGetResult
	info, err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogGet,
		protocol.CatalogGetParams{ID: in.Item, Version: in.Version, Avatar: true}, &res)
	if err != nil {
		return nil, err
	}
	prov := fmt.Sprintf("catalog:%d:%s@%s", in.ID, in.Item, res.Version)
	source := "plugin:" + info.Plugin
	var ents *library.Entities
	var id int64
	var created bool
	// An avatar sent with anything but a card stays in blob_tmp until garbage collection.
	switch res.Item.Kind {
	case protocol.KindCard:
		ents = s.Lib.Cards
		id, created, err = s.Lib.ImportCard(ctx, prov, source, res.Data, res.Avatar)
	case protocol.KindLorebook:
		ents = s.Lib.Lorebooks
		var meta struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(res.Data, &meta)
		id, created, err = s.Lib.Lorebooks.Import(ctx, prov, cmp.Or(meta.Name, res.Item.Name),
			library.Edit{Data: res.Data, Source: source, Message: "Adopted"})
	default:
		return nil, huma.Error400BadRequest(fmt.Sprintf("can't adopt a %s; the library holds cards and lorebooks", res.Item.Kind))
	}
	if errors.Is(err, library.ErrInvalid) {
		return nil, huma.Error502BadGateway(fmt.Sprintf("catalog %s sent an item the library can't take: %v", info.Name, err))
	}
	if err != nil {
		return nil, s.fail(err)
	}
	out, err := s.entity(ctx, ents, id)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return &adoptCatalogOut{Status: status, Body: out.Body}, nil
}
```

In `catalogRoutes`, after the `get-catalog-item` registration, add:

```go
	huma.Register(api, huma.Operation{OperationID: "adopt-catalog-item", Method: http.MethodPost, Path: "/catalogs/{id}/items/{item}/adopt",
		Summary: "Adopt a catalog item into the library",
		Description: "Copies one version in as a new card or lorebook; a card's character_book becomes a linked lorebook. " +
			"Returns 201 with the new entry, or 200 with the one an earlier adopt of the same version made.", Tags: tags},
		s.adoptCatalogItem)
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/api -run 'TestCatalog'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/catalogs.go internal/api/catalogs_test.go
git commit -m "feat(api): adopt catalog items into the library"
```

---

### Task 7: Catalog matches

**Files:** Modify `internal/api/catalogs.go`, `internal/api/catalogs_test.go`.

- [ ] **Step 1: Write the failing test**

Append to `internal/api/catalogs_test.go`:

```go
func TestCatalogMatches(t *testing.T) {
	e := newEnv(t)
	up := e.catalog("Archive", nil)
	down := e.catalog("Down", map[string]any{"down": true})
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)

	var out catalogMatches
	e.call("GET", fmt.Sprintf("/cards/%d/catalog-matches", near.ID), nil, 200, &out)
	if len(out.Matches) != 1 || out.Matches[0].CatalogID != up || out.Matches[0].Catalog != "Archive" ||
		len(out.Matches[0].Items) != 1 || out.Matches[0].Items[0].ID != "carrow" {
		t.Fatalf("matches = %+v", out.Matches)
	}
	if len(out.Unreachable) != 1 || out.Unreachable[0].ID != down || !strings.Contains(out.Unreachable[0].Error, "catalog Down is unavailable") {
		t.Fatalf("unreachable = %+v", out.Unreachable)
	}

	var lb entityOut
	e.call("POST", fmt.Sprintf("/catalogs/%d/items/forge-lore/adopt", up), nil, 201, &lb)
	e.call("GET", fmt.Sprintf("/lorebooks/%d/catalog-matches", lb.ID), nil, 200, &out)
	if len(out.Matches) != 1 || out.Matches[0].Items[0].ID != "forge-lore" {
		t.Fatalf("lorebook matches = %+v", out.Matches)
	}
	e.call("GET", "/cards/999999/catalog-matches", nil, 404, nil)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api -run TestCatalogMatches`
Expected: FAIL, `undefined: catalogMatches`.

- [ ] **Step 3: Write the route**

In `internal/api/catalogs.go`, add `"slices"` to the imports. After `catalogTimeout`, add:

```go
// catalogMatchLimit is the most items each catalog returns for one card or
// lorebook.
const catalogMatchLimit = 20
```

After the `adoptCatalogOut` type, add:

```go
type catalogMatch struct {
	CatalogID int64                  `json:"catalog_id"`
	Catalog   string                 `json:"catalog"`
	Items     []protocol.CatalogItem `json:"items"`
}

type catalogRef struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

type catalogMatches struct {
	Matches     []catalogMatch `json:"matches" doc:"One per catalog with matches"`
	Unreachable []catalogRef   `json:"unreachable" doc:"Catalogs that didn't answer, which may hold more matches"`
}

type catalogMatchesOut struct{ Body catalogMatches }
```

Append:

```go
// findInCatalogs asks every catalog that answers catalog.similar for an
// entity's head revision.
//
// ponytail: asks one catalog at a time; ask in parallel if many catalogs make this slow.
func (s *Server) findInCatalogs(ctx context.Context, ents *library.Entities, kind string, id int64) (*catalogMatchesOut, error) {
	e, err := ents.Get(ctx, id)
	if err != nil {
		return nil, s.fail(err)
	}
	rev, err := ents.Revision(ctx, e.HeadRevisionID)
	if err != nil {
		return nil, s.fail(err)
	}
	h := protocol.CatalogHashes{ContentHash: rev.ContentHash, TextSimhash: rev.TextSimhash}
	if rev.AvatarBlob != nil {
		b, err := s.Lib.Blob(ctx, *rev.AvatarBlob)
		if err != nil {
			return nil, s.fail(err)
		}
		h.ImagePhash = b.AvatarPhash
	}
	cats, err := s.catalogs(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	out := &catalogMatchesOut{Body: catalogMatches{Matches: []catalogMatch{}, Unreachable: []catalogRef{}}}
	for _, c := range cats {
		if c.Info == nil {
			out.Body.Unreachable = append(out.Body.Unreachable, catalogRef{c.ID, c.Name, c.Error})
			continue
		}
		if !c.Info.Similar || !slices.Contains(c.Info.Kinds, kind) {
			continue
		}
		var res protocol.CatalogSimilarResult
		params := protocol.CatalogSimilarParams{Kind: kind, Hashes: h, MaxDistance: library.NearDistance, Limit: catalogMatchLimit}
		if _, err := s.catalogCall(ctx, c.ID, protocol.MethodCatalogSimilar, params, &res); err != nil {
			out.Body.Unreachable = append(out.Body.Unreachable, catalogRef{c.ID, c.Name, err.Error()})
			continue
		}
		if len(res.Items) > 0 {
			out.Body.Matches = append(out.Body.Matches, catalogMatch{c.ID, c.Name, res.Items})
		}
	}
	return out, nil
}
```

At the end of `catalogRoutes`, add:

```go
	for _, k := range []struct {
		plural, kind string
		ents         *library.Entities
	}{{"cards", protocol.KindCard, s.Lib.Cards}, {"lorebooks", protocol.KindLorebook, s.Lib.Lorebooks}} {
		huma.Register(api, huma.Operation{OperationID: "find-" + k.kind + "-in-catalogs", Method: http.MethodGet,
			Path: "/" + k.plural + "/{id}/catalog-matches", Summary: "Find a " + k.kind + " in the catalogs",
			Description: "Asks every catalog that finds similar items for the same content, near-identical text, or a similar avatar.",
			Tags:        tags},
			func(ctx context.Context, in *idIn) (*catalogMatchesOut, error) {
				return s.findInCatalogs(ctx, k.ents, k.kind, in.ID)
			})
	}
```

- [ ] **Step 4: Run the tests to see them pass**

Run: `go test ./internal/api -run 'TestCatalog|TestOperationTiers'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/catalogs.go internal/api/catalogs_test.go
git commit -m "feat(api): find a card or lorebook in every catalog"
```

---

### Task 8: Catalog sources in `/search`

`POST /search` and `POST /lorebooks/search` take `catalog:<id>` in `sources`. The library is searched when `sources` is empty or names a non-catalog source. Each catalog source adds its first page, searched by `text` alone, to `catalogs`. A catalog that fails reports its error there, and the rest of the search still answers.

**Files:**
- Modify: `internal/api/search.go`, `internal/library/search.go`, `internal/mcp/tools.go`
- Test: `internal/api/catalogs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/api/catalogs_test.go`:

```go
func TestSearchCatalogSources(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	down := e.catalog("Down", map[string]any{"down": true})
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)
	src := fmt.Sprintf("catalog:%d", inst)

	var res searchResults
	e.call("POST", "/search", map[string]any{"text": "carrow", "sources": []string{"library", src, fmt.Sprintf("catalog:%d", down)}}, 200, &res)
	if res.Total != 1 || res.Hits[0].ID != near.ID || len(res.Catalogs) != 2 {
		t.Fatalf("search = %+v", res)
	}
	if c := res.Catalogs[0]; c.CatalogID != inst || catalogIDs(c.Items) != "carrow" || len(c.Items[0].Library) != 1 || c.Error != "" {
		t.Fatalf("archive = %+v", c)
	}
	if c := res.Catalogs[1]; c.CatalogID != down || len(c.Items) != 0 || !strings.Contains(c.Error, "unavailable") {
		t.Fatalf("down = %+v", c)
	}

	e.call("POST", "/search", map[string]any{"sources": []string{src}}, 200, &res)
	if len(res.Hits) != 0 || res.Total != 0 || catalogIDs(res.Catalogs[0].Items) != "brakka,carrow" {
		t.Fatalf("catalog only = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{"sources": []string{src}}, 200, &res)
	if catalogIDs(res.Catalogs[0].Items) != "forge-lore" {
		t.Fatalf("lorebooks = %+v", res)
	}
	e.call("POST", "/search", map[string]any{"sources": []string{"catalog:x"}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{fmt.Sprintf("catalog:%d", e.importID)}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{"chub"}}, 400, nil)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/api -run TestSearchCatalogSources`
Expected: FAIL, `undefined: searchResults`.

- [ ] **Step 3: Rewrite `internal/api/search.go`**

```go
package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

type searchIn struct{ Body library.Query }

// searchResults are the library's hits and a page from each catalog source.
type searchResults struct {
	library.Results
	Catalogs []catalogResults `json:"catalogs,omitempty" doc:"One per catalog:<id> source, in order"`
}

type searchOut struct{ Body searchResults }

// search runs q over the library with run, if q asks for it, and over each
// catalog source for kind.
func (s *Server) search(ctx context.Context, q library.Query, kind string,
	run func(context.Context, library.Query) (library.Results, error)) (*searchOut, error) {
	var catalogs []int64
	var rest []string
	for _, src := range q.Sources {
		v, ok := strings.CutPrefix(src, "catalog:")
		if !ok {
			rest = append(rest, src)
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("source %q: want catalog:<instance id>", src))
		}
		catalogs = append(catalogs, id)
	}
	out := &searchOut{Body: searchResults{Results: library.Results{Hits: []library.Hit{}}}}
	if len(rest) > 0 || len(catalogs) == 0 {
		q.Sources = rest
		res, err := run(ctx, q)
		if err != nil {
			return nil, s.fail(err)
		}
		out.Body.Results = res
	}
	p := protocol.CatalogSearchParams{Q: q.Text, Kind: kind, Page: protocol.CatalogPage{Size: min(cmp.Or(q.Page.Size, 50), protocol.MaxCatalogPage)}}
	for _, id := range catalogs {
		res, err := s.searchCatalog(ctx, id, p)
		var se huma.StatusError
		switch {
		case errors.As(err, &se) && se.GetStatus() == http.StatusNotFound:
			return nil, huma.Error400BadRequest(fmt.Sprintf("source \"catalog:%d\": %v", id, err))
		case err != nil:
			res = catalogResults{CatalogID: id, Items: []catalogHit{}, Error: err.Error()}
		}
		out.Body.Catalogs = append(out.Body.Catalogs, res)
	}
	return out, nil
}

func (s *Server) searchRoutes(api huma.API) {
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "search-cards", Method: http.MethodPost, Path: "/search",
		Summary: "Search the library",
		Description: "Send {} to list every card that isn't archived, newest first. " +
			"With catalog:<id> sources, also returns each catalog's first page of cards matching text.",
		Tags: []string{"Search"}},
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			return s.search(ctx, in.Body, protocol.KindCard, s.Lib.Search)
		})
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "search-lorebooks", Method: http.MethodPost, Path: "/lorebooks/search",
		Summary: "Search the library's lorebooks",
		Description: "Takes the same query as POST /search. Text matches names, descriptions, entry keys, and entry content. " +
			"Lorebooks have no tags or collections. Send {} to list every lorebook that isn't archived, newest first. " +
			"With catalog:<id> sources, also returns each catalog's first page of lorebooks matching text.",
		Tags: []string{"Search"}},
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			return s.search(ctx, in.Body, protocol.KindLorebook, s.Lib.SearchLorebooks)
		})
}
```

The search runs each catalog in turn. A catalog that doesn't declare the kind refuses the search with `-32602`, and its error goes in `catalogs`.

- [ ] **Step 4: Update the `sources` docs**

In `internal/library/search.go`, change the `Sources` field's `doc` tag in `Query` to:

```go
	Sources  []string `json:"sources,omitempty" doc:"library, or catalog:<instance id> to also search a catalog by text alone. Default: library."`
```

In `internal/mcp/tools.go`, change the `searchIn` `jsonschema` tag to:

```go
	Query map[string]any `json:"query" jsonschema:"The search query, as the API's POST /search body: text, filter, sources (library, or catalog:<instance id> to search a catalog too), include, fields, page"`
```

- [ ] **Step 5: Run the API and MCP tests**

Run: `go test ./internal/api ./internal/mcp ./internal/library`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/search.go internal/api/catalogs_test.go internal/library/search.go internal/mcp/tools.go
git commit -m "feat(api): search catalogs from /search with catalog sources"
```

---

### Task 9: OpenAPI and spec

**Files:** Modify `web/openapi.json`, `web/src/api/schema.d.ts`, and the spec. Edit the spec with the Edit tool; the doc-writing hook checks it.

- [ ] **Step 1: Regenerate the API types**

```bash
cd web && npm run gen && npm run build
```

Expected: the build passes. The search response schema is now `SearchResults`; nothing in `web/src` names the old `Results` schema outside `schema.d.ts`.

- [ ] **Step 2: Update the spec**

In `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, under Core:

1. Replace the **Routes** bullet and its sub-bullets with:

```markdown
- **Routes:**
  - `GET /catalogs` lists catalog instances with their `describe` result and
    the last ingest job.
  - `GET /catalogs/{id}/search` forwards to `catalog.search`, with `filters`
    as one JSON-object query parameter, and adds library matches to each
    result.
  - `GET /catalogs/{id}/items/{item}` forwards to `catalog.get` without
    `avatar`. An item ID escapes `/` as `%2F`.
  - `POST /catalogs/{id}/items/{item}/adopt` takes an optional `version`
    query parameter. It returns 201 with the new library card or lorebook,
    or 200 with the one an earlier adopt of that version made.
  - `POST /catalogs/{id}/ingest` queues a `catalog.ingest` job, or returns
    409 while the catalog's last ingest is unfinished.
  - `GET /cards/{id}/catalog-matches` and `GET /lorebooks/{id}/catalog-matches`
    return matches from every catalog that supports `similar`, and name the
    catalogs that didn't answer.
```

2. Replace the **`/search`** bullet with:

```markdown
- **`/search`** accepts `catalog:<instance>` in `sources`, so the CLI and MCP
  can search the library and catalogs together. Each catalog source adds its
  first page, matched on `text` alone, to a `catalogs` list beside the
  library's hits. The library is searched when `sources` is empty or names
  `library`.
```

3. Replace the **Library matches** bullet with:

```markdown
- **Library matches** are one query per kind over the page's hashes: exact by
  `content_hash`, near by simhash or phash within 8 bits, against each
  entry's head revision, as `/cards/{id}/similar` compares.
```

Under Errors, replace the first bullet with:

```markdown
- **A plugin is down or times out:** a catalog that won't start, exits, takes
  over 30 seconds, or returns a retryable error makes that catalog's routes
  return 503 naming the catalog, and the Archive page shows the error inline.
  In `/search`, the catalog's entry carries the error instead. "Found in
  archives" skips the catalog and names the ones it couldn't reach.
```

- [ ] **Step 3: Commit**

```bash
git add web/openapi.json web/src/api/schema.d.ts docs/superpowers/specs/2026-10-09-archive-catalog-design.md
git commit -m "docs: describe the catalog routes as built"
```

---

### Task 10: Full check

- [ ] **Step 1: Run everything**

```bash
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./...
go vet ./...
cd web && npm test && npm run gen && git diff --exit-code -- openapi.json src/api/schema.d.ts
```

Expected: PASS, and no diff. If `internal/api` prints `SKIP`, the database isn't up and the run hasn't passed.
