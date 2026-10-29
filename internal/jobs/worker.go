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
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Handler runs one job. Return nil when it is done, an error wrapping
// ErrRetry to have it retried later, or any other error to fail it.
type Handler func(ctx context.Context, r *Run) error

// Once runs h for a job that must not run twice, such as one that costs the
// owner money: a job claimed again after its worker died fails without
// running, and every error is final, so running it again is the owner's
// choice.
func Once(h Handler) Handler {
	return func(ctx context.Context, r *Run) error {
		err := errors.New("the run was interrupted; start it again")
		if r.Attempts <= 1 {
			err = h(ctx, r)
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrFinal, err)
		}
		return nil
	}
}

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
	// Metrics, if set, records each job that succeeds or fails.
	Metrics *Metrics

	id   string
	wake chan struct{}
}

// Run is a claimed job as its handler sees it.
type Run struct {
	Job
	q      *Queue
	worker string
	lease  time.Duration
	log    *slog.Logger

	mu       sync.Mutex
	progress json.RawMessage
	cards    int
}

// Log adds a line to the job's log, where the owner reads it on the job's
// page. Level is info, warn, or error. A line that can't be saved goes to
// the worker's log instead, since logging never fails a job.
func (r *Run) Log(level, message string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.q.AppendLog(ctx, r.ID, level, message); err != nil && r.log != nil {
		r.log.Warn("save job log line", "err", err, "message", message)
	}
}

// SetProgress records the job's progress. It is saved with the next lease
// renewal, checkpoint, or finish.
func (r *Run) SetProgress(p json.RawMessage) {
	r.mu.Lock()
	r.progress = p
	r.mu.Unlock()
}

// AddCardsChanged counts n more cards the job changed. Like progress, it is
// saved with the next lease renewal, checkpoint, or finish.
func (r *Run) AddCardsChanged(n int) {
	r.mu.Lock()
	r.cards += n
	r.mu.Unlock()
}

// Cards is how many cards the job has changed, counting earlier attempts.
func (r *Run) Cards() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cards
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

// SaveProgress saves the current progress now, for a job whose watchers
// shouldn't wait for the next lease renewal.
func (r *Run) SaveProgress(ctx context.Context) error {
	return r.q.touch(ctx, r.q.pool, &r.Job, r.worker, r.lease, r.current(), nil)
}

// SaveCheckpoint saves where a retry should resume, along with the current
// progress. The retry finds it in Run.Checkpoint.
func (r *Run) SaveCheckpoint(ctx context.Context, cp json.RawMessage) error {
	return r.q.touch(ctx, r.q.pool, &r.Job, r.worker, r.lease, r.current(), cp)
}

// SaveCheckpointTx is SaveCheckpoint inside the caller's transaction, so the
// checkpoint commits with the work done before it.
func (r *Run) SaveCheckpointTx(ctx context.Context, tx pgx.Tx, cp json.RawMessage) error {
	return r.q.touch(ctx, tx, &r.Job, r.worker, r.lease, r.current(), cp)
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
	run := &Run{Job: *j, q: w.Queue, worker: w.id, lease: w.Lease, log: log, progress: j.Progress, cards: j.CardsChanged}
	run.Log("info", fmt.Sprintf("Attempt %d of %d started", j.Attempts, j.MaxAttempts))
	// Jobs the handler enqueues are automatic.
	hctx, cancel := context.WithCancelCause(context.WithValue(ctx, inJobKey{}, true))
	defer cancel(nil)
	stop := make(chan struct{})
	var beat sync.WaitGroup
	beat.Go(func() { w.heartbeat(hctx, cancel, run, stop) })
	err := safeCall(hctx, w.Handlers[j.Kind], run)
	close(stop)
	beat.Wait()

	if errors.Is(err, ErrLeaseLost) || errors.Is(context.Cause(hctx), ErrLeaseLost) {
		log.Warn("job lease lost: cancelled or taken by another worker", "err", err)
		run.Log("warn", "This worker lost the job: it was cancelled, or its lease ran out and another worker took it")
		return
	}
	o := outcome{status: StatusSucceeded}
	switch {
	case err == nil:
	case errors.Is(err, ErrFinal):
		// The marker is for the worker; the owner reads the reason alone.
		o = outcome{status: StatusFailed, err: strings.TrimPrefix(err.Error(), ErrFinal.Error()+": ")}
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
	st := run.current()
	if err := w.Queue.finish(fctx, j, w.id, st, o); err != nil {
		if errors.Is(err, ErrLeaseLost) {
			log.Warn("job lease lost: cancelled or taken by another worker", "err", err)
			return
		}
		log.Error("finish job", "err", err)
		return
	}
	if w.Metrics != nil && o.status != StatusQueued {
		w.Metrics.observe(j, o.status, st.cards)
	}
	log.Info("job ended", "status", o.status, "err", o.err)
	run.Log(o.logLevel(), o.String())
}

// logLevel is the job log level for how a run ended.
func (o outcome) logLevel() string {
	switch {
	case o.status == StatusFailed:
		return "error"
	case o.err != "":
		return "warn"
	}
	return "info"
}

func (o outcome) String() string {
	switch {
	case o.status == StatusQueued && o.err != "":
		return "Stopped, to run again: " + o.err
	case o.err != "":
		return "Failed: " + o.err
	}
	return "Succeeded"
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
		err := r.q.touch(ctx, r.q.pool, &r.Job, r.worker, r.lease, r.current(), nil)
		if errors.Is(err, ErrLeaseLost) {
			cancel(ErrLeaseLost)
			return
		}
		if err != nil && ctx.Err() == nil {
			w.Log.Warn("renew job lease", "job", r.ID, "err", err)
			r.Log("warn", "Renewing the lease failed: "+err.Error())
		}
	}
}
