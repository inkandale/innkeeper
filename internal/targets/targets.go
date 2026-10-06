// Package targets syncs library cards and lorebooks with target apps through
// push and pull plugins: bindings, pushes, drift checks, and adopting app
// changes.
package targets

import (
	"cmp"
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

// Push is one push of a card or lorebook to a target, or the bind that
// linked it to an item the app already had.
type Push struct {
	ID         int64  `json:"id"`
	Kind       string `json:"kind" enum:"push,bind"`
	RevisionID int64  `json:"revision_id"`
	// LorebookRevisionIDs are the embedded lorebooks' revisions; absent when
	// the target doesn't embed lorebooks.
	LorebookRevisionIDs []int64 `json:"lorebook_revision_ids,omitempty"`
	// LorebookRemoteIDs are the card's lorebooks in the app; absent unless
	// the target keeps lorebooks on their own.
	LorebookRemoteIDs []string  `json:"lorebook_remote_ids,omitempty"`
	PushedHash        string    `json:"pushed_hash"`
	RemoteID          string    `json:"remote_id"`
	PushedAt          time.Time `json:"pushed_at"`
}

// Binding is a card's or lorebook's link to its copy in one target app.
// Exactly one of CardID and LorebookID is set.
type Binding struct {
	ID         int64  `json:"id"`
	InstanceID int64  `json:"instance_id"`
	CardID     int64  `json:"card_id,omitempty"`
	LorebookID int64  `json:"lorebook_id,omitempty"`
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

const bindingQuery = `SELECT b.id, b.instance_id, coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.remote_id,
		s.status, s.ahead, b.remote_state, b.checked_at, b.created_at, p.id, p.kind, p.revision_id, p.lorebook_revision_ids,
		p.lorebook_remote_ids, p.pushed_hash, p.remote_id, p.pushed_at, b.accepted_data, b.seen_data
	FROM binding b JOIN binding_status s ON s.binding_id = b.id
	JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE`

func scanBinding(row pgx.Row) (Binding, error) {
	var b Binding
	var accepted, seen []byte
	p := &b.LastPush
	err := row.Scan(&b.ID, &b.InstanceID, &b.CardID, &b.LorebookID, &b.RemoteID, &b.Status, &b.Ahead, &b.RemoteState,
		&b.CheckedAt, &b.CreatedAt, &p.ID, &p.Kind, &p.RevisionID, &p.LorebookRevisionIDs, &p.LorebookRemoteIDs, &p.PushedHash,
		&p.RemoteID, &p.PushedAt, &accepted, &seen)
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
	return s.bindings(ctx, cardKind, cardID)
}

// LorebookBindings returns a lorebook's bindings, oldest first.
func (s *Service) LorebookBindings(ctx context.Context, lorebookID int64) ([]Binding, error) {
	return s.bindings(ctx, lorebookKind, lorebookID)
}

func (s *Service) bindings(ctx context.Context, k kind, id int64) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, bindingQuery+` WHERE b.`+k.col+` = $1 ORDER BY b.id`, id)
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
	rows, err := s.pool.Query(ctx, `SELECT id, kind, revision_id, lorebook_revision_ids, lorebook_remote_ids, pushed_hash,
		remote_id, pushed_at FROM push WHERE binding_id = $1 ORDER BY id DESC`, bindingID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Push, error) {
		var p Push
		err := row.Scan(&p.ID, &p.Kind, &p.RevisionID, &p.LorebookRevisionIDs, &p.LorebookRemoteIDs, &p.PushedHash, &p.RemoteID,
			&p.PushedAt)
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

// lock serializes pushes and binds of one card or lorebook to one target, so
// concurrent first pushes can't make two copies. It holds until tx ends.
func lock(ctx context.Context, tx pgx.Tx, k kind, instanceID, id int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("binding %d %s %d", instanceID, k.name, id))
	return err
}

// stored is a binding row as checks and adopts read it.
type stored struct {
	k kind
	// id is the card's or lorebook's ID.
	instanceID, id                      int64
	remoteID, remoteState, acceptedHash string
	accepted, seen                      []byte
	// version is bumped by every push and adopt. Writes that must lose to
	// one that landed meanwhile guard on it.
	version int64
}

func (s *Service) stored(ctx context.Context, id int64) (stored, error) {
	var b stored
	var card, lorebook *int64
	err := s.pool.QueryRow(ctx, `SELECT instance_id, card_id, lorebook_id, remote_id, remote_state, accepted_hash,
		accepted_data, seen_data, version FROM binding b WHERE b.id = $1`, id).
		Scan(&b.instanceID, &card, &lorebook, &b.remoteID, &b.remoteState, &b.acceptedHash, &b.accepted, &b.seen, &b.version)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil {
		return b, err
	}
	b.k, b.id = cardKind, *cmp.Or(card, lorebook)
	if lorebook != nil {
		b.k = lorebookKind
	}
	return b, nil
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

// rendered is a card or lorebook as one target gets it.
type rendered struct {
	// payload is the CCv3 card, or the lorebook's character_book.
	payload  json.RawMessage
	data     json.RawMessage
	avatar   string
	revision int64
	// lorebooks is nil unless the target embeds lorebooks.
	lorebooks []int64
	// lorebookRemoteIDs is nil unless the target keeps lorebooks on their own.
	lorebookRemoteIDs []string
}

// render builds what a target gets: the head revision's data with the
// instance's override applied, and for a card, linked lorebooks embedded if
// the target takes them that way.
func (s *Service) render(ctx context.Context, k kind, instanceID, id int64, info protocol.TargetInfo) (rendered, error) {
	ents := k.entities(s.lib)
	c, err := ents.Get(ctx, id)
	if err != nil {
		return rendered{}, err
	}
	rev, err := ents.Revision(ctx, c.HeadRevisionID)
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
	if k.isLorebook() {
		r.payload = r.data
		return r, nil
	}
	if info.Lorebooks == protocol.LorebooksEmbedded {
		var book json.RawMessage
		if book, r.lorebooks, err = s.lib.CardBook(ctx, id); err != nil {
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
	r.payload, err = json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": r.data})
	return r, err
}

// fetch reads the app's copy, limited to the fields it stores. It calls the
// plugin host directly, not through the registry, because Push calls it
// inside a transaction and the registry's start-failure path needs the pool.
// Callers resolve the instance with describe first, which marks a plugin
// that won't start as broken.
func (s *Service) fetch(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo, remoteID string) (json.RawMessage, string, error) {
	data, err := s.fetchRaw(ctx, k, inst, remoteID)
	if err != nil {
		return nil, "", err
	}
	return project(data, k.fields(info))
}

// fetchRaw reads the app's copy as it is, before projection.
func (s *Service) fetchRaw(ctx context.Context, k kind, inst pluginhost.Instance, remoteID string) (json.RawMessage, error) {
	if k.isLorebook() {
		var got protocol.TargetGetLorebookResult
		if err := s.plugins.Call(ctx, inst, k.get, protocol.TargetGetParams{RemoteID: remoteID}, &got); err != nil {
			return nil, err
		}
		if _, err := object(got.Lorebook); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrBadReply, k.get, err)
		}
		return got.Lorebook, nil
	}
	got, err := s.getCard(ctx, inst, protocol.TargetGetParams{RemoteID: remoteID})
	if err != nil {
		return nil, err
	}
	return cardData(got.Card)
}

// getCard calls target.get and checks that the reply has a card.
func (s *Service) getCard(ctx context.Context, inst pluginhost.Instance, p protocol.TargetGetParams) (protocol.TargetGetResult, error) {
	var got protocol.TargetGetResult
	if err := s.plugins.Call(ctx, inst, protocol.MethodTargetGet, p, &got); err != nil {
		return got, err
	}
	if _, err := cardData(got.Card); err != nil {
		return got, fmt.Errorf("%w: %s: %w", ErrBadReply, protocol.MethodTargetGet, err)
	}
	return got, nil
}

func remoteNotFound(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeRemoteNotFound
}
