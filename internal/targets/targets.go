// Package targets syncs library cards with target apps through push and
// pull plugins: bindings, pushes, drift checks, and adopting app changes.
package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

var (
	ErrNotFound = errors.New("binding not found")
	// ErrCannot means the instance's plugin lacks the capability.
	ErrCannot = errors.New("plugin can't push")
	// ErrNewCopy means the target can't update its copy, so pushing again
	// makes another one. The caller must ask for that.
	ErrNewCopy = errors.New("this target can't update cards; push with new_copy to make another copy")
	// ErrNoChanges means there are no app changes to adopt.
	ErrNoChanges = errors.New("the app's copy has no changes to adopt")
	// ErrBadReply means the plugin replied with something the core can't use.
	ErrBadReply = errors.New("plugin sent a bad reply")
	// ErrUncertain means a create failed in a way that may have made the
	// card in the app. Pushing again could make a duplicate.
	ErrUncertain = errors.New("the app may have the card; check it before pushing again")
)

// Remote states: what the last check found in the app.
const (
	remoteUnknown   = "unknown"
	remoteUnchanged = "unchanged"
	remoteChanged   = "changed"
	remoteMissing   = "missing"
)

// Service pushes cards to targets and checks them for drift.
type Service struct {
	pool    *pgxpool.Pool
	lib     *library.Library
	reg     *plugins.Registry
	plugins *pluginhost.Manager
	log     *slog.Logger
}

// New returns a Service that runs target plugins with mgr.
func New(pool *pgxpool.Pool, lib *library.Library, reg *plugins.Registry, mgr *pluginhost.Manager, log *slog.Logger) *Service {
	return &Service{pool: pool, lib: lib, reg: reg, plugins: mgr, log: log}
}

// Push is one push of a card to a target.
type Push struct {
	ID         int64 `json:"id"`
	RevisionID int64 `json:"revision_id"`
	// LorebookRevisionIDs are the embedded lorebooks' revisions; absent when
	// the target doesn't embed lorebooks.
	LorebookRevisionIDs []int64   `json:"lorebook_revision_ids,omitempty"`
	PushedHash          string    `json:"pushed_hash"`
	RemoteID            string    `json:"remote_id"`
	PushedAt            time.Time `json:"pushed_at"`
}

// Binding is a card's link to its copy in one target app.
type Binding struct {
	ID         int64  `json:"id"`
	InstanceID int64  `json:"instance_id"`
	CardID     int64  `json:"card_id"`
	RemoteID   string `json:"remote_id"`
	Status     string `json:"status" enum:"in_sync,ahead,remote_changed,diverged,missing,cant_check"`
	// Ahead means the library changed since the last push.
	Ahead       bool       `json:"ahead"`
	RemoteState string     `json:"remote_state" enum:"unknown,unchanged,changed,missing"`
	CheckedAt   *time.Time `json:"checked_at,omitempty"`
	LastPush    Push       `json:"last_push"`
	// Changes are the fields the app changed since the last push or adopt.
	Changes   []Change  `json:"changes,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

const bindingQuery = `SELECT b.id, b.instance_id, b.card_id, b.remote_id, s.status, s.ahead, b.remote_state,
		b.checked_at, b.created_at, p.id, p.revision_id, p.lorebook_revision_ids, p.pushed_hash, p.remote_id,
		p.pushed_at, b.accepted_data, b.seen_data
	FROM binding b JOIN binding_status s ON s.binding_id = b.id
	JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE`

func scanBinding(row pgx.Row) (Binding, error) {
	var b Binding
	var accepted, seen []byte
	p := &b.LastPush
	err := row.Scan(&b.ID, &b.InstanceID, &b.CardID, &b.RemoteID, &b.Status, &b.Ahead, &b.RemoteState,
		&b.CheckedAt, &b.CreatedAt, &p.ID, &p.RevisionID, &p.LorebookRevisionIDs, &p.PushedHash, &p.RemoteID,
		&p.PushedAt, &accepted, &seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil || seen == nil {
		return b, err
	}
	b.Changes, err = diff(accepted, seen)
	return b, err
}

// Binding returns one binding.
func (s *Service) Binding(ctx context.Context, id int64) (Binding, error) {
	return scanBinding(s.pool.QueryRow(ctx, bindingQuery+` WHERE b.id = $1`, id))
}

// Bindings returns a card's bindings, oldest first.
func (s *Service) Bindings(ctx context.Context, cardID int64) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, bindingQuery+` WHERE b.card_id = $1 ORDER BY b.id`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Binding, error) { return scanBinding(row) })
}

// Pushes returns a binding's pushes, newest first.
func (s *Service) Pushes(ctx context.Context, bindingID int64) ([]Push, error) {
	if _, err := s.Binding(ctx, bindingID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id, revision_id, lorebook_revision_ids, pushed_hash, remote_id, pushed_at
		FROM push WHERE binding_id = $1 ORDER BY id DESC`, bindingID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Push, error) {
		var p Push
		err := row.Scan(&p.ID, &p.RevisionID, &p.LorebookRevisionIDs, &p.PushedHash, &p.RemoteID, &p.PushedAt)
		return p, err
	})
}

// Unbind forgets a binding and its pushes. The app keeps its copy.
func (s *Service) Unbind(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM binding WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// lock serializes pushes of one card to one target, so concurrent first
// pushes can't make two copies. It holds until tx ends.
func lock(ctx context.Context, tx pgx.Tx, instanceID, cardID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("binding %d card %d", instanceID, cardID))
	return err
}

// stored is a binding row as checks and adopts read it.
type stored struct {
	instanceID, cardID                  int64
	remoteID, remoteState, acceptedHash string
	accepted, seen                      []byte
	// version is bumped by every push and adopt. Writes that must lose to
	// one that landed meanwhile guard on it.
	version int64
}

func (s *Service) stored(ctx context.Context, id int64) (stored, error) {
	var b stored
	err := s.pool.QueryRow(ctx, `SELECT instance_id, card_id, remote_id, remote_state, accepted_hash, accepted_data,
		seen_data, version FROM binding b WHERE b.id = $1`, id).
		Scan(&b.instanceID, &b.cardID, &b.remoteID, &b.remoteState, &b.acceptedHash, &b.accepted, &b.seen, &b.version)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, err
}

// describe resolves a push plugin's instance and asks what its app stores.
func (s *Service) describe(ctx context.Context, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	var info protocol.TargetInfo
	inst, err := s.reg.Instance(ctx, instanceID)
	if err != nil {
		return inst, info, err
	}
	name := inst.Manifest.Name
	if !s.reg.HasCapability(name, "push") {
		return inst, info, fmt.Errorf("%w: %s", ErrCannot, name)
	}
	if err := s.reg.Call(ctx, s.plugins, inst, protocol.MethodTargetDescribe, nil, &info); err != nil {
		return inst, info, err
	}
	if err := info.Validate(); err != nil {
		return inst, info, fmt.Errorf("%w: target.describe: %w", ErrBadReply, err)
	}
	info.Pull = info.Pull && s.reg.HasCapability(name, "pull")
	return inst, info, nil
}

// rendered is a card as one target gets it.
type rendered struct {
	card     json.RawMessage
	data     json.RawMessage
	avatar   string
	revision int64
	// lorebooks is nil unless the target embeds lorebooks.
	lorebooks []int64
}

// render builds the card a target gets: the head revision's data with the
// instance's override applied, and linked lorebooks embedded if the target
// takes them that way.
//
// ponytail: a target with standalone lorebooks gets cards without them until
// target.put_lorebook exists.
func (s *Service) render(ctx context.Context, instanceID, cardID int64, info protocol.TargetInfo) (rendered, error) {
	c, err := s.lib.Cards.Get(ctx, cardID)
	if err != nil {
		return rendered{}, err
	}
	rev, err := s.lib.Cards.Revision(ctx, c.HeadRevisionID)
	if err != nil {
		return rendered{}, err
	}
	r := rendered{data: rev.Data, revision: rev.ID}
	overrides, err := object(rev.Overrides)
	if err != nil {
		return rendered{}, err
	}
	if patch, ok := overrides[strconv.FormatInt(instanceID, 10)]; ok {
		if r.data, err = MergePatch(r.data, patch); err != nil {
			return rendered{}, err
		}
	}
	if info.Lorebooks == protocol.LorebooksEmbedded {
		var book json.RawMessage
		if book, r.lorebooks, err = s.lib.CardBook(ctx, cardID); err != nil {
			return rendered{}, err
		}
		if book != nil {
			data, err := object(r.data)
			if err != nil {
				return rendered{}, err
			}
			data["character_book"] = book
			if r.data, err = json.Marshal(data); err != nil {
				return rendered{}, err
			}
		}
	}
	if rev.AvatarBlob != nil {
		if r.avatar, err = s.lib.BlobPath(*rev.AvatarBlob); err != nil {
			return rendered{}, err
		}
	}
	r.card, err = json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": r.data})
	return r, err
}

// fetch reads the app's copy, limited to the fields it stores. It calls the
// plugin host directly, not through the registry, because Push calls it
// inside a transaction and the registry's start-failure path needs the pool.
// Callers resolve the instance with describe first, which marks a plugin
// that won't start as broken.
func (s *Service) fetch(ctx context.Context, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string) (json.RawMessage, string, error) {
	var got protocol.TargetGetResult
	err := s.plugins.Call(ctx, inst, protocol.MethodTargetGet, protocol.TargetGetParams{RemoteID: remoteID}, &got)
	if err != nil {
		return nil, "", err
	}
	data, err := cardData(got.Card)
	if err != nil {
		return nil, "", fmt.Errorf("%w: target.get: %w", ErrBadReply, err)
	}
	return project(data, info.Fields)
}

func remoteNotFound(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeRemoteNotFound
}
