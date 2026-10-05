package targets

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
)

// Where Adopt puts the app's changes.
const (
	// IntoBase writes them into the card's own data.
	IntoBase = "base"
	// IntoOverride writes them into this target's override.
	IntoOverride = "override"
	// IntoIgnore leaves the library as it is and stops reporting them.
	IntoIgnore = "ignore"
)

// ErrMoved means a push or check changed the binding while Adopt ran. The
// adopted revision may already have been written.
var ErrMoved = errors.New("the binding changed while adopting; look at it again")

// Adopt takes the app's changes into the library and accepts the app's copy
// as the new baseline for checks. Unless into is IntoIgnore, it adds a
// revision on top of base, so a stale base returns library.ErrConflict.
// Changes to character_book are not adopted; edit the lorebooks instead. If
// they are the only changes, Adopt into the base or an override returns
// ErrNoChanges; ignore them, or push to overwrite them. To discard the app's
// other changes, push: a push overwrites them.
func (s *Service) Adopt(ctx context.Context, id int64, into string, base int64) (Binding, error) {
	if into != IntoBase && into != IntoOverride && into != IntoIgnore {
		return Binding{}, fmt.Errorf("%w: into must be base, override, or ignore", library.ErrInvalid)
	}
	b, err := s.stored(ctx, id)
	if err != nil {
		return Binding{}, err
	}
	if b.remoteState != remoteChanged {
		return Binding{}, ErrNoChanges
	}
	changes, err := diff(b.accepted, b.seen)
	if err != nil {
		return Binding{}, err
	}
	// ponytail: the revision commits before the binding does. If a push or
	// check lands in between, the revision stays and Adopt returns ErrMoved.
	if into != IntoIgnore {
		if err := s.adopt(ctx, b.k, b.instanceID, b.id, base, into, changes); err != nil {
			return Binding{}, err
		}
	}
	if err := s.accept(ctx, id, b); err != nil {
		return Binding{}, err
	}
	return s.Binding(ctx, id)
}

// accept makes the app's copy the baseline for checks. It guards on b's
// version, and on seen_data too, because a check can replace it without
// bumping the version.
func (s *Service) accept(ctx context.Context, id int64, b stored) error {
	hash, err := cardhash.ContentHash(b.seen)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE binding SET accepted_data = seen_data, accepted_hash = $2, seen_data = NULL,
			remote_state = 'unchanged', version = version + 1
		WHERE id = $1 AND remote_state = 'changed' AND version = $3 AND seen_data = $4`,
		id, hash, b.version, b.seen)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrMoved
	}
	return nil
}

// adopt writes changes into the entity's data or the instance's override, in
// a revision on top of base.
func (s *Service) adopt(ctx context.Context, k kind, instanceID, id, base int64, into string, changes []Change) error {
	ents := k.entities(s.lib)
	rev, err := ents.Revision(ctx, base)
	if errors.Is(err, library.ErrNotFound) || err == nil && rev.EntityID != id {
		return library.ErrConflict
	}
	if err != nil {
		return err
	}
	inst, err := s.reg.GetInstance(ctx, instanceID)
	if err != nil {
		return err
	}
	e := library.Edit{Data: rev.Data, Overrides: rev.Overrides, Source: "adopt", Message: "Adopt changes from " + inst.Name}
	if rev.AvatarBlob != nil {
		e.AvatarBlob = *rev.AvatarBlob
	}
	var changed bool
	if into == IntoBase {
		if e.Data, changed, err = applyChanges(rev.Data, changes); err == nil && changed {
			// The override would win over the adopted data when rendering.
			e.Overrides, _, err = clearOverride(rev.Overrides, strconv.FormatInt(instanceID, 10), changes)
		}
	} else {
		e.Overrides, changed, err = overrideChanges(rev.Data, rev.Overrides, strconv.FormatInt(instanceID, 10), changes)
	}
	if err != nil {
		return err
	}
	if !changed {
		return ErrNoChanges
	}
	_, err = ents.Update(ctx, id, base, e)
	return err
}
