package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// CompareItem compares an app character with a library card. cardID 0 means
// the card bound to remoteID.
func (s *Service) CompareItem(ctx context.Context, instanceID int64, remoteID string, cardID int64) (Comparison, error) {
	return s.compareItem(ctx, cardKind, instanceID, remoteID, cardID)
}

// CompareLorebookItem compares an app world book with a library lorebook.
// lorebookID 0 means the lorebook bound to remoteID.
func (s *Service) CompareLorebookItem(ctx context.Context, instanceID int64, remoteID string, lorebookID int64) (Comparison, error) {
	return s.compareItem(ctx, lorebookKind, instanceID, remoteID, lorebookID)
}

func (s *Service) compareItem(ctx context.Context, k kind, instanceID int64, remoteID string, id int64) (Comparison, error) {
	c := Comparison{InstanceID: instanceID, RemoteID: remoteID, CardID: id, Base: "none"}
	inst, info, err := s.describeReconcile(ctx, k, instanceID)
	if err != nil {
		return c, err
	}
	b, err := itemBinding(ctx, s.pool, k, instanceID, remoteID)
	if err != nil {
		return c, err
	}
	if c.CardID == 0 {
		if b.entityID == 0 {
			return c, ErrNotFound
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
	ents, err := s.loadEntries(ctx, k, inst, info, `e.id = $2`, c.CardID)
	if err != nil {
		return c, err
	}
	if len(ents) == 0 {
		return c, library.ErrNotFound
	}
	lib := ents[0]
	c.HeadRevisionID = lib.head
	it, err := s.readItem(ctx, k, inst, info, remoteID, true)
	if err != nil {
		return c, err
	}
	if it.avatarErr != nil {
		// Resolve trusts this comparison, so a hidden avatar difference is worse than an error.
		return c, fmt.Errorf("app avatar: %w", it.avatarErr)
	}
	c.AppHash = it.hash
	if c.Fields, err = compare(k, info, lib.data, it.data, accepted); err != nil {
		return c, err
	}
	if lib.avatar != it.avatar {
		c.Avatar = &AvatarDiff{Library: lib.avatar, App: it.avatar}
		if lib.phash != nil && it.phash != nil {
			d := cardhash.Distance(*lib.phash, *it.phash)
			c.Avatar.PhashDistance = &d
		}
	}
	return c, nil
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
// since an app can reuse a missing binding's ID.
func itemBinding(ctx context.Context, q queryRower, k kind, instanceID int64, remoteID string) (boundItem, error) {
	var b boundItem
	err := q.QueryRow(ctx, `SELECT id, `+k.col+`, accepted_hash, remote_state, accepted_data FROM binding
		WHERE instance_id = $1 AND remote_id = $2 AND `+k.col+` IS NOT NULL
		ORDER BY remote_state = 'missing', id LIMIT 1`, instanceID, remoteID).
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
