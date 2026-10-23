package library

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// MoveResult is what a move to another library changed.
type MoveResult struct {
	Moved                        []int64 `json:"moved" doc:"IDs moved, the selection's variants included, ascending."`
	Archived                     int     `json:"archived" doc:"How many of the moved are archived."`
	VariantLinksCleared          []int64 `json:"variant_links_cleared" doc:"Moved IDs that are no longer a variant of anything."`
	CollectionMembershipsDropped int     `json:"collection_memberships_dropped"`
	FromLibraries                []int64 `json:"from_libraries" doc:"Libraries the moved came from."`
	BoundInstances               []int64 `json:"bound_instances" doc:"Target app instances the moved stay bound to."`
}

// Move moves the entities ids, with every variant below them, to library.
// Those already there stay as they are.
func (s *Entities) Move(ctx context.Context, ids []int64, library int64) (MoveResult, error) {
	var res MoveResult
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		res, err = s.MoveTx(ctx, tx, ids, library)
		return err
	})
	return res, err
}

// MoveTx is Move inside the caller's transaction. An unknown or archived id,
// or a library that doesn't exist, is ErrNotFound. Variants come along
// whether archived or not; a moved entity whose parent stays behind loses the
// link, and a card leaves its collections.
func (s *Entities) MoveTx(ctx context.Context, tx pgx.Tx, ids []int64, library int64) (MoveResult, error) {
	// A variant link made meanwhile could cross libraries once the move commits.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, s.k.Table+":variant_of"); err != nil {
		return MoveResult{}, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM library WHERE id = $1)`, library).Scan(&exists); err != nil {
		return MoveResult{}, err
	}
	if !exists {
		return MoveResult{}, fmt.Errorf("%w: library %d", ErrNotFound, library)
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT x FROM unnest($1::bigint[]) x
		WHERE NOT EXISTS (SELECT 1 FROM `+s.k.Table+` WHERE id = x AND archived_at IS NULL) ORDER BY x`, ids)
	if err != nil {
		return MoveResult{}, err
	}
	refused, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return MoveResult{}, err
	}
	if len(refused) > 0 {
		return MoveResult{}, fmt.Errorf("%w: %s %v is unknown or archived", ErrNotFound, s.k.Name, refused)
	}

	res := MoveResult{Moved: []int64{}, FromLibraries: []int64{}}
	rows, err = tx.Query(ctx, `WITH RECURSIVE down AS (
			SELECT id FROM `+s.k.Table+` WHERE id = ANY($1)
			UNION
			SELECT c.id FROM `+s.k.Table+` c JOIN down ON c.variant_of_id = down.id)
		SELECT id, library_id, archived_at IS NOT NULL FROM `+s.k.Table+`
		WHERE id IN (SELECT id FROM down) AND library_id <> $2 ORDER BY id FOR UPDATE`, ids, library)
	if err != nil {
		return MoveResult{}, err
	}
	var id, from int64
	var archived bool
	if _, err := pgx.ForEachRow(rows, []any{&id, &from, &archived}, func() error {
		res.Moved = append(res.Moved, id)
		if archived {
			res.Archived++
		}
		if !slices.Contains(res.FromLibraries, from) {
			res.FromLibraries = append(res.FromLibraries, from)
		}
		return nil
	}); err != nil {
		return MoveResult{}, err
	}
	slices.Sort(res.FromLibraries)

	// Every variant moves with its parent, so only links up out of the set break.
	rows, err = tx.Query(ctx, `UPDATE `+s.k.Table+` SET variant_of_id = NULL
		WHERE id = ANY($1) AND variant_of_id IS NOT NULL AND NOT variant_of_id = ANY($1) RETURNING id`, res.Moved)
	if err != nil {
		return MoveResult{}, err
	}
	if res.VariantLinksCleared, err = pgx.CollectRows(rows, pgx.RowTo[int64]); err != nil {
		return MoveResult{}, err
	}
	slices.Sort(res.VariantLinksCleared)
	if s.k.dropCollections != "" {
		tag, err := tx.Exec(ctx, s.k.dropCollections, res.Moved)
		if err != nil {
			return MoveResult{}, err
		}
		res.CollectionMembershipsDropped = int(tag.RowsAffected())
	}
	if _, err := tx.Exec(ctx, `UPDATE `+s.k.Table+` SET library_id = $2 WHERE id = ANY($1)`, res.Moved, library); err != nil {
		return MoveResult{}, err
	}
	rows, err = tx.Query(ctx, `SELECT DISTINCT instance_id FROM binding WHERE `+s.k.FK+` = ANY($1) AND NOT imported ORDER BY instance_id`, res.Moved)
	if err != nil {
		return MoveResult{}, err
	}
	res.BoundInstances, err = pgx.CollectRows(rows, pgx.RowTo[int64])
	return res, err
}
