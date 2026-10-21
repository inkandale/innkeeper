package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	promtest "github.com/prometheus/client_golang/prometheus/testutil"
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

func TestWorkerStoppedBeforeListeningReturnsNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := &Worker{Queue: newQueue(t), Log: quiet, Handlers: map[string]Handler{"test": func(context.Context, *Run) error { return nil }}}
	if err := w.Run(ctx); err != nil {
		t.Fatalf("run = %v, want nil for a shutdown", err)
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

func TestSaveProgressWritesBeforeTheNextRenewal(t *testing.T) {
	q := newQueue(t)
	saved, release := make(chan error), make(chan struct{})
	startWorker(t, &Worker{Queue: q, Lease: time.Minute, Handlers: map[string]Handler{
		"test": func(ctx context.Context, r *Run) error {
			r.SetProgress(json.RawMessage(`{"message": "Writing"}`))
			saved <- r.SaveProgress(ctx)
			<-release
			return nil
		},
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	got, err := q.Get(context.Background(), j.ID)
	close(release)
	if err != nil || got.Status != StatusRunning || string(got.Progress) != `{"message": "Writing"}` {
		t.Fatalf("job = %+v, %v", got, err)
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

func TestMetricsRecordFinishedJobOnceAcrossRetries(t *testing.T) {
	q := newQueue(t)
	reg := prometheus.NewRegistry()
	startWorker(t, &Worker{Queue: q, Metrics: NewMetrics(reg), Backoff: func(int) time.Duration { return 0 }, Handlers: map[string]Handler{
		"test": func(_ context.Context, r *Run) error {
			r.AddCardsChanged(2)
			if r.Attempts == 1 {
				return fmt.Errorf("%w: flaky", ErrRetry)
			}
			return nil
		},
	}})
	j, _ := q.Enqueue(context.Background(), Spec{Kind: "test"})
	waitStatus(t, q, j.ID, StatusSucceeded)
	// The worker observes just after the finish commits.
	want := `
# HELP innkeeper_cards_changed_total Cards finished jobs changed in the library or in an app.
# TYPE innkeeper_cards_changed_total counter
innkeeper_cards_changed_total{kind="test"} 4
`
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if err = promtest.GatherAndCompare(reg, strings.NewReader(want), "innkeeper_cards_changed_total"); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if n, err := promtest.GatherAndCount(reg, "innkeeper_job_duration_seconds"); err != nil || n != 1 {
		t.Fatalf("duration series = %d, %v; want one, for the succeeded job only", n, err)
	}
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() != "innkeeper_job_duration_seconds" {
			continue
		}
		h := mf.GetMetric()[0]
		if got := h.GetHistogram().GetSampleCount(); got != 1 {
			t.Fatalf("duration samples = %d, want 1 for the job's two attempts", got)
		}
		for _, l := range h.GetLabel() {
			if l.GetName() == "status" && l.GetValue() != StatusSucceeded {
				t.Fatalf("status label = %s, want succeeded", l.GetValue())
			}
		}
	}
}

func TestOnceMakesErrorsFinalAndRefusesASecondAttempt(t *testing.T) {
	ran := 0
	h := Once(func(context.Context, *Run) error { ran++; return errors.New("model refused") })
	if err := h(context.Background(), &Run{Job: Job{Attempts: 1}}); !errors.Is(err, ErrFinal) || ran != 1 {
		t.Fatalf("first attempt: err = %v, ran %d times; want a final error after one run", err, ran)
	}
	err := h(context.Background(), &Run{Job: Job{Attempts: 2}})
	if !errors.Is(err, ErrFinal) || !strings.Contains(err.Error(), "interrupted") || ran != 1 {
		t.Fatalf("second attempt: err = %v, ran %d times; want a final error without running", err, ran)
	}
	if err := Once(func(context.Context, *Run) error { return nil })(context.Background(), &Run{Job: Job{Attempts: 1}}); err != nil {
		t.Fatalf("success: err = %v", err)
	}
}

func TestTriggerTellsScheduledManualAndAutomaticJobsApart(t *testing.T) {
	q := newQueue(t)
	ctx := context.Background()
	started := make(chan int64, 1)
	startWorker(t, &Worker{Queue: q, Poll: 50 * time.Millisecond,
		Schedules: []Schedule{{Kind: "tick", Every: time.Hour}},
		Handlers: map[string]Handler{
			"tick": func(ctx context.Context, r *Run) error {
				j, err := q.EnqueueBulk(ctx, Spec{Kind: "bulk"}, []Spec{{Kind: "child"}})
				started <- j.ID
				return err
			},
		}})
	manual, err := q.Enqueue(ctx, Spec{Kind: "child"})
	if err != nil {
		t.Fatal(err)
	}
	var bulkID int64
	select {
	case bulkID = <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("scheduled job never ran")
	}
	children, err := q.List(ctx, ListOptions{Parent: &bulkID})
	if err != nil || len(children) != 1 || children[0].Trigger != TriggerAutomatic {
		t.Fatalf("children = %+v, %v; want one automatic", children, err)
	}
	top, err := q.List(ctx, ListOptions{Triggers: []string{TriggerManual, TriggerScheduled}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, j := range top {
		got[j.Kind] = j.Trigger
	}
	if len(top) != 2 || got["tick"] != TriggerScheduled || got["child"] != TriggerManual || top[1].ID != manual.ID {
		t.Fatalf("manual and scheduled jobs = %+v; want the tick (scheduled) and the manual child, not the automatic bulk", top)
	}
}
