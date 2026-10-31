// Package maintenance previews and runs the owner's database cleanups:
// trimming enrichment history, removing stale vectors, clearing recorded
// failures, vacuuming, and purging archived cards.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
)

// Maintenance runs cleanups on one database.
type Maintenance struct {
	pool *pgxpool.Pool
	lib  *library.Library
}

func New(pool *pgxpool.Pool, lib *library.Library) *Maintenance {
	return &Maintenance{pool: pool, lib: lib}
}

// Table is one table's size.
type Table struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Preview is what each action would remove, and the database's size.
type Preview struct {
	DatabaseBytes int64   `json:"database_bytes"`
	Largest       []Table `json:"largest" doc:"The five largest tables."`
	History       struct {
		Rows      int64 `json:"rows"`
		Trimmable int64 `json:"trimmable" doc:"Rows trim would delete for keep."`
	} `json:"history"`
	Vectors struct {
		Available bool  `json:"available"`
		Rows      int64 `json:"rows"`
	} `json:"vectors"`
	Failures int64 `json:"failures"`
	Purge    struct {
		Cards     int64 `json:"cards"`
		Lorebooks int64 `json:"lorebooks"`
	} `json:"purge"`
	LastBackup *time.Time `json:"last_backup,omitempty" doc:"When the last backup job succeeded, if any has."`
}

// purgeBatch is how many cards one purge transaction deletes.
const purgeBatch = 1000

// trimmable numbers each value newest first within its instance, card and
// key; those numbered past keep are the ones trim deletes.
const trimmable = `SELECT id FROM (SELECT id, row_number() OVER (PARTITION BY instance_id, card_id, key
	ORDER BY created_at DESC, id DESC) AS n FROM enrichment) x WHERE n > $1`

// purgeCards are cards archived more than $1 days ago; purgeLorebooks are
// such lorebooks that no live card links to.
const (
	purgeCards     = `SELECT id FROM card WHERE archived_at < now() - make_interval(days => $1) ORDER BY id`
	purgeLorebooks = `SELECT l.id FROM lorebook l WHERE l.archived_at < now() - make_interval(days => $1)
		AND NOT EXISTS (SELECT 1 FROM card_lorebook cl JOIN card c ON c.id = cl.card_id
			WHERE cl.lorebook_id = l.id AND c.archived_at IS NULL) ORDER BY l.id`
)

// Preview counts what each action would do for keep and days.
func (m *Maintenance) Preview(ctx context.Context, keep, days int) (Preview, error) {
	var p Preview
	err := m.pool.QueryRow(ctx, `SELECT pg_database_size(current_database()),
			(SELECT count(*) FROM enrichment), (SELECT count(*) FROM (`+trimmable+`) t),
			(SELECT count(*) FROM enrich_failure),
			(SELECT max(finished_at) FROM job WHERE kind LIKE 'backup.%' AND status = 'succeeded')`, keep).
		Scan(&p.DatabaseBytes, &p.History.Rows, &p.History.Trimmable, &p.Failures, &p.LastBackup)
	if err != nil {
		return p, fmt.Errorf("preview maintenance: %w", err)
	}
	// A query of its own, since the purge queries' $1 is days, not keep.
	if err := m.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM (`+purgeCards+`) c), (SELECT count(*) FROM (`+purgeLorebooks+`) l)`, days).
		Scan(&p.Purge.Cards, &p.Purge.Lorebooks); err != nil {
		return p, fmt.Errorf("preview purge: %w", err)
	}
	rows, err := m.pool.Query(ctx, `SELECT relname, pg_total_relation_size(oid) FROM pg_class
		WHERE relkind = 'r' AND relnamespace = 'public'::regnamespace ORDER BY 2 DESC LIMIT 5`)
	if err != nil {
		return p, err
	}
	if p.Largest, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Table]); err != nil {
		return p, err
	}
	if p.Vectors.Available, err = m.lib.HasVectors(ctx); err != nil || !p.Vectors.Available {
		return p, err
	}
	err = m.pool.QueryRow(ctx, `SELECT count(*) FROM card_embedding`).Scan(&p.Vectors.Rows)
	return p, err
}

// TrimHistory keeps the newest keep values per instance, card and key, and
// returns how many it deleted.
func (m *Maintenance) TrimHistory(ctx context.Context, keep int) (int64, error) {
	tag, err := m.pool.Exec(ctx, `DELETE FROM enrichment WHERE id IN (`+trimmable+`)`, keep)
	if err != nil {
		return 0, fmt.Errorf("trim enrichment history: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RemoveStaleVectors deletes vectors not from instance or not in space. Without
// pgvector there are none, and card_embedding doesn't exist.
func (m *Maintenance) RemoveStaleVectors(ctx context.Context, instance int64, space string) (int64, error) {
	if ok, err := m.lib.HasVectors(ctx); err != nil || !ok {
		return 0, err
	}
	tag, err := m.pool.Exec(ctx, `DELETE FROM card_embedding WHERE instance_id <> $1 OR space <> $2`, instance, space)
	if err != nil {
		return 0, fmt.Errorf("remove stale vectors: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClearFailures forgets every recorded enrich failure, so the sweep tries
// those cards again.
func (m *Maintenance) ClearFailures(ctx context.Context) (int64, error) {
	tag, err := m.pool.Exec(ctx, `DELETE FROM enrich_failure`)
	if err != nil {
		return 0, fmt.Errorf("clear enrich failures: %w", err)
	}
	return tag.RowsAffected(), nil
}

// Vacuum runs VACUUM (ANALYZE) and returns the database size after.
// VACUUM can't run in a transaction, and Exec without arguments sends it alone.
func (m *Maintenance) Vacuum(ctx context.Context) (int64, error) {
	if _, err := m.pool.Exec(ctx, `VACUUM (ANALYZE)`); err != nil {
		return 0, fmt.Errorf("vacuum (the database user must own Innkeeper's tables): %w", err)
	}
	var size int64
	err := m.pool.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&size)
	return size, err
}

// Purge deletes cards archived more than days ago, then the archived
// lorebooks no live card links to, calling done with each batch's count.
func (m *Maintenance) Purge(ctx context.Context, days int, done func(int)) (int, error) {
	rows, err := m.pool.Query(ctx, purgeCards, days)
	if err != nil {
		return 0, err
	}
	cards, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return 0, err
	}
	total := 0
	for i := 0; i < len(cards); i += purgeBatch {
		n, err := m.lib.DeleteCards(ctx, cards[i:min(i+purgeBatch, len(cards))])
		if err != nil {
			return total, err
		}
		total += n
		done(n)
	}
	// After the cards, so a lorebook only they linked to qualifies too.
	if rows, err = m.pool.Query(ctx, purgeLorebooks, days); err != nil {
		return total, err
	}
	lorebooks, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(lorebooks) == 0 {
		return total, err
	}
	return total, m.lib.DeleteLorebooks(ctx, lorebooks)
}

// Kind is the maintenance job kind. Its payload names the action.
const Kind = "maintenance"

// Actions are the cleanups a Payload can name.
var Actions = []string{"trim-history", "stale-vectors", "clear-failures", "vacuum", "purge"}

// Payload is a maintenance job's input.
type Payload struct {
	Action string `json:"action"`
	Keep   int    `json:"keep,omitempty"`
	Days   int    `json:"days,omitempty"`
}

// Progress is a maintenance job's progress: rows or cards removed, and the
// database size after a vacuum.
type Progress struct {
	Removed       int64 `json:"removed"`
	DatabaseBytes int64 `json:"database_bytes,omitempty"`
}

// VectorSpace returns the vector instance and the space of its current
// vectors, or 0 when there is none.
type VectorSpace func(ctx context.Context) (int64, string, error)

// Runner is the jobs.Handler for Kind.
type Runner struct {
	M     *Maintenance
	Space VectorSpace
}

func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error {
	var p Payload
	if err := json.Unmarshal(r.Payload, &p); err != nil {
		return fmt.Errorf("read maintenance payload: %w", err)
	}
	var prog Progress
	publish := func() { raw, _ := json.Marshal(prog); r.SetProgress(raw) }
	var err error
	switch p.Action {
	case "trim-history":
		prog.Removed, err = rn.M.TrimHistory(ctx, p.Keep)
	case "stale-vectors":
		inst, space, serr := rn.Space(ctx)
		if serr != nil {
			return serr
		}
		prog.Removed, err = rn.M.RemoveStaleVectors(ctx, inst, space)
	case "clear-failures":
		prog.Removed, err = rn.M.ClearFailures(ctx)
	case "vacuum":
		prog.DatabaseBytes, err = rn.M.Vacuum(ctx)
	case "purge":
		_, err = rn.M.Purge(ctx, p.Days, func(n int) {
			prog.Removed += int64(n)
			r.AddCardsChanged(n)
			publish()
		})
	default:
		return fmt.Errorf("unknown maintenance action %q", p.Action)
	}
	publish()
	return err
}
