package library

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EntityRef names a card or lorebook by its ID, slug, and head revision's name.
type EntityRef struct {
	ID   int64  `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// SetVariantOf makes parent the entity's parent, or clears the link when
// parent is nil. The link isn't content, so it makes no revision.
func (s *Entities) SetVariantOf(ctx context.Context, id int64, parent *int64) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error { return s.SetVariantOfTx(ctx, tx, id, parent) })
}

// SetVariantOfTx is SetVariantOf inside the caller's transaction. A parent
// that is id or a variant below it is ErrVariantCycle; a parent this kind
// doesn't have is ErrNotFound.
func (s *Entities) SetVariantOfTx(ctx context.Context, tx pgx.Tx, id int64, parent *int64) error {
	if parent != nil {
		// Two links checked at once could each pass and together make a cycle.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, s.k.Table+":variant_of"); err != nil {
			return err
		}
		var found int
		var cycle bool
		err := tx.QueryRow(ctx, `WITH RECURSIVE up AS (
				SELECT id, variant_of_id FROM `+s.k.Table+` WHERE id = $1
				UNION
				SELECT p.id, p.variant_of_id FROM `+s.k.Table+` p JOIN up ON p.id = up.variant_of_id)
			SELECT count(*), coalesce(bool_or(id = $2), false) FROM up`, *parent, id).Scan(&found, &cycle)
		switch {
		case err != nil:
			return err
		case found == 0:
			return fmt.Errorf("%w: %s %d", ErrNotFound, s.k.Name, *parent)
		case cycle:
			return ErrVariantCycle
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE `+s.k.Table+` SET variant_of_id = $2 WHERE id = $1`, id, parent)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Parent returns the entity this one is a variant of, or nil.
func (s *Entities) Parent(ctx context.Context, id int64) (*EntityRef, error) {
	refs, err := s.refs(ctx, `c.id = (SELECT variant_of_id FROM `+s.k.Table+` WHERE id = $1)`, id)
	if err != nil || len(refs) == 0 {
		return nil, err
	}
	return &refs[0], nil
}

// Variants returns the entity's direct variants, oldest first.
func (s *Entities) Variants(ctx context.Context, id int64) ([]EntityRef, error) {
	return s.refs(ctx, `c.variant_of_id = $1`, id)
}

// refs returns the entities matching cond, whose one argument is id.
func (s *Entities) refs(ctx context.Context, cond string, id int64) ([]EntityRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.slug, coalesce(r.data->>'name', '') FROM `+from(s.k)+`
		WHERE `+cond+` ORDER BY c.id`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[EntityRef])
}

// Fork creates a variant of entity id from its head revision: its data,
// avatar, and overrides, and the kind's links, but not its sources or
// bindings. A shared source is reconcile's strongest match signal, so
// copying one would make the two hard to tell apart. source is the new
// revision's source, ui or mcp.
func (s *Entities) Fork(ctx context.Context, id int64, source string) (Entity, error) {
	var ent Entity
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		from, err := scanEntity(tx.QueryRow(ctx, `SELECT `+entityCols+` FROM `+s.k.Table+` WHERE id = $1`, id))
		if err != nil {
			return fmt.Errorf("load %s %d: %w", s.k.Name, id, err)
		}
		head, err := scanRevision(tx.QueryRow(ctx, `SELECT `+s.revCols()+` FROM `+s.k.RevTable+` WHERE id = $1`, from.HeadRevisionID))
		if err != nil {
			return fmt.Errorf("load head of %s %d: %w", s.k.Name, id, err)
		}
		e := Edit{Data: head.Data, Overrides: head.Overrides, Source: source,
			Message: fmt.Sprintf("Variant of %s at r%d", from.Slug, head.ID)}
		if head.AvatarBlob != nil {
			e.AvatarBlob = *head.AvatarBlob
		}
		r, err := s.prepare(e)
		if err != nil {
			return err
		}
		if ent, _, err = s.createFree(ctx, tx, from.Slug+"-variant", "variant", r); err != nil {
			return fmt.Errorf("create variant of %s %d: %w", s.k.Name, id, err)
		}
		for _, q := range s.k.forkCopies {
			if _, err := tx.Exec(ctx, q, id, ent.ID); err != nil {
				return fmt.Errorf("copy links of %s %d: %w", s.k.Name, id, err)
			}
		}
		return s.SetVariantOfTx(ctx, tx, ent.ID, &id)
	})
	return ent, err
}
