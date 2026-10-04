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

func TestCancelStopsRunningHandler(t *testing.T) {
	q := newQueue(t)
	running, returned := make(chan struct{}), make(chan struct{})
	startWorker(t, &Worker{Queue: q, Lease: 300 * time.Millisecond, Handlers: map[string]Handler{
		"test": func(ctx context.Context, _ *Run) error {
			close(running)
			<-ctx.Done()
			close(returned)
			return ctx.Err()
		},
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	select {
	case <-running:
	case <-time.After(10 * time.Second):
		t.Fatal("job never started")
	}
	if err := q.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("handler kept running after cancel")
	}
	// Give the worker time to attempt its finish, which must not overwrite the cancel.
	time.Sleep(500 * time.Millisecond)
	got := waitStatus(t, q, j.ID, StatusFailed)
	if errText(got) != "cancelled" || got.Attempts != 1 {
		t.Fatalf("job = %+v, want failed, cancelled, 1 attempt", got)
	}
}

func TestFinalErrorFailsEvenOnShutdown(t *testing.T) {
	q := newQueue(t)
	started := make(chan struct{})
	w := &Worker{Queue: q, Log: quiet, Poll: time.Hour, Handlers: map[string]Handler{
		"test": func(ctx context.Context, _ *Run) error {
			close(started)
			<-ctx.Done()
			return fmt.Errorf("%w: the app may have the card", ErrFinal)
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
	if got, _ := q.Get(context.Background(), j.ID); got.Status != StatusFailed {
		t.Fatalf("job after shutdown = %+v", got)
	}
}
