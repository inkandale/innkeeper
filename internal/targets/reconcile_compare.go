package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/library"
)

// CompareItem compares an app item with a library card or lorebook. id 0
// means the one bound to remoteID.
func (s *Service) CompareItem(ctx context.Context, k *library.Kind, instanceID int64, remoteID string, id int64) (Comparison, error) {
	return s.compareItem(ctx, targetKind(k), instanceID, remoteID, id, false)
}

// CompareItemTwoWay is CompareItem without the binding's accepted copy as
// the ancestor, so every difference is the owner's choice. A bind accepts the
// app's copy as it is, so with the ancestor the library's side of each
// difference would be kept without asking. Resolve it with base none.
func (s *Service) CompareItemTwoWay(ctx context.Context, k *library.Kind, instanceID int64, remoteID string, id int64) (Comparison, error) {
	return s.compareItem(ctx, targetKind(k), instanceID, remoteID, id, true)
}

func (s *Service) compareItem(ctx context.Context, k kind, instanceID int64, remoteID string, id int64, twoWay bool) (Comparison, error) {
	c, _, err := s.compareRead(ctx, k, instanceID, remoteID, id, twoWay)
	return c, err
}

// compareRead is compareItem that also returns the app item it read.
func (s *Service) compareRead(ctx context.Context, k kind, instanceID int64, remoteID string, id int64,
	twoWay bool) (Comparison, appItem, error) {
	c := Comparison{InstanceID: instanceID, RemoteID: remoteID, CardID: id, Base: "none"}
	inst, info, it, imported, err := s.importedItem(ctx, s.pool, k, instanceID, remoteID, id)
	if err != nil {
		return c, appItem{}, err
	}
	if !imported {
		if inst, info, err = s.describeReconcile(ctx, k, instanceID); err != nil {
			return c, appItem{}, err
		}
	}
	b, err := itemBinding(ctx, s.pool, k, instanceID, remoteID, c.CardID)
	if err != nil {
		return c, appItem{}, err
	}
	if c.CardID == 0 {
		if b.entityID == 0 {
			return c, appItem{}, ErrNotFound
		}
		c.CardID = b.entityID
	}
	if b.remoteState != remoteMissing {
		c.BindingID = b.id
	}
	accepted := b.ancestor(c.CardID)
	if accepted != nil {
		c.Base, c.AcceptedHash = "accepted", b.acceptedHash
	}
	if twoWay {
		// The hash still guards the binding against a change before resolve.
		c.Base, accepted = "none", nil
	}
	ents, err := s.loadEntries(ctx, k, inst, info, `e.id = $2`, c.CardID)
	if err != nil {
		return c, appItem{}, err
	}
	if len(ents) == 0 {
		return c, appItem{}, library.ErrNotFound
	}
	lib := ents[0]
	c.HeadRevisionID = lib.head
	if !imported {
		if it, err = s.readItem(ctx, k, inst, info, remoteID, true); err != nil {
			return c, appItem{}, err
		}
	}
	if it.avatarErr != nil {
		// Resolve trusts this comparison, so a hidden avatar difference is worse than an error.
		return c, appItem{}, fmt.Errorf("app avatar: %w", it.avatarErr)
	}
	c.AppHash = it.hash
	if c.Fields, err = compare(k, info, lib.data, it.data, accepted); err != nil {
		return c, appItem{}, err
	}
	same, err := s.lib.SameImage(ctx, lib.avatar, it.avatar)
	if err != nil {
		return c, appItem{}, err
	}
	if !same {
		c.Avatar = &AvatarDiff{Library: lib.avatar, App: it.avatar}
		if lib.phash != nil && it.phash != nil {
			d := cardhash.Distance(*lib.phash, *it.phash)
			c.Avatar.PhashDistance = &d
		}
	}
	return c, it, nil
}

// boundItem is the binding an app item has on an instance, or the zero value.
type boundItem struct {
	id, entityID              int64
	acceptedHash, remoteState string
	accepted                  json.RawMessage
}

type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// itemBinding finds the app item's binding: the live one, else a missing one,
// since an app can reuse a missing binding's ID. Cards that share a followed
// source each have one, so id, when set, picks the entity's followed binding.
func itemBinding(ctx context.Context, q queryRower, k kind, instanceID int64, remoteID string, id int64) (boundItem, error) {
	var b boundItem
	err := q.QueryRow(ctx, `SELECT id, `+k.FK+`, accepted_hash, remote_state, accepted_data FROM binding
		WHERE instance_id = $1 AND remote_id = $2 AND `+k.FK+` IS NOT NULL
		ORDER BY (NOT push AND `+k.FK+` = $3) DESC, remote_state = 'missing', id LIMIT 1`, instanceID, remoteID, id).
		Scan(&b.id, &b.entityID, &b.acceptedHash, &b.remoteState, &b.accepted)
	if errors.Is(err, pgx.ErrNoRows) {
		return boundItem{}, nil
	}
	return b, err
}

// ancestor is the accepted copy when the item is bound to entity id, else
// nil. A missing binding's copy may be another character under a reused ID.
func (b boundItem) ancestor(id int64) json.RawMessage {
	if b.entityID != id || b.remoteState == remoteMissing {
		return nil
	}
	return b.accepted
}
