package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
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
// returns the entity's bindings, or the first error of a follow or check
// that failed, since the owner asked and is waiting on the answer.
func (s *Service) CheckEntitySources(ctx context.Context, lk *library.Kind, id int64) ([]Binding, error) {
	p, err := s.checkSources(ctx, lk, id, func(ScanProgress) {})
	if err != nil {
		return nil, err
	}
	if p.firstErr != nil {
		return nil, p.firstErr
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
	var wanted []string
	if only != nil {
		if wanted, err = s.sourceKinds(ctx, only, id); err != nil {
			return p, err
		}
	}
	for inst, kinds := range followers {
		// One card's check must not fail on a plugin that follows none of its sources.
		if only != nil && !slices.ContainsFunc(kinds, func(kind string) bool { return slices.Contains(wanted, kind) }) {
			continue
		}
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
	return p, s.checkEach(ctx, ids, &p, report, func(b *Binding) error {
		if b.RemoteState != remoteChanged || !b.Autosync {
			return nil
		}
		applied, err := s.applyUpdate(ctx, *b)
		if applied {
			p.Applied++
			b.RemoteState = remoteUnchanged
		}
		return err
	})
}

// sourceKinds lists the kinds of the entity's sources, removed ones too: unbinding one needs its plugin.
func (s *Service) sourceKinds(ctx context.Context, lk *library.Kind, id int64) ([]string, error) {
	k := targetKind(lk)
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT kind FROM `+k.SourceTable+` WHERE `+k.FK+` = $1`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// applyUpdate takes a followed source's change into the library, as a review
// keeping the library side would, when no hunk conflicts and the site's
// avatar is the one accepted. The card keeps its own avatar. It says whether
// it applied.
func (s *Service) applyUpdate(ctx context.Context, b Binding) (bool, error) {
	lk, entity := bindingEntity(b)
	k := targetKind(lk)
	c, it, err := s.compareRead(ctx, k, b.InstanceID, b.RemoteID, entity, false)
	if err != nil {
		return false, err
	}
	st, err := s.stored(ctx, b.ID)
	if err != nil {
		return false, err
	}
	site, err := s.avatarKey(ctx, it.avatar)
	if err != nil {
		return false, err
	}
	// Only the site avatar's hash is kept, so a new one has no ancestor to merge against.
	if (st.avatarHash != nil && *st.avatarHash != site) || hasConflict(c) {
		return false, nil
	}
	// ponytail: resolve's stale check covers data only, so a site avatar changed after compareRead is accepted unreviewed
	// by autosync and dismiss alike; add the avatar key to the stale check (all resolve callers) if that matters.
	_, err = s.resolve(ctx, k, Resolution{InstanceID: b.InstanceID, RemoteID: b.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: entity,
		Default: merge.ChooseLibrary}, resolveOpts{skipLeftOut: true})
	return err == nil, err
}

// hasConflict says whether any hunk of c needs the owner.
func hasConflict(c Comparison) bool {
	for _, f := range c.Fields {
		for _, h := range f.Hunks {
			if h.Status == merge.Conflict {
				return true
			}
		}
	}
	return false
}

// Dismiss accepts the app's copy, leaving the library as it is. appHash is
// the copy the owner saw; if the app's differs now, it returns a StaleError.
// For a followed source, this is "ignore this change": the next change on
// the site shows again.
func (s *Service) Dismiss(ctx context.Context, id int64, appHash string) (Binding, error) {
	b, err := s.Binding(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	lk, entity := bindingEntity(b)
	k := targetKind(lk)
	// Two-way, so the author's edits to fields the owner never touched are refused too.
	c, err := s.compareItem(ctx, k, b.InstanceID, b.RemoteID, entity, true)
	if err != nil {
		return Binding{}, err
	}
	if _, err := s.resolve(ctx, k, Resolution{InstanceID: b.InstanceID, RemoteID: b.RemoteID, AppHash: appHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: entity, Base: "none",
		Default: merge.ChooseLibrary}, resolveOpts{bindOnly: true, skipLeftOut: true}); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// bindingEntity is the kind and ID of what b binds.
func bindingEntity(b Binding) (*library.Kind, int64) {
	if b.LorebookID != 0 {
		return library.Lorebook, b.LorebookID
	}
	return library.Card, b.CardID
}
