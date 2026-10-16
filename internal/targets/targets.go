// Package targets syncs library cards and lorebooks with target apps through
// push and pull plugins: bindings, pushes, drift checks, and resolving app
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

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

var (
	// ErrNotFound is a library.ErrNotFound, so callers map both the same way.
	ErrNotFound = fmt.Errorf("binding %w", library.ErrNotFound)
	// ErrCannot means the instance's plugin lacks the capability.
	ErrCannot = errors.New("plugin can't push")
	// ErrNewCopy means the target can't update its copy, so pushing again
	// makes another one. The caller must ask for that.
	ErrNewCopy = errors.New("this target can't update cards; push with new_copy to make another copy")
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

const bindingQuery = `SELECT b.id, b.instance_id, coalesce(b.card_id, 0), coalesce(b.lorebook_id, 0), b.remote_id,
		s.status, s.ahead, b.remote_state, b.autosync, b.push, b.paused, b.check_failures, coalesce(b.check_error, ''),
		b.checked_at, b.created_at, p.id, p.kind, p.revision_id, p.lorebook_revision_ids, p.lorebook_remote_ids, p.pushed_hash, p.remote_id, p.pushed_at, b.accepted_data, b.seen_data
	FROM binding b JOIN binding_status s ON s.binding_id = b.id
	JOIN LATERAL (SELECT * FROM push WHERE binding_id = b.id ORDER BY id DESC LIMIT 1) p ON TRUE`

func scanBinding(row pgx.Row) (Binding, error) {
	var b Binding
	var accepted, seen []byte
	p := &b.LastPush
	err := row.Scan(&b.ID, &b.InstanceID, &b.CardID, &b.LorebookID, &b.RemoteID, &b.Status, &b.Ahead, &b.RemoteState, &b.Autosync,
		&b.Push, &b.Paused, &b.CheckFailures, &b.CheckError,
		&b.CheckedAt, &b.CreatedAt, &p.ID, &p.Kind, &p.RevisionID, &p.LorebookRevisionIDs, &p.LorebookRemoteIDs, &p.PushedHash,
		&p.RemoteID, &p.PushedAt, &accepted, &seen)
	if errors.Is(err, pgx.ErrNoRows) {
		return b, ErrNotFound
	}
	if err != nil || seen == nil {
		return b, err
	}
	if b.Changes, err = diff(accepted, seen); err != nil {
		return b, err
	}
	b.AppHash, err = cardhash.ContentHash(seen)
	return b, err
}

// Binding returns one binding.
func (s *Service) Binding(ctx context.Context, id int64) (Binding, error) {
	return scanBinding(s.pool.QueryRow(ctx, bindingQuery+` WHERE b.id = $1`, id))
}

// Bindings returns a card's or lorebook's bindings, oldest first.
func (s *Service) Bindings(ctx context.Context, k *library.Kind, id int64) ([]Binding, error) {
	rows, err := s.pool.Query(ctx, bindingQuery+` WHERE b.`+k.FK+` = $1 ORDER BY b.id`, id)
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

// SetAutosync turns autosync on or off for a binding.
func (s *Service) SetAutosync(ctx context.Context, id int64, on bool) (Binding, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE binding SET autosync = $2 WHERE id = $1`, id, on)
	if err != nil {
		return Binding{}, err
	}
	if tag.RowsAffected() == 0 {
		return Binding{}, ErrNotFound
	}
	return s.Binding(ctx, id)
}

// SetPaused stops or restarts checks of a binding that follows a site source.
func (s *Service) SetPaused(ctx context.Context, id int64, on bool) (Binding, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE binding SET paused = $2 WHERE id = $1 AND NOT push`, id, on)
	if err != nil {
		return Binding{}, err
	}
	if tag.RowsAffected() == 0 {
		if _, err := s.Binding(ctx, id); err != nil {
			return Binding{}, err
		}
		return Binding{}, fmt.Errorf("%w: only a binding that follows a site source can be paused", ErrCannot)
	}
	return s.Binding(ctx, id)
}

// lock serializes pushes and binds of one card or lorebook to one target, so
// concurrent first pushes can't make two copies. It holds until tx ends.
func lock(ctx context.Context, tx pgx.Tx, k kind, instanceID, id int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("binding %d %s %d", instanceID, k.Name, id))
	return err
}

// stored is a binding row as checks read it.
type stored struct {
	k kind
	// id is the card's or lorebook's ID.
	instanceID, id                      int64
	remoteID, remoteState, acceptedHash string
	accepted, seen                      []byte
	// version is bumped by every push and resolve. Writes that must lose to
	// one that landed meanwhile guard on it.
	version int64
	// push is false for a followed source; paused skips checks; avatarHash is
	// the site's accepted avatar, nil before the first check.
	push, paused bool
	avatarHash   *string
}

func (s *Service) stored(ctx context.Context, id int64) (stored, error) {
	var b stored
	var card, lorebook *int64
	err := s.pool.QueryRow(ctx, `SELECT instance_id, card_id, lorebook_id, remote_id, remote_state, accepted_hash,
		accepted_data, seen_data, version, push, paused, avatar_hash FROM binding b WHERE b.id = $1`, id).
		Scan(&b.instanceID, &card, &lorebook, &b.remoteID, &b.remoteState, &b.acceptedHash, &b.accepted, &b.seen, &b.version,
			&b.push, &b.paused, &b.avatarHash)
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

// describe resolves a target plugin's instance, one that pushes or pulls,
// and asks what its app stores.
func (s *Service) describe(ctx context.Context, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	var info protocol.TargetInfo
	inst, err := s.reg.Instance(ctx, instanceID)
	if err != nil {
		return inst, info, err
	}
	if !inst.Manifest.Has("push") && !inst.Manifest.Has("pull") {
		return inst, info, fmt.Errorf("%w: %s", ErrCannot, inst.Manifest.Name)
	}
	if err := s.reg.Call(ctx, s.plugins, inst, protocol.MethodTargetDescribe, nil, &info); err != nil {
		return inst, info, err
	}
	if err := info.Validate(); err != nil {
		return inst, info, fmt.Errorf("%w: target.describe: %w", ErrBadReply, err)
	}
	info.Pull = info.Pull && inst.Manifest.Has("pull")
	return inst, info, nil
}

// describePush is describe for a target the library's copy is sent to, so
// not one that only follows site sources.
func (s *Service) describePush(ctx context.Context, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	inst, info, err := s.describe(ctx, instanceID)
	if err == nil {
		err = pushCheck(inst)
	}
	return inst, info, err
}

// pushCheck refuses a target that only follows site sources.
func pushCheck(inst pluginhost.Instance) error {
	if inst.Manifest.Has("push") {
		return nil
	}
	return fmt.Errorf("%w: %s only follows site sources", ErrCannot, inst.Manifest.Name)
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
	ents := s.lib.Entities(k.Kind)
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
	r.payload, err = library.WrapCard(r.data)
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
	return Project(data, k.fields(info))
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
