package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/jobs"
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
		if info.Name == "" && err != nil {
			return fmt.Errorf("%w: %w", jobs.ErrRetry, err)
		}
		// A prune error still leaves the backup it came after, so a retry
		// would only add another.
		if info.Name != "" {
			r.SetProgress(fmt.Appendf(nil, `{"name": %q, "size": %d}`, info.Name, info.Size))
		}
		return err
	}
}

// RestorePayload is the payload of a RestoreKind job.
type RestorePayload struct {
	Path    string `json:"path"`
	Replace bool   `json:"replace"`
}

// RestoreDir is where uploaded archives wait for a restore job.
func RestoreDir(dir string) string { return filepath.Join(dir, "restores") }

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
