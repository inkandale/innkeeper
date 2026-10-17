package targets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

// Preview renders the card or lorebook for a target without pushing it, and
// compares it with the app's copy.
func (s *Service) Preview(ctx context.Context, lk *library.Kind, instanceID, id int64) (Preview, error) {
	k := targetKind(lk)
	p, err := s.compare(ctx, k, instanceID, id)
	if err != nil {
		return Preview{}, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return Preview{}, err
	}
	sum := sha256.Sum256(raw)
	p.Confirm = hex.EncodeToString(sum[:])
	return p, nil
}

// compare is preview without Confirm.
func (s *Service) compare(ctx context.Context, k kind, instanceID, id int64) (Preview, error) {
	inst, info, err := s.describePush(ctx, instanceID)
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
		WHERE instance_id = $1 AND `+k.FK+` = $2 AND NOT imported`, instanceID, id).Scan(&remoteID, &state, &app)
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
	sent, _, err := Project(r.data, k.fields(info))
	if err != nil {
		return Preview{}, err
	}
	p.Replaces = true
	changes, err := diff(app, sent)
	if err != nil {
		return Preview{}, err
	}
	if changes != nil {
		p.Changes = changes
	}
	return p, nil
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
