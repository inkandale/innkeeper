package targets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
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

// Items lists the app's items of kind lk: its characters or its lorebooks.
func (s *Service) Items(ctx context.Context, lk *library.Kind, instanceID int64) ([]Item, error) {
	k := targetKind(lk)
	inst, _, err := s.describePull(ctx, k, instanceID)
	if err != nil {
		return nil, err
	}
	listed, err := s.listItems(ctx, k, inst)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT remote_id, id FROM binding WHERE instance_id = $1 AND `+k.FK+` IS NOT NULL
		AND remote_state <> 'missing' AND NOT imported`,
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
	items := make([]Item, len(listed))
	for i, it := range listed {
		items[i].TargetItem = it
		if id, ok := bound[it.RemoteID]; ok {
			items[i].BindingID = &id
		}
	}
	return items, nil
}

// listItems lists the app's items of kind k. A pull-only target has no app,
// so it has none.
func (s *Service) listItems(ctx context.Context, k kind, inst pluginhost.Instance) ([]protocol.TargetItem, error) {
	if inst.Manifest.PullOnly() {
		return nil, nil
	}
	var res protocol.TargetListResult
	err := s.reg.Call(ctx, s.plugins, inst, k.list, nil, &res)
	return res.Items, err
}

// Bind links a card or lorebook to one the app already has, without
// changing the library: a resolve that keeps the library's copy and skips the
// app's linked world books. The app's copy becomes the accepted one, so the
// entity is ahead where they differ and the next push sends the library's.
func (s *Service) Bind(ctx context.Context, lk *library.Kind, instanceID, id int64, remoteID string) (Binding, error) {
	k := targetKind(lk)
	// Bind is for an unbound pair; resolve would also rebind or update one.
	bound := func(where string, args ...any) error {
		var b bool
		err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM binding WHERE instance_id = $1 AND NOT imported AND `+where+`)`,
			append([]any{instanceID}, args...)...).Scan(&b)
		if err == nil && b {
			err = ErrBound
		}
		return err
	}
	if err := bound(k.FK+` = $2`, id); err != nil {
		return Binding{}, err
	}
	c, err := s.compareItem(ctx, k, instanceID, remoteID, id, false)
	if err != nil {
		return Binding{}, err
	}
	if err := bound(`remote_id = $2 AND `+k.FK+` IS NOT NULL AND remote_state <> 'missing'`, remoteID); err != nil {
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

// describePull is describe for a target that must read the app, and keep
// lorebooks on their own if k is a lorebook.
func (s *Service) describePull(ctx context.Context, k kind, instanceID int64) (pluginhost.Instance, protocol.TargetInfo, error) {
	inst, info, err := s.describe(ctx, instanceID)
	switch {
	case err != nil:
	case !info.Pull:
		err = fmt.Errorf("%w: %s can't read the app", ErrCannot, inst.Manifest.Name)
	default:
		err = lorebookCheck(k, inst, info)
	}
	return inst, info, err
}

// lorebookCheck refuses a lorebook for a target that keeps them inside cards.
func lorebookCheck(k kind, inst pluginhost.Instance, info protocol.TargetInfo) error {
	if k.isLorebook() && info.Lorebooks != protocol.LorebooksStandalone {
		return fmt.Errorf("%w: lorebooks for %s", ErrCannot, inst.Manifest.Name)
	}
	return nil
}

// CheckLorebookPush fails with ErrCannot if the target can't take lorebooks on their own.
func (s *Service) CheckLorebookPush(ctx context.Context, instanceID int64) error {
	inst, info, err := s.describePush(ctx, instanceID)
	if err != nil {
		return err
	}
	return lorebookCheck(lorebookKind, inst, info)
}
