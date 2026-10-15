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

// Token names this attempt of the job, for plugin calls about it. It
// includes the attempt, so calls from an abandoned earlier attempt can't
// reach this one.
func (r *Run) Token() string { return fmt.Sprintf("%d.%d", r.ID, r.Attempts) }

// SaveCheckpoint saves where a retry should resume, along with the current
// progress. The retry finds it in Run.Checkpoint.
func (r *Run) SaveCheckpoint(ctx context.Context, cp json.RawMessage) error {
	return r.q.touch(ctx, &r.Job, r.worker, r.lease, r.currentProgress(), cp)
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
	if ctx.Err() != nil {
		// Stopped during startup: a clean shutdown, same as stopping later.
		if conn != nil {
			conn.Close(context.Background())
		}
		return nil
	}
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
		log.Warn("job lease lost: cancelled or taken by another worker", "err", err)
		return
	}
	o := outcome{status: StatusSucceeded}
	switch {
	case err == nil:
	case errors.Is(err, ErrFinal):
		o = outcome{status: StatusFailed, err: err.Error()}
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
		if errors.Is(err, ErrLeaseLost) {
			log.Warn("job lease lost: cancelled or taken by another worker", "err", err)
			return
		}
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
		err := r.q.touch(ctx, &r.Job, r.worker, r.lease, r.currentProgress(), nil)
		if errors.Is(err, ErrLeaseLost) {
			cancel(ErrLeaseLost)
			return
		}
		if err != nil && ctx.Err() == nil {
			w.Log.Warn("renew job lease", "job", r.ID, "err", err)
		}
	}
}
