package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// ErrBound means the card or lorebook, or the app item, already has a
// binding on that instance.
var ErrBound = errors.New("already bound on this instance")

// Item is a card or lorebook the app has.
type Item struct {
	protocol.TargetItem
	// BindingID is the binding that already uses the item, if any.
	BindingID *int64 `json:"binding_id,omitempty"`
}

// Items lists the characters the app has.
func (s *Service) Items(ctx context.Context, instanceID int64) ([]Item, error) {
	return s.items(ctx, cardKind, instanceID)
}

// LorebookItems lists the lorebooks the app has.
func (s *Service) LorebookItems(ctx context.Context, instanceID int64) ([]Item, error) {
	return s.items(ctx, lorebookKind, instanceID)
}

func (s *Service) items(ctx context.Context, k kind, instanceID int64) ([]Item, error) {
	inst, _, err := s.describePull(ctx, k, instanceID)
	if err != nil {
		return nil, err
	}
	var res protocol.TargetListResult
	if err := s.reg.Call(ctx, s.plugins, inst, k.list, nil, &res); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT remote_id, id FROM binding WHERE instance_id = $1 AND `+k.col+` IS NOT NULL
		AND remote_state <> 'missing'`,
		instanceID)
	if err != nil {
		return nil, err
	}
	bound := map[string]int64{}
	var remoteID string
	var id int64
	if _, err := pgx.ForEachRow(rows, []any{&remoteID, &id}, func() error {
		bound[remoteID] = id
		return nil
	}); err != nil {
		return nil, err
	}
	items := make([]Item, len(res.Items))
	for i, it := range res.Items {
		items[i].TargetItem = it
		if id, ok := bound[it.RemoteID]; ok {
			items[i].BindingID = &id
		}
	}
	return items, nil
}

// Bind links a card to a character the app already has, without changing
// the library: a resolve that keeps the library's copy and skips the app's
// linked world books. The app's copy becomes the accepted one, so the card is
// ahead where they differ and the next push sends the library's.
func (s *Service) Bind(ctx context.Context, instanceID, cardID int64, remoteID string) (Binding, error) {
	return s.bindResolved(ctx, cardKind, instanceID, cardID, remoteID)
}

// BindLorebook links a lorebook to one the app already has, as Bind does for
// cards.
func (s *Service) BindLorebook(ctx context.Context, instanceID, lorebookID int64, remoteID string) (Binding, error) {
	return s.bindResolved(ctx, lorebookKind, instanceID, lorebookID, remoteID)
}

func (s *Service) bindResolved(ctx context.Context, k kind, instanceID, id int64, remoteID string) (Binding, error) {
	// Bind is for an unbound pair; resolve would also rebind or update one.
	bound := func(where string, args ...any) error {
		var b bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM binding WHERE instance_id = $1 AND `+where+`)`,
			append([]any{instanceID}, args...)...).Scan(&b)
		if err == nil && b {
			err = ErrBound
		}
		return err
	}
	if err := bound(k.col+` = $2`, id); err != nil {
		return Binding{}, err
	}
	c, err := s.compareItem(ctx, k, instanceID, remoteID, id)
	if err != nil {
		return Binding{}, err
	}
	if err := bound(`remote_id = $2 AND `+k.col+` IS NOT NULL AND remote_state <> 'missing'`, remoteID); err != nil {
		return Binding{}, err
	}
	// The app's linked world books are left for reconcile, so bind changes nothing in the library.
	res, err := s.resolve(ctx, k, Resolution{InstanceID: instanceID, RemoteID: remoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: id,
		Default: merge.ChooseLibrary}, resolveOpts{bindOnly: true, skipLeftOut: true})
	if err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, res.BindingID)
}

// bind links an item to a library one whose content matches it exactly, with
// the library's copy as the baseline, for target import.
func (s *Service) bind(ctx context.Context, k kind, instanceID, id int64, remoteID string) (Binding, error) {
	inst, info, err := s.describePull(ctx, k, instanceID)
	if err != nil {
		return Binding{}, err
	}
	r, err := s.render(ctx, k, instanceID, id, info)
	if err != nil {
		return Binding{}, err
	}
	var links []string
	var appData json.RawMessage
	if k.isLorebook() {
		appData, err = s.fetchRaw(ctx, k, inst, remoteID)
	} else {
		var got protocol.TargetGetResult
		got, err = s.getCard(ctx, inst, protocol.TargetGetParams{RemoteID: remoteID})
		links = got.LorebookRemoteIDs
		if err == nil {
			appData, err = cardData(got.Card)
		}
	}
	if remoteNotFound(err) {
		return Binding{}, fmt.Errorf("%w: the app has no %s %s", library.ErrNotFound, k.name, remoteID)
	} else if err != nil {
		return Binding{}, err
	}
	// The app's copy is where the sources are, even if the target drops extensions.
	if err := k.entities(s.lib).DetectSources(ctx, id, appData); err != nil {
		return Binding{}, err
	}
	// The bind row records the lorebooks the app links to the character, so
	// the card is ahead only if the library links others. A plugin that can't
	// tell records none, and a card with lorebooks shows as ahead.
	if !k.isLorebook() && info.Lorebooks == protocol.LorebooksStandalone {
		r.lorebookRemoteIDs = slices.Sorted(slices.Values(links))
		if r.lorebookRemoteIDs == nil {
			r.lorebookRemoteIDs = []string{}
		}
	}
	accepted, hash, err := Project(r.data, k.fields(info))
	if err != nil {
		return Binding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Binding{}, err
	}
	defer tx.Rollback(ctx)
	if err := lock(ctx, tx, k, instanceID, id); err != nil {
		return Binding{}, err
	}
	var bindingID int64
	err = tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.col+`, remote_id, remote_state, accepted_data,
			accepted_hash)
		VALUES ($1, $2, $3, 'unknown', $4, $5) RETURNING id`, instanceID, id, remoteID, accepted, hash).Scan(&bindingID)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Binding{}, ErrBound
	}
	if err != nil {
		return Binding{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO push (binding_id, kind, revision_id, lorebook_revision_ids, lorebook_remote_ids,
			pushed_hash, remote_id)
		VALUES ($1, 'bind', $2, $3, $4, $5, $6)`, bindingID, r.revision, r.lorebooks, r.lorebookRemoteIDs, hash, remoteID)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, err
	}
	return s.Check(ctx, bindingID)
}

// describePull is describe for a target that must read the app, and keep
// lorebooks on their own if k is a lorebook.
func (s *Service) describePull(ctx context.Context, k kind, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	inst, info, err := s.describe(ctx, instanceID)
	switch {
	case err != nil:
	case !info.Pull:
		err = fmt.Errorf("%w: %s can't read the app", ErrCannot, inst.Manifest.Name)
	case k.isLorebook() && info.Lorebooks != protocol.LorebooksStandalone:
		err = fmt.Errorf("%w: lorebooks for %s", ErrCannot, inst.Manifest.Name)
	}
	return inst, info, err
}
