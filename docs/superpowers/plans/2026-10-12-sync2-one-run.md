# One Sync Run Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One `target.sync` job reconciles every target app in turn, pulls edits made only in an app, runs hourly, and holds a lock that keeps other writes from interleaving with it.

**Architecture:** Step 2 of `docs/superpowers/specs/2026-10-11-single-sync-design.md` (INN-44).
- **The run:** `Service.Sync` takes the sync lock, then calls the existing `Reconcile` for each app.
- **Per-app results:** each app's snapshot is saved as a finished top-level `target.reconcile` job (`Queue.Record`). The current Sync page and review queue show them without UI changes.
- **Pulling edits:** reconcile marks a bound item `app_only` when every differing hunk is `auto_app`, and auto-apply binds it without `bindOnly`, so the app's changes become a library revision.
- **Locking:** a run holds a session advisory lock. Resolve and push transactions take the shared form of the same lock, unless their ctx belongs to the run.
- **Out of scope:** pass 2, pushing autosync bindings, is INN-45.

**Tech Stack:** Go, pgx v5, PostgreSQL advisory locks, Huma.

**Tests:** DB tests skip without the database URL, so a run without it proves nothing. Always run with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
`go test ./internal/targets` takes about two and a half minutes.

---

## Files

- Modify `internal/jobs/queue.go`: `Record` and `EnqueueOnce`. Test: `internal/jobs/queue_test.go`.
- Modify `internal/wire/reconcile.go`: `AppOnly` and `AutoPulled` on items; `SyncKind`, `SyncParams`, `SyncResult` and `SyncApp`.
- Modify `internal/targets/wire.go`: aliases for the new types.
- Modify `internal/targets/reconcile.go`: mark app-only items, pull them in `autoApply`, count `auto_pulled`. Test: `internal/targets/reconcile_test.go`.
- Create `internal/targets/sync.go`: the lock, `Sync`, `RunSync` and `EnqueueSync`. Test: `internal/targets/sync_test.go`.
- Modify `internal/targets/reconcile_job.go`: take the lock.
- Modify `internal/targets/push.go` and `internal/targets/resolve.go`: wait for a running sync.
- Modify `internal/api/reconcile.go`: `POST /syncs`. Test: `internal/api/reconcile_test.go`.
- Modify `cmd/innkeeper/worker.go`: the handler and the `INNKEEPER_SYNC_EVERY` schedule.
- Modify `README.md`: the env var row.

---

### Task 1: `Queue.Record` and `Queue.EnqueueOnce`

**Files:**
- Modify: `internal/jobs/queue.go`
- Test: `internal/jobs/queue_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/jobs/queue_test.go`. Add `"encoding/json"` and `"errors"` to its imports if missing.

```go
func TestEnqueueOnce(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	a, err := q.EnqueueOnce(ctx, Spec{Kind: "t.once"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.EnqueueOnce(ctx, Spec{Kind: "t.once"})
	if err != nil || b.ID != a.ID {
		t.Fatalf("second start = %d, %v; want job %d", b.ID, err, a.ID)
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET status = 'succeeded', finished_at = now() WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	c, err := q.EnqueueOnce(ctx, Spec{Kind: "t.once"})
	if err != nil || c.ID == a.ID || c.Status != StatusQueued {
		t.Fatalf("start after finish = %+v, %v", c, err)
	}
}

func TestRecord(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	ok, err := q.Record(ctx, Spec{Kind: "t.rec", Payload: map[string]int{"n": 1}}, json.RawMessage(`{"x":1}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	var p struct{ X int }
	if ok.Status != StatusSucceeded || ok.FinishedAt == nil || json.Unmarshal(ok.Progress, &p) != nil || p.X != 1 {
		t.Errorf("recorded = %+v", ok)
	}
	bad, err := q.Record(ctx, Spec{Kind: "t.rec"}, nil, errors.New("boom"))
	if err != nil || bad.Status != StatusFailed || bad.Error == nil || *bad.Error != "boom" || bad.FinishedAt == nil {
		t.Errorf("failed record = %+v, %v", bad, err)
	}
	listed, err := q.List(ctx, ListOptions{Kind: "t.rec"})
	if err != nil || len(listed) != 2 {
		t.Errorf("listed %d, %v", len(listed), err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/jobs -run 'EnqueueOnce|Record'`
Expected: a build failure, `q.EnqueueOnce undefined`.

- [ ] **Step 3: Implement**

In `internal/jobs/queue.go`, replace the payload lines at the top of `insert`:

```go
	payload := []byte(`{}`)
	if s.Payload != nil {
		var err error
		if payload, err = json.Marshal(s.Payload); err != nil {
			return Job{}, err
		}
	}
```

with

```go
	payload, err := payloadOf(s)
	if err != nil {
		return Job{}, err
	}
```

Then add after `insert`:

```go
func payloadOf(s Spec) ([]byte, error) {
	if s.Payload == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(s.Payload)
}
```

Add after `Enqueue`:

```go
// EnqueueOnce returns the queued or running top-level job of s's kind, or
// enqueues s when there is none.
func (q *Queue) EnqueueOnce(ctx context.Context, s Spec) (Job, error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback(ctx)
	// The schedule's lock, so a scheduled run and a manual start don't both enqueue.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-schedule'))`); err != nil {
		return Job{}, err
	}
	j, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobCols+` FROM job WHERE kind = $1 AND parent_id IS NULL
		AND status IN ('queued', 'running') ORDER BY id LIMIT 1`, s.Kind))
	if errors.Is(err, ErrNotFound) {
		j, err = insert(ctx, tx, s, nil, StatusQueued)
	}
	if err != nil {
		return Job{}, err
	}
	return j, tx.Commit(ctx)
}

// Record adds a finished top-level job holding progress, for work another
// job did itself. A failure fails it with that message.
func (q *Queue) Record(ctx context.Context, s Spec, progress json.RawMessage, failure error) (Job, error) {
	payload, err := payloadOf(s)
	if err != nil {
		return Job{}, err
	}
	if progress == nil {
		progress = json.RawMessage(`{}`)
	}
	status, msg := StatusSucceeded, (*string)(nil)
	if failure != nil {
		m := failure.Error()
		status, msg = StatusFailed, &m
	}
	return scanJob(q.pool.QueryRow(ctx, `INSERT INTO job (kind, instance_id, payload, status, progress, error, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, now()) RETURNING `+jobCols,
		s.Kind, s.InstanceID, payload, status, progress, msg))
}
```

- [ ] **Step 4: Run the package**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/jobs`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs/queue.go internal/jobs/queue_test.go
git commit -m "feat(jobs): enqueue a kind once, and record work another job did"
```

---

### Task 2: reconcile pulls edits made only in the app

**Files:**
- Modify: `internal/wire/reconcile.go`
- Modify: `internal/targets/reconcile.go`
- Test: `internal/targets/reconcile_test.go`

A bound item is **app-only** when its comparison with the library, using the binding's accepted copy as the ancestor, has differing hunks and every one of them is `merge.AutoApp`. A bind resolution applies `auto_*` hunks by status whatever the choices, so a plain bind takes the app's changes. It has to run without `bindOnly`, because it writes a library revision.

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/reconcile_test.go`. It uses `setup`, `push`, `editRemote`, `update`, `setField`, `head` and `status` from `env_test.go`.

```go
// An edit made only in the app becomes a library revision.
func TestReconcilePullsAppOnlyEdits(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote(b.RemoteID, "description", "Edited in the app.")
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := snap.Items[0]
	if !it.AppOnly || !it.AutoPulled || it.AutoBound || snap.Totals["auto_pulled"] != 1 {
		t.Fatalf("item = %+v, totals %v", it, snap.Totals)
	}
	var data map[string]any
	if err := json.Unmarshal(e.head(e.card).Data, &data); err != nil || data["description"] != "Edited in the app." {
		t.Errorf("library = %v, %v", data, err)
	}
	if got := e.status(e.card); got != "in_sync" {
		t.Errorf("status %s", got)
	}
}

// Edits on both sides stay for the owner, even when they don't conflict.
func TestReconcileLeavesBothSidesEdited(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	e.editRemote(b.RemoteID, "description", "Edited in the app.")
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "personality", "Edited in the library.") })
	before := e.head(e.card).ID
	snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
	if err != nil {
		t.Fatal(err)
	}
	it := snap.Items[0]
	if it.AppOnly || it.AutoPulled || e.head(e.card).ID != before {
		t.Errorf("item = %+v; head moved: %v", it, e.head(e.card).ID != before)
	}
}
```

If `Binding` names the remote ID field differently from `RemoteID`, use its real name; see `internal/wire`.

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets -run 'PullsAppOnly|BothSidesEdited'`
Expected: a build failure, `it.AppOnly undefined`.

- [ ] **Step 3: Add the wire fields**

In `internal/wire/reconcile.go`, add to `ReconcileItem` after `AutoImported`:

```go
	// AppOnly means the app changed this bound item and the library didn't
	// since the last push or resolve.
	AppOnly bool `json:"app_only,omitempty"`
	// AutoPulled means the run took the app's changes into the library.
	AutoPulled bool `json:"auto_pulled,omitempty"`
```

Change `Snapshot.Totals`'s comment to `// by verdict, auto_bound, auto_imported, and auto_pulled; each world book once`.

- [ ] **Step 4: Mark app-only items in `suggest`**

In `internal/targets/reconcile.go`, `suggest`:

1. Change `var core []int` to:

```go
	var core []int
	// appOnly[i]: every hunk that differs from candidate i is the app's change alone.
	var appOnly []bool
```

2. In the candidate loop, declare `only := true` next to `n := 0`. In the hunk loop, after the `merge.Conflict` check, add:

```go
				if h.Status != merge.Same && h.Status != merge.AutoApp {
					only = false
				}
```

3. Right after `core = append(core, n)`, add:

```go
		appOnly = append(appOnly, only && c.DifferingFields > 0)
```

4. Replace the `VerdictBound` case:

```go
	case VerdictBound:
		if len(item.Candidates) > 0 && item.Candidates[0].DifferingFields > 0 {
			why := "the app and the library differ; bind to resolve the drift"
			if item.AppOnly = appOnly[0]; item.AppOnly {
				why = "changed only in the app"
			}
			bind(item.Candidates[0], why)
		}
```

- [ ] **Step 5: Pull them in `autoApply`**

In `autoApply`, replace the block from `var r Resolution` through the `res, err := s.resolve(...)` line with:

```go
		var r Resolution
		pull := false
		switch {
		case it.Suggested == nil:
			continue
		case it.Verdict == VerdictExact && it.Suggested.Action == "bind":
			r = Resolution{InstanceID: instanceID, RemoteID: it.RemoteID, AppHash: it.AppHash,
				BaseRevisionID: idx.byID[it.Suggested.CardID].head, Action: "bind", CardID: it.Suggested.CardID,
				Default: merge.ChooseLibrary}
		case it.Verdict == VerdictNew && it.Suggested.Action == "import" && !imported[k.name+":"+it.AppHash]:
			r = Resolution{InstanceID: instanceID, RemoteID: it.RemoteID, AppHash: it.AppHash,
				BindingID: it.BindingID, Action: "import"}
		case it.Verdict == VerdictBound && it.AppOnly && it.Suggested.Action == "bind":
			// A bind takes auto_app hunks by status, so it brings the app's changes in.
			r = Resolution{InstanceID: instanceID, RemoteID: it.RemoteID, AppHash: it.AppHash,
				BaseRevisionID: idx.byID[it.Suggested.CardID].head, AcceptedHash: idx.byRemote[it.RemoteID].acceptedHash,
				Action: "bind", CardID: it.Suggested.CardID}
			pull = true
		default:
			continue
		}
		opts := resolveOpts{bindOnly: true, importNew: true}
		if pull {
			// Writes a revision, and leaves the card's world books as they are.
			opts = resolveOpts{skipLeftOut: true}
		} else {
			// This card's own world books may be copies of each other too.
			importing := map[string]bool{}
			for _, lb := range it.Lorebooks {
				// (keep the existing loop body here unchanged)
			}
		}
		res, err := s.resolve(ctx, k, r, opts)
```

Keep the existing world-book loop body as it is. Its `continue items` still works inside the `else`. Then replace

```go
		it.AutoBound, it.AutoImported = res.Action == "bind", res.Action == "import"
```

with

```go
		it.AutoPulled = pull
		it.AutoBound, it.AutoImported = res.Action == "bind" && !pull, res.Action == "import"
```

ponytail: a world book linked to a card is only resolved through its card, so an app-only edit to it waits for the owner. Add this line as a `ponytail:` comment on the `pull` case.

- [ ] **Step 6: Count `auto_pulled`**

In `Reconcile`, inside `if p.Apply != ApplyNone`:

```go
		type auto struct{ bound, imported, pulled bool }
```

```go
			books[lb.RemoteID] = auto{a.bound || lb.AutoBound, a.imported || lb.AutoImported, a.pulled || lb.AutoPulled}
```

```go
			all = append(all, auto{it.AutoBound, it.AutoImported, it.AutoPulled})
```

and in the final switch, add after `case a.bound:` and its line:

```go
			case a.pulled:
				snap.Totals["auto_pulled"]++
```

- [ ] **Step 7: Run the whole package**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets`
Expected: `ok`. `withDefaults` makes `ApplyObvious` the default, so an existing test that calls `Reconcile` without `Apply`, on a bound item edited only in the app, now pulls the edit:
- If that test is about previewing, set `Apply: ApplyNone` in its params.
- If it asserts the old reason text for an app-only drift, change the expected text to `changed only in the app`.

Change nothing else, and list every test you changed in your report.

- [ ] **Step 8: Commit**

```bash
git add internal/wire/reconcile.go internal/targets/reconcile.go internal/targets/reconcile_test.go
git commit -m "feat(targets): a sync pulls edits made only in the app"
```

---

### Task 3: the sync lock

**Files:**
- Create: `internal/targets/sync.go`
- Modify: `internal/targets/reconcile_job.go`, `internal/targets/push.go`, `internal/targets/resolve.go`
- Test: `internal/targets/sync_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/targets/sync_test.go`:

```go
package targets

import (
	"testing"
	"time"
)

// A resolve outside a sync waits for it; the sync's own resolves don't.
func TestResolveWaitsForSync(t *testing.T) {
	e := setup(t, nil)
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	e.appCard("r2", map[string]any{"name": "Mira", "description": miraText}, nil)
	items := e.reconcile(ReconcileParams{InstanceID: e.inst}, "r1", "r2")
	runCtx, release, err := e.s.lockSync(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Resolve(runCtx, Resolution{InstanceID: e.inst, RemoteID: "r2", AppHash: items["r2"].AppHash,
		Action: "import"}); err != nil {
		t.Fatalf("the sync's own resolve: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "r1", AppHash: items["r1"].AppHash,
			Action: "import"})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("resolve finished during a sync: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets -run TestResolveWaitsForSync`
Expected: a build failure, `e.s.lockSync undefined`.

- [ ] **Step 3: Implement the lock**

Create `internal/targets/sync.go`:

```go
package targets

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// syncLock is the advisory lock a sync or reconcile run holds for its whole
// run. Resolves and pushes take it shared, so they wait for a run instead of
// deciding from what the run is about to change.
const syncLock = "innkeeper-sync"

// syncRun marks a ctx as belonging to the run that holds syncLock.
type syncRun struct{}

// lockSync takes syncLock on a connection of its own and returns ctx marked
// as the run's, so the run's own resolves don't wait on it. A ctx that
// already holds it is returned as is.
//
// ponytail: one more pooled connection per run; the worker's pool allows
// two per job.
func (s *Service) lockSync(ctx context.Context) (context.Context, func(), error) {
	if ctx.Value(syncRun{}) != nil {
		return ctx, func() {}, nil
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return ctx, nil, err
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, syncLock); err != nil {
		conn.Release()
		return ctx, nil, err
	}
	release := func() {
		// A session lock outlives the run on a pooled connection; drop one that can't unlock.
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, syncLock); err != nil {
			_ = conn.Conn().Close(context.Background())
		}
		conn.Release()
	}
	return context.WithValue(ctx, syncRun{}, true), release, nil
}

// waitForSync makes tx wait for a running sync, unless ctx is that run.
func waitForSync(ctx context.Context, tx pgx.Tx) error {
	if ctx.Value(syncRun{}) != nil {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))`, syncLock)
	return err
}
```

- [ ] **Step 4: Take it in resolve, push, and the reconcile job**

`internal/targets/resolve.go`, in `resolve`, after `defer tx.Rollback(ctx)`:

```go
	if err := waitForSync(ctx, tx); err != nil {
		return ResolveResult{}, err
	}
```

`internal/targets/push.go`, after `defer tx.Rollback(ctx)` (around line 77) and before `lock(ctx, tx, ...)`:

```go
	if err := waitForSync(ctx, tx); err != nil {
		return Binding{}, err
	}
```

`internal/targets/reconcile_job.go`, in `RunReconcile`, after `p.InstanceID = *r.InstanceID`:

```go
	ctx, release, err := s.lockSync(ctx)
	if err != nil {
		return err
	}
	defer release()
```

Then change the next line's `snap, err := s.Reconcile(` to `snap, err = s.Reconcile(` and declare `var snap Snapshot` above it, or rename, whichever compiles cleanly.

- [ ] **Step 5: Run the whole package**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets`
Expected: `ok`. If a test hangs, a path takes the shared lock while its own run holds the exclusive one. Find the path that was reached without the run's ctx and pass the ctx through. Don't remove the lock.

- [ ] **Step 6: Commit**

```bash
git add internal/targets/sync.go internal/targets/sync_test.go internal/targets/resolve.go internal/targets/push.go internal/targets/reconcile_job.go
git commit -m "feat(targets): resolves and pushes wait for a running sync"
```

---

### Task 4: the sync run

**Files:**
- Modify: `internal/wire/reconcile.go`, `internal/targets/wire.go`, `internal/targets/sync.go`
- Test: `internal/targets/sync_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/targets/sync_test.go`. Add the imports `"encoding/json"`, `"errors"`, `"fmt"`, `"os"`, `"path/filepath"`, `"github.com/davidgibbons/innkeeper/internal/jobs"`, `"github.com/davidgibbons/innkeeper/internal/library"` and `"github.com/davidgibbons/innkeeper/internal/plugins"`.

```go
// addApp adds another fake target app with a dir of its own.
func (e *env) addApp() (int64, string) {
	e.t.Helper()
	dir := e.t.TempDir()
	id, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "fake",
		Name: fmt.Sprintf("App %s", filepath.Base(dir)), Config: map[string]any{"dir": dir}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id, dir
}

// copyItem puts e's app item remoteID into another app's dir too.
func (e *env) copyItem(remoteID, dir string) {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, remoteID+".json"))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, remoteID+".json"), raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// cardsNamed counts library cards whose head is named name.
func (e *env) cardsNamed(name string) []int64 {
	e.t.Helper()
	var ids []int64
	rows, err := e.pool.Query(e.ctx, `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE r.data->>'name' = $1 ORDER BY c.id`, name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// A sync runs every app in turn, so a card in two apps ends up as one card.
func TestSyncEveryApp(t *testing.T) {
	e := setup(t, nil)
	inst2, dir2 := e.addApp()
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	e.copyItem("r1", dir2)
	raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0",
		"data": map[string]any{"name": "Mira", "description": miraText}})
	if err := os.WriteFile(filepath.Join(dir2, "r2.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Sync(e.ctx, SyncParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Apps) != 2 || res.Apps[0].InstanceID != e.inst || res.Apps[1].InstanceID != inst2 {
		t.Fatalf("apps = %+v", res.Apps)
	}
	lia := e.cardsNamed("Lia")
	if len(lia) != 1 || len(e.cardsNamed("Mira")) != 1 {
		t.Fatalf("Lia %v, Mira %v", lia, e.cardsNamed("Mira"))
	}
	if bs, err := e.s.Bindings(e.ctx, lia[0]); err != nil || len(bs) != 2 {
		t.Errorf("Lia bindings %d, %v", len(bs), err)
	}
	for _, app := range res.Apps {
		j, err := jobs.New(e.pool).Get(e.ctx, app.JobID)
		var snap Snapshot
		if err != nil || app.Error != "" || j.Kind != ReconcileKind || j.Status != jobs.StatusSucceeded ||
			*j.InstanceID != app.InstanceID || json.Unmarshal(j.Progress, &snap) != nil || len(snap.Items) == 0 {
			t.Errorf("app %d: job %+v, %v", app.InstanceID, j, err)
		}
	}
}

// One app failing doesn't stop the others.
func TestSyncAppFails(t *testing.T) {
	e := setup(t, nil)
	_, dir2 := e.addApp()
	if err := os.WriteFile(filepath.Join(dir2, "r9.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	res, err := e.s.Sync(e.ctx, SyncParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Apps) != 2 || res.Apps[0].Error != "" || res.Apps[1].Error == "" || len(e.cardsNamed("Lia")) != 1 {
		t.Fatalf("apps = %+v", res.Apps)
	}
	j, err := jobs.New(e.pool).Get(e.ctx, res.Apps[1].JobID)
	if err != nil || j.Status != jobs.StatusFailed {
		t.Errorf("failed app job = %+v, %v", j, err)
	}
}

// Starting a sync while one is queued returns that one.
func TestEnqueueSyncJoins(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	a, err := e.s.EnqueueSync(e.ctx, q, SyncParams{})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := e.s.EnqueueSync(e.ctx, q, SyncParams{InstanceID: e.inst}); err != nil || b.ID != a.ID {
		t.Errorf("second start = %d, %v; want %d", b.ID, err, a.ID)
	}
	if _, err := e.s.EnqueueSync(e.ctx, q, SyncParams{Apply: "all"}); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("bad apply: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets -run 'TestSync|EnqueueSync'`
Expected: a build failure, `undefined: SyncParams`.

- [ ] **Step 3: Add the wire types**

Append to `internal/wire/reconcile.go`:

```go
// SyncKind is the job kind that syncs every target app, one at a time.
const SyncKind = "target.sync"

// SyncParams starts a sync.
type SyncParams struct {
	// InstanceID limits the sync to one app; 0 syncs every app that can pull.
	InstanceID int64 `json:"instance_id,omitempty" required:"false"`
	// Apply is as in ReconcileParams; empty means ApplyObvious.
	Apply string `json:"apply,omitempty" enum:"obvious,none" required:"false"`
}

// SyncResult is a sync's progress, and its result once it finishes.
type SyncResult struct {
	Apps []SyncApp `json:"apps"`
}

// SyncApp is one app's part of a sync.
type SyncApp struct {
	InstanceID int64 `json:"instance_id"`
	// JobID is the finished reconcile job holding this app's snapshot, as
	// the review queue reads it.
	JobID  int64          `json:"job_id"`
	Totals map[string]int `json:"totals,omitempty"`
	Error  string         `json:"error,omitempty"`
}
```

In `internal/targets/wire.go`, add `SyncParams = wire.SyncParams`, `SyncResult = wire.SyncResult` and `SyncApp = wire.SyncApp` to the type block, and `SyncKind = wire.SyncKind` to the const block.

- [ ] **Step 4: Implement the run**

In `internal/targets/sync.go`, extend the imports to `"cmp"`, `"context"`, `"encoding/json"`, `"fmt"`, `"slices"`, pgx, and `"github.com/davidgibbons/innkeeper/internal/jobs"` and `"github.com/davidgibbons/innkeeper/internal/library"`. Then append:

```go
// EnqueueSync queues a sync, or returns the one already queued or running.
func (s *Service) EnqueueSync(ctx context.Context, q *jobs.Queue, p SyncParams) (jobs.Job, error) {
	p.Apply = cmp.Or(p.Apply, ApplyObvious)
	if p.Apply != ApplyObvious && p.Apply != ApplyNone {
		return jobs.Job{}, fmt.Errorf("%w: apply must be obvious or none", library.ErrInvalid)
	}
	if p.InstanceID != 0 {
		if _, _, err := s.describeReconcile(ctx, cardKind, p.InstanceID); err != nil {
			return jobs.Job{}, err
		}
	}
	// ponytail: a start joins any running sync, even one limited to another app.
	return q.EnqueueOnce(ctx, jobs.Spec{Kind: SyncKind, Payload: p})
}

// RunSync is the jobs.Handler for SyncKind.
func (s *Service) RunSync(ctx context.Context, r *jobs.Run) error {
	var p SyncParams
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("sync payload: %w", err)
	}
	report := func(res SyncResult) {
		raw, _ := json.Marshal(res)
		r.SetProgress(raw)
	}
	res, err := s.Sync(ctx, p, report)
	if err != nil {
		return err
	}
	report(res)
	return nil
}

// Sync reconciles every target app that can pull, one at a time, or only
// p.InstanceID, holding the sync lock throughout. Each app's run is recorded
// as a finished reconcile job, which the review queue reads. An app that
// fails is recorded as a failed job and the sync goes on. progress, if set,
// hears after each app.
func (s *Service) Sync(ctx context.Context, p SyncParams, progress func(SyncResult)) (SyncResult, error) {
	res := SyncResult{Apps: []SyncApp{}}
	ids, err := s.syncApps(ctx, p.InstanceID)
	if err != nil {
		return res, err
	}
	ctx, release, err := s.lockSync(ctx)
	if err != nil {
		return res, err
	}
	defer release()
	q := jobs.New(s.pool)
	for _, id := range ids {
		params := ReconcileParams{InstanceID: id, Apply: cmp.Or(p.Apply, ApplyObvious)}
		snap, err := s.Reconcile(ctx, params, nil)
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		app := SyncApp{InstanceID: id}
		var raw json.RawMessage
		if err != nil {
			app.Error = err.Error()
		} else {
			app.Totals, params = snap.Totals, snap.Params
			if raw, err = json.Marshal(snap); err != nil {
				return res, err
			}
		}
		j, rerr := q.Record(ctx, jobs.Spec{Kind: ReconcileKind, InstanceID: &id, Payload: params}, raw, errorOrNil(app.Error))
		if rerr != nil {
			return res, rerr
		}
		app.JobID = j.ID
		res.Apps = append(res.Apps, app)
		if progress != nil {
			progress(res)
		}
	}
	return res, nil
}

// syncApps is the instances a sync visits: only, or every working
// instance whose plugin can push and pull, in ID order.
func (s *Service) syncApps(ctx context.Context, only int64) ([]int64, error) {
	if only != 0 {
		return []int64{only}, nil
	}
	insts, err := s.reg.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, in := range insts {
		if in.BrokenError == nil && s.reg.HasCapability(in.Plugin, "push") && s.reg.HasCapability(in.Plugin, "pull") {
			ids = append(ids, in.ID)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func errorOrNil(msg string) error {
	if msg == "" {
		return nil
	}
	return errors.New(msg)
}
```

Add `"errors"` to the imports. Remove any import left unused.

- [ ] **Step 5: Run the whole package**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
git add internal/wire/reconcile.go internal/targets/wire.go internal/targets/sync.go internal/targets/sync_test.go
git commit -m "feat(targets): one sync run reconciles every app in turn"
```

---

### Task 5: API route, worker schedule, README

**Files:**
- Modify: `internal/api/reconcile.go`, `internal/api/reconcile_test.go`, `cmd/innkeeper/worker.go`, `README.md`

- [ ] **Step 1: Write the failing API test**

In `internal/api/reconcile_test.go`, add `targets.SyncKind: e.s.Targets.RunSync` to the `Handlers` map in `work`. Then append:

```go
func TestSyncRoute(t *testing.T) {
	e := newEnv(t)
	e.work()
	_, dir := e.target()
	e.appCard(dir, "rCarrow", "Carrow", "Hello.")
	var j jobOut
	e.call("POST", "/syncs", map[string]any{}, 202, &j)
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("sync = %+v\n%s", last, r.Body)
	}
	e.call("GET", fmt.Sprintf("/jobs/%d", j.ID), nil, 200, &j)
	var res targets.SyncResult
	if err := json.Unmarshal(j.Progress, &res); err != nil || len(res.Apps) != 1 || res.Apps[0].JobID == 0 {
		t.Fatalf("result = %s, %v", j.Progress, err)
	}
	var app jobOut
	e.call("GET", fmt.Sprintf("/jobs/%d", res.Apps[0].JobID), nil, 200, &app)
	if app.Kind != targets.ReconcileKind || app.Status != "succeeded" {
		t.Errorf("app job = %+v", app)
	}
	e.call("POST", "/syncs", map[string]any{"instance_id": 999}, 404, nil)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run TestSyncRoute`
Expected: FAIL. `POST /syncs` returns 404 or 405.

- [ ] **Step 3: Add the route**

In `internal/api/reconcile.go`, add next to `reconcileIn`:

```go
type syncIn struct{ Body targets.SyncParams }
```

and register it in `reconcileRoutes`, before `start-reconcile`:

```go
	huma.Register(api, huma.Operation{Metadata: needs(tier.Write), OperationID: "start-sync", Method: http.MethodPost,
		Path: "/syncs", Summary: "Sync every target app with the library", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Queues a sync of every app, one at a time, or returns the one already queued or running. " +
			"Each app's snapshot is a finished reconcile job named in the result's apps; read it from GET /jobs/{id}."},
		func(ctx context.Context, in *syncIn) (*jobResp, error) {
			if in.Body.InstanceID != 0 {
				if err := s.usable(ctx, in.Body.InstanceID, "pull"); err != nil {
					return nil, err
				}
			}
			j, err := s.Targets.EnqueueSync(ctx, s.Queue, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
```

- [ ] **Step 4: Run the API package**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api`
Expected: `ok`. If a test enumerates every operation, such as an OpenAPI golden file or a tier table, add `start-sync` to it as `write`.

- [ ] **Step 5: Schedule it in the worker**

In `cmd/innkeeper/worker.go`, after the `INNKEEPER_WORKER_CONCURRENCY` block, add:

```go
	syncEvery := time.Hour
	if v := os.Getenv("INNKEEPER_SYNC_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return fmt.Errorf("INNKEEPER_SYNC_EVERY: %q is not a duration like 1h, or 0 for no schedule", v)
		}
		syncEvery = d
	}
```

Add `targets.SyncKind: tg.RunSync,` to the `Handlers` map, and after the `Schedules` literal:

```go
	if syncEvery > 0 {
		w.Schedules = append(w.Schedules, jobs.Schedule{Kind: targets.SyncKind, Every: syncEvery})
	}
```

Add `"fmt"` to the imports if missing. Run `go build ./cmd/innkeeper`.

- [ ] **Step 6: Document the variable**

In `README.md`, in the environment table, add this row after the `INNKEEPER_BACKUP_ASSETS_EVERY` row, matching its format:

```markdown
| `INNKEEPER_SYNC_EVERY` | `worker` | How often to sync every app, such as `30m` or `6h`. `0` turns the schedule off. Default `1h` |
```

- [ ] **Step 7: Commit**

```bash
git add internal/api/reconcile.go internal/api/reconcile_test.go cmd/innkeeper/worker.go README.md
git commit -m "feat(api): start a sync of every app, and run one hourly"
```

---

### Task 6: full check

- [ ] **Step 1:** Run `go vet ./... && INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./...`. Expected: every package `ok` or `no test files`.
- [ ] **Step 2:** Run `cd web && npm test -- --run` to confirm the web app still passes. It reads the same reconcile jobs as before.
