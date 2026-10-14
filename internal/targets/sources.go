package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/library"
)

// followers maps each pull-only instance to the source kinds it follows. A
// kind two plugins claim goes to the oldest working instance.
func (s *Service) followers(ctx context.Context) (map[int64][]string, error) {
	insts, err := s.reg.InstancesWith(ctx, "pull")
	if err != nil {
		return nil, err
	}
	manifests := s.reg.Manifests()
	claimed := map[string]bool{}
	out := map[int64][]string{}
	for _, in := range insts {
		m := manifests[in.Plugin]
		if in.BrokenError != nil || m == nil || m.Has("push") {
			continue
		}
		for _, kind := range m.SourceKinds {
			if !claimed[kind] {
				claimed[kind] = true
				out[in.ID] = append(out[in.ID], kind)
			}
		}
	}
	return out, nil
}

// follow binds each live source of kinds that has no binding on the
// instance yet, and unbinds the instance's followed bindings whose source the
// owner removed. only limits it to one entity; 0 means all of them. The
// accepted copy is the entity's first revision a plugin wrote, else its
// first, so an update published since the import shows on the first check.
//
// ponytail: an entity with two sources of one followed kind follows the
// older; a binding per source needs a key wider than (instance, entity).
func (s *Service) follow(ctx context.Context, k kind, instanceID int64, kinds []string, only int64) error {
	_, info, err := s.describePull(ctx, k, instanceID)
	if k.isLorebook() && errors.Is(err, ErrCannot) {
		// The target keeps lorebooks inside cards, so it can't follow one.
		return nil
	}
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM binding b WHERE b.instance_id = $1 AND NOT b.push AND b.`+k.FK+` IS NOT NULL
			AND ($2::bigint = 0 OR b.`+k.FK+` = $2) AND NOT EXISTS (SELECT 1 FROM `+k.SourceTable+` src
				WHERE src.`+k.FK+` = b.`+k.FK+` AND src.removed_at IS NULL AND src.kind || ':' || src.ref = b.remote_id)`,
			instanceID, only); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT DISTINCT ON (src.`+k.FK+`) src.`+k.FK+`, src.kind || ':' || src.ref, e.head_revision_id,
				(SELECT r.data FROM `+k.RevTable+` r WHERE r.`+k.FK+` = src.`+k.FK+`
					ORDER BY r.source LIKE 'plugin:%' DESC, r.id LIMIT 1)
			FROM `+k.SourceTable+` src JOIN `+k.Table+` e ON e.id = src.`+k.FK+`
			WHERE src.removed_at IS NULL AND src.kind = ANY($2) AND e.archived_at IS NULL AND ($3::bigint = 0 OR src.`+k.FK+` = $3)
				AND NOT EXISTS (SELECT 1 FROM binding b WHERE b.instance_id = $1 AND b.`+k.FK+` = src.`+k.FK+`)
			ORDER BY src.`+k.FK+`, src.id`, instanceID, kinds, only)
		if err != nil {
			return err
		}
		type want struct {
			id, head int64
			remoteID string
			data     json.RawMessage
		}
		wants, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (want, error) {
			var w want
			err := row.Scan(&w.id, &w.remoteID, &w.head, &w.data)
			return w, err
		})
		if err != nil {
			return err
		}
		for _, w := range wants {
			accepted, hash, err := Project(w.data, k.fields(info))
			if err != nil {
				return err
			}
			var bindingID int64
			err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.FK+`, remote_id, remote_state, accepted_data,
					accepted_hash, push)
				VALUES ($1, $2, $3, 'unknown', $4, $5, false) ON CONFLICT DO NOTHING RETURNING id`,
				instanceID, w.id, w.remoteID, accepted, hash).Scan(&bindingID)
			if errors.Is(err, pgx.ErrNoRows) {
				// Another check bound it first.
				continue
			}
			if err != nil {
				return err
			}
			// binding_status needs a push row; a pull-only binding is never ahead of it.
			if _, err := tx.Exec(ctx, `INSERT INTO push (binding_id, kind, revision_id, pushed_hash, remote_id)
				VALUES ($1, 'bind', $2, $3, $4)`, bindingID, w.head, hash, w.remoteID); err != nil {
				return err
			}
		}
		return nil
	})
}

// CheckSources binds the sources that pull-only plugins follow, then checks
// each followed binding that isn't paused. report gets the progress after each binding.
func (s *Service) CheckSources(ctx context.Context, report func(ScanProgress)) (ScanProgress, error) {
	return s.checkSources(ctx, nil, 0, report)
}

// CheckEntitySources is CheckSources for one card or lorebook, now. It
// returns the entity's bindings.
func (s *Service) CheckEntitySources(ctx context.Context, lk *library.Kind, id int64) ([]Binding, error) {
	if _, err := s.checkSources(ctx, lk, id, func(ScanProgress) {}); err != nil {
		return nil, err
	}
	return s.Bindings(ctx, lk, id)
}

// checkSources follows and checks every entity's sources, or with only, one entity's.
func (s *Service) checkSources(ctx context.Context, only *library.Kind, id int64, report func(ScanProgress)) (ScanProgress, error) {
	var p ScanProgress
	followers, err := s.followers(ctx)
	if err != nil {
		return p, err
	}
	for inst, kinds := range followers {
		for _, lk := range library.Kinds {
			if only != nil && lk != only {
				continue
			}
			if err := s.follow(ctx, targetKind(lk), inst, kinds, id); err != nil {
				if ctx.Err() != nil {
					return p, ctx.Err()
				}
				p.fail(fmt.Sprintf("instance %d", inst), err)
			}
		}
	}
	q := `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		LEFT JOIN card c ON c.id = b.card_id LEFT JOIN lorebook l ON l.id = b.lorebook_id
		WHERE NOT b.push AND NOT b.paused AND i.broken_error IS NULL AND coalesce(c.archived_at, l.archived_at) IS NULL`
	var args []any
	if only != nil {
		q += ` AND b.` + only.FK + ` = $1`
		args = append(args, id)
	}
	rows, err := s.pool.Query(ctx, q+` ORDER BY b.id`, args...)
	if err != nil {
		return p, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return p, err
	}
	return p, s.checkEach(ctx, ids, &p, report, nil)
}
