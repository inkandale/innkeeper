package jobs

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClaimWaitsWhileTheSameJobRuns(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	lib1 := Spec{Kind: "test", Payload: map[string]int{"library": 1}}
	first, _ := q.Enqueue(ctx, lib1)
	if c, err := q.claim(ctx, "w1", []string{"test"}, time.Minute); err != nil || c == nil || c.ID != first.ID {
		t.Fatalf("first claim = %v, %v", c, err)
	}
	same, _ := q.Enqueue(ctx, lib1)
	other, _ := q.Enqueue(ctx, Spec{Kind: "test", Payload: map[string]int{"library": 2}})
	if c, err := q.claim(ctx, "w2", []string{"test"}, time.Minute); err != nil || c == nil || c.ID != other.ID {
		t.Fatalf("claim while library 1 runs = %v, %v; want job %d", c, err, other.ID)
	}
	if c, err := q.claim(ctx, "w2", []string{"test"}, time.Minute); err != nil || c != nil {
		t.Fatalf("claim of the same job = %v, %v; want nothing", c, err)
	}
	// A lease nobody renews doesn't hold the next run back.
	if _, err := q.pool.Exec(ctx, `UPDATE job SET locked_until = now() - interval '1 second' WHERE id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := q.claim(ctx, "w2", []string{"test"}, time.Minute); err != nil || c == nil || c.ID != same.ID {
		t.Fatalf("claim after the lease expired = %v, %v; want job %d", c, err, same.ID)
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
	lines, err := q.Logs(ctx, j.ID, 0, 10)
	if err != nil || len(lines) != 2 || lines[0].Level != "warn" || lines[1].Level != "error" ||
		lines[1].Message != "The worker stopped renewing its lease after attempt 2 of 1; out of attempts" {
		t.Fatalf("log = %+v, %v", lines, err)
	}
}

// A checkpoint saved at the end of a transaction that ran longer than the
// lease must still leave the lease running.
func TestCheckpointInLongTransactionKeepsLease(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	if _, err := q.Enqueue(ctx, Spec{Kind: "test"}); err != nil {
		t.Fatal(err)
	}
	lease := time.Second
	c, err := q.claim(ctx, "w1", []string{"test"}, lease)
	if err != nil || c == nil {
		t.Fatalf("claim = %v, %v", c, err)
	}
	run := &Run{Job: *c, q: q, worker: "w1", lease: lease}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_sleep(1.5)`); err != nil {
		t.Fatal(err)
	}
	if err := run.SaveCheckpointTx(ctx, tx, json.RawMessage(`{"at": 1}`)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.reap(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, c.ID); got.Status != StatusRunning {
		t.Fatalf("job after a long checkpoint transaction = %+v, want still running", got)
	}
}

func TestRetryQueuesAFailedJobFromItsCheckpoint(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	if _, err := q.pool.Exec(ctx, `UPDATE job SET status = 'failed', attempts = max_attempts, error = 'boom',
		finished_at = now(), checkpoint = '{"at": 7}' WHERE id = $1`, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(ctx, j.ID)
	if got.Status != StatusQueued || got.Attempts != 0 || got.Error != nil || got.FinishedAt != nil || string(got.Checkpoint) != `{"at": 7}` {
		t.Fatalf("retried job = %+v", got)
	}
	if err := q.Retry(ctx, j.ID); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("retrying a queued job: err = %v, want ErrNotRetryable", err)
	}
	child, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	if _, err := q.pool.Exec(ctx, `UPDATE job SET parent_id = CASE WHEN id = $2 THEN $1::bigint END, status = 'failed',
		finished_at = now() WHERE id IN ($1, $2)`, j.ID, child.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{j.ID, child.ID} {
		if err := q.Retry(ctx, id); !errors.Is(err, ErrNotRetryable) {
			t.Fatalf("retrying job %d of a bulk job: err = %v, want ErrNotRetryable", id, err)
		}
	}
	if err := q.Retry(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retrying a missing job: err = %v, want ErrNotFound", err)
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

func TestStaleRunLosesLeaseAfterReclaim(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	q.Enqueue(ctx, Spec{Kind: "test"})
	old, err := q.claim(ctx, "w1", []string{"test"}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if err := q.reap(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.claim(ctx, "w1", []string{"test"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := q.touch(ctx, q.pool, old, "w1", time.Minute, runState{}, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale touch = %v; want ErrLeaseLost", err)
	}
	if err := q.finish(ctx, old, "w1", runState{}, outcome{status: StatusSucceeded}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale finish = %v; want ErrLeaseLost", err)
	}
}

func TestCancel(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	if err := q.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := q.Get(ctx, j.ID)
	if got.Status != StatusFailed || errText(got) != "cancelled" || got.FinishedAt == nil {
		t.Fatalf("cancelled job = %+v", got)
	}
	if err := q.Cancel(ctx, j.ID); !errors.Is(err, ErrFinished) {
		t.Fatalf("second cancel: %v", err)
	}
	if err := q.Cancel(ctx, j.ID+100); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancel missing job: %v", err)
	}
}

func TestCancelParentCancelsChildren(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}, {Kind: "item"}})
	if err := q.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	children, err := q.ChildrenFor(ctx, []int64{p.ID})
	if err != nil {
		t.Fatal(err)
	}
	if counts := children[p.ID].Counts; counts[StatusFailed] != 2 || len(counts) != 1 {
		t.Fatalf("children = %v, want 2 failed", counts)
	}
	if got, _ := q.Get(ctx, p.ID); got.Status != StatusFailed {
		t.Fatalf("parent = %+v", got)
	}
}

func TestCancelLastChildFinishesParent(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}})
	children, err := q.List(ctx, ListOptions{Parent: &p.ID})
	if err != nil || len(children) != 1 {
		t.Fatalf("children = %v, %v", children, err)
	}
	if err := q.Cancel(ctx, children[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, p.ID); got.Status != StatusFailed || errText(got) != "1 of 1 jobs failed" {
		t.Fatalf("parent = %+v", got)
	}
}

func TestCancelRunningJobTakesItsLease(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	j, _ := q.Enqueue(ctx, Spec{Kind: "test"})
	c, err := q.claim(ctx, "w1", []string{"test"}, time.Minute)
	if err != nil || c == nil {
		t.Fatalf("claim = %v, %v", c, err)
	}
	if err := q.Cancel(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.touch(ctx, q.pool, c, "w1", time.Minute, runState{}, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("touch after cancel: %v", err)
	}
}

func TestReapFinishesOrphanedParent(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}})
	// A worker that died between finishing the child and checking the parent.
	if _, err := q.pool.Exec(ctx, `UPDATE job SET status = 'succeeded', finished_at = now() WHERE parent_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.reap(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, p.ID); got.Status != StatusSucceeded {
		t.Fatalf("parent = %+v", got)
	}
}

func TestListAndPrune(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	a, _ := q.Enqueue(ctx, Spec{Kind: "a"})
	b, _ := q.Enqueue(ctx, Spec{Kind: "b"})
	p, _ := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "item"}})

	top, err := q.List(ctx, ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 3 || top[0].ID != p.ID || top[2].ID != a.ID {
		t.Fatalf("top-level jobs = %v", ids(top))
	}
	if got, _ := q.List(ctx, ListOptions{Kind: "b"}); len(got) != 1 || got[0].ID != b.ID {
		t.Fatalf("kind b = %v", ids(got))
	}
	if got, _ := q.List(ctx, ListOptions{Status: StatusWaiting}); len(got) != 1 || got[0].ID != p.ID {
		t.Fatalf("waiting = %v", ids(got))
	}
	if got, _ := q.List(ctx, ListOptions{Before: b.ID}); len(got) != 1 || got[0].ID != a.ID {
		t.Fatalf("before b = %v", ids(got))
	}
	if got, _ := q.List(ctx, ListOptions{Limit: 1}); len(got) != 1 {
		t.Fatalf("limit 1 = %v", ids(got))
	}

	if err := q.Cancel(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := q.Cancel(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := q.pool.Exec(ctx, `UPDATE job SET finished_at = now() - interval '40 days' WHERE id = $1 OR parent_id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	n, err := q.Prune(ctx, 30*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("Prune = %d, %v; want 1", n, err)
	}
	var left int
	if err := q.pool.QueryRow(ctx, `SELECT count(*) FROM job`).Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 2 {
		t.Fatalf("%d jobs left, want a and b", left)
	}
}

func ids(js []Job) []int64 {
	out := make([]int64, len(js))
	for i, j := range js {
		out[i] = j.ID
	}
	return out
}

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

func TestEnqueueUnlessQueuedJoinsOnlyAQueuedJobWithTheSamePayload(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	lib1 := Spec{Kind: "t.unless", Payload: map[string]int{"library": 1}}
	a, err := q.EnqueueUnlessQueued(ctx, lib1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := q.EnqueueUnlessQueued(ctx, lib1)
	if err != nil || b.ID != a.ID {
		t.Fatalf("second start = %d, %v; want job %d", b.ID, err, a.ID)
	}
	other, err := q.EnqueueUnlessQueued(ctx, Spec{Kind: "t.unless", Payload: map[string]int{"library": 2}})
	if err != nil || other.ID == a.ID {
		t.Fatalf("start for another library = %d, %v; want a new job", other.ID, err)
	}
	// A running job may have read the library already, so a start queues another.
	if _, err := q.pool.Exec(ctx, `UPDATE job SET status = 'running' WHERE id = $1`, a.ID); err != nil {
		t.Fatal(err)
	}
	c, err := q.EnqueueUnlessQueued(ctx, lib1)
	if err != nil || c.ID == a.ID || c.Status != StatusQueued {
		t.Fatalf("start while running = %+v, %v", c, err)
	}
}

func TestRecord(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
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
	if err != nil || bad.Status != StatusFailed || bad.Error == nil || *bad.Error != "boom" || bad.FinishedAt == nil {
		t.Errorf("failed record = %+v, %v", bad, err)
	}
	listed, err := q.List(ctx, ListOptions{Kind: "t.rec"})
	if err != nil || len(listed) != 2 {
		t.Errorf("listed %d, %v", len(listed), err)
	}
}

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
	if err := q.finish(ctx, first, "w1", runState{}, outcome{status: StatusQueued, err: "try again"}); err != nil {
		t.Fatal(err)
	}
	again, err := q.claim(ctx, "w1", []string{"test"}, time.Minute)
	if err != nil || again == nil || again.StartedAt == nil || !again.StartedAt.Equal(*first.StartedAt) {
		t.Fatalf("retry claim = %+v, %v; want started at %v", again, err, first.StartedAt)
	}
	if err := q.finish(ctx, again, "w1", runState{}, outcome{status: StatusFailed, err: "boom"}); err != nil {
		t.Fatal(err)
	}
	if err := q.Retry(ctx, j.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := q.Get(ctx, j.ID); got.StartedAt != nil {
		t.Fatalf("manually retried job started at %v, want nil", got.StartedAt)
	}
}

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
