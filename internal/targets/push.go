package targets

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Push sends a card's head revision to a target and records the push. A
// card pushed there before is updated in place, which overwrites changes
// made in the app. A target that can't update gets another copy, but only
// when newCopy is set. If the app lost its copy, the push makes a new one.
func (s *Service) Push(ctx context.Context, instanceID, cardID int64, newCopy bool) (Binding, error) {
	return s.push(ctx, instanceID, cardID, newCopy, rand.Text(), nil)
}

// pushExpect is what a queued push job saw when it was queued.
type pushExpect struct {
	// lastPush is the binding's latest push id; nil if it wasn't bound.
	lastPush *int64
}

// errAlreadyPushed means the card's pushes aren't what the job expected, so
// an earlier run of the job (or another push) already pushed it.
var errAlreadyPushed = errors.New("already pushed")

// push is Push, except that with expect it gives up with errAlreadyPushed
// if the card was pushed since expect. key is the target.put key for a
// create; a re-run of the same push must pass the same key.
func (s *Service) push(ctx context.Context, instanceID, cardID int64, newCopy bool, key string, expect *pushExpect) (Binding, error) {
	inst, info, err := s.describe(ctx, instanceID)
	if err != nil {
		return Binding{}, err
	}
	r, err := s.render(ctx, instanceID, cardID, info)
	if err != nil {
		return Binding{}, err
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
	if err := lock(ctx, tx, instanceID, cardID); err != nil {
		return Binding{}, err
	}
	var remoteID, state string
	var lastPush *int64
	err = tx.QueryRow(ctx, `SELECT remote_id, remote_state, (SELECT max(id) FROM push WHERE binding_id = b.id)
		FROM binding b WHERE instance_id = $1 AND card_id = $2`,
		instanceID, cardID).Scan(&remoteID, &state, &lastPush)
	bound := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Binding{}, err
	}
	// A job re-run must not create a second copy, so a create skips if a push
	// landed since the job was queued. An update re-sends the head, which is
	// safe, so it always goes ahead. Pushes are serialized by the lock, so
	// this can't change before the put.
	create := !bound || state == remoteMissing || !info.Update
	if expect != nil && create && bound && (expect.lastPush == nil || lastPush == nil || *expect.lastPush != *lastPush) {
		return Binding{}, errAlreadyPushed
	}
	put := protocol.TargetPutParams{Card: r.card, Avatar: r.avatar, Key: key}
	switch {
	case !bound || state == remoteMissing:
	case info.Update:
		put.RemoteID, put.Key = remoteID, ""
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
	err = s.plugins.Call(putCtx, inst, protocol.MethodTargetPut, put, &res)
	if put.RemoteID != "" && remoteNotFound(err) {
		put.RemoteID, put.Key = "", key
		err = s.plugins.Call(putCtx, inst, protocol.MethodTargetPut, put, &res)
	}
	if err != nil {
		if put.RemoteID == "" && (errors.Is(err, protocol.ErrClosed) || errors.Is(err, context.DeadlineExceeded)) {
			// The plugin died or hung mid-create; the app may have the card.
			return Binding{}, fmt.Errorf("%w: %w", ErrUncertain, err)
		}
		return Binding{}, err
	}
	if res.RemoteID == "" {
		return Binding{}, fmt.Errorf("%w: target.put returned no remote_id", ErrBadReply)
	}
	// The app has the card now; record it even if ctx is cancelled.
	readCtx, cancelRead := context.WithTimeout(detached, 20*time.Second)
	accepted, hash, remoteState, err := s.readBack(readCtx, inst, info, res.RemoteID, r.data)
	cancelRead()
	if err != nil {
		return Binding{}, err
	}
	// A hung read-back can't eat the writes' time.
	ctx, cancel := context.WithTimeout(detached, 30*time.Second)
	defer cancel()
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, card_id, remote_id, remote_state, accepted_data,
			accepted_hash, checked_at)
		VALUES ($1, $2, $3, $4, $5, $6, CASE WHEN $4 = 'unknown' THEN NULL ELSE now() END)
		ON CONFLICT (instance_id, card_id) DO UPDATE SET remote_id = EXCLUDED.remote_id,
			remote_state = EXCLUDED.remote_state, accepted_data = EXCLUDED.accepted_data,
			accepted_hash = EXCLUDED.accepted_hash, seen_data = NULL, checked_at = EXCLUDED.checked_at,
			version = binding.version + 1
		RETURNING id`, instanceID, cardID, res.RemoteID, remoteState, accepted, hash).Scan(&id)
	if err != nil {
		return Binding{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO push (binding_id, revision_id, lorebook_revision_ids, pushed_hash, remote_id)
		VALUES ($1, $2, $3, $4, $5)`, id, r.revision, r.lorebooks, hash, res.RemoteID)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// readBack returns what the app holds after a push, its hash, and the
// remote state. A target that can pull is read back, so an app that
// reformats what it gets doesn't show as changed.
func (s *Service) readBack(ctx context.Context, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string, sent json.RawMessage) (json.RawMessage, string, string, error) {
	if info.Pull {
		data, hash, err := s.fetch(ctx, inst, info, remoteID)
		if err == nil {
			return data, hash, remoteUnchanged, nil
		}
		// The push worked, so record it against what was sent instead.
		s.log.Warn("read back pushed card", "instance", inst.ID, "remote_id", remoteID, "err", err)
	}
	data, hash, err := project(sent, info.Fields)
	state := remoteUnknown
	if info.Pull {
		state = remoteUnchanged
	}
	return data, hash, state, err
}

// Preview is what a push would send, for the owner to confirm first.
type Preview struct {
	// Card is the CCv3 card the target would get.
	Card json.RawMessage `json:"card"`
	// DroppedFields are fields with content that the target doesn't store.
	DroppedFields []string `json:"dropped_fields"`
	// NewCopy means the push makes another copy, because the target can't
	// update the one it has.
	NewCopy bool `json:"new_copy"`
}

// Preview renders the card for a target without pushing it.
func (s *Service) Preview(ctx context.Context, instanceID, cardID int64) (Preview, error) {
	_, info, err := s.describe(ctx, instanceID)
	if err != nil {
		return Preview{}, err
	}
	r, err := s.render(ctx, instanceID, cardID, info)
	if err != nil {
		return Preview{}, err
	}
	dropped, err := droppedFields(r.data, info.Fields)
	if err != nil {
		return Preview{}, err
	}
	var state string
	err = s.pool.QueryRow(ctx, `SELECT remote_state FROM binding WHERE instance_id = $1 AND card_id = $2`,
		instanceID, cardID).Scan(&state)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Preview{}, err
	}
	return Preview{Card: r.card, DroppedFields: dropped, NewCopy: err == nil && state != remoteMissing && !info.Update}, nil
}
