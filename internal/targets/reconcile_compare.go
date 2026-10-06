package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
)

// Comparison is one app item set field by field against one library card or
// lorebook, as a reconcile run sees the pair.
type Comparison struct {
	InstanceID int64  `json:"instance_id"`
	RemoteID   string `json:"remote_id"`
	CardID     int64  `json:"card_id"` // or the lorebook's ID
	// Base is accepted when the pair is bound and the binding's copy is the
	// ancestor, none otherwise.
	Base string `json:"base" enum:"none,accepted"`
	// AppHash is the projected hash of the app's copy compared here.
	AppHash string        `json:"app_hash"`
	Fields  []merge.Field `json:"fields"`
	// Avatar is set when the two avatars differ.
	Avatar *AvatarDiff `json:"avatar,omitempty"`
}

// AvatarDiff names each side's avatar blob.
type AvatarDiff struct {
	Library       string `json:"library,omitempty"` // blob sha256
	App           string `json:"app,omitempty"`
	PhashDistance *int   `json:"phash_distance,omitempty"`
}

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
	var boundID int64
	var accepted json.RawMessage
	err = s.pool.QueryRow(ctx, `SELECT `+k.col+`, accepted_data FROM binding
		WHERE instance_id = $1 AND remote_id = $2 AND `+k.col+` IS NOT NULL
		ORDER BY remote_state = 'missing', id LIMIT 1`, instanceID, remoteID).Scan(&boundID, &accepted)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return c, err
	}
	if c.CardID == 0 {
		if boundID == 0 {
			return c, ErrNotFound
		}
		c.CardID = boundID
	}
	if boundID == c.CardID {
		c.Base = "accepted"
	} else {
		accepted = nil
	}
	ents, err := s.loadEntries(ctx, k, inst, info, `e.id = $2`, c.CardID)
	if err != nil {
		return c, err
	}
	if len(ents) == 0 {
		return c, library.ErrNotFound
	}
	lib := ents[0]
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
