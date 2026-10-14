package targets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Check reads a bound card or lorebook back from its app and records whether the app's
// copy changed since the last push or resolve. If a push or resolve lands while
// it reads, Check records nothing and returns the binding as they left it.
func (s *Service) Check(ctx context.Context, id int64) (Binding, error) {
	b, err := s.stored(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	inst, info, err := s.describe(ctx, b.instanceID)
	if err != nil {
		return Binding{}, err
	}
	state, seen := remoteUnknown, any(nil)
	if info.Pull {
		data, hash, err := s.fetch(ctx, b.k, inst, info, b.remoteID)
		switch {
		case remoteNotFound(err):
			state = remoteMissing
		case err != nil:
			return Binding{}, err
		case hash == b.acceptedHash:
			state = remoteUnchanged
		default:
			state, seen = remoteChanged, data
		}
	}
	if err := s.record(ctx, id, b, state, seen); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// record stores what a check found, unless a push or resolve landed since b
// was read. It guards on version alone: the accepted hash can't tell, because
// a push of identical content leaves it unchanged. The guard must be a column
// of the row, since a subquery would keep the statement's old snapshot when
// the UPDATE waits on a push's row lock.
func (s *Service) record(ctx context.Context, id int64, b stored, state string, seen any) error {
	_, err := s.pool.Exec(ctx, `UPDATE binding SET remote_state = $2, seen_data = $3,
		checked_at = CASE WHEN $2 = 'unknown' THEN NULL ELSE now() END
		WHERE id = $1 AND version = $4`, id, state, seen, b.version)
	// Another binding took the ID while this one was missing, so the app's
	// item there is a different one, and this binding stays missing.
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" && b.remoteState == remoteMissing {
		return nil
	}
	return err
}

// ScanProgress counts what a scan found.
type ScanProgress struct {
	Checked int      `json:"checked"`
	Changed int      `json:"changed"`
	Missing int      `json:"missing"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
	// NewInApp counts, by instance ID, the app items no binding uses.
	NewInApp map[int64]int `json:"new_in_app,omitempty"`
}

// maxErrors is how many check errors a scan's progress keeps.
const maxErrors = 20

// Scan checks every binding on a target that can pull, except on broken
// instances. A binding that fails to check is counted, and the scan goes on.
// It then counts each app's items that no binding uses. report gets the
// progress after each binding and each app.
func (s *Service) Scan(ctx context.Context, report func(ScanProgress)) (ScanProgress, error) {
	var p ScanProgress
	// Includes 'unknown' bindings, so a target that gains pull gets checked.
	rows, err := s.pool.Query(ctx, `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		WHERE i.broken_error IS NULL ORDER BY b.id`)
	if err != nil {
		return p, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return p, err
	}
	for _, id := range ids {
		b, err := s.Check(ctx, id)
		switch {
		case ctx.Err() != nil:
			return p, ctx.Err()
		case errors.Is(err, ErrNotFound):
			continue
		case err != nil:
			p.Failed++
			if len(p.Errors) < maxErrors {
				p.Errors = append(p.Errors, fmt.Sprintf("binding %d: %v", id, err))
			}
		case b.RemoteState == remoteUnknown:
			// The target still can't pull, so nothing was checked.
		default:
			p.Checked++
			switch b.RemoteState {
			case remoteChanged:
				p.Changed++
			case remoteMissing:
				p.Missing++
			}
		}
		report(p)
	}
	apps, err := s.syncApps(ctx, 0)
	if err != nil {
		return p, err
	}
	for _, id := range apps {
		n, err := s.newInApp(ctx, id)
		switch {
		case ctx.Err() != nil:
			return p, ctx.Err()
		case errors.Is(err, ErrCannot):
			// The app can't be read right now, so there's nothing to count.
			continue
		case err != nil:
			p.Failed++
			if len(p.Errors) < maxErrors {
				p.Errors = append(p.Errors, fmt.Sprintf("instance %d: %v", id, err))
			}
			continue
		}
		if p.NewInApp == nil {
			p.NewInApp = map[int64]int{}
		}
		p.NewInApp[id] = n
		report(p)
	}
	return p, nil
}

// newInApp counts the app's characters, and world books on an app that keeps
// them on their own, that no binding on the instance uses.
func (s *Service) newInApp(ctx context.Context, instanceID int64) (int, error) {
	inst, info, err := s.describeReconcile(ctx, cardKind, instanceID)
	if err != nil {
		return 0, err
	}
	kinds := []kind{cardKind}
	if info.Lorebooks == protocol.LorebooksStandalone {
		kinds = append(kinds, lorebookKind)
	}
	n := 0
	for _, k := range kinds {
		var res protocol.TargetListResult
		if err := s.reg.Call(ctx, s.plugins, inst, k.list, nil, &res); err != nil {
			return 0, err
		}
		ids := make([]string, len(res.Items))
		for i, it := range res.Items {
			ids[i] = it.RemoteID
		}
		var bound int
		if err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT remote_id) FROM binding
			WHERE instance_id = $1 AND `+k.FK+` IS NOT NULL AND remote_state <> 'missing' AND remote_id = ANY($2)`,
			instanceID, ids).Scan(&bound); err != nil {
			return 0, err
		}
		n += len(ids) - bound
	}
	return n, nil
}
