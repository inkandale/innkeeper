package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/testdb"
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
	if err := q.touch(ctx, old, "w1", time.Minute, nil, nil); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale touch = %v; want ErrLeaseLost", err)
	}
	if err := q.finish(ctx, old, "w1", nil, outcome{status: StatusSucceeded}); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale finish = %v; want ErrLeaseLost", err)
	}
}
