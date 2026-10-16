package targets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/protocol"
)

// Unpublish deletes a card's or lorebook's copies from the apps it was pushed
// to, and drops those bindings. An app that can't delete keeps its copy and
// its binding. Followed site sources are left alone. It stops at the first
// failure, so calling it again finishes the rest.
func (s *Service) Unpublish(ctx context.Context, lk *library.Kind, id int64) error {
	k := targetKind(lk)
	rows, err := s.pool.Query(ctx, `SELECT id, instance_id FROM binding WHERE push AND `+k.FK+` = $1 ORDER BY id`, id)
	if err != nil {
		return err
	}
	type bound struct{ ID, InstanceID int64 }
	list, err := pgx.CollectRows(rows, pgx.RowToStructByPos[bound])
	if err != nil {
		return err
	}
	for _, b := range list {
		if err := s.unpublishOne(ctx, k, id, b.ID, b.InstanceID); err != nil {
			return err
		}
	}
	return nil
}

// unpublishOne deletes one binding's app copy and the binding, holding the
// push lock so a concurrent push can't recreate it in between.
func (s *Service) unpublishOne(ctx context.Context, k kind, id, bindingID, instanceID int64) error {
	inst, info, err := s.describe(ctx, instanceID)
	if err != nil {
		return err
	}
	if !info.Delete {
		return nil
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := lock(ctx, tx, k, instanceID, id); err != nil {
			return err
		}
		var remoteID string
		err := tx.QueryRow(ctx, `SELECT remote_id FROM binding WHERE id = $1`, bindingID).Scan(&remoteID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		err = s.reg.Call(ctx, s.plugins, inst, k.delete, protocol.TargetDeleteParams{RemoteID: remoteID}, nil)
		if err != nil && !remoteNotFound(err) {
			return fmt.Errorf("delete %s from instance %d: %w", remoteID, instanceID, err)
		}
		_, err = tx.Exec(ctx, `DELETE FROM binding WHERE id = $1`, bindingID)
		return err
	})
}
