package targets

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

// Push sends a card's or lorebook's head revision to a target and records
// the push. One pushed there before is updated in place, which overwrites
// changes made in the app. A target that can't update gets another copy, but
// only when newCopy is set. If the app lost its copy, the push makes a new
// one. A lorebook needs a target that keeps lorebooks on their own.
func (s *Service) Push(ctx context.Context, k *library.Kind, instanceID, id int64, newCopy bool) (Binding, error) {
	return s.push(ctx, targetKind(k), instanceID, id, newCopy, rand.Text(), nil)
}

// pushExpect is what a queued push job saw when it was queued.
type pushExpect struct {
	// lastPush is the binding's latest push id; nil if it wasn't bound.
	lastPush *int64
	// createOnly gives up if the app has a copy, so the push never updates.
	createOnly bool
	// appHash, if set, is the app copy the push may replace.
	appHash string
}

// errAlreadyPushed means the card's pushes aren't what the job expected, so
// an earlier run of the job (or another push) already pushed it.
var errAlreadyPushed = errors.New("already pushed")

// push is Push, except that with expect it gives up with errAlreadyPushed
// if the entity was pushed since expect. key is the put key for a create; a
// re-run of the same push must pass the same key.
func (s *Service) push(ctx context.Context, k kind, instanceID, id int64, newCopy bool, key string, expect *pushExpect) (Binding, error) {
	inst, info, err := s.describePush(ctx, instanceID)
	if err != nil {
		return Binding{}, err
	}
	if err := lorebookCheck(k, inst, info); err != nil {
		return Binding{}, err
	}
	r, err := s.render(ctx, k, instanceID, id, info)
	if err != nil {
		return Binding{}, err
	}
	if !k.isLorebook() && info.Lorebooks == protocol.LorebooksStandalone {
		if r.lorebookRemoteIDs, err = s.pushCardLorebooks(ctx, instanceID, id, key); err != nil {
			return Binding{}, err
		}
	}
	// Everything from here to the commit uses only tx's connection (plugin
	// calls go to the host directly, not through the registry, which would
	// use the pool), so concurrent pushes can't use up the pool while
	// holding the lock.
	//
	// ponytail: holds the transaction across the plugin calls; fine at one
	// owner's scale.
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback(ctx)
	if err := waitForSync(ctx, tx); err != nil {
		return Binding{}, err
	}
	if err := lock(ctx, tx, k, instanceID, id); err != nil {
		return Binding{}, err
	}
	var remoteID, state, acceptedHash string
	var lastPush *int64
	err = tx.QueryRow(ctx, `SELECT remote_id, remote_state, accepted_hash, (SELECT max(id) FROM push WHERE binding_id = b.id)
		FROM binding b WHERE instance_id = $1 AND `+k.FK+` = $2`,
		instanceID, id).Scan(&remoteID, &state, &acceptedHash, &lastPush)
	bound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Binding{}, err
	}
	// A job re-run must not create a second copy, so a create skips if a push
	// landed since the job was queued. An update re-sends the head, which is
	// safe, so it always goes ahead. Pushes are serialized by the lock, so
	// this can't change before the put.
	create := !bound || state == remoteMissing || !info.Update
	if expect != nil && expect.createOnly && bound && state != remoteMissing {
		return Binding{}, errAlreadyPushed
	}
	pushedSince := expect != nil && (expect.lastPush == nil || lastPush == nil || *expect.lastPush != *lastPush)
	if create && bound && pushedSince {
		return Binding{}, errAlreadyPushed
	}
	if expect != nil && expect.appHash != "" && !create && info.Pull {
		// A push since then replaced the copy the owner agreed to, so the
		// app must still hold what that push left.
		want := expect.appHash
		if pushedSince {
			want = acceptedHash
		}
		_, hash, err := s.fetch(ctx, k, inst, info, remoteID)
		switch {
		case remoteNotFound(err):
			// The put below makes a new copy.
		case err != nil:
			return Binding{}, err
		case hash != want:
			return Binding{}, ErrAppChanged
		}
	}
	putRemote, putKey := "", key
	switch {
	case !bound || state == remoteMissing:
	case info.Update:
		putRemote, putKey = remoteID, ""
	case !newCopy:
		return Binding{}, ErrNewCopy
	}
	// The app may finish a put after ctx is cancelled (a shutdown mid-bulk-
	// push), so let it finish and record the result. The timeout still bounds
	// a hung plugin. A hard kill before the commit records nothing; the key
	// stops the re-run creating a second copy.
	detached := context.WithoutCancel(ctx)
	putCtx, cancelPut := context.WithTimeout(detached, 2*time.Minute)
	defer cancelPut()
	var res protocol.TargetPutResult
	put := func() error {
		var params any = protocol.TargetPutParams{Card: r.payload, RemoteID: putRemote, Avatar: r.avatar, Key: putKey,
			LorebookRemoteIDs: r.lorebookRemoteIDs}
		if k.isLorebook() {
			params = protocol.TargetPutLorebookParams{Lorebook: r.payload, RemoteID: putRemote, Key: putKey}
		}
		return s.plugins.Call(putCtx, inst, k.put, params, &res)
	}
	err = put()
	if putRemote != "" && remoteNotFound(err) {
		putRemote, putKey = "", key
		err = put()
	}
	if err != nil {
		if putRemote == "" && (errors.Is(err, protocol.ErrClosed) || errors.Is(err, context.DeadlineExceeded)) {
			// The plugin died or hung mid-create; the app may have the card.
			return Binding{}, fmt.Errorf("%w: %w", ErrUncertain, err)
		}
		return Binding{}, err
	}
	if res.RemoteID == "" {
		return Binding{}, fmt.Errorf("%w: %s returned no remote_id", ErrBadReply, k.put)
	}
	// The app has the card now; record it even if ctx is cancelled.
	readCtx, cancelRead := context.WithTimeout(detached, 20*time.Second)
	accepted, hash, remoteState, err := s.readBack(readCtx, k, inst, info, res.RemoteID, r.data)
	cancelRead()
	if err != nil {
		return Binding{}, err
	}
	// A hung read-back can't eat the writes' time.
	ctx, cancel := context.WithTimeout(detached, 30*time.Second)
	defer cancel()
	var bindingID int64
	err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.FK+`, remote_id, remote_state, accepted_data,
			accepted_hash, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $4 = 'unknown' THEN NULL ELSE now() END)
		ON CONFLICT (instance_id, `+k.FK+`) DO UPDATE SET remote_id = EXCLUDED.remote_id,
			remote_state = EXCLUDED.remote_state, accepted_data = EXCLUDED.accepted_data,
			accepted_hash = EXCLUDED.accepted_hash, seen_data = NULL, checked_at = EXCLUDED.checked_at,
			version = binding.version + 1
		RETURNING id`, instanceID, id, res.RemoteID, remoteState, accepted, hash).Scan(&bindingID)
	if err != nil {
		return Binding{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO push (binding_id, revision_id, lorebook_revision_ids, lorebook_remote_ids,
			pushed_hash, remote_id)
		VALUES ($1, $2, $3, $4, $5, $6)`, bindingID, r.revision, r.lorebooks, r.lorebookRemoteIDs, hash, res.RemoteID)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, bindingID)
}

// pushCardLorebooks pushes each of the card's lorebooks that the app doesn't
// have, and returns the remote IDs of all of them, sorted. It doesn't update
// lorebooks the app has, since the owner sees those changes in their own
// preview. key is the card push's key.
func (s *Service) pushCardLorebooks(ctx context.Context, instanceID, cardID int64, key string) ([]string, error) {
	links, err := s.lib.CardLorebooks(ctx, cardID)
	if err != nil {
		return nil, err
	}
	// Not nil: a push that sent none must record that, for the ahead check.
	ids := []string{}
	for _, lid := range links {
		var remoteID, state string
		err := s.pool.QueryRow(ctx, `SELECT remote_id, remote_state FROM binding WHERE instance_id = $1 AND lorebook_id = $2`,
			instanceID, lid).Scan(&remoteID, &state)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err != nil || state == remoteMissing {
			// Create only: a concurrent push may bind it first, and its copy
			// must not be updated without a preview.
			b, err := s.push(ctx, lorebookKind, instanceID, lid, false, fmt.Sprintf("%s:lorebook:%d", key, lid),
				&pushExpect{createOnly: true})
			if errors.Is(err, errAlreadyPushed) {
				err = s.pool.QueryRow(ctx, `SELECT remote_id FROM binding WHERE instance_id = $1 AND lorebook_id = $2`,
					instanceID, lid).Scan(&b.RemoteID)
			}
			if err != nil {
				return nil, fmt.Errorf("lorebook %d: %w", lid, err)
			}
			remoteID = b.RemoteID
		}
		ids = append(ids, remoteID)
	}
	slices.Sort(ids)
	return ids, nil
}

// readBack returns what the app holds after a push, its hash, and the
// remote state. A target that can pull is read back, so an app that
// reformats what it gets doesn't show as changed.
func (s *Service) readBack(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string, sent json.RawMessage) (json.RawMessage, string, string, error) {
	if info.Pull {
		data, hash, err := s.fetch(ctx, k, inst, info, remoteID)
		if err == nil {
			return data, hash, remoteUnchanged, nil
		}
		// The push worked, so record it against what was sent instead.
		s.log.Warn("read back pushed card", "instance", inst.ID, "remote_id", remoteID, "err", err)
	}
	data, hash, err := Project(sent, k.fields(info))
	state := remoteUnknown
	if info.Pull {
		state = remoteUnchanged
	}
	return data, hash, state, err
}
