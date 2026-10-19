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
	"github.com/jackc/pgx/v5/pgconn"
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
	// ErrFinal marks a handler error that must fail the job even when the
	// worker is stopping, because running the job again could do harm.
	ErrFinal = errors.New("final")
	// ErrLeaseLost means the job is no longer this worker's: its lease
	// expired and the reaper re-queued it.
	ErrLeaseLost = errors.New("job lease lost")
	// ErrFinished means the job already succeeded or failed.
	ErrFinished = errors.New("job already finished")
	// ErrNotRetryable means the job isn't a failed job of its own: it is
	// still open, succeeded, or is a bulk job or one of its children.
	ErrNotRetryable = errors.New("only a failed job outside a bulk job can be retried")
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
	// StartedAt is when the first attempt was claimed.
	StartedAt    *time.Time
	CardsChanged int
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
	run_after, parent_id, error, created_at, finished_at, started_at, cards_changed`

func scanJob(row pgx.Row) (Job, error) {
	var j Job
	err := row.Scan(&j.ID, &j.Kind, &j.InstanceID, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts,
		&j.Progress, &j.Checkpoint, &j.RunAfter, &j.ParentID, &j.Error, &j.CreatedAt, &j.FinishedAt,
		&j.StartedAt, &j.CardsChanged)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func insert(ctx context.Context, db querier, s Spec, parent *int64, status string) (Job, error) {
	payload, err := payloadOf(s)
	if err != nil {
		return Job{}, err
	}
	return scanJob(db.QueryRow(ctx, `INSERT INTO job (kind, instance_id, payload, parent_id, status, finished_at)
		VALUES ($1, $2, $3, $4, $5, CASE WHEN $5 = 'succeeded' THEN now() END) RETURNING `+jobCols,
		s.Kind, s.InstanceID, payload, parent, status))
}

func payloadOf(s Spec) ([]byte, error) {
	if s.Payload == nil {
		return []byte(`{}`), nil
	}
	return json.Marshal(s.Payload)
}

// Enqueue adds a job to run as soon as a worker is free.
func (q *Queue) Enqueue(ctx context.Context, s Spec) (Job, error) {
	return insert(ctx, q.pool, s, nil, StatusQueued)
}

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
			started_at = coalesce(started_at, now()),
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

// touch renews the lease and saves the run's state, and the checkpoint
// unless it is nil.
func (q *Queue) touch(ctx context.Context, db execer, j *Job, worker string, lease time.Duration, st runState, checkpoint json.RawMessage) error {
	// clock_timestamp, not now(): inside a caller's long transaction, now() is when it began.
	tag, err := db.Exec(ctx, `UPDATE job SET locked_until = clock_timestamp() + make_interval(secs => $3),
			progress = coalesce($4::jsonb, progress), checkpoint = coalesce($5::jsonb, checkpoint), cards_changed = $7
		WHERE id = $1 AND locked_by = $2 AND attempts = $6 AND status = 'running'`,
		j.ID, worker, lease.Seconds(), st.progress, checkpoint, j.Attempts, st.cards)
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
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	if j.ParentID != nil && o.status != StatusQueued {
		return q.completeParent(ctx, *j.ParentID)
	}
	return nil
}

// completeParent finishes a waiting parent whose children have all
// finished. It runs after the child's update commits, so when two children
// finish at once, whichever checks second sees both.
func (q *Queue) completeParent(ctx context.Context, id int64) error {
	return q.completeParents(ctx, `parent_id = $1`, id)
}

// completeWaitingParents checks every waiting parent, which catches a worker
// that died between finishing a child and checking its parent.
func (q *Queue) completeWaitingParents(ctx context.Context) error {
	return q.completeParents(ctx, `parent_id IN (SELECT id FROM job WHERE status = 'waiting')`)
}

// completeParents finishes waiting parents of the children that match where.
// Each caller passes a fixed condition, so each gets its own query plan.
func (q *Queue) completeParents(ctx context.Context, where string, args ...any) error {
	_, err := q.pool.Exec(ctx, `UPDATE job p SET
			status = CASE WHEN s.failed > 0 THEN 'failed' ELSE 'succeeded' END,
			error = CASE WHEN s.failed > 0 THEN s.failed || ' of ' || s.total || ' jobs failed' END,
			finished_at = now()
		FROM (SELECT parent_id, count(*) AS total,
				count(*) FILTER (WHERE status = 'failed') AS failed,
				count(*) FILTER (WHERE status NOT IN ('succeeded', 'failed')) AS open
			FROM job WHERE `+where+` GROUP BY parent_id) s
		WHERE p.id = s.parent_id AND p.status = 'waiting' AND s.open = 0`, args...)
	return err
}

// reap re-queues jobs whose lease expired because their worker died, or
// fails them once they are out of attempts.
func (q *Queue) reap(ctx context.Context) error {
	if _, err := q.pool.Exec(ctx, `WITH reaped AS (UPDATE job SET
			status = CASE WHEN attempts >= max_attempts THEN 'failed' ELSE 'queued' END,
			error = 'worker stopped renewing its lease',
			finished_at = CASE WHEN attempts >= max_attempts THEN now() END,
			locked_by = NULL, locked_until = NULL, run_after = now()
		WHERE status = 'running' AND locked_until < now() RETURNING id, status, attempts, max_attempts)
		INSERT INTO job_log (job_id, level, message)
		SELECT id, CASE WHEN status = 'failed' THEN 'error' ELSE 'warn' END,
			format('The worker stopped renewing its lease after attempt %s of %s; %s', attempts, max_attempts,
				CASE WHEN status = 'failed' THEN 'out of attempts' ELSE 'queued to run again' END)
		FROM reaped`); err != nil {
		return err
	}
	return q.completeWaitingParents(ctx)
}

// Cancel fails a job that hasn't finished, along with its unfinished
// children. A running job's worker loses its lease at the next renewal,
// which cancels the handler.
func (q *Queue) Cancel(ctx context.Context, id int64) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	j, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobCols+` FROM job WHERE id = $1 FOR UPDATE`, id))
	if err != nil {
		return err
	}
	if j.Status == StatusSucceeded || j.Status == StatusFailed {
		return ErrFinished
	}
	if _, err := tx.Exec(ctx, `UPDATE job SET status = 'failed', error = 'cancelled', finished_at = now(),
			locked_by = NULL, locked_until = NULL
		WHERE (id = $1 OR parent_id = $1) AND status IN ('queued', 'running', 'waiting')`, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if j.ParentID != nil {
		// The cancel is committed; if this fails, the reaper's waiting-parents sweep finishes the parent.
		_ = q.completeParent(ctx, *j.ParentID)
	}
	return nil
}

// Retry queues a failed job again with all its attempts, from its last
// checkpoint.
func (q *Queue) Retry(ctx context.Context, id int64) error {
	tag, err := q.pool.Exec(ctx, `UPDATE job SET status = 'queued', attempts = 0, error = NULL, finished_at = NULL,
			started_at = NULL, run_after = now(), locked_by = NULL, locked_until = NULL
		WHERE id = $1 AND status = 'failed' AND parent_id IS NULL
			AND NOT EXISTS (SELECT 1 FROM job c WHERE c.parent_id = $1)`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	if _, err := q.Get(ctx, id); err != nil {
		return err
	}
	return ErrNotRetryable
}

// ListOptions filters List. Zero values match everything.
type ListOptions struct {
	Status string
	Kind   string
	// Instance lists only that plugin instance's jobs; 0 lists every one's.
	Instance int64
	// Plugin lists only jobs run by an instance of that plugin.
	Plugin string
	// Parent lists that parent's children. Without it, List returns only
	// top-level jobs.
	Parent *int64
	// Payload lists only jobs whose payload contains it, as jsonb @> does.
	Payload json.RawMessage
	// Before returns only jobs with smaller IDs, for paging.
	Before int64
	// Limit defaults to 50.
	Limit int
}

// List returns jobs, newest first.
func (q *Queue) List(ctx context.Context, o ListOptions) ([]Job, error) {
	if o.Limit <= 0 {
		o.Limit = 50
	}
	rows, err := q.pool.Query(ctx, `SELECT `+jobCols+` FROM job
		WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR kind = $2)
			AND parent_id IS NOT DISTINCT FROM $3::bigint AND ($4::bigint = 0 OR id < $4)
			AND ($6::bigint = 0 OR instance_id = $6) AND ($7::jsonb IS NULL OR payload @> $7::jsonb)
			AND ($8::text = '' OR instance_id IN (SELECT id FROM plugin_instance WHERE plugin = $8))
		ORDER BY id DESC LIMIT $5`, o.Status, o.Kind, o.Parent, o.Before, o.Limit, o.Instance, o.Payload, o.Plugin)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Job, error) { return scanJob(row) })
}

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

// ChildOutcomes counts a parent's finished children by the outcome in their
// progress, for children that report one.
func (q *Queue) ChildOutcomes(ctx context.Context, parent int64) (map[string]int, error) {
	rows, err := q.pool.Query(ctx, `SELECT progress->>'outcome', count(*)::int FROM job
		WHERE parent_id = $1 AND progress->>'outcome' IS NOT NULL GROUP BY 1`, parent)
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	var outcome string
	var n int
	_, err = pgx.ForEachRow(rows, []any{&outcome, &n}, func() error {
		out[outcome] = n
		return nil
	})
	return out, err
}

// Prune deletes top-level jobs that finished more than age ago, with their
// children.
func (q *Queue) Prune(ctx context.Context, age time.Duration) (int64, error) {
	tag, err := q.pool.Exec(ctx, `DELETE FROM job WHERE parent_id IS NULL AND status IN ('succeeded', 'failed')
		AND finished_at < now() - make_interval(secs => $1)`, age.Seconds())
	return tag.RowsAffected(), err
}
