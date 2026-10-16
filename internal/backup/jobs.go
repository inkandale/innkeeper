package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/internal/jobs"
)

// RestoreKind is the job that restores an uploaded archive.
const RestoreKind = "backup.restore"

// Kind is the job kind that backs up type t.
func Kind(t Type) string { return "backup." + string(t) }

var errNoDir = errors.New("INNKEEPER_BACKUP_DIR isn't set on the worker")

// BackupHandler writes a backup of type t into s.Dir and prunes to s.Keep[t].
func BackupHandler(s Settings, pool *pgxpool.Pool, o Options, t Type) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) error {
		if s.Dir == "" {
			return fmt.Errorf("%w: %w", jobs.ErrFinal, errNoDir)
		}
		o.Dir, o.Keep = s.Dir, s.Keep[t]
		info, err := Write(ctx, pool, t, o)
		if info.Name == "" {
			return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
		}
		r.SetProgress(fmt.Appendf(nil, `{"name": %q, "size": %d}`, info.Name, info.Size))
		// The backup is written, so failing or retrying would hide or
		// duplicate it. The next run prunes again.
		if err != nil {
			orDefault(o.Log).Warn("backup written but not pruned", "name", info.Name, "err", err)
		}
		return nil
	}
}

// RestorePayload is the payload of a RestoreKind job.
type RestorePayload struct {
	Path    string `json:"path"`
	Replace bool   `json:"replace"`
}

// RestoreDir is where uploaded archives wait for a restore job.
func RestoreDir(dir string) string { return filepath.Join(dir, "restores") }

// SweepUploads removes files in dir's restore folder untouched for age that
// no queued or running restore job names: uploads whose job was cancelled,
// or whose API stopped before queueing it. It returns how many it removed.
func SweepUploads(ctx context.Context, pool *pgxpool.Pool, dir string, age time.Duration) (int, error) {
	// Listed before the jobs are read, so an upload queued in between is kept.
	es, err := os.ReadDir(RestoreDir(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	rows, _ := pool.Query(ctx, `SELECT coalesce(payload->>'path', '') FROM job
		WHERE kind = $1 AND status IN ('queued', 'running')`, RestoreKind)
	named, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range es {
		path := filepath.Join(RestoreDir(dir), e.Name())
		fi, err := e.Info()
		// age also keeps an upload the API saved but hasn't queued yet.
		if err != nil || !fi.Mode().IsRegular() || time.Since(fi.ModTime()) < age || slices.Contains(named, path) {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return n, err
		}
		n++
	}
	return n, nil
}

// RestoreHandler restores the archive named by the job's payload, then calls
// afterConfig (if not nil) once a config restore succeeds.
func RestoreHandler(s Settings, o RestoreOptions, afterConfig func(context.Context) error) jobs.Handler {
	return func(ctx context.Context, r *jobs.Run) (err error) {
		if s.Dir == "" {
			return fmt.Errorf("%w: %w", jobs.ErrFinal, errNoDir)
		}
		var p RestorePayload
		if err := json.Unmarshal(r.Payload, &p); err != nil {
			return fmt.Errorf("%w: payload: %w", jobs.ErrFinal, err)
		}
		// Payloads are database rows, so they can't be trusted to name only uploads.
		if filepath.Clean(p.Path) != p.Path || filepath.Dir(p.Path) != RestoreDir(s.Dir) {
			return fmt.Errorf("%w: %q is not a file in %s", jobs.ErrFinal, p.Path, RestoreDir(s.Dir))
		}
		if fi, err := os.Lstat(p.Path); err != nil {
			return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
		} else if !fi.Mode().IsRegular() {
			return fmt.Errorf("%w: %q is not a regular file", jobs.ErrFinal, p.Path)
		}
		// A cancelled ctx means the worker is stopping and the job will run again.
		defer func() {
			if ctx.Err() == nil {
				os.Remove(p.Path)
			}
		}()
		f, err := os.Open(p.Path)
		if err != nil {
			return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
		}
		defer f.Close()
		o.Replace = p.Replace
		res, err := Restore(ctx, f, o)
		if res.Type != "" {
			if b, merr := json.Marshal(res); merr == nil {
				r.SetProgress(b)
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return err // ErrFinal would stop the worker re-queueing the job
			}
			return fmt.Errorf("%w: %w", jobs.ErrFinal, err)
		}
		if res.Type == Config && afterConfig != nil {
			if err := afterConfig(ctx); err != nil {
				return fmt.Errorf("%w: after restore: %w", jobs.ErrFinal, err)
			}
		}
		return nil
	}
}
