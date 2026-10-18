# Job metrics implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record each job's start time and the cards it changed, and show duration and cards on the Jobs pages.

**Architecture:** Two new `job` columns, `started_at` and `cards_changed`. `claim` sets the start; handlers call `Run.AddCardsChanged`, which the worker saves with each lease renewal and at finish. A bulk parent's values come from its children through `Queue.ChildrenFor`. The API adds `started_at`, `duration_ms` and `cards_changed` to `jobOut`.

**Tech stack:** Go, PostgreSQL (pgx), huma, React, TanStack Query, Vitest.

**Spec:** [2026-10-18-job-metrics-design.md](../specs/2026-10-18-job-metrics-design.md)

**Database tests:** Go tests skip without `INNKEEPER_TEST_DATABASE_URL`. Set it before running any `go test` below, or the tests "pass" without running.

---

## File map

| File | Change |
|---|---|
| `internal/db/migrations/0042_job_metrics.sql` | New: the two columns |
| `internal/jobs/queue.go` | `Job` fields, `jobCols`, `claim`, `Record`, `Retry`, `touch`, `finish`, `ChildrenFor` |
| `internal/jobs/worker.go` | `Run.AddCardsChanged`, `runState` |
| `internal/api/jobs.go` | `jobOut` fields, bulk fill |
| `internal/bulk/bulk.go` | Archive, Tag, Collect, Purge, Hide, DeleteLibrary count |
| `internal/library/libraries.go` | `DeleteLibrary` callback reports kind and size |
| `internal/importer/importer.go` | Count imported and revised cards |
| `internal/enrich/enrich.go` | Count cards that got values |
| `internal/wire/reconcile.go` | `SyncResult.CardsChanged` (not serialized) |
| `internal/targets/{jobs,resolve_job,reconcile_job,sync,check,sources}.go` | Push, resolve, reconcile, sync, source check count |
| `web/src/settings/{jobUtil.ts,Jobs.tsx,Job.tsx}` | `span`, Duration and Cards columns, job page rows |

---

### Task 1: Store when a job started

**Files:**
- Create: `internal/db/migrations/0042_job_metrics.sql`
- Modify: `internal/jobs/queue.go` (`Job`, `jobCols`, `scanJob`, `claim`, `Record`, `Retry`)
- Modify: `internal/targets/sync.go` (the one `Record` caller)
- Test: `internal/jobs/queue_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/jobs/queue_test.go`:

```go
func TestClaimSetsStartedAtOnce(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	if j.StartedAt != nil {
		t.Fatalf("queued job started at %v", j.StartedAt)
	}
	first, err := q.claim(ctx, "w1", []string{"test"}, time.Minute)
	if err != nil || first == nil || first.StartedAt == nil {
		t.Fatalf("claim = %+v, %v", first, err)
	}
	if err := q.finish(ctx, first, "w1", nil, outcome{status: StatusQueued, err: "try again"}); err != nil {
		t.Fatal(err)
	}
	again, err := q.claim(ctx, "w1", []string{"test"}, time.Minute)
	if err != nil || again == nil || again.StartedAt == nil || !again.StartedAt.Equal(*first.StartedAt) {
		t.Fatalf("retry claim = %+v, %v; want started at %v", again, err, first.StartedAt)
	}
	if err := q.finish(ctx, again, "w1", nil, outcome{status: StatusFailed, err: "boom"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, j.ID); got.StartedAt != nil {
		t.Fatalf("manually retried job started at %v, want nil", got.StartedAt)
	}
}
```

In `TestRecord`, change the two `Record` calls and the first check:

```go
	ok, err := q.Record(ctx, Spec{Kind: "t.rec", Payload: map[string]int{"n": 1}}, json.RawMessage(`{"x":1}`), 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	var p struct{ X int }
	if ok.Status != StatusSucceeded || ok.FinishedAt == nil || ok.StartedAt == nil || ok.CardsChanged != 3 ||
		json.Unmarshal(ok.Progress, &p) != nil || p.X != 1 {
		t.Errorf("recorded = %+v", ok)
	}
	bad, err := q.Record(ctx, Spec{Kind: "t.rec"}, nil, 0, errors.New("boom"))
```

- [ ] **Step 2: Run the tests to check they fail**

Run: `go test ./internal/jobs -run 'TestClaimSetsStartedAtOnce|TestRecord' -v`
Expected: compile errors, because `StartedAt` and `CardsChanged` aren't fields of `Job` and `Record` takes no cards argument.

- [ ] **Step 3: Add the migration**

Create `internal/db/migrations/0042_job_metrics.sql`:

```sql
-- started_at is when the first attempt was claimed; a manual retry clears
-- it. cards_changed counts the cards the job changed, across every attempt.
ALTER TABLE job
    ADD COLUMN started_at timestamptz,
    ADD COLUMN cards_changed int NOT NULL DEFAULT 0 CHECK (cards_changed >= 0);
```

Check that no other branch has taken `0042` since: `git fetch && git ls-tree --name-only origin/develop internal/db/migrations/ | tail -1`. Renumber if it has.

- [ ] **Step 4: Add the fields and set them**

In `internal/jobs/queue.go`, add to `Job` after `FinishedAt`:

```go
	// StartedAt is when the first attempt was claimed.
	StartedAt    *time.Time
	CardsChanged int
```

Extend `jobCols` and `scanJob`:

```go
const jobCols = `id, kind, instance_id, payload, status, attempts, max_attempts, progress, checkpoint,
	run_after, parent_id, error, created_at, finished_at, started_at, cards_changed`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.InstanceID, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
		&j.Progress, &j.Checkpoint, &j.RunAfter, &j.ParentID, &j.Error, &j.CreatedAt, &j.FinishedAt,
		&j.StartedAt, &j.CardsChanged)
```

In `claim`, set the start on the first claim only:

```go
	j, err := scanJob(q.pool.QueryRow(ctx, `UPDATE job SET status = 'running', attempts = attempts + 1,
			started_at = coalesce(started_at, now()),
			locked_by = $1, locked_until = now() + make_interval(secs => $2)
```

Replace `Record` with a version that takes the cards and sets the start:

```go
// Record adds a finished top-level job holding progress and the cards it
// changed, for work another job did itself. A failure fails it with that
// message.
func (q *Queue) Record(ctx context.Context, s Spec, progress json.RawMessage, cards int, failure error) (Job, error) {
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
	return scanJob(q.pool.QueryRow(ctx, `INSERT INTO job (kind, instance_id, payload, status, progress, error, cards_changed,
			started_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now(), now()) RETURNING `+jobCols,
		s.Kind, s.InstanceID, payload, status, progress, msg, cards))
}
```

In `Retry`, clear the start:

```go
	tag, err := q.pool.Exec(ctx, `UPDATE job SET status = 'queued', attempts = 0, error = NULL, finished_at = NULL,
			started_at = NULL, run_after = now(), locked_by = NULL, locked_until = NULL
```

In `internal/targets/sync.go`, pass `0` for now (Task 9 sets the real count):

```go
		j, rerr := q.Record(ctx, jobs.Spec{Kind: ReconcileKind, InstanceID: &id, Payload: params}, raw, 0, failure)
```

- [ ] **Step 5: Run the tests to check they pass**

Run: `go test ./internal/jobs ./internal/db ./internal/targets -run 'TestClaimSetsStartedAtOnce|TestRecord' -v`
Expected: PASS. Then run all of `go test ./internal/jobs`; everything should pass.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0042_job_metrics.sql internal/jobs/queue.go internal/jobs/queue_test.go internal/targets/sync.go
git commit -m "feat(jobs): record when a job started"
```

---

### Task 2: Count the cards a job changed

**Files:**
- Modify: `internal/jobs/worker.go` (`Run`, `execute`, `heartbeat`, `SaveProgress`, `SaveCheckpoint`, `SaveCheckpointTx`)
- Modify: `internal/jobs/queue.go` (`touch`, `finish`)
- Test: `internal/jobs/worker_test.go`, `internal/jobs/queue_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/jobs/worker_test.go`:

```go
func TestCardsChangedAddsUpAcrossAttempts(t *testing.T) {
	q := newQueue(t)
	saved, release := make(chan error), make(chan struct{})
	startWorker(t, &Worker{Queue: q, Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]Handler{
		"test": func(ctx context.Context, r *Run) error {
			r.AddCardsChanged(2)
			if r.Attempts == 1 {
				saved <- r.SaveProgress(ctx)
				<-release
				return fmt.Errorf("%w: crashed", ErrRetry)
			}
			return errors.New("bad input")
		},
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	running, err := q.Get(context.Background(), j.ID)
	close(release)
	if err != nil || running.CardsChanged != 2 {
		t.Fatalf("running job = %+v, %v; want 2 cards saved", running, err)
	}
	if got := waitStatus(t, q, j.ID, StatusFailed); got.CardsChanged != 4 {
		t.Fatalf("failed job changed %d cards, want 4 across both attempts", got.CardsChanged)
	}
}
```

- [ ] **Step 2: Run the test to check it fails**

Run: `go test ./internal/jobs -run TestCardsChangedAddsUpAcrossAttempts -v`
Expected: compile error, `r.AddCardsChanged undefined`.

- [ ] **Step 3: Add the count to `Run`**

In `internal/jobs/worker.go`, add a field to `Run` under the existing mutex:

```go
	mu       sync.Mutex
	progress json.RawMessage
	cards    int
```

Replace `currentProgress` with `current`, and add `AddCardsChanged` and `runState` next to `SetProgress`:

```go
// AddCardsChanged counts n more cards the job changed. Like progress, it is
// saved with the next lease renewal, checkpoint, or finish.
func (r *Run) AddCardsChanged(n int) {
	r.mu.Lock()
	r.cards += n
	r.mu.Unlock()
}

// runState is what a run saves with each renewal and at its finish.
type runState struct {
	progress json.RawMessage
	cards    int
}

func (r *Run) current() runState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return runState{r.progress, r.cards}
}
```

Replace every `r.currentProgress()` and `run.currentProgress()` with `r.current()` or `run.current()`. They appear in `SaveProgress`, `SaveCheckpoint`, `SaveCheckpointTx`, `execute` (the `finish` call) and `heartbeat`.

In `execute`, start the count from the saved value so it carries across attempts:

```go
	run := &Run{Job: *j, q: w.Queue, worker: w.id, lease: w.Lease, log: log, progress: j.Progress, cards: j.CardsChanged}
```

- [ ] **Step 4: Save the count in `touch` and `finish`**

In `internal/jobs/queue.go`:

```go
// touch renews the lease and saves the run's state, and the checkpoint
// unless it is nil.
func (q *Queue) touch(ctx context.Context, db execer, j *Job, worker string, lease time.Duration, st runState, checkpoint json.RawMessage) error {
	// clock_timestamp, not now(): inside a caller's long transaction, now() is when it began.
	tag, err := db.Exec(ctx, `UPDATE job SET locked_until = clock_timestamp() + make_interval(secs => $3),
			progress = coalesce($4::jsonb, progress), checkpoint = coalesce($5::jsonb, checkpoint), cards_changed = $7
		WHERE id = $1 AND locked_by = $2 AND attempts = $6 AND status = 'running'`,
		j.ID, worker, lease.Seconds(), st.progress, checkpoint, j.Attempts, st.cards)
```

```go
func (q *Queue) finish(ctx context.Context, j *Job, worker string, st runState, o outcome) error {
	refund := 0
	if o.refund {
		refund = 1
	}
	tag, err := q.pool.Exec(ctx, `UPDATE job SET status = $3, error = nullif($4, ''),
			run_after = now() + make_interval(secs => $5), attempts = attempts - $6,
			progress = coalesce($7::jsonb, progress), cards_changed = $9, locked_by = NULL, locked_until = NULL,
			finished_at = CASE WHEN $3 IN ('succeeded', 'failed') THEN now() END
		WHERE id = $1 AND locked_by = $2 AND attempts = $8 AND status = 'running'`,
		j.ID, worker, o.status, o.err, o.runAfter.Seconds(), refund, st.progress, j.Attempts, st.cards)
```

In `internal/jobs/queue_test.go`, replace the `nil` progress argument with `runState{}` in the existing `q.touch` calls (lines near 191 and 264) and the `q.finish` call (near 194). Do the same for both `q.finish` calls in `TestClaimSetsStartedAtOnce` from Task 1.

- [ ] **Step 5: Run the tests to check they pass**

Run: `go test -race ./internal/jobs`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/jobs
git commit -m "feat(jobs): count the cards a job changed"
```

---

### Task 3: Sum a bulk job's children

**Files:**
- Modify: `internal/jobs/queue.go` (replace `ChildCounts` and `ChildCountsFor` with `ChildrenFor`)
- Modify: `internal/api/jobs.go` (the two callers; done fully in Task 4, kept compiling here)
- Test: `internal/jobs/queue_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/jobs/queue_test.go`:

```go
func TestChildrenForSumsChildMetrics(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}, {Kind: "item"}, {Kind: "item"}})
	first, _ := q.claim(ctx, "w1", []string{"item"}, time.Minute)
	second, _ := q.claim(ctx, "w1", []string{"item"}, time.Minute)
	for i, c := range []*Job{first, second} {
		if err := q.finish(ctx, c, "w1", runState{cards: i + 2}, outcome{status: StatusSucceeded}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := q.ChildrenFor(ctx, []int64{p.ID, 999999})
	if err != nil {
		t.Fatal(err)
	}
	c := got[p.ID]
	if len(got) != 1 || c.Counts[StatusSucceeded] != 2 || c.Counts[StatusQueued] != 1 || c.CardsChanged != 5 ||
		c.StartedAt == nil || !c.StartedAt.Equal(*first.StartedAt) {
		t.Fatalf("children = %+v", got)
	}
}
```

In `TestCancelParentCancelsChildren`, replace the `ChildCounts` call:

```go
	children, err := q.ChildrenFor(ctx, []int64{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if counts := children[p.ID].Counts; counts[StatusFailed] != 2 || len(counts) != 1 {
		t.Fatalf("children = %v, want 2 failed", counts)
	}
```

- [ ] **Step 2: Run the test to check it fails**

Run: `go test ./internal/jobs -run 'TestChildrenFor|TestCancelParentCancelsChildren' -v`
Expected: compile error, `q.ChildrenFor undefined`.

- [ ] **Step 3: Replace `ChildCounts` and `ChildCountsFor`**

In `internal/jobs/queue.go`, delete both functions and add:

```go
// Children sums up a bulk job's children.
type Children struct {
	// Counts is how many children have each status.
	Counts map[string]int
	// StartedAt is the earliest child's start, nil until one starts.
	StartedAt    *time.Time
	CardsChanged int
}

// ChildrenFor sums up each parent's children in one query. Parents without
// children are left out.
func (q *Queue) ChildrenFor(ctx context.Context, parents []int64) (map[int64]Children, error) {
	rows, err := q.pool.Query(ctx, `SELECT parent_id, status, count(*)::int, min(started_at), sum(cards_changed)::int
		FROM job WHERE parent_id = ANY($1) GROUP BY parent_id, status`, parents)
	if err != nil {
		return nil, err
	}
	out := map[int64]Children{}
	var parent int64
	var status string
	var n, cards int
	var started *time.Time
	_, err = pgx.ForEachRow(rows, []any{&parent, &status, &n, &started, &cards}, func() error {
		c := out[parent]
		if c.Counts == nil {
			c.Counts = map[string]int{}
		}
		c.Counts[status] = n
		c.CardsChanged += cards
		if started != nil && (c.StartedAt == nil || started.Before(*c.StartedAt)) {
			c.StartedAt = new(*started)
		}
		out[parent] = c
		return nil
	})
	return out, err
}
```

In `internal/api/jobs.go`, keep the build green until Task 4.

In `job()`:

```go
		children, err := s.Queue.ChildrenFor(ctx, []int64{id})
		if err != nil {
			return jobOut{}, s.fail(err)
		}
		out.Children = children[id].Counts
```

In `list-jobs`:

```go
			children, err := s.Queue.ChildrenFor(ctx, parents)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &jobsOut{Body: make([]jobOut, len(list))}
			for i, j := range list {
				out.Body[i] = summary(toJob(j))
				out.Body[i].Children = children[j.ID].Counts
			}
```

- [ ] **Step 4: Run the tests to check they pass**

Run: `go vet ./... && go test ./internal/jobs ./internal/api -run 'Children|Cancel|Bulk' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/jobs internal/api/jobs.go
git commit -m "feat(jobs): sum a bulk job's children with their start and cards"
```

---

### Task 4: Show the metrics in the API

**Files:**
- Modify: `internal/api/jobs.go` (`jobOut`, `toJob`, `job`, `list-jobs`)
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts` (with `npm run gen`, never by hand)
- Test: `internal/api/jobs_test.go`

- [ ] **Step 1: Write the failing test**

In `TestBulkArchiveAndWatch` in `internal/api/jobs_test.go`, extend the last-event check and the list check:

```go
	if last.Status != "succeeded" || last.Children["succeeded"] != 2 || last.StartedAt == nil || last.DurationMS == nil {
		t.Fatalf("last event = %+v\n%s", last, r.Body)
	}
```

```go
	if len(list) != 1 || list[0].Children["succeeded"] != 2 || list[0].StartedAt == nil || list[0].DurationMS == nil {
		t.Fatalf("listed bulk job = %+v", list)
	}
```

In `TestImportAndCancel`, after the first `e.call("GET", "/jobs", nil, 200, &list)` check, add:

```go
	if list[0].StartedAt != nil || list[0].DurationMS != nil || list[0].CardsChanged != 0 {
		t.Fatalf("queued job metrics = %+v", list[0])
	}
```

- [ ] **Step 2: Run the tests to check they fail**

Run: `go test ./internal/api -run 'TestBulkArchiveAndWatch|TestImportAndCancel' -v`
Expected: compile error, `last.StartedAt undefined`.

- [ ] **Step 3: Add the fields**

In `internal/api/jobs.go`, add to `jobOut` after `FinishedAt`:

```go
	StartedAt    *time.Time `json:"started_at,omitempty" doc:"When the first attempt started; a bulk job's is its earliest child's. A manual retry clears it."`
	DurationMS   *int64     `json:"duration_ms,omitempty" doc:"Milliseconds from started_at to finished_at, once the job finishes."`
	CardsChanged int        `json:"cards_changed" doc:"Cards the job changed in the library or an app, across every attempt; a bulk job's sums its children's."`
```

Set them in `toJob`, and add the two helpers below it:

```go
func toJob(j jobs.Job) jobOut {
	return jobOut{ID: j.ID, Kind: j.Kind, InstanceID: j.InstanceID, Payload: j.Payload, Status: j.Status,
		Attempts: j.Attempts, MaxAttempts: j.MaxAttempts, Progress: j.Progress, ParentID: j.ParentID, Error: j.Error,
		RunAfter: j.RunAfter, CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt, StartedAt: j.StartedAt,
		DurationMS: durationMS(j.StartedAt, j.FinishedAt), CardsChanged: j.CardsChanged}
}

func durationMS(start, end *time.Time) *int64 {
	if start == nil || end == nil {
		return nil
	}
	return new(end.Sub(*start).Milliseconds())
}

// withChildren fills a bulk job's counts, start, and cards from its children.
func (o *jobOut) withChildren(c jobs.Children) {
	o.Children, o.StartedAt, o.CardsChanged = c.Counts, c.StartedAt, c.CardsChanged
	o.DurationMS = durationMS(o.StartedAt, o.FinishedAt)
}
```

In `job()`, replace `out.Children = children[id].Counts` with:

```go
		out.withChildren(children[id])
```

In `list-jobs`, fill only the jobs that have children, so a job without children keeps its own values:

```go
			for i, j := range list {
				out.Body[i] = summary(toJob(j))
				if c, ok := children[j.ID]; ok {
					out.Body[i].withChildren(c)
				}
			}
```

- [ ] **Step 4: Run the tests to check they pass**

Run: `go test ./internal/api -run 'TestBulkArchiveAndWatch|TestImportAndCancel|TestReconcileJobSummary' -v`
Expected: PASS.

- [ ] **Step 5: Regenerate the API schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` gain `started_at`, `duration_ms` and `cards_changed` on `JobOut`, and the typecheck passes.

- [ ] **Step 6: Commit**

```bash
git add internal/api web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): add started_at, duration_ms, and cards_changed to jobs"
```

---

### Task 5: Count cards in the per-card bulk actions

**Files:**
- Modify: `internal/bulk/bulk.go` (`Archive`, `Tag`, `Collect`)
- Test: `internal/bulk/bulk_test.go`, `internal/api/jobs_test.go`

- [ ] **Step 1: Write the failing tests**

In `TestArchive` in `internal/bulk/bulk_test.go`, after `wait(t, q, parent.ID)`:

```go
	if children, err := q.ChildrenFor(ctx, []int64{parent.ID}); err != nil || children[parent.ID].CardsChanged != 2 {
		t.Fatalf("children = %+v, %v; want 2 cards changed", children, err)
	}
```

In `TestTagAndCollect`, after the tag job's `wait(t, q, p.ID)`:

```go
	if children, err := q.ChildrenFor(ctx, []int64{p.ID}); err != nil || children[p.ID].CardsChanged != 2 {
		t.Fatalf("tag children = %+v, %v; want 2 cards changed", children, err)
	}
```

Add the same check after the collect job's `wait`, using that job's variable.

In `TestBulkArchiveAndWatch` in `internal/api/jobs_test.go`, add `|| last.CardsChanged != 2` to the last-event check and `|| list[0].CardsChanged != 2` to the list check from Task 4.

- [ ] **Step 2: Run the tests to check they fail**

Run: `go test ./internal/bulk ./internal/api -run 'TestArchive$|TestTagAndCollect|TestBulkArchiveAndWatch' -v`
Expected: FAIL with `want 2 cards changed` and a 0 count.

- [ ] **Step 3: Count each card**

In `internal/bulk/bulk.go`:

```go
// Archive is the jobs.Handler for ArchiveKind.
//
// ponytail: counts the card even when it was already archived; compare
// before and after if the count must be exact.
func Archive(lib *library.Library, apps unpublisher) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p archivePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		if err := SetArchived(ctx, lib, apps, library.Card, p.CardID, p.Archived, p.Unpublish); err != nil {
			return err
		}
		r.AddCardsChanged(1)
		return nil
	}
}
```

```go
// Tag is the jobs.Handler for TagKind.
func Tag(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p tagPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		if err := lib.EditCardTags(ctx, p.CardID, p.Add, p.Remove); err != nil {
			return err
		}
		r.AddCardsChanged(1)
		return nil
	}
}
```

```go
// Collect is the jobs.Handler for CollectKind.
func Collect(lib *library.Library) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		var p collectPayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return err
		}
		edit := lib.AddToCollection
		if p.Remove {
			edit = lib.RemoveFromCollection
		}
		if err := edit(ctx, p.CollectionID, p.CardID); err != nil {
			return err
		}
		r.AddCardsChanged(1)
		return nil
	}
}
```

`ArchiveLorebook` stays as it is: lorebooks aren't cards.

- [ ] **Step 4: Run the tests to check they pass**

Run: `go test ./internal/bulk ./internal/api -run 'TestArchive|TestTagAndCollect|TestBulk' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bulk internal/api/jobs_test.go
git commit -m "feat(bulk): count the cards archive, tag, and collect change"
```

---

### Task 6: Count cards in purge, hide, and library delete

**Files:**
- Modify: `internal/bulk/bulk.go` (`Purge`, `Hide`, `DeleteLibrary`)
- Modify: `internal/library/libraries.go` (`DeleteLibrary` callback)
- Test: `internal/api/purge_test.go`, `internal/api/hide_test.go`, `internal/api/libraries_test.go`, `internal/library/libraries_test.go`

- [ ] **Step 1: Write the failing tests**

In `TestPurgeCards` (`internal/api/purge_test.go`), extend the success check:

```go
	if last.Status != "succeeded" || last.CardsChanged != 2 {
		t.Fatalf("purge = %+v\n%s", last, resp.Body)
	}
```

In `TestHideArchivesLowScoresAndDuplicates` (`internal/api/hide_test.go`), add to the `hide` helper after unmarshalling `p`:

```go
		if last.CardsChanged != p.Archived+p.Unarchived {
			t.Fatalf("hide changed %d cards, progress %+v", last.CardsChanged, p)
		}
```

In `TestDeleteLibraryRoute` (`internal/api/libraries_test.go`), extend the success check:

```go
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" || last.CardsChanged != 1 {
		t.Fatalf("delete = %+v\n%s", last, r.Body)
	}
```

In `internal/library/libraries_test.go`, the test near line 125 checks progress. Replace that check with:

```go
	type batch struct {
		kind *Kind
		n    int
	}
	var seen []batch
	if err := lib.DeleteLibrary(ctx, archive.ID, func(k *Kind, n int) { seen = append(seen, batch{k, n}) }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []batch{{Card, 2}, {Lorebook, 1}}) {
		t.Errorf("batches = %v, want 2 cards then 1 lorebook", seen)
	}
```

Change the other three `DeleteLibrary` callbacks in that file from `func(int)` to `func(*Kind, int)`.

- [ ] **Step 2: Run the tests to check they fail**

Run: `go test ./internal/library ./internal/api -run 'DeleteLibrary|Purge|Hide' -v`
Expected: compile error in `internal/library`, and FAIL for purge and hide with a 0 count.

- [ ] **Step 3: Report each batch's kind**

In `internal/library/libraries.go`, change `DeleteLibrary`'s callback so it hears each batch:

```go
// DeleteLibrary deletes a library with its cards, lorebooks, and
// collections. It works in batches, each its own transaction, so a run that
// stops part way can run again. progress hears each batch's kind and size.
// Blobs stay until garbage collection finds them unused.
func (l *Library) DeleteLibrary(ctx context.Context, id int64, progress func(k *Kind, n int)) error {
	if id == DefaultLibrary {
		return fmt.Errorf("%w: the default library can't be deleted", ErrInvalid)
	}
	// An import still writing to the library can add entities after the
	// batches; the library row's delete then fails, and the batches run again.
	for range 3 {
		for _, s := range []*Entities{l.Cards, l.Lorebooks} {
			for {
				n, err := s.deleteSome(ctx, id)
				if err != nil {
					return fmt.Errorf("delete %s of library %d: %w", s.k.Plural, id, err)
				}
				if n == 0 {
					break
				}
				progress(s.k, n)
			}
		}
```

The rest of the function is unchanged. The `deleted` total moves to the job.

- [ ] **Step 4: Count in the handlers**

In `internal/bulk/bulk.go`, `DeleteLibrary`:

```go
		deleted := 0
		err := lib.DeleteLibrary(ctx, p.Library, func(k *library.Kind, n int) {
			deleted += n
			if k == library.Card {
				r.AddCardsChanged(n)
			}
			r.SetProgress(fmt.Appendf(nil, `{"deleted": %d}`, deleted))
		})
```

In `Purge`, after `prog.Done += n`:

```go
			r.AddCardsChanged(n)
```

In `Hide`, after the `SetHidden` error check and before the progress is marshalled:

```go
		r.AddCardsChanged(prog.Archived + prog.Unarchived)
```

- [ ] **Step 5: Run the tests to check they pass**

Run: `go test ./internal/library ./internal/bulk ./internal/api -run 'DeleteLibrary|Purge|Hide|Librar' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/library internal/bulk internal/api
git commit -m "feat(bulk): count the cards purge, hide, and library delete change"
```

---

### Task 7: Count cards an import stores

**Files:**
- Modify: `internal/importer/importer.go` (`store`, `flush`)
- Test: `internal/importer/importer_test.go`

- [ ] **Step 1: Write the failing test**

In `TestImportCards`, extend the first check. The fixture imports 3 cards and 1 standalone lorebook, and only the cards count:

```go
	if j.Status != jobs.StatusSucceeded || p.Imported != 4 || p.Done != 3 || p.Total != 3 || j.CardsChanged != 3 {
		t.Fatalf("job %s, %d cards changed, progress %+v", j.Status, j.CardsChanged, p)
	}
```

At the end of the test, change the re-import line to capture the job and check it:

```go
	j, p = e.run(t, `{"cards": 3, "book": true, "avatar": true, "lorebooks": 1}`)
	if p.Imported != 0 || p.Skipped != 4 || e.count(t, "card") != 3 || j.CardsChanged != 0 {
		t.Fatalf("re-import: %d cards changed, progress %+v", j.CardsChanged, p)
	}
```

If the repo has a test that runs a bulk-mode plugin (`Manifest.Bulk`), add `j.CardsChanged` to its first success check too. Search with `grep -n "Bulk" internal/importer/*_test.go`.

- [ ] **Step 2: Run the test to check it fails**

Run: `go test ./internal/importer -run TestImportCards -v`
Expected: FAIL with `0 cards changed`.

- [ ] **Step 3: Count once each item is stored**

In `internal/importer/importer.go`, add below `count`:

```go
// cardChanged reports whether storing it created or revised a card.
func cardChanged(it library.PluginItem, got library.Imported, err error) bool {
	return err == nil && it.Kind == library.Card && (got.Outcome == library.Created || got.Outcome == library.Revised)
}
```

In `store`, after the non-bulk `ImportItem`:

```go
	got, err := s.im.Lib.ImportItem(s.ctx, it)
	err = invalid(err)
	s.count(item, got, err)
	if cardChanged(it, got, err) {
		s.run.AddCardsChanged(1)
	}
	s.autosync = append(s.autosync, got.Autosync...)
```

In `flush`, count inside the transaction but add only once it commits, the same way its progress counts are restored when it fails:

```go
	var auto []int64
	cards := 0
	err := s.im.Lib.ImportBatch(s.ctx, items, func(tx pgx.Tx, got []library.Imported, errs []error) error {
		cards = 0
		for i, b := range buf {
			s.warn(b.name, b.warnings)
			err := invalid(errs[i])
			s.count(b.name, got[i], err)
			if cardChanged(b.it, got[i], err) {
				cards++
			}
			auto = append(auto, got[i].Autosync...)
		}
		s.publish()
		return s.run.SaveCheckpointTx(s.ctx, tx, cp)
	})
	if err != nil {
		s.mu.Lock()
		s.p = before
		s.mu.Unlock()
		s.publish()
		s.fail(retryable(err))
		return
	}
	s.run.AddCardsChanged(cards)
	s.autosync = append(s.autosync, auto...)
```

Then add a `ponytail:` line above `applyAutosync`:

```go
// ponytail: updates applied here aren't counted in cards changed; the
// session holds only binding IDs. Return the binding's kind if it matters.
```

- [ ] **Step 4: Run the tests to check they pass**

Run: `go test -race ./internal/importer`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/importer
git commit -m "feat(importer): count the cards an import creates or revises"
```

---

### Task 8: Count cards an enrich run scores

**Files:**
- Modify: `internal/enrich/enrich.go` (`run`)
- Test: `internal/api/enrichments_test.go`

- [ ] **Step 1: Write the failing test**

In `TestEnrichRunStoresCheckedValues`, extend the first check (`p.Done` is 2):

```go
	if j.Status != jobs.StatusSucceeded || p.Total != 5 || p.Done != 2 || p.Failed != 3 || j.CardsChanged != 2 {
		t.Fatalf("job %s, %d cards changed, progress %+v", j.Status, j.CardsChanged, p)
	}
```

- [ ] **Step 2: Run the test to check it fails**

Run: `go test ./internal/api -run TestEnrichRunStoresCheckedValues -v`
Expected: FAIL with `0 cards changed`.

- [ ] **Step 3: Count each batch's done cards**

In `internal/enrich/enrich.go`, in the `else` branch after `prog.Done += checked.Done`:

```go
			r.AddCardsChanged(checked.Done)
```

- [ ] **Step 4: Run the tests to check they pass**

Run: `go test ./internal/api -run TestEnrich -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/enrich internal/api/enrichments_test.go
git commit -m "feat(enrich): count the cards an enrich run scores"
```

---

### Task 9: Count cards in pushes, resolves, reconciles, syncs, and source checks

**Files:**
- Modify: `internal/targets/jobs.go` (`RunPush`, `RunSourceCheck`), `internal/targets/resolve_job.go` (`RunResolve`), `internal/targets/reconcile_job.go` (`RunReconcile`), `internal/targets/reconcile.go` (new `cardsChanged`), `internal/targets/sync.go` (`RunSync`, `Sync`, `autosync`), `internal/targets/check.go` (`ScanProgress`), `internal/targets/sources.go` (`checkSources`)
- Modify: `internal/wire/reconcile.go` (`SyncResult`)
- Test: `internal/targets/jobs_test.go`, `internal/targets/resolve_job_test.go`, `internal/targets/sources_test.go`, `internal/api/reconcile_test.go`

- [ ] **Step 1: Write the failing tests**

In `TestJobs` (`internal/targets/jobs_test.go`), after the bulk push succeeds:

```go
	if children, err := q.ChildrenFor(e.ctx, []int64{parent.ID}); err != nil || children[parent.ID].CardsChanged != 2 {
		t.Fatalf("push children = %+v, %v; want 2 cards changed", children, err)
	}
```

In `TestLorebookPushJob`, after its push succeeds, assert that the push job's `CardsChanged` is 0. A lorebook push changes no card. Read the test to find the job variable.

In `TestBulkResolveRepeatedImportIsStale` (`internal/targets/resolve_job_test.go`), capture each parent and check its children:

```go
	changed := func(parent jobs.Job) int {
		t.Helper()
		children, err := jobs.New(e.pool).ChildrenFor(e.ctx, []int64{parent.ID})
		if err != nil {
			t.Fatal(err)
		}
		return children[parent.ID].CardsChanged
	}
	parent, out := e.runResolve(rs)
	if out["import"] != 1 || changed(parent) != 1 {
		t.Fatalf("first: outcomes %v, %d cards changed", out, changed(parent))
	}
	cards := e.cardCount()
	if parent, out := e.runResolve(rs); out["stale"] != 1 || e.cardCount() != cards || changed(parent) != 0 {
		t.Fatalf("again: outcomes %v, %d cards, want %d", out, e.cardCount(), cards)
	}
```

In `TestAutosyncTakesASourceUpdateWithoutConflicts` (`internal/targets/sources_test.go`), extend the check:

```go
	if err != nil || p.Applied != 1 || p.Changed != 0 || p.cardsApplied != 1 {
```

In `TestSyncRoute` (`internal/api/reconcile_test.go`), the app holds one new card, `rCarrow`, which the sync imports. After the `app` job is fetched:

```go
	if app.CardsChanged != 1 || j.CardsChanged < 1 {
		t.Errorf("sync changed %d cards, its reconcile %d; want the imported card counted in both", j.CardsChanged, app.CardsChanged)
	}
```

If this fails because the sync didn't import `rCarrow`, look at the app job's snapshot for `auto_imported` before you change any code. Don't loosen the assertion.

- [ ] **Step 2: Run the tests to check they fail**

Run: `go test ./internal/targets ./internal/api -run 'TestJobs$|TestLorebookPushJob|RepeatedImport|TestAutosyncTakesASourceUpdate|TestSyncRoute' -v`
Expected: compile error for `p.cardsApplied`, and FAIL for the rest with 0 counts.

- [ ] **Step 3: Count pushes and resolves**

In `RunPush` (`internal/targets/jobs.go`), before the final success `SetProgress`:

```go
	if p.LorebookID == 0 {
		r.AddCardsChanged(1)
	}
```

The `errAlreadyPushed` path returns through `skipPush` and doesn't count.

In `RunResolve` (`internal/targets/resolve_job.go`), after `res, err := s.resolve(...)`:

```go
	if err == nil && res.RevisionID != 0 && !kindNamed(r.Kind).isLorebook() {
		run.AddCardsChanged(1)
	}
```

- [ ] **Step 4: Count reconciles and syncs**

In `internal/targets/reconcile.go`, add below `autoTotals`:

```go
// cardsChanged counts the cards a reconcile imported or pulled the app's
// changes into.
func cardsChanged(snap Snapshot) int {
	n := 0
	for _, it := range snap.Items {
		if it.AutoImported || it.AutoPulled {
			n++
		}
	}
	return n
}
```

In `RunReconcile` (`internal/targets/reconcile_job.go`), after the error check on `s.Reconcile`:

```go
	r.AddCardsChanged(cardsChanged(snap))
```

In `internal/wire/reconcile.go`, add to `SyncResult`:

```go
	// CardsChanged counts the cards the sync imported, pulled, or
	// autosync pushed. The sync job stores it, so progress leaves it out.
	CardsChanged int `json:"-"`
```

In `Sync` (`internal/targets/sync.go`), set the recorded reconcile's count and add it to the sync's:

```go
		var raw json.RawMessage
		cards := 0
		if err != nil {
			app.Error = err.Error()
		} else {
			app.Totals, params = snap.Totals, snap.Params
			cards = cardsChanged(snap)
			if raw, err = json.Marshal(snap); err != nil {
				return res, err
			}
		}
		j, rerr := q.Record(ctx, jobs.Spec{Kind: ReconcileKind, InstanceID: &id, Payload: params}, raw, cards, failure)
		if rerr != nil {
			return res, rerr
		}
		res.CardsChanged += cards
```

Make `autosync` return the cards it pushed. Change its signature to `func (s *Service) autosync(ctx context.Context, app *SyncApp) (cards int)`. Its existing bare `return` statements then return the count so far. In its `switch`:

```go
		case err == nil:
			app.Totals["autosync_pushed"]++
			if d.lorebook == 0 {
				cards++
			}
```

At its call site in `Sync`:

```go
		if res.Apps[i].Error == "" {
			res.CardsChanged += s.autosync(ctx, &res.Apps[i])
		}
```

In `RunSync`, record the count whether or not the sync failed partway:

```go
	res, err := s.Sync(ctx, p, report)
	r.AddCardsChanged(res.CardsChanged)
	if err != nil {
		return err
	}
```

- [ ] **Step 5: Count source-check updates**

In `ScanProgress` (`internal/targets/check.go`), add an unexported field after `Applied`:

```go
	// cardsApplied counts the Applied updates that went to cards.
	cardsApplied int
```

In `checkSources` (`internal/targets/sources.go`), in `then`:

```go
		if applied {
			p.Applied++
			if b.CardID != 0 {
				p.cardsApplied++
			}
			b.RemoteState = remoteUnchanged
		}
```

In `RunSourceCheck` (`internal/targets/jobs.go`), rename the payload variable so the result can be named `res`:

```go
func (s *Service) RunSourceCheck(ctx context.Context, r *jobs.Run) error {
	var p SourceCheckPayload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("source check payload: %w", err)
	}
	res, err := s.CheckSources(ctx, cmp.Or(p.Library, library.DefaultLibrary), func(p ScanProgress) {
		raw, _ := json.Marshal(p)
		r.SetProgress(raw)
	})
	r.AddCardsChanged(res.cardsApplied)
	return err
}
```

`RunScan` changes no cards and stays as it is.

- [ ] **Step 6: Run the tests to check they pass**

Run: `go test -race ./internal/targets ./internal/api`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/targets internal/wire internal/api
git commit -m "feat(targets): count the cards pushes, resolves, and syncs change"
```

---

### Task 10: Show duration and cards on the Jobs pages

**Files:**
- Modify: `web/src/settings/jobUtil.ts` (`span`, `age`)
- Modify: `web/src/settings/Jobs.tsx`, `web/src/settings/Job.tsx`
- Test: `web/src/settings/jobUtil.test.ts`, `web/src/settings/Jobs.test.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/settings/jobUtil.test.ts`, import `span` and add:

```ts
test("span is short", () => {
  expect(span(0)).toBe("0s");
  expect(span(45_000)).toBe("45s");
  expect(span(90 * 60_000)).toBe("1h");
});
```

In `web/src/settings/Jobs.test.tsx`, give the export a finished run and the failed import a count:

```ts
const exportJob = {
  id: 6, kind: "card.export", status: "succeeded", payload: {}, progress: { done: 2, total: 2, blob: "cd".repeat(32) },
  attempts: 1, max_attempts: 3, run_after: now, created_at: now, started_at: now, finished_at: now, duration_ms: 90_000,
  cards_changed: 0,
};
const failedImport = {
  id: 8, kind: "import", instance_id: 3, status: "failed", payload: {}, progress: { done: 10, total: 100 }, attempts: 8, max_attempts: 8,
  run_after: now, created_at: now, finished_at: now, error: "worker stopped renewing its lease", cards_changed: 12,
};
```

Add `within` to the `@testing-library/react` import, then add:

```ts
test("the list shows how long each job ran and the cards it changed", async () => {
  renderAt("/settings/jobs");
  await screen.findByText("card.export");
  const cells = (text: string) =>
    within(screen.getByText(text).closest("tr")!).getAllByRole("cell").map((c) => c.textContent);
  expect(cells("card.export").slice(5, 7)).toEqual(["1m", ""]);
  expect(cells("Chub archive").slice(5, 7)).toEqual(["", "12"]);
});
```

In `"a finished export links to its zip and opens no stream"`, add:

```ts
  expect(screen.getByText("Ran for").nextElementSibling).toHaveTextContent("1m");
  expect(screen.getByText("Cards changed").nextElementSibling).toHaveTextContent("0");
```

- [ ] **Step 2: Run the tests to check they fail**

Run: `cd web && npx vitest run src/settings`
Expected: FAIL. `span` isn't exported, and there are no Duration cells or "Ran for" row.

- [ ] **Step 3: Add `span`**

In `web/src/settings/jobUtil.ts`, replace `age`:

```ts
/** span is a length of time, short: 30s, 45m, 10h, 4d. */
export function span(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** age is how long ago t was, as span shows it. */
export const age = (t: string, now = Date.now()) => span(now - Date.parse(t));
```

- [ ] **Step 4: Add the columns and rows**

In `web/src/settings/Jobs.tsx`, import `span` from `./jobUtil`. Add two headers after Progress:

```tsx
            <th>Progress</th>
            <th>Duration</th>
            <th>Cards</th>
            <th>Age</th>
```

Add two cells after the progress cell:

```tsx
              <td>{progressText(j)}</td>
              <td>{j.duration_ms != null ? span(j.duration_ms) : ""}</td>
              <td>{j.cards_changed || ""}</td>
              <td>{age(j.created_at)}</td>
```

In `web/src/settings/Job.tsx`, import `span` from `./jobUtil`. Add after the Age row:

```tsx
        <dt>Age</dt>
        <dd>{age(j.created_at)}</dd>
        {j.started_at && (
          <>
            <dt>Waited</dt>
            <dd>{span(Date.parse(j.started_at) - Date.parse(j.created_at))}</dd>
          </>
        )}
        {j.duration_ms != null && (
          <>
            <dt>Ran for</dt>
            <dd>{span(j.duration_ms)}</dd>
          </>
        )}
        <dt>Cards changed</dt>
        <dd>{j.cards_changed}</dd>
```

- [ ] **Step 5: Run the tests to check they pass**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/settings
git commit -m "feat(web): show job duration and cards changed"
```

---

### Task 11: Run what CI runs

- [ ] **Step 1: Run the full suite**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass, with `INNKEEPER_TEST_DATABASE_URL` set so the database tests run.

- [ ] **Step 2: Check the diff for leftovers**

Run: `git diff develop --stat` and read the diff. Confirm:

- No reference to `ChildCounts`, `ChildCountsFor` or `currentProgress` remains (`grep -rn "ChildCounts\|currentProgress" internal`).
- Every `Record` caller passes a card count.
- `web/openapi.json` and `web/src/api/schema.d.ts` changed only through `npm run gen`.

- [ ] **Step 3: Check it in the app**

Start the app, then run an import and a bulk archive. On the Jobs page, each should show a duration and a card count, and the job page should show Waited, Ran for and Cards changed.
