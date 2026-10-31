# Maintenance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Settings tab, Maintenance, that previews and runs five cleanups: trim enrichment history, remove stale vectors, clear recorded failures, vacuum and analyze, and purge archived cards.

**Architecture:** A new package, `internal/maintenance`, owns the previews and the actions and runs them as one job kind, `maintenance`, at most one at a time. Purge reuses `Library.DeleteCards` and `Entities.deleteIn`, which change so every delete path deletes bindings and re-links variants. The API adds `GET /maintenance` and `POST /maintenance/runs`; the web adds a tab.

**Tech stack:** Go, pgx, PostgreSQL, huma, React and TanStack Query.

**Spec:** [2026-10-30-maintenance-design.md](../specs/2026-10-30-maintenance-design.md). Needs card-embed plans 2 and 3 (vectors, `enrich_failure`).

---

## Conventions

- Work in `~/git/innkeeper-card-embed` (branch `card-embed-spec`).
- `$DB` stands for
  `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
  Without it database tests skip, and a skip is not a pass.
- After any API change, run `cd web && npm run gen`.
- Comments say why, in one line. Follow `AGENTS.md`.

## Files

| File | Change |
|---|---|
| `internal/library/libraries.go`, `libraries_test.go` | `DeleteCards` deletes bindings; variants re-link; `DeleteLorebooks` |
| `internal/bulk/bulk.go`, `internal/api/purge.go`, `purge_test.go` | Drop "refused" |
| `internal/maintenance/maintenance.go` | `Preview`, the actions, `Kind`, `Runner` |
| `internal/maintenance/maintenance_test.go` | Tests |
| `internal/api/maintenance.go`, `maintenance_test.go` | Routes |
| `internal/api/server.go`, `internal/api/jobs.go`, `cmd/innkeeper/worker.go` | Register |
| `web/src/settings/Maintenance.tsx`, `Maintenance.test.tsx`, `Settings.tsx`, `web/src/App.tsx` | The tab |

---

### Task 1: Shared delete: bindings go, variants re-link, lorebooks too

**Files:** `internal/library/libraries.go`, `internal/library/libraries_test.go`,
`internal/bulk/bulk.go`, `internal/api/purge.go`, `internal/api/purge_test.go`

- [ ] **Step 1: Rewrite the bound-card test and add a re-link test**

In `internal/library/libraries_test.go`, rename
`TestDeleteCardsRefusesCardsBoundToAnApp` to
`TestDeleteCardsDeletesAPushedCardsBinding`, keep its setup, and replace
its assertions from `deleted, refused, err := ...` on:

```go
	deleted, err := lib.DeleteCards(ctx, []int64{plain, pushed, imported, 999999})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("deleted %d", deleted)
	}
	var bindings int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM binding`).Scan(&bindings); err != nil || bindings != 0 {
		t.Fatalf("bindings left: %d, %v", bindings, err)
	}
```

Keep whatever the rest of the test asserts about the lorebook link.

Add:

```go
func TestDeleteCardsRelinksVariantsToTheNearestSurvivor(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	root, _ := newCard(t, lib, "root", `{"name": "Root"}`)
	mid, _ := newCard(t, lib, "mid", `{"name": "Mid"}`)
	low, _ := newCard(t, lib, "low", `{"name": "Low"}`)
	leaf, _ := newCard(t, lib, "leaf", `{"name": "Leaf"}`)
	orphan, _ := newCard(t, lib, "orphan", `{"name": "Orphan"}`)
	for child, parent := range map[int64]int64{mid: root, low: mid, leaf: low, orphan: root} {
		if err := lib.Cards.SetVariantOf(ctx, child, parent); err != nil {
			t.Fatal(err)
		}
	}
	// Purging mid and low together leaves leaf two levels from a survivor.
	if _, err := lib.DeleteCards(ctx, []int64{mid, low}); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, leaf); c.VariantOfID == nil || *c.VariantOfID != root {
		t.Fatalf("leaf's parent = %v, want %d", c.VariantOfID, root)
	}
	if _, err := lib.DeleteCards(ctx, []int64{root}); err != nil {
		t.Fatal(err)
	}
	if c, _ := lib.Cards.Get(ctx, orphan); c.VariantOfID != nil {
		t.Fatalf("orphan's parent = %v, want none", c.VariantOfID)
	}
}
```

Use the library's real method and field for setting and reading
`variant_of_id` (search `entities.go` for `variant_of_id`); the names above
stand for them.

Add a lorebook test:

```go
func TestDeleteLorebooksDeletesThemAndTheirLinks(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	lore, _, err := lib.Lorebooks.Create(ctx, DefaultLibrary, "lore", "upload", edit(`{"name": "Lore", "entries": []}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.DeleteLorebooks(ctx, []int64{lore.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Lorebooks.Get(ctx, lore.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

```bash
$DB go test ./internal/library -run 'TestDeleteCards|TestDeleteLorebooks' -count=1
```

Expected: compile errors: `DeleteCards` returns three values,
`DeleteLorebooks` undefined.

- [ ] **Step 3: Change the shared code**

In `internal/library/libraries.go`, replace `DeleteCards`:

```go
// DeleteCards deletes the cards ids with their revisions and what hangs off
// them, including bindings: an app's copy stays in the app, and archiving
// with unpublish is how to remove it first. IDs that don't exist are
// skipped. It returns how many it deleted.
func (l *Library) DeleteCards(ctx context.Context, ids []int64) (int, error) {
	var deleted int
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM card WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
		if err != nil {
			return err
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(found) == 0 {
			return err
		}
		deleted = len(found)
		return l.Cards.deleteIn(ctx, tx, found)
	})
	return deleted, err
}

// DeleteLorebooks deletes the lorebooks ids with their revisions and links.
func (l *Library) DeleteLorebooks(ctx context.Context, ids []int64) error {
	return pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error { return l.Lorebooks.deleteIn(ctx, tx, ids) })
}
```

In `deletes()`, replace the `UPDATE ... SET variant_of_id = NULL` line with:

```go
		// A live variant moves up to its nearest ancestor that survives, so its family stays connected.
		`WITH RECURSIVE up AS (
			SELECT t.id AS child, t.variant_of_id AS anc FROM ` + k.Table + ` t
			WHERE t.variant_of_id = ANY($1) AND NOT t.id = ANY($1)
			UNION ALL
			SELECT up.child, p.variant_of_id FROM up JOIN ` + k.Table + ` p ON p.id = up.anc WHERE up.anc = ANY($1)
		)
		UPDATE ` + k.Table + ` t SET variant_of_id = up.anc
		FROM up WHERE up.child = t.id AND (up.anc IS NULL OR NOT up.anc = ANY($1))`,
```

The walk stops at the first ancestor outside `$1`, or at a root, whose
`anc` is null and which the `UPDATE` then sets.

- [ ] **Step 4: Follow the signature change**

In `internal/bulk/bulk.go`, `Purge` becomes:

```go
		for batch := range slices.Chunk(p.CardIDs, purgeBatch) {
			n, err := lib.DeleteCards(ctx, batch)
			if err != nil {
				return err
			}
			prog.Done += n
			r.AddCardsChanged(n)
			raw, _ := json.Marshal(prog)
			r.SetProgress(raw)
		}
```

and `PurgeProgress` loses `Skipped` and `Refused`; its comment becomes
"PurgeProgress is a PurgeKind job's progress. Done counts the cards
deleted." In `internal/api/purge.go`, replace "A card bound to a target
app is kept, and the job's progress lists it." with "A card pushed to an
app loses its link; the app's copy stays, so archive with unpublish first
to remove it." Update `purge_test.go`'s bound-card assertions to expect
the card deleted. Search the web for `refused` in the purge UI and remove
that display:

```bash
grep -rn "refused" web/src --include='*.tsx' | grep -iv "test\|schema"
```

- [ ] **Step 5: Run the tests**

```bash
$DB go test ./internal/library ./internal/bulk ./internal/api -count=1
(cd web && npm run gen && npm run typecheck)
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal web
git commit -m "feat(library): delete pushed cards' bindings and re-link variants when deleting cards"
```

---

### Task 2: The maintenance package

**Files:** create `internal/maintenance/maintenance.go`,
`internal/maintenance/maintenance_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/maintenance/maintenance_test.go`, in `package maintenance_test`,
using `testdb.New` and `library.New` the way other packages' tests do
(copy `newLibrary`'s two lines, not a helper package):

```go
func TestTrimHistoryKeepsTheNewestValues(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	inst := instanceRow(t, pool)
	card, rev := cardRow(t, lib)
	for i := range 5 {
		if err := lib.AddEnrichments(ctx, inst, []library.NewEnrichment{{CardID: card, RevisionID: rev, Key: "q",
			Value: json.RawMessage(strconv.Itoa(i))}}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := m.Preview(ctx, 2, 30)
	if err != nil || p.History.Trimmable != 3 {
		t.Fatalf("preview %+v, %v", p.History, err)
	}
	n, err := m.TrimHistory(ctx, 2)
	if err != nil || n != 3 {
		t.Fatalf("trimmed %d, %v", n, err)
	}
	vals, _ := lib.Enrichments(ctx, card, false)
	if len(vals) != 1 || string(vals[0].Value) != "4" {
		t.Fatalf("current values = %+v", vals)
	}
}

func TestRemoveStaleVectorsKeepsOnlyTheCurrentSpace(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	cur, other := instanceRow(t, pool), instanceRow(t, pool)
	a, ra := cardRow(t, lib)
	b, rb := cardRow(t, lib)
	c, rc := cardRow(t, lib)
	save := func(inst, card, rev int64, space string) {
		if err := lib.SaveVectors(ctx, inst, []library.NewVector{{CardID: card, RevisionID: rev, Space: space, Vector: []float32{1, 0}}}); err != nil {
			t.Fatal(err)
		}
	}
	save(cur, a, ra, "new")
	save(cur, b, rb, "old")
	save(other, c, rc, "new")
	n, err := m.RemoveStaleVectors(ctx, cur, "new")
	if err != nil || n != 2 {
		t.Fatalf("removed %d, %v", n, err)
	}
}

func TestPurgeDeletesOnlyCardsArchivedLongEnoughAgo(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	old, _ := cardRow(t, lib)
	recent, _ := cardRow(t, lib)
	for id, ago := range map[int64]string{old: "31 days", recent: "29 days"} {
		if _, err := pool.Exec(ctx, `UPDATE card SET archived_at = now() - $2::interval WHERE id = $1`, id, ago); err != nil {
			t.Fatal(err)
		}
	}
	p, _ := m.Preview(ctx, 3, 30)
	if p.Purge.Cards != 1 {
		t.Fatalf("preview %+v", p.Purge)
	}
	if _, err := m.Purge(ctx, 30, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Cards.Get(ctx, old); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("old card: %v", err)
	}
	if _, err := lib.Cards.Get(ctx, recent); err != nil {
		t.Fatalf("recent card: %v", err)
	}
}

func TestPurgeKeepsALorebookALiveCardLinks(t *testing.T) {
	m, lib, pool := newMaintenance(t)
	ctx := context.Background()
	card, _ := cardRow(t, lib)
	linked := lorebookRow(t, lib)
	alone := lorebookRow(t, lib)
	if err := lib.LinkLorebook(ctx, card, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE lorebook SET archived_at = now() - interval '40 days'`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Purge(ctx, 30, func(int) {}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.Lorebooks.Get(ctx, linked); err != nil {
		t.Fatalf("linked lorebook: %v", err)
	}
	if _, err := lib.Lorebooks.Get(ctx, alone); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("lone lorebook: %v", err)
	}
}

func TestRevisionsStayImmutableOutsideADelete(t *testing.T) {
	_, lib, pool := newMaintenance(t)
	card, _ := cardRow(t, lib)
	if _, err := pool.Exec(context.Background(), `DELETE FROM card_revision WHERE card_id = $1`, card); err == nil {
		t.Fatal("deleted a revision without innkeeper.delete")
	}
}
```

Write the four helpers at the bottom of the file, each a few lines:
`newMaintenance` (a `testdb.New` pool, `library.New(pool, store)` with a
temp blob store as `library`'s own tests make one, and
`maintenance.New(pool, lib)`), `instanceRow` (an `INSERT INTO
plugin_instance` returning the id), `cardRow` (`lib.Cards.Create` with a
unique slug, returning the card and head revision IDs), and
`lorebookRow`.

- [ ] **Step 2: Run them and see them fail**

```bash
$DB go test ./internal/maintenance -count=1
```

Expected: compile errors: no package.

- [ ] **Step 3: Write the package**

`internal/maintenance/maintenance.go`:

```go
// Package maintenance previews and runs the owner's database cleanups:
// trimming enrichment history, removing stale vectors, clearing recorded
// failures, vacuuming, and purging archived cards.
package maintenance

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/library"
)

// Maintenance runs cleanups on one database.
type Maintenance struct {
	pool *pgxpool.Pool
	lib  *library.Library
}

func New(pool *pgxpool.Pool, lib *library.Library) *Maintenance { return &Maintenance{pool: pool, lib: lib} }

// Table is one table's size.
type Table struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Preview is what each action would remove, and the database's size.
type Preview struct {
	DatabaseBytes int64   `json:"database_bytes"`
	Largest       []Table `json:"largest" doc:"The five largest tables."`
	History       struct {
		Rows      int64 `json:"rows"`
		Trimmable int64 `json:"trimmable" doc:"Rows trim would delete for keep."`
	} `json:"history"`
	Vectors struct {
		Available bool  `json:"available"`
		Rows      int64 `json:"rows"`
	} `json:"vectors"`
	Failures int64 `json:"failures"`
	Purge    struct {
		Cards     int64 `json:"cards"`
		Lorebooks int64 `json:"lorebooks"`
	} `json:"purge"`
	LastBackup *time.Time `json:"last_backup,omitempty" doc:"When the last backup job succeeded, if any has."`
}

// purgeBatch is how many cards one purge transaction deletes.
const purgeBatch = 1000

// trimmable numbers each value newest first within its instance, card and
// key; those numbered past keep are the ones trim deletes.
const trimmable = `SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY instance_id, card_id, key
	ORDER BY created_at DESC, id DESC) AS n FROM enrichment) x WHERE n > $1`

// purgeCards are cards archived more than $1 days ago; purgeLorebooks are
// such lorebooks that no live card links to.
const (
	purgeCards     = `SELECT id FROM card WHERE archived_at < now() - make_interval(days => $1) ORDER BY id`
	purgeLorebooks = `SELECT l.id FROM lorebook l WHERE l.archived_at < now() - make_interval(days => $1)
		AND NOT EXISTS (SELECT 1 FROM card_lorebook cl JOIN card c ON c.id = cl.card_id
			WHERE cl.lorebook_id = l.id AND c.archived_at IS NULL) ORDER BY l.id`
)

// Preview counts what each action would do for keep and days.
func (m *Maintenance) Preview(ctx context.Context, keep, days int) (Preview, error) {
	var p Preview
	err := m.pool.QueryRow(ctx, `SELECT pg_database_size(current_database()),
			(SELECT count(*) FROM enrichment), (SELECT count(*) FROM (`+trimmable+`) t),
			(SELECT count(*) FROM enrich_failure),
			(SELECT max(finished_at) FROM job WHERE kind LIKE 'backup.%' AND status = 'succeeded')`, keep).
		Scan(&p.DatabaseBytes, &p.History.Rows, &p.History.Trimmable, &p.Failures, &p.LastBackup)
	if err != nil {
		return p, fmt.Errorf("preview maintenance: %w", err)
	}
	// A query of its own, since the purge queries' $1 is days, not keep.
	if err := m.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM (`+purgeCards+`) c), (SELECT count(*) FROM (`+purgeLorebooks+`) l)`, days).
		Scan(&p.Purge.Cards, &p.Purge.Lorebooks); err != nil {
		return p, fmt.Errorf("preview purge: %w", err)
	}
	rows, err := m.pool.Query(ctx, `SELECT relname, pg_total_relation_size(oid) FROM pg_class
		WHERE relkind = 'r' AND relnamespace = 'public'::regnamespace ORDER BY 2 DESC LIMIT 5`)
	if err != nil {
		return p, err
	}
	if p.Largest, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Table]); err != nil {
		return p, err
	}
	if p.Vectors.Available, err = m.lib.HasVectors(ctx); err != nil || !p.Vectors.Available {
		return p, err
	}
	err = m.pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding`).Scan(&p.Vectors.Rows)
	return p, err
}

// TrimHistory keeps the newest keep values per instance, card and key, and
// returns how many it deleted.
func (m *Maintenance) TrimHistory(ctx context.Context, keep int) (int64, error) {
	tag, err := m.pool.Exec(ctx, `DELETE FROM enrichment WHERE id IN (`+trimmable+`)`, keep)
	if err != nil {
		return 0, fmt.Errorf("trim enrichment history: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RemoveStaleVectors deletes vectors not from instance or not in space.
func (m *Maintenance) RemoveStaleVectors(ctx context.Context, instance int64, space string) (int64, error) {
	tag, err := m.pool.Exec(ctx, `DELETE FROM card_embedding WHERE instance_id <> $1 OR space <> $2`, instance, space)
	if err != nil {
		return 0, fmt.Errorf("remove stale vectors: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClearFailures forgets every recorded enrich failure, so the sweep tries
// those cards again.
func (m *Maintenance) ClearFailures(ctx context.Context) (int64, error) {
	tag, err := m.pool.Exec(ctx, `DELETE FROM enrich_failure`)
	if err != nil {
		return 0, fmt.Errorf("clear enrich failures: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Vacuum runs VACUUM (ANALYZE) and returns the database size after.
// VACUUM can't run in a transaction, and Exec without arguments sends it alone.
func (m *Maintenance) Vacuum(ctx context.Context) (int64, error) {
	if _, err := m.pool.Exec(ctx, `VACUUM (ANALYZE)`); err != nil {
		return 0, fmt.Errorf("vacuum (the database user must own Innkeeper's tables): %w", err)
	}
	var size int64
	err := m.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&size)
	return size, err
}

// Purge deletes cards archived more than days ago, then the archived
// lorebooks no live card links to, calling done with each batch's count.
func (m *Maintenance) Purge(ctx context.Context, days int, done func(int)) (int, error) {
	rows, err := m.pool.Query(ctx, purgeCards, days)
	if err != nil {
		return 0, err
	}
	cards, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	total := 0
	for i := 0; i < len(cards); i += purgeBatch {
		n, err := m.lib.DeleteCards(ctx, cards[i:min(i+purgeBatch, len(cards))])
		if err != nil {
			return total, err
		}
		total += n
		done(n)
	}
	// After the cards, so a lorebook only they linked to qualifies too.
	if rows, err = m.pool.Query(ctx, purgeLorebooks, days); err != nil {
		return total, err
	}
	lorebooks, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(lorebooks) == 0 {
		return total, err
	}
	return total, m.lib.DeleteLorebooks(ctx, lorebooks)
}
```

- [ ] **Step 4: Run the tests**

```bash
$DB go test ./internal/maintenance -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/maintenance
git commit -m "feat(maintenance): preview and run database cleanups"
```

---

### Task 3: The job

**Files:** `internal/maintenance/maintenance.go`, `cmd/innkeeper/worker.go`,
`internal/api/jobs.go`

- [ ] **Step 1: Add the job kind and runner**

Append to `internal/maintenance/maintenance.go`:

```go
// Kind is the maintenance job kind. Its payload names the action.
const Kind = "maintenance"

// Actions are the cleanups a Payload can name.
var Actions = []string{"trim-history", "stale-vectors", "clear-failures", "vacuum", "purge"}

// Payload is a maintenance job's input.
type Payload struct {
	Action string `json:"action"`
	Keep   int    `json:"keep,omitempty"`
	Days   int    `json:"days,omitempty"`
}

// Progress is a maintenance job's progress: rows or cards removed, and the
// database size after a vacuum.
type Progress struct {
	Removed       int64 `json:"removed"`
	DatabaseBytes int64 `json:"database_bytes,omitempty"`
}

// VectorSpace returns the vector instance and the space of its current
// vectors, or 0 when there is none.
type VectorSpace func(ctx context.Context) (int64, string, error)

// Runner is the jobs.Handler for Kind.
type Runner struct {
	M     *Maintenance
	Space VectorSpace
}

func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	var p Payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("read maintenance payload: %w", err)
	}
	var prog Progress
	publish := func() { raw, _ := json.Marshal(prog); r.SetProgress(raw) }
	var err error
	switch p.Action {
	case "trim-history":
		prog.Removed, err = rn.M.TrimHistory(ctx, p.Keep)
	case "stale-vectors":
		inst, space, serr := rn.Space(ctx)
		if serr != nil {
			return serr
		}
		prog.Removed, err = rn.M.RemoveStaleVectors(ctx, inst, space)
	case "clear-failures":
		prog.Removed, err = rn.M.ClearFailures(ctx)
	case "vacuum":
		prog.DatabaseBytes, err = rn.M.Vacuum(ctx)
	case "purge":
		_, err = rn.M.Purge(ctx, p.Days, func(n int) {
			prog.Removed += int64(n)
			r.AddCardsChanged(n)
			publish()
		})
	default:
		return fmt.Errorf("unknown maintenance action %q", p.Action)
	}
	publish()
	return err
}
```

With no vector instance, `Space` returns `0, ""`, and
`RemoveStaleVectors(ctx, 0, "")` deletes every vector, which the spec
says is right: none can be read.

Add imports `encoding/json` and `internal/jobs`.

- [ ] **Step 2: Wire it in the worker**

In `cmd/innkeeper/worker.go`:

```go
	maint := &maintenance.Runner{M: maintenance.New(svc.pool, svc.lib), Space: func(ctx context.Context) (int64, string, error) {
		inst, err := svc.registry.VectorInstance(ctx)
		if err != nil || inst == 0 {
			return 0, "", err
		}
		// The space vector.query answers in is the one current vectors use.
		v, err := svc.registry.VectorQuery(ctx, mgr, inst, "space probe")
		return inst, v.Space, err
	}}
```

and `maintenance.Kind: maint.Run` in the handlers map. In
`internal/api/jobs.go`'s labels: `maintenance.Kind: "Maintenance"`.

- [ ] **Step 3: Build and commit**

```bash
go build ./... && $DB go test ./cmd/... -count=1
git add internal cmd
git commit -m "feat(maintenance): run cleanups as one job kind"
```

---

### Task 4: The API

**Files:** create `internal/api/maintenance.go`, `internal/api/maintenance_test.go`,
`internal/api/server.go`

- [ ] **Step 1: Write the failing test**

`internal/api/maintenance_test.go`:

```go
func TestMaintenanceRoutes(t *testing.T) {
	e := newEnv(t)
	var p maintenance.Preview
	e.call("GET", "/maintenance?keep=3&days=30", nil, 200, &p)
	if p.DatabaseBytes == 0 || len(p.Largest) == 0 {
		t.Fatalf("preview %+v", p)
	}
	var j jobOut
	e.call("POST", "/maintenance/runs", map[string]any{"action": "clear-failures"}, 202, &j)
	var again jobOut
	e.call("POST", "/maintenance/runs", map[string]any{"action": "vacuum"}, 202, &again)
	if again.ID != j.ID {
		t.Fatal("a second maintenance job started while one was queued")
	}
	e.call("POST", "/maintenance/runs", map[string]any{"action": "nope"}, 422, nil)
	e.call("POST", "/maintenance/runs", map[string]any{"action": "purge", "days": 0}, 422, nil)
}

func TestMaintenanceRefusesAWriteToken(t *testing.T) {
	e := newEnv(t)
	tok := e.tokenWithTier("write")
	e.callAs(tok, "GET", "/maintenance", nil, 403, nil)
	e.callAs(tok, "POST", "/maintenance/runs", map[string]any{"action": "vacuum"}, 403, nil)
}
```

`tokenWithTier` and `callAs` stand for however `api_test.go` already makes a
token of a tier and calls with it (search for `tier.Write` in the tests);
use those.

- [ ] **Step 2: Run it and see it fail**

```bash
$DB go test ./internal/api -run TestMaintenance -count=1
```

Expected: 404 from `GET /maintenance`.

- [ ] **Step 3: Write the routes**

`internal/api/maintenance.go`:

```go
package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/maintenance"
	"github.com/inkandale/innkeeper/internal/tier"
)

type maintenancePreviewIn struct {
	Keep int `query:"keep" default:"3" minimum:"1" maximum:"100" doc:"Values to keep per instance, card and key."`
	Days int `query:"days" default:"30" minimum:"1" doc:"Purge cards archived at least this many days ago."`
}

type maintenancePreviewOut struct{ Body maintenance.Preview }

type maintenanceRunIn struct {
	Body struct {
		Action string `json:"action" enum:"trim-history,stale-vectors,clear-failures,vacuum,purge"`
		Keep   int    `json:"keep,omitempty" minimum:"1" maximum:"100" default:"3"`
		Days   int    `json:"days,omitempty" minimum:"1" default:"30"`
	}
}

func (s *Server) maintenanceRoutes(api huma.API) {
	tags := []string{"Maintenance"}
	m := maintenance.New(s.pool, s.Lib)
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "preview-maintenance", Method: http.MethodGet,
		Path: "/maintenance", Summary: "Preview the database cleanups", Tags: tags},
		func(ctx context.Context, in *maintenancePreviewIn) (*maintenancePreviewOut, error) {
			p, err := m.Preview(ctx, in.Keep, in.Days)
			if err != nil {
				return nil, s.fail(err)
			}
			return &maintenancePreviewOut{Body: p}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "run-maintenance", Method: http.MethodPost,
		Path: "/maintenance/runs", Summary: "Run a database cleanup", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues the cleanup. While one is queued or running, returns it instead. Trim, stale vectors, and purge can't be undone."},
		func(ctx context.Context, in *maintenanceRunIn) (*jobResp, error) {
			b := in.Body
			j, err := s.Queue.EnqueueOnce(ctx, jobs.Spec{Kind: maintenance.Kind, Payload: maintenance.Payload{Action: b.Action, Keep: b.Keep, Days: b.Days}})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
}
```

Check the `Server` field holding the pool (it may be `s.pool`, or reach it
through another field); if `Server` has none, add a `Maintenance
*maintenance.Maintenance` field set where `Server` is built in
`cmd/innkeeper/api.go`, rather than giving `Server` the pool.

Register `s.maintenanceRoutes(api)` in `server.go` after
`s.backupRoutes(api)`.

- [ ] **Step 4: Run, regenerate, commit**

```bash
$DB go test ./internal/api -run TestMaintenance -count=1
(cd web && npm run gen)
git add internal web/openapi.json web/src/api/schema.d.ts cmd
git commit -m "feat(api): preview and start database cleanups"
```

---

### Task 5: The tab

**Files:** create `web/src/settings/Maintenance.tsx`,
`web/src/settings/Maintenance.test.tsx`; modify `web/src/settings/Settings.tsx`,
`web/src/App.tsx`

- [ ] **Step 1: Write the failing test**

`web/src/settings/Maintenance.test.tsx`, following `Libraries.test.tsx`'s
mocking of `api.GET` and `api.POST`:

- renders the preview: database size, history rows and trimmable count,
  failures, purge counts, last backup;
- "Trim" posts `{action: "trim-history", keep: 3}`;
- the Purge button is disabled until the input holds `purge`, then posts
  `{action: "purge", days: 30}`;
- with `vectors.available: false`, the stale-vectors section isn't shown.

```bash
cd web && npx vitest run src/settings/Maintenance.test.tsx
```

Expected: FAIL: no module.

- [ ] **Step 2: Write the component**

`web/src/settings/Maintenance.tsx`:

```tsx
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import { JobLine } from "../cards/ImportForm";

const mb = (bytes: number) => `${(bytes / 1048576).toFixed(0)} MB`;

type Action = "trim-history" | "stale-vectors" | "clear-failures" | "vacuum" | "purge";

/** Maintenance previews and starts the database cleanups. */
export function Maintenance() {
  const [keep, setKeep] = useState(3);
  const [days, setDays] = useState(30);
  const [confirm, setConfirm] = useState("");
  const [job, setJob] = useState<number>();
  const preview = useQuery({
    queryKey: ["maintenance", keep, days],
    queryFn: async () => must(await api.GET("/maintenance", { params: { query: { keep, days } } })),
  });
  const run = useMutation({
    mutationFn: async (body: { action: Action; keep?: number; days?: number }) =>
      must(await api.POST("/maintenance/runs", { body })),
    onSuccess: (j) => setJob(j.id),
  });
  const p = preview.data;
  if (!p) return null;
  const busy = run.isPending;
  return (
    <div className="maintenance">
      <p className="muted">
        Database: {mb(p.database_bytes)}. Largest: {p.largest.map((t) => `${t.name} ${mb(t.bytes)}`).join(", ")}.
      </p>
      {job && <JobLine label="Maintenance" id={job} to={`/settings/jobs/${job}`} />}

      <section>
        <h3>Trim enrichment history</h3>
        <label>
          Keep the newest{" "}
          <input className="input" type="number" min={1} max={100} value={keep} onChange={(e) => setKeep(Number(e.target.value))} />{" "}
          values per card and key
        </label>
        <p className="muted">
          Deletes {p.history.trimmable.toLocaleString()} of {p.history.rows.toLocaleString()} values. Current values don't change.
        </p>
        <button className="button" disabled={busy} onClick={() => run.mutate({ action: "trim-history", keep })}>
          Trim
        </button>
      </section>

      {p.vectors.available && (
        <section>
          <h3>Remove stale vectors</h3>
          <p className="muted">
            Deletes vectors similar cards and search can't use: from another plugin, or made by an older model. Those cards
            rank by tags until they're run again. {p.vectors.rows.toLocaleString()} vectors stored.
          </p>
          <button className="button" disabled={busy} onClick={() => run.mutate({ action: "stale-vectors" })}>
            Remove stale vectors
          </button>
        </section>
      )}

      <section>
        <h3>Clear recorded failures</h3>
        <p className="muted">
          {p.failures.toLocaleString()} cards a plugin failed on aren't retried automatically. Clear them to retry, such as
          after changing a model.
        </p>
        <button className="button" disabled={busy || p.failures === 0} onClick={() => run.mutate({ action: "clear-failures" })}>
          Clear failures
        </button>
      </section>

      <section>
        <h3>Vacuum and analyze</h3>
        <p className="muted">Makes space freed by deletes reusable and refreshes query plans. Files on disk don't shrink.</p>
        <button className="button" disabled={busy} onClick={() => run.mutate({ action: "vacuum" })}>
          Vacuum
        </button>
      </section>

      <section>
        <h3>Purge archived cards</h3>
        <label>
          Archived at least{" "}
          <input className="input" type="number" min={1} value={days} onChange={(e) => setDays(Number(e.target.value))} /> days
          ago
        </label>
        <p className="muted">
          Deletes {p.purge.cards.toLocaleString()} cards and {p.purge.lorebooks.toLocaleString()} lorebooks for good. Copies
          in apps stay; archive with unpublish to remove those first.{" "}
          {p.last_backup ? `Last backup: ${new Date(p.last_backup).toLocaleString()}.` : "No backup has run."}
        </p>
        <label>
          Type <code>purge</code> to confirm{" "}
          <input className="input" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </label>{" "}
        <button
          className="button danger"
          disabled={busy || confirm !== "purge" || p.purge.cards + p.purge.lorebooks === 0}
          onClick={() => run.mutate({ action: "purge", days })}
        >
          Purge
        </button>
      </section>
    </div>
  );
}
```

Check the button class the app uses for destructive actions (search for
`danger` in `web/src`); use that one.

- [ ] **Step 3: Add the tab and route**

`Settings.tsx`: `<NavLink to="/settings/maintenance">Maintenance</NavLink>`
after Jobs. `App.tsx`, beside `{ path: "jobs", element: <Jobs /> }`:
`{ path: "maintenance", element: <Maintenance /> }` with its import.

- [ ] **Step 4: Run the web checks and commit**

```bash
cd web && npm run typecheck && npm test
git add web/src
git commit -m "feat(web): a Maintenance tab for database cleanups"
```

---

### Task 6: Docs and full check

- [ ] **Step 1: Document the tab**

Invoke the `documentation-writing` skill. Find where `docs/using.md`
describes Settings tabs (`grep -n "Settings" docs/using.md`) and add a
Maintenance section: one line per action, saying what it removes and that
trim, stale vectors, and purge can't be undone. Fix `docs/protocol.md` or
`docs/using.md` wherever they say purge keeps cards bound to an app.

- [ ] **Step 2: Run what CI runs**

```bash
go vet ./...
$DB go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add docs
git commit -m "docs: the Maintenance tab"
```
