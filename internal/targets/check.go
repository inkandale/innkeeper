package targets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Check reads a bound card back from its app and records whether the app's
// copy changed since the last push or adopt. If a push or adopt lands while
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
		data, hash, err := s.fetch(ctx, inst, info, b.remoteID)
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

// record stores what a check found, unless a push landed since b was read.
// The accepted hash alone isn't enough to tell: a push of identical content
// leaves it unchanged.
func (s *Service) record(ctx context.Context, id int64, b stored, state string, seen any) error {
	_, err := s.pool.Exec(ctx, `UPDATE binding SET remote_state = $3, seen_data = $4,
		checked_at = CASE WHEN $3 = 'unknown' THEN NULL ELSE now() END
		WHERE id = $1 AND accepted_hash = $2
		AND (SELECT max(id) FROM push WHERE binding_id = $1) = $5`, id, b.acceptedHash, state, seen, b.lastPush)
	return err
}

// ScanProgress counts what a scan found.
type ScanProgress struct {
	Checked int      `json:"checked"`
	Changed int      `json:"changed"`
	Missing int      `json:"missing"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

// maxErrors is how many check errors a scan's progress keeps.
const maxErrors = 20

// Scan checks every binding on a target that can pull, except on broken
// instances. A binding that fails to check is counted, and the scan goes on.
// report gets the progress after each binding.
func (s *Service) Scan(ctx context.Context, report func(ScanProgress)) (ScanProgress, error) {
	var p ScanProgress
	rows, err := s.pool.Query(ctx, `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		WHERE b.remote_state <> 'unknown' AND i.broken_error IS NULL ORDER BY b.id`)
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
	return p, nil
}
