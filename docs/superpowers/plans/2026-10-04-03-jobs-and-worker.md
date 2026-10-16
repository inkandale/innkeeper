# Jobs and Worker Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the job queue, the `innkeeper worker` service that runs it, checkpointed imports through `import` plugins, bulk actions as parent and child jobs, and blob garbage collection.

**Architecture:** `internal/jobs` keeps jobs in our own `job` table. Workers claim jobs with `FOR UPDATE SKIP LOCKED`, hold them with a lease they renew, and wake on `LISTEN/NOTIFY`. `internal/plugins` maps `plugin_instance` rows to what the plugin host runs. `internal/importer` calls `import.run` and stores what the plugin emits. A run token in every notification routes it to its job. `internal/bulk` holds per-card child job kinds. `cmd/innkeeper/worker.go` wires everything together.

**Tech Stack:** Go 1.27, `github.com/jackc/pgx/v5` v5.11.0, PostgreSQL 18. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, sections "Architecture", "Jobs", "Plugin protocol" (`import`, Errors, Blobs), and "Error handling".

**Kaneo:** INN-3.

---

## Why our own table, not River

The INN-3 ticket says to use River if checkpointing fits. Its resumable steps would do for checkpoints, but two other things don't fit:

- **No lease.** River's rescuer re-runs a job once `RescueStuckJobsAfter` (default one hour) has passed since the job started. There is no heartbeat. An import that runs for hours would need a rescue window longer than the import, so a job whose worker crashed would wait that long too.
- **Parent and child jobs are River Pro.** Workflows are a paid feature.

The spec's `job` table already describes what we need, and our version is about as much code as River's setup and adapters would be.

## Plan series

This is plan 3 of the core. See plan 1 for the full list.

Constraints this plan leaves for later plans:

- **Plan 4:**
  - Instance secrets are unbuilt: add an encrypted `secrets` column, the startup check for the secrets key, and pass decrypted secrets in `plugins.Registry.Instance`.
  - Saving an instance must clear `broken_error` and `broken_at`, and call the `Manager.Stop(id)` that plan 1 asked for.
  - Map `library.ErrInvalid` to `400`.
  - Enqueue imports as `jobs.Spec{Kind: importer.Kind, InstanceID: &id, Payload: importer.Payload{Params: ...}}`. A bulk action turns a search query into card IDs, then enqueues the children, as `bulk.EnqueueArchive` does.
  - The job list reads `job.progress`. An import's progress has `done`, `total`, `message`, `imported`, `skipped`, `failed`, and `errors`. A parent's state comes from counting its children.
  - Unbuilt: cancelling a job, and deleting old finished jobs.
- **Plan 5:** Pushes and drift scans are new job kinds. Scheduled drift scans use `jobs.Worker.Schedules`, and a bulk push uses `Queue.EnqueueBulk`. Override keys are `plugin_instance.id` values as strings.
- **Plan 6:** The folder importer implements `import.run` as `docs/protocol.md` now describes it. For uploads, the API has to get the file to the plugin; decide how in plan 4 or 6.
- **Archive catalog:** `initialize` still sends no `database_url`. The plugin's own role and schema are unbuilt.
- **Contract kit:** it has no `import` check, because each plugin defines its own `import.run` params.

## Design notes

- **Run token.** One plugin process can run several imports at once, and notifications carry no request ID. So `import.run` sends a `run` token, `<job id>.<attempt>`, and the plugin puts it in every notification. A notification from an abandoned earlier attempt has a stale token, so it is dropped.
- **Ordering.** The plugin connection handles notifications one at a time in the order sent. So when a `checkpoint` arrives, every item emitted before it is already stored.
- **Resuming.** Items emitted after the last checkpoint are emitted again on retry. `Entities.Import` skips an item when an entity with the same provenance (`<plugin>:<item>`) has ever had the same content hash. That also makes importing the same folder twice a no-op.
- **Failures.** A plugin crash (`protocol.ErrClosed`), a retryable JSON-RPC error, or a database error while storing items re-queues the job with backoff. A plugin that fails to start marks its instance broken and fails the job. A bad item is counted as failed, and the import goes on.

## File structure

| File | Responsibility |
|---|---|
| `internal/db/migrations/0002_jobs.sql` | `plugin_instance`, `job`, the NOTIFY trigger, lookup indexes |
| `internal/jobs/queue.go` | Enqueue, bulk enqueue, schedules; claim, lease, finish, reap |
| `internal/jobs/worker.go` | `Worker`: claim loops, LISTEN, heartbeat, retries, shutdown |
| `protocol/messages.go` | `import.run` and its notifications |
| `internal/fakeplugin/main.go` | Adds `-caps` and a fake importer |
| `internal/testutil/fakeplugin.go` | Adds `FakePluginWith` for other capabilities |
| `internal/pluginhost/manager.go` | Adds `StartError` |
| `internal/plugins/plugins.go` | Manifests on disk, `plugin_instance` rows, marking broken |
| `internal/library/import.go` | `Slugify`, `Entities.Import` with provenance dedupe |
| `internal/library/gc.go` | `CollectGarbage` for unreferenced blobs |
| `internal/blobstore/blobstore.go` | Adds `Remove` and `RemoveStale` |
| `internal/importer/importer.go` | Import job handler and notification routing |
| `internal/importer/ingest.go` | Stores emitted cards and lorebooks |
| `internal/bulk/bulk.go` | `card.archive` bulk action |
| `cmd/innkeeper/worker.go` | `innkeeper worker` |
| `docs/protocol.md` | Documents the `import` capability |

Run the database tests with a local server:

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

---

### Task 1: Jobs schema

**Files:**
- Create: `internal/db/migrations/0002_jobs.sql`
- Modify: `internal/db/db_test.go`

- [ ] **Step 1: Update the failing test**

In `internal/db/db_test.go`, `TestMigrateTwice` counts applied migrations. Change both `1`s to `2`:

```go
	if n != 2 {
		t.Fatalf("schema_migration has %d rows, want 2", n)
	}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/db -run TestMigrateTwice`
Expected: FAIL with `schema_migration has 1 rows, want 2`.

- [ ] **Step 3: Write the migration**

Create `internal/db/migrations/0002_jobs.sql`:

```sql
CREATE TABLE plugin_instance (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    plugin text NOT NULL CHECK (plugin ~ '^[a-z][a-z0-9]*(-[a-z0-9]+)*$'),
    name text NOT NULL UNIQUE CHECK (name <> ''),
    config jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(config) = 'object'),
    -- Set when the plugin fails to start. While set, nothing starts it.
    broken_error text,
    broken_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE job (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind ~ '^[a-z][a-z0-9_.-]*$'),
    instance_id bigint REFERENCES plugin_instance,
    payload jsonb NOT NULL DEFAULT '{}',
    -- A bulk action's parent job is 'waiting' until its last child finishes.
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'waiting', 'succeeded', 'failed')),
    attempts int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 8 CHECK (max_attempts > 0),
    progress jsonb NOT NULL DEFAULT '{}',
    checkpoint jsonb,
    run_after timestamptz NOT NULL DEFAULT now(),
    locked_by text,
    locked_until timestamptz,
    parent_id bigint REFERENCES job,
    error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz
);
CREATE INDEX job_claim ON job (run_after, id) WHERE status = 'queued';
CREATE INDEX job_lease ON job (locked_until) WHERE status = 'running';
CREATE INDEX ON job (parent_id);
CREATE INDEX ON job (kind, finished_at);

-- Workers LISTEN on innkeeper_job. Firing from a trigger covers every way a
-- job becomes queued, including a retry and a row inserted with psql.
CREATE FUNCTION notify_job_queued() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('innkeeper_job', '');
    RETURN NULL;
END
$$;
CREATE TRIGGER job_queued AFTER INSERT OR UPDATE OF status ON job
    FOR EACH ROW WHEN (NEW.status = 'queued') EXECUTE FUNCTION notify_job_queued();

-- Imports look up earlier imports of the same item by provenance.
CREATE INDEX ON card (provenance);
CREATE INDEX ON lorebook (provenance);

-- Blob garbage collection looks for revisions that still use a blob.
CREATE INDEX ON card_revision (avatar_blob);
CREATE INDEX ON lorebook_revision (avatar_blob);
```

- [ ] **Step 4: Run the tests to make sure they pass**

Run: `go test -race ./internal/db ./internal/library`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/migrations/0002_jobs.sql internal/db/db_test.go
git commit -m "Add job and plugin instance schema"
```

---

### Task 2: Job queue

`queue.go` also holds the lease operations the worker uses (`touch`, `finish`, `completeParents`). Task 3 tests those through the worker.

**Files:**
- Create: `internal/jobs/queue.go`
- Test: `internal/jobs/queue_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/jobs/queue_test.go`:

```go
package jobs

import (
	"context"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/testdb"
)

func newQueue(t *testing.T) *Queue {
	t.Helper()
	return New(testdb.New(t))
}

func TestClaimTakesEachJobOnce(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, err := q.Enqueue(ctx, Spec{Kind: "test", Payload: map[string]int{"n": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusQueued || string(j.Payload) != `{"n": 1}` {
		t.Fatalf("enqueued %+v", j)
	}
	if c, err := q.claim(ctx, "w1", []string{"other"}, time.Minute); err != nil || c != nil {
		t.Fatalf("claim of another kind = %v, %v; want nothing", c, err)
	}
	c, err := q.claim(ctx, "w1", []string{"test"}, time.Minute)
	if err != nil || c == nil || c.ID != j.ID || c.Status != StatusRunning || c.Attempts != 1 {
		t.Fatalf("claim = %+v, %v", c, err)
	}
	if c, err := q.claim(ctx, "w2", []string{"test"}, time.Minute); err != nil || c != nil {
		t.Fatalf("second claim = %v, %v; want nothing", c, err)
	}
}

func TestReapRequeuesExpiredLease(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	if _, err := q.claim(ctx, "dead-worker", []string{"test"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := q.reap(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, j.ID); got.Status != StatusQueued || got.Attempts != 1 {
		t.Fatalf("reaped job = %+v", got)
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET max_attempts = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := q.claim(ctx, "dead-worker", []string{"test"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := q.reap(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, j.ID); got.Status != StatusFailed {
		t.Fatalf("job out of attempts = %+v, want failed", got)
	}
}

func TestEnsureScheduled(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	count := func() (n int) {
		if err := q.pool.QueryRow(ctx, `SELECT count(*) FROM job WHERE kind = 'gc'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for range 2 {
		if err := q.EnsureScheduled(ctx, "gc", time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(); n != 1 {
		t.Fatalf("%d gc jobs after two checks, want 1", n)
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET status = 'succeeded', finished_at = now() - interval '30 minutes'`); err != nil {
		t.Fatal(err)
	}
	q.EnsureScheduled(ctx, "gc", time.Hour)
	if n := count(); n != 1 {
		t.Fatalf("%d gc jobs half an hour after the last, want 1", n)
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET finished_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	q.EnsureScheduled(ctx, "gc", time.Hour)
	if n := count(); n != 2 {
		t.Fatalf("%d gc jobs two hours after the last, want 2", n)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/jobs`
Expected: FAIL with `undefined: Queue`.

- [ ] **Step 3: Write the queue**

Create `internal/jobs/queue.go`:

```go
// Package jobs is the core's job queue. Jobs are rows in the job table.
// Workers claim them with FOR UPDATE SKIP LOCKED, hold them with a lease they
// keep renewing, and wake on LISTEN/NOTIFY.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job statuses.
const (
	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusWaiting   = "waiting"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
)

var (
	ErrNotFound = errors.New("job not found")
	// ErrRetry marks a handler error as temporary. Wrap it, as in
	// fmt.Errorf("%w: %w", jobs.ErrRetry, err), to have the job retried.
	ErrRetry = errors.New("retryable")
	// ErrLeaseLost means the job is no longer this worker's: its lease
	// expired and the reaper re-queued it.
	ErrLeaseLost = errors.New("job lease lost")
)

// Job is one row of the job table.
type Job struct {
	ID          int64
	Kind        string
	InstanceID  *int64
	Payload     json.RawMessage
	Status      string
	Attempts    int
	MaxAttempts int
	Progress    json.RawMessage
	Checkpoint  json.RawMessage
	RunAfter    time.Time
	ParentID    *int64
	Error       *string
	CreatedAt   time.Time
	FinishedAt  *time.Time
}

// Spec describes a job to enqueue.
type Spec struct {
	Kind       string
	InstanceID *int64
	// Payload is marshalled to JSON. Nil means {}.
	Payload any
}

// Queue adds jobs and reads them back.
type Queue struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Queue { return &Queue{pool: pool} }

const jobCols = `id, kind, instance_id, payload, status, attempts, max_attempts, progress, checkpoint,
	run_after, parent_id, error, created_at, finished_at`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.InstanceID, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
		&j.Progress, &j.Checkpoint, &j.RunAfter, &j.ParentID, &j.Error, &j.CreatedAt, &j.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func insert(ctx context.Context, db querier, s Spec, parent *int64, status string) (Job, error) {
	payload := []byte(`{}`)
	if s.Payload != nil {
		var err error
		if payload, err = json.Marshal(s.Payload); err != nil {
			return Job{}, err
		}
	}
	return scanJob(db.QueryRow(ctx, `INSERT INTO job (kind, instance_id, payload, parent_id, status, finished_at)
		VALUES ($1, $2, $3, $4, $5, CASE WHEN $5 = 'succeeded' THEN now() END) RETURNING `+jobCols,
		s.Kind, s.InstanceID, payload, parent, status))
}

// Enqueue adds a job to run as soon as a worker is free.
func (q *Queue) Enqueue(ctx context.Context, s Spec) (Job, error) {
	return insert(ctx, q.pool, s, nil, StatusQueued)
}

// EnqueueBulk adds a parent job with one child job per spec, in one
// transaction. The parent never runs: it finishes when its last child does,
// and fails if any child failed.
func (q *Queue) EnqueueBulk(ctx context.Context, parent Spec, children []Spec) (Job, error) {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback(ctx)
	status := StatusWaiting
	if len(children) == 0 {
		status = StatusSucceeded
	}
	p, err := insert(ctx, tx, parent, nil, status)
	if err != nil {
		return Job{}, err
	}
	// ponytail: one INSERT per child; switch to CopyFrom if bulk actions
	// over tens of thousands of cards get slow.
	for _, c := range children {
		if _, err := insert(ctx, tx, c, &p.ID, StatusQueued); err != nil {
			return Job{}, err
		}
	}
	return p, tx.Commit(ctx)
}

// Get returns one job.
func (q *Queue) Get(ctx context.Context, id int64) (Job, error) {
	return scanJob(q.pool.QueryRow(ctx, `SELECT `+jobCols+` FROM job WHERE id = $1`, id))
}

// EnsureScheduled enqueues kind unless a job of that kind is queued or
// running, or finished less than every ago.
func (q *Queue) EnsureScheduled(ctx context.Context, kind string, every time.Duration) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Every worker checks the schedule; the lock stops two from both enqueueing.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('innkeeper-schedule'))`); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO job (kind) SELECT $1::text WHERE NOT EXISTS (
		SELECT 1 FROM job WHERE kind = $1 AND parent_id IS NULL
			AND (status IN ('queued', 'running') OR finished_at > now() - make_interval(secs => $2)))`,
		kind, every.Seconds()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// claim marks the next due job of one of kinds as running and leases it to
// worker. It returns nil when no job is due.
func (q *Queue) claim(ctx context.Context, worker string, kinds []string, lease time.Duration) (*Job, error) {
	j, err := scanJob(q.pool.QueryRow(ctx, `UPDATE job SET status = 'running', attempts = attempts + 1,
			locked_by = $1, locked_until = now() + make_interval(secs => $2)
		WHERE id = (
			SELECT id FROM job WHERE status = 'queued' AND run_after <= now() AND kind = ANY($3)
			ORDER BY run_after, id LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+jobCols, worker, lease.Seconds(), kinds))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// touch renews the lease and saves progress, and the checkpoint unless it is
// nil.
func (q *Queue) touch(ctx context.Context, id int64, worker string, lease time.Duration, progress, checkpoint json.RawMessage) error {
	tag, err := q.pool.Exec(ctx, `UPDATE job SET locked_until = now() + make_interval(secs => $3),
			progress = coalesce($4::jsonb, progress), checkpoint = coalesce($5::jsonb, checkpoint)
		WHERE id = $1 AND locked_by = $2 AND status = 'running'`,
		id, worker, lease.Seconds(), progress, checkpoint)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// outcome is how a run ended.
type outcome struct {
	status   string // succeeded, failed, or queued to retry
	err      string
	runAfter time.Duration
	// refund gives the attempt back: the worker stopped, the job didn't fail.
	refund bool
}

func (q *Queue) finish(ctx context.Context, j *Job, worker string, progress json.RawMessage, o outcome) error {
	refund := 0
	if o.refund {
		refund = 1
	}
	tag, err := q.pool.Exec(ctx, `UPDATE job SET status = $3, error = nullif($4, ''),
			run_after = now() + make_interval(secs => $5), attempts = attempts - $6,
			progress = coalesce($7::jsonb, progress), locked_by = NULL, locked_until = NULL,
			finished_at = CASE WHEN $3 IN ('succeeded', 'failed') THEN now() END
		WHERE id = $1 AND locked_by = $2 AND status = 'running'`,
		j.ID, worker, o.status, o.err, o.runAfter.Seconds(), refund, progress)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	if j.ParentID != nil && o.status != StatusQueued {
		return q.completeParents(ctx, j.ParentID)
	}
	return nil
}

// completeParents finishes waiting parents whose children have all finished.
// It runs after the child's update commits, so when two children finish at
// once, whichever checks second sees both. A nil id checks every parent,
// which catches a worker that died between the two steps.
func (q *Queue) completeParents(ctx context.Context, id *int64) error {
	_, err := q.pool.Exec(ctx, `UPDATE job p SET
			status = CASE WHEN s.failed > 0 THEN 'failed' ELSE 'succeeded' END,
			error = CASE WHEN s.failed > 0 THEN s.failed || ' of ' || s.total || ' jobs failed' END,
			finished_at = now()
		FROM (SELECT parent_id, count(*) AS total,
				count(*) FILTER (WHERE status = 'failed') AS failed,
				count(*) FILTER (WHERE status NOT IN ('succeeded', 'failed')) AS open
			FROM job WHERE parent_id = coalesce($1, parent_id) GROUP BY parent_id) s
		WHERE p.id = s.parent_id AND p.status = 'waiting' AND s.open = 0`, id)
	return err
}

// reap re-queues jobs whose lease expired because their worker died, or
// fails them once they are out of attempts.
func (q *Queue) reap(ctx context.Context) error {
	if _, err := q.pool.Exec(ctx, `UPDATE job SET
			status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
			error = 'worker stopped renewing its lease',
			finished_at = CASE WHEN attempts >= max_attempts THEN now() END,
			locked_by = NULL, locked_until = NULL, run_after = now()
		WHERE status = 'running' AND locked_until < now()`); err != nil {
		return err
	}
	return q.completeParents(ctx, nil)
}
```

- [ ] **Step 4: Run the tests to make sure they pass**

Run: `go test -race ./internal/jobs`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs
git commit -m "Add job queue"
```

---

### Task 3: Worker

**Files:**
- Create: `internal/jobs/worker.go`
- Test: `internal/jobs/worker_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/jobs/worker_test.go`. `TestWorkerRunsQueuedJob` sleeps before enqueueing so every loop has gone idle. Without that, a loop's first claim picks up the job, and the test passes even when LISTEN is broken.

```go
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// startWorker runs w until the test ends. Its Poll is an hour, so only
// LISTEN/NOTIFY can wake it, unless the test sets Poll itself.
func startWorker(t *testing.T, w *Worker) {
	t.Helper()
	if w.Log == nil {
		w.Log = quiet
	}
	if w.Poll == 0 {
		w.Poll = time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})
}

// waitStatus polls until the job has the status and returns it.
func waitStatus(t *testing.T, q *Queue, id int64, status string) Job {
	t.Helper()
	var j Job
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		var err error
		if j, err = q.Get(context.Background(), id); err != nil {
			t.Fatal(err)
		}
		if j.Status == status {
			return j
		}
	}
	t.Fatalf("job %d is %s (error %q), want %s", id, j.Status, errText(j), status)
	return j
}

func errText(j Job) string {
	if j.Error == nil {
		return ""
	}
	return *j.Error
}

func TestWorkerRunsQueuedJob(t *testing.T) {
	q := newQueue(t)
	ran := make(chan json.RawMessage, 1)
	startWorker(t, &Worker{Queue: q, Handlers: map[string]Handler{
		"test": func(_ context.Context, r *Run) error {
			ran <- r.Payload
			return nil
		},
	}})
	// Let every loop find the queue empty and go idle, so only NOTIFY can wake one.
	time.Sleep(200 * time.Millisecond)
	j, err := q.Enqueue(context.Background(), Spec{Kind: "test", Payload: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-ran:
		if string(p) != `"hi"` {
			t.Fatalf("payload = %s", p)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job never ran; LISTEN/NOTIFY didn't wake the worker")
	}
	if got := waitStatus(t, q, j.ID, StatusSucceeded); got.FinishedAt == nil || got.Error != nil {
		t.Fatalf("finished job = %+v", got)
	}
}

func TestRetryableErrorRequeuesWithBackoff(t *testing.T) {
	q := newQueue(t)
	startWorker(t, &Worker{Queue: q, Handlers: map[string]Handler{
		"test": func(context.Context, *Run) error { return fmt.Errorf("%w: rate limited", ErrRetry) },
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		got, _ := q.Get(context.Background(), j.ID)
		if got.Attempts == 1 && got.Status == StatusQueued {
			if errText(got) != "retryable: rate limited" {
				t.Fatalf("error = %q", errText(got))
			}
			if wait := time.Until(got.RunAfter); wait < 5*time.Second {
				t.Fatalf("retry due in %s, want about 10s", wait)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job = %+v, want queued for retry", got)
		}
	}
}

func TestJobFailsOnPermanentErrorOrLastAttempt(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	startWorker(t, &Worker{Queue: q, Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]Handler{
		"fatal": func(context.Context, *Run) error { return errors.New("bad input") },
		"flaky": func(context.Context, *Run) error { return fmt.Errorf("%w: timeout", ErrRetry) },
		"panic": func(context.Context, *Run) error { panic("boom") },
	}})
	fatal, _ := q.Enqueue(ctx, Spec{Kind: "fatal"})
	if got := waitStatus(t, q, fatal.ID, StatusFailed); got.Attempts != 1 || errText(got) != "bad input" {
		t.Fatalf("fatal job = %+v", got)
	}
	flaky, _ := q.Enqueue(ctx, Spec{Kind: "flaky"})
	if got := waitStatus(t, q, flaky.ID, StatusFailed); got.Attempts != got.MaxAttempts {
		t.Fatalf("flaky job failed after %d of %d attempts", got.Attempts, got.MaxAttempts)
	}
	p, _ := q.Enqueue(ctx, Spec{Kind: "panic"})
	if got := waitStatus(t, q, p.ID, StatusFailed); errText(got) != "handler panicked: boom" {
		t.Fatalf("panic job error = %q", errText(got))
	}
}

func TestRetryResumesFromCheckpoint(t *testing.T) {
	q := newQueue(t)
	seen := make(chan string, 2)
	startWorker(t, &Worker{Queue: q, Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]Handler{
		"test": func(ctx context.Context, r *Run) error {
			seen <- string(r.Checkpoint)
			if r.Checkpoint != nil {
				return nil
			}
			r.SetProgress(json.RawMessage(`{"done": 5}`))
			if err := r.SaveCheckpoint(ctx, json.RawMessage(`{"next": 5}`)); err != nil {
				return err
			}
			return fmt.Errorf("%w: crashed", ErrRetry)
		},
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	got := waitStatus(t, q, j.ID, StatusSucceeded)
	if first, second := <-seen, <-seen; first != "" || second != `{"next": 5}` {
		t.Fatalf("checkpoints seen = %q, %q", first, second)
	}
	if string(got.Progress) != `{"done": 5}` || got.Attempts != 2 {
		t.Fatalf("job = %+v", got)
	}
}

func TestLostLeaseCancelsHandler(t *testing.T) {
	q := newQueue(t)
	started, cancelled := make(chan struct{}), make(chan struct{})
	startWorker(t, &Worker{Queue: q, Lease: 300 * time.Millisecond, Handlers: map[string]Handler{
		"test": func(ctx context.Context, _ *Run) error {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return ctx.Err()
		},
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	<-started
	// As if the reaper had given the job to another worker.
	if _, err := q.pool.Exec(context.Background(), `UPDATE job SET locked_by = 'other' WHERE id = $1`, j.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("handler kept running after its lease was lost")
	}
	time.Sleep(100 * time.Millisecond)
	if got, _ := q.Get(context.Background(), j.ID); got.Status != StatusRunning {
		t.Fatalf("worker changed a job it no longer holds: %+v", got)
	}
}

func TestShutdownRequeuesWithoutUsingAttempt(t *testing.T) {
	q := newQueue(t)
	started := make(chan struct{})
	w := &Worker{Queue: q, Log: quiet, Poll: time.Hour, Handlers: map[string]Handler{
		"test": func(ctx context.Context, _ *Run) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- w.Run(ctx) }()
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(context.Background(), j.ID); got.Status != StatusQueued || got.Attempts != 0 {
		t.Fatalf("job after shutdown = %+v", got)
	}
}

func TestBulkParentFinishesWithChildren(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	startWorker(t, &Worker{Queue: q, Handlers: map[string]Handler{
		"item": func(_ context.Context, r *Run) error {
			if string(r.Payload) == `"bad"` {
				return errors.New("bad item")
			}
			return nil
		},
	}})

	ok, err := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item", Payload: "a"}, {Kind: "item", Payload: "b"}})
	if err != nil || ok.Status != StatusWaiting {
		t.Fatalf("EnqueueBulk = %+v, %v", ok, err)
	}
	waitStatus(t, q, ok.ID, StatusSucceeded)

	mixed, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item", Payload: "a"}, {Kind: "item", Payload: "bad"}})
	if got := waitStatus(t, q, mixed.ID, StatusFailed); errText(got) != "1 of 2 jobs failed" {
		t.Fatalf("parent error = %q", errText(got))
	}

	empty, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, nil)
	if empty.Status != StatusSucceeded || empty.FinishedAt == nil {
		t.Fatalf("empty bulk = %+v", empty)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/jobs`
Expected: FAIL with `undefined: Worker`.

- [ ] **Step 3: Write the worker**

Create `internal/jobs/worker.go`:

```go
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Handler runs one job. Return nil when it is done, an error wrapping
// ErrRetry to have it retried later, or any other error to fail it.
type Handler func(ctx context.Context, r *Run) error

// Schedule enqueues Kind every Every; see Queue.EnsureScheduled.
type Schedule struct {
	Kind  string
	Every time.Duration
}

// Worker claims and runs jobs of the kinds it has handlers for. Zero
// durations and counts get the defaults noted on each field.
type Worker struct {
	Queue     *Queue
	Handlers  map[string]Handler
	Schedules []Schedule
	Log       *slog.Logger
	// Concurrency is how many jobs run at once. Default 4.
	Concurrency int
	// Lease is how long a claimed job stays this worker's without renewal.
	// It is renewed every Lease/3. Default 30s.
	Lease time.Duration
	// Poll is the longest an idle worker waits before looking for due jobs,
	// and how often it reaps expired leases and checks schedules. Default 5s.
	Poll time.Duration
	// Backoff is the delay before retry number attempt. Default 10s,
	// doubling each attempt, at most 30m.
	Backoff func(attempt int) time.Duration

	id   string
	wake chan struct{}
}

// Run is a claimed job as its handler sees it.
type Run struct {
	Job
	q      *Queue
	worker string
	lease  time.Duration

	mu       sync.Mutex
	progress json.RawMessage
}

// SetProgress records the job's progress. It is saved with the next lease
// renewal, checkpoint, or finish.
func (r *Run) SetProgress(p json.RawMessage) {
	r.mu.Lock()
	r.progress = p
	r.mu.Unlock()
}

func (r *Run) currentProgress() json.RawMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.progress
}

// SaveCheckpoint saves where a retry should resume, along with the current
// progress. The retry finds it in Run.Checkpoint.
func (r *Run) SaveCheckpoint(ctx context.Context, cp json.RawMessage) error {
	return r.q.touch(ctx, r.ID, r.worker, r.lease, r.currentProgress(), cp)
}

func backoff(attempt int) time.Duration {
	return min(10*time.Second<<min(max(attempt-1, 0), 20), 30*time.Minute)
}

// Run claims and runs jobs until ctx is cancelled, then waits for running
// jobs to stop. A job stopped that way goes back to the queue without using
// up an attempt.
func (w *Worker) Run(ctx context.Context) error {
	if w.Concurrency <= 0 {
		w.Concurrency = 4
	}
	if w.Lease <= 0 {
		w.Lease = 30 * time.Second
	}
	if w.Poll <= 0 {
		w.Poll = 5 * time.Second
	}
	if w.Backoff == nil {
		w.Backoff = backoff
	}
	if w.Log == nil {
		w.Log = slog.Default()
	}
	host, _ := os.Hostname()
	w.id = fmt.Sprintf("%s:%d:%08x", host, os.Getpid(), rand.Uint32())
	w.wake = make(chan struct{}, w.Concurrency)
	kinds := slices.Sorted(maps.Keys(w.Handlers))

	// Listen before the first claim, so no job queued after it is missed.
	conn, err := w.subscribe(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	wg.Go(func() { w.listen(ctx, conn) })
	wg.Go(func() { w.maintain(ctx) })
	for range w.Concurrency {
		wg.Go(func() { w.loop(ctx, kinds) })
	}
	wg.Wait()
	return nil
}

func (w *Worker) loop(ctx context.Context, kinds []string) {
	for ctx.Err() == nil {
		j, err := w.Queue.claim(ctx, w.id, kinds, w.Lease)
		if err != nil && ctx.Err() == nil {
			w.Log.Error("claim job", "err", err)
		}
		if j == nil {
			select {
			case <-ctx.Done():
			case <-w.wake:
			case <-time.After(w.Poll):
			}
			continue
		}
		w.execute(ctx, j)
	}
}

func (w *Worker) subscribe(ctx context.Context) (*pgx.Conn, error) {
	c, err := w.Queue.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	// Taken out of the pool so the LISTEN never ends up on another query's connection.
	conn := c.Hijack()
	if _, err := conn.Exec(ctx, "LISTEN innkeeper_job"); err != nil {
		conn.Close(context.Background())
		return nil, err
	}
	return conn, nil
}

// listen wakes idle loops when a job is queued. Polling covers jobs whose
// run_after is in the future and anything queued while reconnecting.
func (w *Worker) listen(ctx context.Context, conn *pgx.Conn) {
	for {
		for conn == nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(w.Poll):
			}
			var err error
			if conn, err = w.subscribe(ctx); err != nil && ctx.Err() == nil {
				w.Log.Warn("listen for jobs", "err", err)
			}
		}
		if _, err := conn.WaitForNotification(ctx); err != nil {
			conn.Close(context.Background())
			conn = nil
			if ctx.Err() == nil {
				w.Log.Warn("listen for jobs; reconnecting", "err", err)
			}
			continue
		}
		for range w.Concurrency {
			select {
			case w.wake <- struct{}{}:
			default:
			}
		}
	}
}

// maintain reaps expired leases and enqueues scheduled jobs.
func (w *Worker) maintain(ctx context.Context) {
	t := time.NewTicker(w.Poll)
	defer t.Stop()
	for {
		if err := w.Queue.reap(ctx); err != nil && ctx.Err() == nil {
			w.Log.Error("reap expired job leases", "err", err)
		}
		for _, s := range w.Schedules {
			if err := w.Queue.EnsureScheduled(ctx, s.Kind, s.Every); err != nil && ctx.Err() == nil {
				w.Log.Error("schedule job", "kind", s.Kind, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (w *Worker) execute(ctx context.Context, j *Job) {
	log := w.Log.With("job", j.ID, "kind", j.Kind, "attempt", j.Attempts)
	run := &Run{Job: *j, q: w.Queue, worker: w.id, lease: w.Lease, progress: j.Progress}
	hctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stop := make(chan struct{})
	var beat sync.WaitGroup
	beat.Go(func() { w.heartbeat(hctx, cancel, run, stop) })
	err := safeCall(hctx, w.Handlers[j.Kind], run)
	close(stop)
	beat.Wait()

	if errors.Is(err, ErrLeaseLost) || errors.Is(context.Cause(hctx), ErrLeaseLost) {
		log.Warn("lost job lease; another worker will run it", "err", err)
		return
	}
	o := outcome{status: StatusSucceeded}
	switch {
	case err == nil:
	case ctx.Err() != nil:
		o = outcome{status: StatusQueued, err: "worker stopped", refund: true}
	case errors.Is(err, ErrRetry) && j.Attempts < j.MaxAttempts:
		o = outcome{status: StatusQueued, err: err.Error(), runAfter: w.Backoff(j.Attempts)}
	default:
		o = outcome{status: StatusFailed, err: err.Error()}
	}
	// ctx may already be cancelled, and the job must still be released.
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer fcancel()
	if err := w.Queue.finish(fctx, j, w.id, run.currentProgress(), o); err != nil {
		log.Error("finish job", "err", err)
		return
	}
	log.Info("job ended", "status", o.status, "err", o.err)
}

func safeCall(ctx context.Context, h Handler, r *Run) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("handler panicked: %v", p)
		}
	}()
	return h(ctx, r)
}

// heartbeat renews the lease until stop closes. If the lease is lost it
// cancels the handler's context.
func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, r *Run, stop <-chan struct{}) {
	t := time.NewTicker(w.Lease / 3)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
		}
		err := r.q.touch(ctx, r.ID, r.worker, r.lease, r.currentProgress(), nil)
		if errors.Is(err, ErrLeaseLost) {
			cancel(ErrLeaseLost)
			return
		}
		if err != nil && ctx.Err() == nil {
			w.Log.Warn("renew job lease", "job", r.ID, "err", err)
		}
	}
}
```

- [ ] **Step 4: Run the tests to make sure they pass**

Run: `go test -race -count=3 ./internal/jobs`
Expected: PASS on all three runs.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs
git commit -m "Add job worker with leases, retries, and bulk parents"
```

---

### Task 4: Import protocol and fake importer

**Files:**
- Modify: `protocol/messages.go`
- Modify: `internal/fakeplugin/main.go`
- Modify: `internal/testutil/fakeplugin.go`
- Modify: `docs/protocol.md`

The importer tests in Task 8 exercise all of this. This task only has to build.

- [ ] **Step 1: Add the import messages**

In `protocol/messages.go`, add `import "encoding/json"` under the package clause, then append:

```go
// The import capability. The core calls import.run; until it replies, the
// plugin sends the notifications below, each carrying the call's Run token.
const (
	MethodImportRun    = "import.run"
	NotifyEmitCard     = "emit.card"
	NotifyEmitLorebook = "emit.lorebook"
	NotifyProgress     = "progress"
	NotifyCheckpoint   = "checkpoint"
)

// ImportRunParams starts an import. Checkpoint is null on the first run, or
// the last checkpoint the plugin sent before an interrupted run.
type ImportRunParams struct {
	Run        string          `json:"run"`
	Params     json.RawMessage `json:"params"`
	Checkpoint json.RawMessage `json:"checkpoint"`
}

// EmitCard sends one imported card. Item is the plugin's stable ID for the
// source, Card is a CCv3 card, and Avatar is a file in blob_tmp, or empty.
type EmitCard struct {
	Run    string          `json:"run"`
	Item   string          `json:"item"`
	Card   json.RawMessage `json:"card"`
	Avatar string          `json:"avatar,omitempty"`
}

// EmitLorebook sends one standalone lorebook, a CCv3 character_book.
type EmitLorebook struct {
	Run      string          `json:"run"`
	Item     string          `json:"item"`
	Lorebook json.RawMessage `json:"lorebook"`
}

// Progress reports how far the import has got. Total is 0 when unknown.
type Progress struct {
	Run     string `json:"run"`
	Done    int64  `json:"done"`
	Total   int64  `json:"total,omitempty"`
	Message string `json:"message,omitempty"`
}

// Checkpoint is where to resume if the run is interrupted. The core saves it
// after handling every notification sent before it.
type Checkpoint struct {
	Run        string          `json:"run"`
	Checkpoint json.RawMessage `json:"checkpoint"`
}
```

- [ ] **Step 2: Let the fake plugin report other capabilities**

In `internal/fakeplugin/main.go`, add `"path/filepath"` to the imports. After the `mode` flag, add:

```go
	caps := flag.String("caps", "push,pull", "comma-separated capabilities to report")
```

After `var conn *protocol.Conn`, add `var blobTmp string`. Replace the start of the `initialize` case with:

```go
		case protocol.MethodInitialize:
			var init protocol.InitializeParams
			_ = json.Unmarshal(params, &init)
			blobTmp = init.BlobTmp
			res := protocol.InitializeResult{
				Protocol:     protocol.Version,
				Name:         "fake",
				Version:      "0.0.1",
				Capabilities: strings.Split(*caps, ","),
			}
```

After the `crash` case, add:

```go
		case protocol.MethodImportRun:
			return importRun(conn, blobTmp, params)
```

- [ ] **Step 3: Add the fake importer**

Append to `internal/fakeplugin/main.go`:

```go
// importOptions are the fake importer's params.
type importOptions struct {
	Cards int `json:"cards"`
	// CrashAt exits after emitting this card, before its checkpoint, unless
	// the run resumed from a checkpoint.
	CrashAt *int `json:"crash_at"`
	// Bad emits this card with a spec the core rejects.
	Bad *int `json:"bad"`
	// Lorebooks is how many standalone lorebooks to emit before the cards.
	Lorebooks int    `json:"lorebooks"`
	Book      bool   `json:"book"`
	Avatar    bool   `json:"avatar"`
	Fail      string `json:"fail"` // retryable or fatal
}

func importRun(conn *protocol.Conn, blobTmp string, params json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	var opts importOptions
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	if err := json.Unmarshal(p.Params, &opts); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	switch opts.Fail {
	case "retryable":
		return nil, protocol.NewError(-32000, "source unavailable", true)
	case "fatal":
		return nil, protocol.NewError(-32000, "no such folder", false)
	}
	var cp struct {
		Next int `json:"next"`
	}
	resumed := len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null"
	if resumed {
		_ = json.Unmarshal(p.Checkpoint, &cp)
	}
	for i := range opts.Lorebooks {
		if resumed {
			break
		}
		book, _ := json.Marshal(map[string]any{"name": fmt.Sprintf("World %d", i), "entries": []any{}})
		_ = conn.Notify(protocol.NotifyEmitLorebook, protocol.EmitLorebook{Run: p.Run, Item: fmt.Sprintf("world-%d", i), Lorebook: book})
	}
	for i := cp.Next; i < opts.Cards; i++ {
		data := map[string]any{"name": fmt.Sprintf("Fake %d", i), "description": fmt.Sprintf("Card number %d.", i)}
		if opts.Book {
			data["character_book"] = map[string]any{"name": fmt.Sprintf("Lore %d", i), "entries": []any{}}
		}
		card := map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data}
		if opts.Bad != nil && *opts.Bad == i {
			card["spec"] = "chara_card_v2"
		}
		raw, _ := json.Marshal(card)
		e := protocol.EmitCard{Run: p.Run, Item: fmt.Sprintf("card-%d", i), Card: raw}
		if opts.Avatar {
			e.Avatar = filepath.Join(blobTmp, fmt.Sprintf("avatar-%d", i))
			if err := os.WriteFile(e.Avatar, fmt.Appendf(nil, "avatar %d", i), 0o644); err != nil {
				return nil, err
			}
		}
		_ = conn.Notify(protocol.NotifyEmitCard, e)
		if opts.CrashAt != nil && *opts.CrashAt == i && !resumed {
			os.Exit(5)
		}
		_ = conn.Notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: int64(i + 1), Total: int64(opts.Cards)})
		next, _ := json.Marshal(map[string]int{"next": i + 1})
		_ = conn.Notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: next})
	}
	return struct{}{}, nil
}
```

- [ ] **Step 4: Add `FakePluginWith`**

In `internal/testutil/fakeplugin.go`, replace the `FakePlugin` function with:

```go
// FakePlugin builds internal/fakeplugin into a temp plugin folder and writes
// a manifest that runs it with args. It returns the folder.
func FakePlugin(t *testing.T, args ...string) string {
	t.Helper()
	return FakePluginWith(t, []string{"push", "pull"}, args...)
}

// FakePluginWith is FakePlugin with the given capabilities.
func FakePluginWith(t *testing.T, caps []string, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeplugin")
	build := exec.Command("go", "build", "-o", bin, "github.com/inkandale/innkeeper/internal/fakeplugin")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeplugin: %v\n%s", err, out)
	}
	cmd := append([]string{"./fakeplugin", "-caps=" + strings.Join(caps, ",")}, args...)
	for i, c := range cmd {
		cmd[i] = strconv.Quote(c)
	}
	quoted := make([]string, len(caps))
	for i, c := range caps {
		quoted[i] = strconv.Quote(c)
	}
	manifest := fmt.Sprintf(`name = "fake"
version = "0.0.1"
protocol = 1
command = [%s]
capabilities = [%s]
`, strings.Join(cmd, ", "), strings.Join(quoted, ", "))
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
```

- [ ] **Step 5: Document the import capability**

In `docs/protocol.md`, replace the body of "Capability methods" with:

````markdown
Each capability's methods are added here as the core implements them. The
planned set is in the design spec's "Plugin protocol" section.

### `import`

The core sends `import.run` and waits for the reply. Until it replies, the
plugin streams what it finds as notifications.

| Param | Meaning |
|---|---|
| `run` | Token for this call. Put it in every notification you send for it |
| `params` | The import's settings, defined by the plugin, such as a folder path |
| `checkpoint` | `null` on the first run. After an interruption, the last checkpoint you sent |

Reply with `{}` when the import is done, or with an error. A plugin crash or
a retryable error re-runs the import later from the last checkpoint.

Notifications, each with `run`:

| Method | Params | Meaning |
|---|---|---|
| `emit.card` | `item`, `card`, `avatar` | One card. `item` is your stable ID for the source, such as a file path. `card` is a CCv3 card (`spec` must be `chara_card_v3`). `avatar` is optional: a file you wrote to `blob_tmp` |
| `emit.lorebook` | `item`, `lorebook` | One standalone lorebook, a CCv3 `character_book` |
| `progress` | `done`, `total`, `message` | How far you've got. `total` and `message` are optional |
| `checkpoint` | `checkpoint` | Any JSON value that tells you where to resume |

```json
{"jsonrpc": "2.0", "method": "emit.card", "params": {"run": "12.1", "item": "brakka.png", "card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}, "avatar": "/blobs/tmp/brakka.png"}}
{"jsonrpc": "2.0", "method": "checkpoint", "params": {"run": "12.1", "checkpoint": {"next": "carrow.png"}}}
```

- Send a checkpoint after each item, or every few items. The core saves it
  only after storing everything you sent before it.
- After a resume you may send items again. The core skips an item whose
  `item` and content it has imported before, so importing the same source
  twice adds nothing.
- An embedded `character_book` becomes a separate lorebook linked to the card.
- A card the core can't read counts as failed. The import goes on.
- The core moves each `avatar` file into its blob store, so don't reuse the
  path.
````

- [ ] **Step 6: Check it builds**

Run: `go vet ./... && go test -race ./internal/pluginhost ./internal/contract`
Expected: PASS. The existing plugin host tests still use `push` and `pull`.

- [ ] **Step 7: Commit**

```bash
git add protocol/messages.go internal/fakeplugin/main.go internal/testutil/fakeplugin.go docs/protocol.md
git commit -m "Define the import capability"
```

---

### Task 5: Start errors

**Files:**
- Modify: `internal/pluginhost/manager.go`
- Test: `internal/pluginhost/manager_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/pluginhost/manager_test.go`:

```go
func TestCallReportsStartError(t *testing.T) {
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, "-mode=wrong-version"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(core, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	err = mgr.Call(context.Background(), Instance{ID: "fake-1", Manifest: m}, "echo", 1, nil)
	var se *StartError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a StartError", err)
	}
	// An error from a running plugin is not a StartError.
	good, inst := newManager(t, time.Minute)
	err = good.Call(context.Background(), inst, "fail", nil, nil)
	if errors.As(err, &se) {
		t.Fatalf("call error %v reported as a StartError", err)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/pluginhost -run TestCallReportsStartError`
Expected: FAIL with `undefined: StartError`.

- [ ] **Step 3: Add `StartError`**

In `Manager.acquire`, wrap the error from `Start`:

```go
		p, err := Start(ctx, inst.Manifest, params, m.core, m.log)
		if err != nil {
			return nil, &StartError{Err: err}
		}
```

Append to `internal/pluginhost/manager.go`:

```go
// StartError means the plugin failed to start or failed its handshake.
type StartError struct{ Err error }

func (e *StartError) Error() string { return e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }
```

- [ ] **Step 4: Run the tests to make sure they pass**

Run: `go test -race ./internal/pluginhost`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pluginhost
git commit -m "Report plugin start failures as StartError"
```

---

### Task 6: Plugin instances

**Files:**
- Create: `internal/plugins/plugins.go`
- Test: `internal/plugins/plugins_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/plugins/plugins_test.go`:

```go
package plugins

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDirSkipsBadPlugins(t *testing.T) {
	dir := t.TempDir()
	writeManifest(t, filepath.Join(dir, "good"), `name = "good"
version = "1.0.0"
protocol = 1
command = ["./good"]
capabilities = ["import"]
`)
	writeManifest(t, filepath.Join(dir, "bad"), `name = "bad"
protocol = 99
`)
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDir(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got["good"] == nil {
		t.Fatalf("loaded %v, want only good", got)
	}
}

func TestInstance(t *testing.T) {
	ctx := context.Background()
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	m := &protocol.Manifest{Name: "fake", Version: "0.0.1"}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m}, store)

	if _, err := reg.CreateInstance(ctx, "missing", "x", nil); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("create for missing plugin: %v", err)
	}
	id, err := reg.CreateInstance(ctx, "fake", "Fake", map[string]any{"path": "/cards"})
	if err != nil {
		t.Fatal(err)
	}
	inst, err := reg.Instance(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Manifest != m || inst.Params.Config["path"] != "/cards" || inst.Params.BlobTmp != store.TmpDir() {
		t.Fatalf("instance = %+v", inst)
	}
	if _, err := os.Stat(store.TmpDir()); err != nil {
		t.Fatalf("blob tmp folder: %v", err)
	}

	if err := reg.MarkBroken(ctx, id, errors.New("wrong protocol")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Instance(ctx, id); !errors.Is(err, ErrBroken) {
		t.Fatalf("broken instance: %v", err)
	}
	if _, err := reg.Instance(ctx, id+1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing instance: %v", err)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/plugins`
Expected: FAIL with `undefined: LoadDir`.

- [ ] **Step 3: Write the registry**

Create `internal/plugins/plugins.go`:

```go
// Package plugins loads installed plugins' manifests and the configured
// instances that run them.
package plugins

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

var (
	ErrNotFound      = errors.New("plugin instance not found")
	ErrUnknownPlugin = errors.New("plugin is not installed")
	// ErrBroken means the instance's plugin failed to start. It stays broken
	// until the owner changes the instance.
	ErrBroken = errors.New("plugin instance is broken")
)

// LoadDir loads the manifest in each folder of dir. A folder with an invalid
// manifest is logged and left out, so one bad plugin doesn't stop the rest.
func LoadDir(dir string, log *slog.Logger) (map[string]*protocol.Manifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]*protocol.Manifest{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := protocol.LoadManifest(filepath.Join(dir, e.Name()))
		if err != nil {
			log.Error("skip plugin", "dir", e.Name(), "err", err)
			continue
		}
		if _, dup := out[m.Name]; dup {
			log.Error("skip plugin: name already loaded", "dir", e.Name(), "plugin", m.Name)
			continue
		}
		out[m.Name] = m
	}
	return out, nil
}

// Registry resolves plugin instances to what the plugin host needs to run
// them.
type Registry struct {
	pool      *pgxpool.Pool
	manifests map[string]*protocol.Manifest
	blobs     *blobstore.Store
}

// New returns a Registry for the installed plugins in manifests, whose
// plugins write new blobs to blobs.
func New(pool *pgxpool.Pool, manifests map[string]*protocol.Manifest, blobs *blobstore.Store) *Registry {
	return &Registry{pool: pool, manifests: manifests, blobs: blobs}
}

// CreateInstance configures a new instance of an installed plugin.
func (r *Registry) CreateInstance(ctx context.Context, plugin, name string, config map[string]any) (int64, error) {
	if _, ok := r.manifests[plugin]; !ok {
		return 0, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	if config == nil {
		config = map[string]any{}
	}
	var id int64
	err := r.pool.QueryRow(ctx, `INSERT INTO plugin_instance (plugin, name, config) VALUES ($1, $2, $3) RETURNING id`,
		plugin, name, config).Scan(&id)
	return id, err
}

// Instance returns what the plugin host needs to run an instance.
func (r *Registry) Instance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	var plugin string
	var config map[string]any
	var broken *string
	err := r.pool.QueryRow(ctx, `SELECT plugin, config, broken_error FROM plugin_instance WHERE id = $1`, id).
		Scan(&plugin, &config, &broken)
	if errors.Is(err, pgx.ErrNoRows) {
		return pluginhost.Instance{}, ErrNotFound
	}
	if err != nil {
		return pluginhost.Instance{}, err
	}
	if broken != nil {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrBroken, *broken)
	}
	m, ok := r.manifests[plugin]
	if !ok {
		return pluginhost.Instance{}, fmt.Errorf("%w: %s", ErrUnknownPlugin, plugin)
	}
	if err := os.MkdirAll(r.blobs.TmpDir(), 0o755); err != nil {
		return pluginhost.Instance{}, err
	}
	// ponytail: no secrets or plugin database role yet; plan 4 adds
	// encrypted secrets, and the archive catalog needs the role.
	return pluginhost.Instance{
		ID:       strconv.FormatInt(id, 10),
		Manifest: m,
		Params: protocol.InitializeParams{
			Config:     config,
			BlobStores: map[string]string{r.blobs.Name: r.blobs.Root},
			BlobTmp:    r.blobs.TmpDir(),
		},
	}, nil
}

// MarkBroken records that an instance's plugin failed to start, so nothing
// starts it again until the owner changes the instance.
func (r *Registry) MarkBroken(ctx context.Context, id int64, cause error) error {
	_, err := r.pool.Exec(ctx, `UPDATE plugin_instance SET broken_error = $2, broken_at = now() WHERE id = $1`,
		id, cause.Error())
	return err
}
```

- [ ] **Step 4: Run the tests to make sure they pass**

Run: `go test -race ./internal/plugins`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins
git commit -m "Add plugin instances and manifest loading"
```

---

### Task 7: Library imports

**Files:**
- Modify: `internal/library/library.go`
- Modify: `internal/library/entities.go`
- Create: `internal/library/import.go`
- Test: `internal/library/import_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/library/import_test.go`:

```go
package library

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestSlugify(t *testing.T) {
	for name, want := range map[string]string{
		"Brakka":                    "brakka",
		"  Sir Reginald, the 3rd! ": "sir-reginald-the-3rd",
		"Zoë--Ann":                  "zo-ann",
		"花子":                        "untitled",
		"":                          "untitled",
	} {
		if got := Slugify(name); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", name, got, want)
		}
	}
	long := Slugify("a234567890 b234567890 c234567890 d234567890 e234567890 f2345678 gg")
	if len(long) > maxSlugLen || long[len(long)-1] == '-' {
		t.Errorf("long slug %q", long)
	}
}

func TestImportSkipsContentSeenBefore(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	e := Edit{Data: json.RawMessage(`{"name": "Brakka"}`), Source: "plugin:folder"}

	id, created, err := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e)
	if err != nil || !created {
		t.Fatalf("first import = %d, %v, %v", id, created, err)
	}
	again, created, err := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e)
	if err != nil || created || again != id {
		t.Fatalf("second import = %d, %v, %v; want existing %d", again, created, err, id)
	}

	// Still skipped after the owner edits the card.
	c, _ := lib.Cards.Get(ctx, id)
	if _, err := lib.Cards.Update(ctx, id, c.HeadRevisionID, Edit{Data: json.RawMessage(`{"name": "Brakka!"}`), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if _, created, _ := lib.Cards.Import(ctx, "folder:brakka.png", "Brakka", e); created {
		t.Fatal("re-import after an edit created a card")
	}

	// The same content from another source is a different card, with a free slug.
	other, created, err := lib.Cards.Import(ctx, "folder:copy.png", "Brakka", e)
	if err != nil || !created {
		t.Fatalf("import from another source = %v, %v", created, err)
	}
	if c, _ := lib.Cards.Get(ctx, other); c.Slug != "brakka-2" {
		t.Fatalf("slug = %q, want brakka-2", c.Slug)
	}
	third, _, _ := lib.Cards.Import(ctx, "folder:third.png", "Brakka", e)
	if c, _ := lib.Cards.Get(ctx, third); c.Slug != "brakka-3" {
		t.Fatalf("slug = %q, want brakka-3", c.Slug)
	}

	if _, _, err := lib.Cards.Import(ctx, "folder:list.json", "List", Edit{Data: json.RawMessage(`[]`), Source: "plugin:folder"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("import of a JSON array: %v, want ErrInvalid", err)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/library -run 'Slugify|Import'`
Expected: FAIL with `undefined: Slugify`.

- [ ] **Step 3: Add `ErrInvalid`**

The importer needs to tell a bad card from a database failure. In `internal/library/library.go`, add to the error `var` block:

```go
	// ErrInvalid means an edit's data is not a valid card or lorebook.
	ErrInvalid = errors.New("invalid data")
```

In `Entities.prepare` in `internal/library/entities.go`, wrap all four validation errors:

```go
		return revisionRow{}, fmt.Errorf("%w: data must be a JSON object", ErrInvalid)
```

```go
			return revisionRow{}, fmt.Errorf("%w: %s data may not contain %s", ErrInvalid, s.k.table, k)
```

```go
	if r.contentHash, err = cardhash.ContentHash(e.Data); err != nil {
		return revisionRow{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
```

```go
		return revisionRow{}, fmt.Errorf("%w: simhash: %w", ErrInvalid, err)
```

- [ ] **Step 4: Write `Import`**

Create `internal/library/import.go`:

```go
package library

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const maxSlugLen = 60

// Slugify turns a name into a slug: lowercase ASCII letters and digits in
// runs joined by single hyphens.
//
// ponytail: other letters are dropped, so a name in another script becomes
// "untitled"; transliterate if that gets common.
func Slugify(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String()[:min(b.Len(), maxSlugLen)], "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// freeSlug returns base, or base-N with N one more than the highest suffix
// in use.
func (s *Entities) freeSlug(ctx context.Context, base string) (string, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT coalesce(max(CASE WHEN slug = $1 THEN 1
			ELSE substring(slug from '-([0-9]{1,9})$')::int END), 0)
		FROM `+s.k.table+` WHERE slug = $1 OR slug ~ ('^' || $1 || '-[0-9]{1,9}$')`, base).Scan(&n)
	if err != nil || n == 0 {
		return base, err
	}
	return fmt.Sprintf("%s-%d", base, n+1), nil
}

// Import adds an entity from a plugin, with a slug made from name, unless an
// entity with the same provenance has ever had this content. It returns the
// entity's ID and whether it created it, so importing the same item twice,
// or again after the owner edited it, adds nothing.
//
// ponytail: a source item whose content changed is imported as a new entity,
// not a new revision of the old one.
func (s *Entities) Import(ctx context.Context, provenance, name string, e Edit) (int64, bool, error) {
	r, err := s.prepare(e)
	if err != nil {
		return 0, false, err
	}
	var id int64
	err = s.pool.QueryRow(ctx, `SELECT e.id FROM `+s.k.table+` e WHERE e.provenance = $1 AND EXISTS (
		SELECT 1 FROM `+s.k.revTable+` r WHERE r.`+s.k.fk+` = e.id AND r.content_hash = $2)
		ORDER BY e.id LIMIT 1`, provenance, r.contentHash).Scan(&id)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	// Retried because another import can take the same slug first.
	for range 5 {
		slug, err := s.freeSlug(ctx, Slugify(name))
		if err != nil {
			return 0, false, err
		}
		ent, _, err := s.Create(ctx, slug, provenance, e)
		if errors.Is(err, ErrSlugTaken) {
			continue
		}
		if err != nil {
			return 0, false, err
		}
		return ent.ID, true, nil
	}
	return 0, false, ErrSlugTaken
}
```

- [ ] **Step 5: Run the tests to make sure they pass**

Run: `go test -race ./internal/library`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/library
git commit -m "Import cards and lorebooks with provenance dedupe"
```

---

### Task 8: Importer

**Files:**
- Create: `internal/importer/importer.go`
- Create: `internal/importer/ingest.go`
- Test: `internal/importer/importer_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/importer/importer_test.go`:

```go
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type env struct {
	pool *pgxpool.Pool
	q    *jobs.Queue
	lib  *library.Library
	reg  *plugins.Registry
	inst int64
}

// setup runs a worker with the importer against a fake import plugin started
// with args.
func setup(t *testing.T, args ...string) *env {
	t.Helper()
	ctx := context.Background()
	pool := testdb.New(t)
	store := &blobstore.Store{Name: "library", Root: t.TempDir()}
	lib := library.New(pool, store)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"import"}, args...))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(pool, map[string]*protocol.Manifest{"fake": m}, store)
	im := New(lib, reg, quiet)
	mgr := pluginhost.NewManager(im.Handle, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	im.Plugins = mgr

	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Log: quiet, Poll: 50 * time.Millisecond,
		Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]jobs.Handler{Kind: im.Run}}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("worker: %v", err)
		}
	})

	inst, err := reg.CreateInstance(ctx, "fake", "Fake", nil)
	if err != nil {
		t.Fatal(err)
	}
	return &env{pool: pool, q: q, lib: lib, reg: reg, inst: inst}
}

// run imports with params and returns the finished job and its progress.
func (e *env) run(t *testing.T, params string) (jobs.Job, progress) {
	t.Helper()
	ctx := context.Background()
	j, err := e.q.Enqueue(ctx, jobs.Spec{Kind: Kind, InstanceID: &e.inst, Payload: Payload{Params: json.RawMessage(params)}})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if j, err = e.q.Get(ctx, j.ID); err != nil {
			t.Fatal(err)
		}
		if j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed {
			var p progress
			if err := json.Unmarshal(j.Progress, &p); err != nil {
				t.Fatal(err)
			}
			return j, p
		}
	}
	t.Fatalf("import job still %s", j.Status)
	return j, progress{}
}

func errText(j jobs.Job) string {
	if j.Error == nil {
		return ""
	}
	return *j.Error
}

func (e *env) count(t *testing.T, table string) (n int) {
	t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportCards(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	j, p := e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 4 || p.Done != 3 || p.Total != 3 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	if n := e.count(t, "card"); n != 3 {
		t.Fatalf("%d cards, want 3", n)
	}
	if n := e.count(t, "lorebook"); n != 4 {
		t.Fatalf("%d lorebooks, want 3 split from cards and 1 standalone", n)
	}
	if _, err := e.lib.Lorebooks.GetBySlug(ctx, "world-0"); err != nil {
		t.Fatalf("standalone lorebook: %v", err)
	}

	c, err := e.lib.Cards.GetBySlug(ctx, "fake-1")
	if err != nil {
		t.Fatal(err)
	}
	rev, err := e.lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Provenance != "fake:card-1" || rev.Source != "plugin:fake" || rev.AvatarBlob == nil {
		t.Fatalf("card %+v, revision %+v", c, rev)
	}
	books, err := e.lib.CardLorebooks(ctx, c.ID)
	if err != nil || len(books) != 1 {
		t.Fatalf("linked lorebooks = %v, %v", books, err)
	}
	book, _ := e.lib.Lorebooks.Get(ctx, books[0])
	if book.Slug != "lore-1" {
		t.Fatalf("linked lorebook slug = %q, want lore-1", book.Slug)
	}

	// Importing the same items again adds nothing.
	_, p = e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if p.Imported != 0 || p.Skipped != 4 || e.count(t, "card") != 3 {
		t.Fatalf("re-import progress %+v", p)
	}
}

func TestImportResumesFromCheckpointAfterCrash(t *testing.T) {
	e := setup(t)
	j, p := e.run(t, `{"cards": 5, "crash_at": 2}`)
	if j.Status != jobs.StatusSucceeded || j.Attempts != 2 {
		t.Fatalf("job %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
	// Only card 2, sent after the last checkpoint, is sent again.
	if p.Skipped != 1 || e.count(t, "card") != 5 {
		t.Fatalf("progress %+v, %d cards", p, e.count(t, "card"))
	}
}

func TestBadCardFailsOnlyThatItem(t *testing.T) {
	e := setup(t)
	j, p := e.run(t, `{"cards": 3, "bad": 1}`)
	if j.Status != jobs.StatusSucceeded || p.Imported != 2 || p.Failed != 1 {
		t.Fatalf("job %s, progress %+v", j.Status, p)
	}
	if len(p.Errors) != 1 || !strings.HasPrefix(p.Errors[0], `card-1: spec "chara_card_v2"`) {
		t.Fatalf("errors = %q", p.Errors)
	}
}

func TestPluginErrors(t *testing.T) {
	e := setup(t)
	j, _ := e.run(t, `{"fail": "fatal"}`)
	if j.Status != jobs.StatusFailed || j.Attempts != 1 || !strings.Contains(errText(j), "no such folder") {
		t.Fatalf("fatal: %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
	j, _ = e.run(t, `{"fail": "retryable"}`)
	if j.Status != jobs.StatusFailed || j.Attempts != j.MaxAttempts || !strings.Contains(errText(j), "source unavailable") {
		t.Fatalf("retryable: %s after %d attempts: %s", j.Status, j.Attempts, errText(j))
	}
}

func TestPluginThatFailsToStartBreaksInstance(t *testing.T) {
	e := setup(t, "-mode=wrong-version")
	j, _ := e.run(t, `{}`)
	if j.Status != jobs.StatusFailed || j.Attempts != 1 {
		t.Fatalf("job %s after %d attempts", j.Status, j.Attempts)
	}
	if _, err := e.reg.Instance(context.Background(), e.inst); !errors.Is(err, plugins.ErrBroken) {
		t.Fatalf("instance: %v, want ErrBroken", err)
	}
}
```

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./internal/importer`
Expected: FAIL with `undefined: New`.

- [ ] **Step 3: Write the job handler and routing**

Create `internal/importer/importer.go`:

```go
// Package importer runs import jobs: it calls an import plugin's import.run
// and stores the cards and lorebooks the plugin emits.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// Kind is the job kind for imports. The job's instance is the import plugin
// instance, and its payload is a Payload.
const Kind = "import"

// maxErrors is how many item errors a job's progress keeps.
const maxErrors = 20

// Payload is an import job's payload. Params go to the plugin unchanged.
type Payload struct {
	Params json.RawMessage `json:"params"`
}

// Importer runs import jobs.
type Importer struct {
	Lib      *library.Library
	Registry *plugins.Registry
	// Plugins runs the import plugins. Its core handler must be Handle.
	Plugins *pluginhost.Manager
	Log     *slog.Logger

	mu   sync.Mutex
	runs map[string]*session
}

func New(lib *library.Library, reg *plugins.Registry, log *slog.Logger) *Importer {
	return &Importer{Lib: lib, Registry: reg, Log: log, runs: map[string]*session{}}
}

// progress is an import job's progress. Done, Total, and Message come from
// the plugin; the counts are the core's.
type progress struct {
	Done     int64    `json:"done,omitempty"`
	Total    int64    `json:"total,omitempty"`
	Message  string   `json:"message,omitempty"`
	Imported int64    `json:"imported"`
	Skipped  int64    `json:"skipped"`
	Failed   int64    `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

// session is one running import.run call.
type session struct {
	im     *Importer
	run    *jobs.Run
	plugin string
	ctx    context.Context
	cancel context.CancelCauseFunc

	mu  sync.Mutex
	p   progress
	err error
}

// Run is the jobs.Handler for Kind.
func (im *Importer) Run(ctx context.Context, r *jobs.Run) error {
	if r.InstanceID == nil {
		return errors.New("import job has no plugin instance")
	}
	inst, err := im.Registry.Instance(ctx, *r.InstanceID)
	if err != nil {
		return err
	}
	var payload Payload
	if err := json.Unmarshal(r.Payload, &payload); err != nil {
		return fmt.Errorf("import payload: %w", err)
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	s := &session{im: im, run: r, plugin: inst.Manifest.Name, ctx: ctx, cancel: cancel}
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
	err = im.Plugins.Call(ctx, inst, protocol.MethodImportRun,
		protocol.ImportRunParams{Run: token, Params: payload.Params, Checkpoint: r.Checkpoint}, nil)
	if serr := s.failure(); serr != nil {
		return serr
	}
	var startErr *pluginhost.StartError
	var rpcErr *protocol.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &startErr):
		if merr := im.Registry.MarkBroken(context.WithoutCancel(ctx), *r.InstanceID, err); merr != nil {
			im.Log.Error("mark plugin instance broken", "instance", *r.InstanceID, "err", merr)
		}
		return err
	case errors.Is(err, protocol.ErrClosed):
		return fmt.Errorf("%w: plugin exited: %w", jobs.ErrRetry, err)
	case errors.As(err, &rpcErr) && rpcErr.Retryable():
		return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
	}
	return err
}

// Handle is the core handler for the plugin manager that runs imports. It
// sends each notification to the run its run token names.
func (im *Importer) Handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.NotifyEmitCard, protocol.NotifyEmitLorebook, protocol.NotifyProgress, protocol.NotifyCheckpoint:
	default:
		return nil, protocol.MethodNotFound(method)
	}
	var head struct {
		Run string `json:"run"`
	}
	_ = json.Unmarshal(params, &head)
	im.mu.Lock()
	s := im.runs[head.Run]
	im.mu.Unlock()
	if s == nil {
		im.Log.Warn("notification for a run that is not active", "method", method, "run", head.Run)
		return nil, nil
	}
	s.handle(method, params)
	return nil, nil
}

// handle runs on the plugin connection's reading goroutine, so notifications
// are handled one at a time in the order sent. A checkpoint is therefore
// saved only after every item sent before it is stored.
func (s *session) handle(method string, params json.RawMessage) {
	if s.ctx.Err() != nil {
		return
	}
	switch method {
	case protocol.NotifyEmitCard:
		var e protocol.EmitCard
		if err := json.Unmarshal(params, &e); err != nil {
			s.count(e.Item, false, &badItem{err})
			break
		}
		created, err := s.im.ingestCard(s.ctx, s.plugin, e)
		s.count(e.Item, created, err)
	case protocol.NotifyEmitLorebook:
		var e protocol.EmitLorebook
		if err := json.Unmarshal(params, &e); err != nil {
			s.count(e.Item, false, &badItem{err})
			break
		}
		created, err := s.im.ingestLorebook(s.ctx, s.plugin, e)
		s.count(e.Item, created, err)
	case protocol.NotifyProgress:
		var p protocol.Progress
		if json.Unmarshal(params, &p) == nil {
			s.mu.Lock()
			s.p.Done, s.p.Total, s.p.Message = p.Done, p.Total, p.Message
			s.mu.Unlock()
		}
	case protocol.NotifyCheckpoint:
		var c protocol.Checkpoint
		if err := json.Unmarshal(params, &c); err != nil {
			s.fail(fmt.Errorf("plugin sent an invalid checkpoint: %w", err))
			return
		}
		s.publish()
		if err := s.run.SaveCheckpoint(s.ctx, c.Checkpoint); err != nil {
			s.fail(retryable(err))
		}
		return
	}
	s.publish()
}

func (s *session) count(item string, created bool, err error) {
	var bad *badItem
	if err != nil && !errors.As(err, &bad) {
		s.fail(retryable(fmt.Errorf("store %q: %w", item, err)))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case bad != nil:
		s.p.Failed++
		if len(s.p.Errors) < maxErrors {
			s.p.Errors = append(s.p.Errors, fmt.Sprintf("%s: %v", item, bad.err))
		}
	case created:
		s.p.Imported++
	default:
		s.p.Skipped++
	}
}

func (s *session) publish() {
	s.mu.Lock()
	raw, _ := json.Marshal(s.p)
	s.mu.Unlock()
	s.run.SetProgress(raw)
}

// fail stops the run: the import.run call is cancelled and Run returns err.
func (s *session) fail(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
	s.cancel(err)
}

func (s *session) failure() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// retryable marks a library or database failure as temporary. A lost lease
// is left as it is, so the worker abandons the job.
func retryable(err error) error {
	if errors.Is(err, jobs.ErrLeaseLost) {
		return err
	}
	return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
}
```

- [ ] **Step 4: Write the ingest code**

Create `internal/importer/ingest.go`:

```go
package importer

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

// badItem is an emitted item the plugin got wrong. It is counted as failed
// and the import goes on.
type badItem struct{ err error }

func (b *badItem) Error() string { return b.err.Error() }
func (b *badItem) Unwrap() error { return b.err }

// invalid turns the library's validation errors into badItem.
func invalid(err error) error {
	if errors.Is(err, library.ErrInvalid) {
		return &badItem{err}
	}
	return err
}

// ingestCard stores an emitted card. An embedded character_book becomes a
// linked lorebook. It returns whether the card is new.
func (im *Importer) ingestCard(ctx context.Context, plugin string, e protocol.EmitCard) (bool, error) {
	if e.Item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	var card struct {
		Spec string                     `json:"spec"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(e.Card, &card); err != nil {
		return false, &badItem{err}
	}
	if card.Spec != "chara_card_v3" {
		return false, &badItem{fmt.Errorf("spec %q is not chara_card_v3", card.Spec)}
	}
	if card.Data == nil {
		return false, &badItem{errors.New("card has no data")}
	}
	var name string
	_ = json.Unmarshal(card.Data["name"], &name)
	book := card.Data["character_book"]
	delete(card.Data, "character_book")
	data, err := json.Marshal(card.Data)
	if err != nil {
		return false, err
	}
	provenance, source := plugin+":"+e.Item, "plugin:"+plugin

	var avatar string
	if e.Avatar != "" {
		b, err := im.Lib.IngestBlob(ctx, e.Avatar)
		if err != nil {
			return false, &badItem{fmt.Errorf("avatar: %w", err)}
		}
		avatar = b.SHA256
	}
	// The lorebook goes first, and linking is idempotent, so a run
	// interrupted partway leaves nothing a retry can't finish.
	var lorebookID int64
	if len(book) > 0 && string(book) != "null" {
		var meta struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(book, &meta)
		lorebookID, _, err = im.Lib.Lorebooks.Import(ctx, provenance, cmp.Or(meta.Name, name+" lorebook"),
			library.Edit{Data: book, Source: source, Message: "Split from imported card"})
		if err != nil {
			return false, invalid(err)
		}
	}
	cardID, created, err := im.Lib.Cards.Import(ctx, provenance, name,
		library.Edit{Data: data, AvatarBlob: avatar, Source: source, Message: "Imported"})
	if err != nil {
		return false, invalid(err)
	}
	if lorebookID != 0 {
		if err := im.Lib.LinkLorebook(ctx, cardID, lorebookID); err != nil {
			return false, err
		}
	}
	return created, nil
}

// ingestLorebook stores an emitted standalone lorebook. It returns whether
// the lorebook is new.
func (im *Importer) ingestLorebook(ctx context.Context, plugin string, e protocol.EmitLorebook) (bool, error) {
	if e.Item == "" {
		return false, &badItem{errors.New("item is empty")}
	}
	var meta struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(e.Lorebook, &meta); err != nil {
		return false, &badItem{err}
	}
	_, created, err := im.Lib.Lorebooks.Import(ctx, plugin+":"+e.Item, cmp.Or(meta.Name, "lorebook"),
		library.Edit{Data: e.Lorebook, Source: "plugin:" + plugin, Message: "Imported"})
	return created, invalid(err)
}
```

- [ ] **Step 5: Run the tests to make sure they pass**

Run: `go test -race -count=3 ./internal/importer`
Expected: PASS on all three runs.

- [ ] **Step 6: Check the resume test depends on the checkpoint**

In `Run`, temporarily change `Checkpoint: r.Checkpoint` to `Checkpoint: nil`, then run `go test ./internal/importer -run Resumes`. Expected: FAIL with `job failed after 8 attempts`. Undo the change.

- [ ] **Step 7: Commit**

```bash
git add internal/importer
git commit -m "Run imports through import plugins"
```

---

### Task 9: Bulk archive

**Files:**
- Create: `internal/bulk/bulk.go`
- Test: `internal/bulk/bulk_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/bulk/bulk_test.go`:

```go
package bulk

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/testdb"
)

func TestArchive(t *testing.T) {
	ctx := context.Background()
	pool := testdb.New(t)
	lib := library.New(pool, &blobstore.Store{Name: "library", Root: t.TempDir()})
	var ids []int64
	for _, slug := range []string{"a", "b"} {
		c, _, err := lib.Cards.Create(ctx, slug, "upload", library.Edit{Data: []byte(`{"name": "x"}`), Source: "ui"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	q := jobs.New(pool)
	w := &jobs.Worker{Queue: q, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Handlers: map[string]jobs.Handler{ArchiveKind: Archive(lib)}}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(wctx) }()
	defer func() { cancel(); <-done }()

	parent, err := EnqueueArchive(ctx, q, ids, true)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		j, _ := q.Get(ctx, parent.ID)
		if j.Status == jobs.StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("bulk job still %s", j.Status)
		}
	}
	for _, id := range ids {
		if c, _ := lib.Cards.Get(ctx, id); c.ArchivedAt == nil {
			t.Errorf("card %d not archived", id)
		}
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/bulk`
Expected: FAIL with `undefined: ArchiveKind`.

- [ ] **Step 3: Write the bulk action**

Create `internal/bulk/bulk.go`:

```go
// Package bulk holds bulk actions: a parent job with one child job per card.
package bulk

import (
	"context"
	"encoding/json"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
)

// Job kinds.
const (
	Kind        = "bulk"
	ArchiveKind = "card.archive"
)

type archivePayload struct {
	CardID   int64 `json:"card_id"`
	Archived bool  `json:"archived"`
}

// EnqueueArchive archives or unarchives cards, one child job per card.
func EnqueueArchive(ctx context.Context, q *jobs.Queue, cardIDs []int64, archived bool) (jobs.Job, error) {
	children := make([]jobs.Spec, len(cardIDs))
	for i, id := range cardIDs {
		children[i] = jobs.Spec{Kind: ArchiveKind, Payload: archivePayload{CardID: id, Archived: archived}}
	}
	return q.EnqueueBulk(ctx, jobs.Spec{Kind: Kind, Payload: map[string]any{"action": ArchiveKind, "archived": archived}}, children)
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
```

- [ ] **Step 4: Run the test to make sure it passes**

Run: `go test -race ./internal/bulk`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bulk
git commit -m "Add bulk archive action"
```

---

### Task 10: Blob garbage collection

**Files:**
- Modify: `internal/blobstore/blobstore.go`
- Create: `internal/library/gc.go`
- Test: `internal/library/gc_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/library/gc_test.go`:

```go
package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectGarbage(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	put := func(s string) Blob {
		b, err := lib.PutBlob(ctx, strings.NewReader(s))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	orphan, kept, recent := put("orphan"), put("kept"), put("recent")
	if _, _, err := lib.Cards.Create(ctx, "a", "upload", Edit{Data: []byte(`{"name": "A"}`), AvatarBlob: kept.SHA256, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lib.pool.Exec(ctx, `UPDATE blob SET created_at = now() - interval '2 days' WHERE sha256 <> $1`, recent.SHA256); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.MkdirAll(lib.blobs.TmpDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	stale, fresh := filepath.Join(lib.blobs.TmpDir(), "stale"), filepath.Join(lib.blobs.TmpDir(), "fresh")
	for _, f := range []string{stale, fresh} {
		if err := os.WriteFile(f, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	blobs, leftovers, err := lib.CollectGarbage(ctx, 24*time.Hour)
	if err != nil || blobs != 1 || leftovers != 1 {
		t.Fatalf("CollectGarbage = %d, %d, %v; want 1, 1", blobs, leftovers, err)
	}
	if _, err := lib.Blob(ctx, orphan.SHA256); err != ErrNotFound {
		t.Errorf("orphan blob row: %v", err)
	}
	if p, _ := lib.blobs.Path(orphan.SHA256); fileExists(p) {
		t.Error("orphan blob file still exists")
	}
	for _, b := range []Blob{kept, recent} {
		if p, _ := lib.blobs.Path(b.SHA256); !fileExists(p) {
			t.Errorf("blob %s was deleted", b.SHA256)
		}
	}
	if fileExists(stale) || !fileExists(fresh) {
		t.Errorf("tmp: stale exists %v, fresh exists %v", fileExists(stale), fileExists(fresh))
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/library -run TestCollectGarbage`
Expected: FAIL with `lib.CollectGarbage undefined`.

- [ ] **Step 3: Add file removal to the blob store**

In `internal/blobstore/blobstore.go`, add `"errors"`, `"io/fs"`, and `"time"` to the imports, then append:

```go
// Remove deletes a blob's file. A file that is already gone is not an error.
func (s *Store) Remove(sha string) error {
	path, err := s.Path(sha)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// RemoveStale deletes entries in the tmp and staging folders last modified
// before cutoff: files from plugins and puts that never finished.
func (s *Store) RemoveStale(cutoff time.Time) (int, error) {
	n := 0
	for _, dir := range []string{s.TmpDir(), s.stagingDir()} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return n, err
		}
		for _, e := range entries {
			info, err := e.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}
```

- [ ] **Step 4: Write `CollectGarbage`**

Create `internal/library/gc.go`:

```go
package library

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// CollectGarbage deletes blobs in this library's store that no revision
// references, and leftover files in its tmp and staging folders. It only
// touches what is older than grace, which keeps it clear of imports in
// flight. It returns how many blobs and leftover files it deleted.
//
// ponytail: a blob re-ingested while it is being deleted can lose its file;
// the grace period makes that rare, as the spec accepts.
func (l *Library) CollectGarbage(ctx context.Context, grace time.Duration) (blobs, leftovers int, err error) {
	rows, err := l.pool.Query(ctx, `DELETE FROM blob b
		WHERE store = $1 AND created_at < now() - make_interval(secs => $2)
			AND NOT EXISTS (SELECT 1 FROM card_revision WHERE avatar_blob = b.sha256)
			AND NOT EXISTS (SELECT 1 FROM lorebook_revision WHERE avatar_blob = b.sha256)
		RETURNING sha256`, l.blobs.Name, grace.Seconds())
	if err != nil {
		return 0, 0, err
	}
	shas, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, 0, err
	}
	for _, sha := range shas {
		if err := l.blobs.Remove(sha); err != nil {
			return blobs, 0, err
		}
		blobs++
	}
	leftovers, err = l.blobs.RemoveStale(time.Now().Add(-grace))
	return blobs, leftovers, err
}
```

- [ ] **Step 5: Run the tests to make sure they pass**

Run: `go test -race ./internal/library ./internal/blobstore`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/blobstore/blobstore.go internal/library/gc.go internal/library/gc_test.go
git commit -m "Collect unreferenced blobs and leftover files"
```

---

### Task 11: `innkeeper worker`

**Files:**
- Create: `cmd/innkeeper/worker.go`
- Modify: `cmd/innkeeper/main.go`
- Modify: `README.md`

- [ ] **Step 1: Write the command**

Create `cmd/innkeeper/worker.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
)

const blobGCKind = "blob.gc"

func worker() error {
	cfg := map[string]string{}
	for _, k := range []string{"INNKEEPER_DATABASE_URL", "INNKEEPER_BLOB_DIR", "INNKEEPER_PLUGINS_DIR"} {
		if cfg[k] = os.Getenv(k); cfg[k] == "" {
			return fmt.Errorf("%s is not set", k)
		}
	}
	concurrency := 4
	if v := os.Getenv("INNKEEPER_WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return errors.New("INNKEEPER_WORKER_CONCURRENCY must be a positive number")
		}
		concurrency = n
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg["INNKEEPER_DATABASE_URL"])
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	store := &blobstore.Store{Name: "library", Root: cfg["INNKEEPER_BLOB_DIR"]}
	lib := library.New(pool, store)
	manifests, err := plugins.LoadDir(cfg["INNKEEPER_PLUGINS_DIR"], log)
	if err != nil {
		return err
	}
	im := importer.New(lib, plugins.New(pool, manifests, store), log)
	mgr := pluginhost.NewManager(im.Handle, 5*time.Minute, log)
	defer mgr.Close()
	im.Plugins = mgr
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mgr.StopIdle()
			}
		}
	}()

	w := &jobs.Worker{
		Queue:       jobs.New(pool),
		Log:         log,
		Concurrency: concurrency,
		Handlers: map[string]jobs.Handler{
			importer.Kind:    im.Run,
			bulk.ArchiveKind: bulk.Archive(lib),
			blobGCKind: func(ctx context.Context, r *jobs.Run) error {
				blobs, leftovers, err := lib.CollectGarbage(ctx, 24*time.Hour)
				r.SetProgress(fmt.Appendf(nil, `{"blobs": %d, "leftovers": %d}`, blobs, leftovers))
				return err
			},
		},
		Schedules: []jobs.Schedule{{Kind: blobGCKind, Every: 24 * time.Hour}},
	}
	log.Info("worker started", "plugins", len(manifests), "concurrency", concurrency)
	return w.Run(ctx)
}
```

- [ ] **Step 2: Route the command**

In `cmd/innkeeper/main.go`, extend `usage`:

```go
const usage = `usage:
  innkeeper plugin check <plugin-dir>
  innkeeper migrate    (reads INNKEEPER_DATABASE_URL)
  innkeeper worker     (reads INNKEEPER_DATABASE_URL, INNKEEPER_BLOB_DIR,
                        INNKEEPER_PLUGINS_DIR, INNKEEPER_WORKER_CONCURRENCY)`
```

Add to `run`, after the `migrate` case:

```go
	if len(args) == 1 && args[0] == "worker" {
		return worker()
	}
```

- [ ] **Step 3: Document it**

Add to the "Development" section of `README.md`:

````markdown
`innkeeper worker` runs jobs: imports, bulk actions, and daily blob cleanup.
It migrates the database at startup and stops cleanly on `SIGTERM`.

| Variable | Meaning |
|---|---|
| `INNKEEPER_DATABASE_URL` | PostgreSQL URL |
| `INNKEEPER_BLOB_DIR` | The library's blob store folder |
| `INNKEEPER_PLUGINS_DIR` | Folder with one subfolder per installed plugin |
| `INNKEEPER_WORKER_CONCURRENCY` | Jobs run at once; default 4 |
````

- [ ] **Step 4: Check it end to end**

This builds the fake plugin as an installed import plugin, starts the worker, and queues an import that crashes once.

```bash
S=$(mktemp -d)
mkdir -p $S/plugins/fake $S/blobs
go build -o $S/innkeeper ./cmd/innkeeper
go build -o $S/plugins/fake/fakeplugin ./internal/fakeplugin
printf 'name = "fake"\nversion = "0.0.1"\nprotocol = 1\ncommand = ["./fakeplugin", "-caps=import"]\ncapabilities = ["import"]\n' > $S/plugins/fake/plugin.toml
PG=postgres://postgres:postgres@localhost:55432
psql -q $PG/postgres -c 'DROP DATABASE IF EXISTS innkeeper_e2e' -c 'CREATE DATABASE innkeeper_e2e'
export INNKEEPER_DATABASE_URL=$PG/innkeeper_e2e INNKEEPER_BLOB_DIR=$S/blobs INNKEEPER_PLUGINS_DIR=$S/plugins
$S/innkeeper worker 2> $S/worker.log &
sleep 2
psql -q $INNKEEPER_DATABASE_URL \
  -c "INSERT INTO plugin_instance (plugin, name) VALUES ('fake', 'Fake')" \
  -c "INSERT INTO job (kind, instance_id, payload) VALUES ('import', 1, '{\"params\": {\"cards\": 4, \"book\": true, \"avatar\": true, \"crash_at\": 1}}')"
sleep 14
psql $INNKEEPER_DATABASE_URL -c "SELECT id, kind, status, attempts, progress FROM job ORDER BY id" -c "SELECT slug, provenance FROM card ORDER BY id"
kill -TERM %1; wait %1; echo "worker exit: $?"; cat $S/worker.log
```

Expected:

- The `blob.gc` job succeeded.
- The `import` job succeeded after 2 attempts, with `"skipped": 1`.
- Cards `fake-0` to `fake-3` exist.
- The worker exits 0.
- The log shows attempt 1 re-queued with `plugin exited`, then attempt 2 succeeded about 10 seconds later.

Then run everything:

```bash
gofmt -l . && go vet ./... && go test -race ./...
```

Expected: no files listed, and every package passes.

- [ ] **Step 5: Commit**

```bash
git add cmd/innkeeper README.md
git commit -m "Add innkeeper worker command"
```
