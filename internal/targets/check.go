package targets

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/db"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Check reads a bound card or lorebook back from its app and records whether the app's
// copy changed since the last push or resolve. If a push or resolve lands while
// it reads, Check records nothing and returns the binding as they left it.
// A paused binding isn't read.
func (s *Service) Check(ctx context.Context, id int64) (Binding, error) {
	b, err := s.stored(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	if b.paused {
		return s.Binding(ctx, id)
	}
	inst, info, err := s.describe(ctx, b.instanceID)
	if err != nil {
		return Binding{}, err
	}
	state, seen, avatar := remoteUnknown, any(nil), ""
	if info.Pull {
		var data json.RawMessage
		var hash string
		if b.push {
			data, hash, err = s.fetch(ctx, b.k, inst, info, b.remoteID)
		} else {
			data, hash, avatar, err = s.fetchFollowed(ctx, b.k, inst, info, b.remoteID)
		}
		switch {
		case remoteNotFound(err):
			state = remoteMissing
		case err != nil:
			if !b.push && ctx.Err() == nil {
				err = errors.Join(err, s.recordFailure(ctx, id, err))
			}
			return Binding{}, err
		// A followed source's first check accepts its avatar silently.
		case hash == b.acceptedHash && (b.push || b.avatarHash == nil || *b.avatarHash == avatar):
			state = remoteUnchanged
		default:
			state, seen = remoteChanged, data
		}
	}
	// "" marks a followed source checked without an avatar, so one added later is an update.
	var accept *string
	if !b.push && (state == remoteUnchanged || state == remoteChanged) {
		accept = &avatar
	}
	if err := s.record(ctx, id, b, state, seen, accept); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// fetchFollowed is fetch for a followed source. It also stores the site's
// avatar as a blob and returns its avatarKey, since a new avatar is an update too.
//
// ponytail: stores the avatar on every check; unreferenced blobs are
// collected after a day. Ask the plugin for a hash first if that grows.
func (s *Service) fetchFollowed(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	remoteID string) (json.RawMessage, string, string, error) {
	if k.isLorebook() {
		data, hash, err := s.fetch(ctx, k, inst, info, remoteID)
		return data, hash, "", err
	}
	got, err := s.getCard(ctx, inst, protocol.TargetGetParams{RemoteID: remoteID, Avatar: true})
	if err != nil {
		return nil, "", "", err
	}
	raw, err := cardData(got.Card)
	if err != nil {
		return nil, "", "", err
	}
	data, hash, err := Project(raw, k.fields(info))
	if err != nil || got.Avatar == "" {
		return data, hash, "", err
	}
	blob, err := s.lib.IngestBlob(ctx, got.Avatar)
	if err != nil {
		return nil, "", "", err
	}
	avatar, err := s.avatarKey(ctx, blob.SHA256)
	return data, hash, avatar, err
}

// avatarKey is what a followed source's avatar is compared by: its pixels, so
// a re-encode or new metadata isn't a change, else its bytes when it isn't an
// image that decodes. "" for no avatar.
func (s *Service) avatarKey(ctx context.Context, sha string) (string, error) {
	if sha == "" {
		return "", nil
	}
	pixels, err := s.lib.PixelHash(ctx, sha)
	return cmp.Or(pixels, sha), err
}

// record stores what a check found, unless a push or resolve landed since b
// was read. It guards on version alone: the accepted hash can't tell, because
// a push of identical content leaves it unchanged. The guard must be a column
// of the row, since a subquery would keep the statement's old snapshot when
// the UPDATE waits on a push's row lock. avatar fills only a NULL avatar_hash.
func (s *Service) record(ctx context.Context, id int64, b stored, state string, seen any, avatar *string) error {
	_, err := s.pool.Exec(ctx, `UPDATE binding SET remote_state = $2, seen_data = $3,
		checked_at = CASE WHEN $2 = 'unknown' THEN NULL ELSE now() END,
		avatar_hash = coalesce(avatar_hash, $5), check_failures = 0, check_error = NULL
		WHERE id = $1 AND version = $4`, id, state, seen, b.version, avatar)
	// Another binding took the ID while this one was missing, so the app's
	// item there is a different one, and this binding stays missing.
	if db.IsCode(err, db.UniqueViolation) && b.remoteState == remoteMissing {
		return nil
	}
	return err
}

// recordFailure counts a failed check of a followed source.
func (s *Service) recordFailure(ctx context.Context, id int64, cause error) error {
	_, err := s.pool.Exec(ctx, `UPDATE binding SET check_failures = check_failures + 1, check_error = $2 WHERE id = $1`,
		id, cause.Error())
	return err
}

// ScanProgress counts what a scan found.
type ScanProgress struct {
	Checked int `json:"checked"`
	Changed int `json:"changed"`
	Missing int `json:"missing"`
	// Applied counts followed sources whose update autosync took.
	Applied int      `json:"applied,omitempty"`
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
	// Followed sources are CheckSources' job, on their own schedule.
	rows, err := s.pool.Query(ctx, `SELECT b.id FROM binding b JOIN plugin_instance i ON i.id = b.instance_id
		WHERE i.broken_error IS NULL AND b.push ORDER BY b.id`)
	if err != nil {
		return p, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return p, err
	}
	if err := s.checkEach(ctx, ids, &p, report, nil); err != nil {
		return p, err
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
			p.fail(fmt.Sprintf("instance %d", id), err)
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

// checkEach checks each binding and counts what it found in p. A binding that
// fails to check is counted, and the rest go on. then, if set, runs after
// each check that worked, and its error counts as the binding's. then may set
// b.RemoteState to remoteUnchanged for a change it took, so it isn't counted as waiting.
func (s *Service) checkEach(ctx context.Context, ids []int64, p *ScanProgress, report func(ScanProgress),
	then func(*Binding) error) error {
	for _, id := range ids {
		b, err := s.Check(ctx, id)
		if err == nil && then != nil {
			err = then(&b)
		}
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.Is(err, ErrNotFound):
			continue
		case err != nil:
			p.fail(fmt.Sprintf("binding %d", id), err)
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
		report(*p)
	}
	return nil
}

// fail counts a failure and keeps its error, up to maxErrors.
func (p *ScanProgress) fail(what string, err error) {
	p.Failed++
	if len(p.Errors) < maxErrors {
		p.Errors = append(p.Errors, fmt.Sprintf("%s: %v", what, err))
	}
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
		listed, err := s.listItems(ctx, k, inst)
		if err != nil {
			return 0, err
		}
		ids := make([]string, len(listed))
		for i, it := range listed {
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
