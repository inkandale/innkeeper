package targets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Preview is what a push would send, for the owner to confirm first.
type Preview struct {
	// Card or Lorebook is what the target would get.
	Card     json.RawMessage `json:"card,omitempty"`
	Lorebook json.RawMessage `json:"lorebook,omitempty"`
	// DroppedFields are fields with content that the target doesn't store.
	DroppedFields []string `json:"dropped_fields"`
	// NewCopy means the push makes another copy, because the target can't
	// update the one it has.
	NewCopy bool `json:"new_copy"`
	// Replaces means the push overwrites the app's copy. Changes lists what
	// it changes there: Before is the app's value, After the pushed one.
	Replaces bool     `json:"replaces"`
	Changes  []Change `json:"changes"`
	// Stale means the app couldn't be read, so Changes compare against what
	// the last push or adopt accepted.
	Stale bool `json:"stale,omitempty"`
	// CreateLorebooks are the card's lorebooks that the push creates in the
	// app first, for a target that keeps lorebooks on their own.
	CreateLorebooks []int64 `json:"create_lorebooks,omitempty"`
}

// Preview renders the card for a target without pushing it, and compares it
// with the app's copy.
func (s *Service) Preview(ctx context.Context, instanceID, cardID int64) (Preview, error) {
	return s.preview(ctx, cardKind, instanceID, cardID)
}

// PreviewLorebook is Preview for a lorebook.
func (s *Service) PreviewLorebook(ctx context.Context, instanceID, lorebookID int64) (Preview, error) {
	return s.preview(ctx, lorebookKind, instanceID, lorebookID)
}

func (s *Service) preview(ctx context.Context, k kind, instanceID, id int64) (Preview, error) {
	inst, info, err := s.describe(ctx, instanceID)
	if err != nil {
		return Preview{}, err
	}
	standalone := info.Lorebooks == protocol.LorebooksStandalone
	if k.isLorebook() && !standalone {
		return Preview{}, fmt.Errorf("%w: lorebooks for %s", ErrCannot, inst.Manifest.Name)
	}
	r, err := s.render(ctx, k, instanceID, id, info)
	if err != nil {
		return Preview{}, err
	}
	p := Preview{Changes: []Change{}}
	if p.DroppedFields, err = droppedFields(r.data, k.fields(info)); err != nil {
		return Preview{}, err
	}
	if k.isLorebook() {
		p.Lorebook = r.payload
	} else {
		p.Card = r.payload
	}
	if !k.isLorebook() && standalone {
		if p.CreateLorebooks, err = s.lorebooksToCreate(ctx, instanceID, id); err != nil {
			return Preview{}, err
		}
	}
	var remoteID, state string
	var app json.RawMessage
	err = s.pool.QueryRow(ctx, `SELECT remote_id, remote_state, accepted_data FROM binding
		WHERE instance_id = $1 AND `+k.col+` = $2`, instanceID, id).Scan(&remoteID, &state, &app)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return p, nil
	case err != nil:
		return Preview{}, err
	case state == remoteMissing:
		return p, nil
	case !info.Update:
		p.NewCopy = true
		return p, nil
	}
	if info.Pull {
		data, _, err := s.fetch(ctx, k, inst, info, remoteID)
		switch {
		case err == nil:
			app = data
		case remoteNotFound(err):
			// The push makes a new copy.
			return p, nil
		default:
			s.log.Warn("read app copy for preview", "instance", instanceID, "remote_id", remoteID, "err", err)
			p.Stale = true
		}
	}
	sent, _, err := project(r.data, k.fields(info))
	if err != nil {
		return Preview{}, err
	}
	p.Replaces = true
	changes, err := diff(app, sent)
	if changes != nil {
		p.Changes = changes
	}
	return p, err
}

// lorebooksToCreate lists the card's lorebooks that a push to this instance
// would create: unbound there, or missing from the app.
func (s *Service) lorebooksToCreate(ctx context.Context, instanceID, cardID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx, `SELECT cl.lorebook_id FROM card_lorebook cl
		LEFT JOIN binding b ON b.lorebook_id = cl.lorebook_id AND b.instance_id = $1
		WHERE cl.card_id = $2 AND (b.id IS NULL OR b.remote_state = 'missing') ORDER BY cl.lorebook_id`,
		instanceID, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
