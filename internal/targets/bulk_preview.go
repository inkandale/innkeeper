package targets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/cardhash"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/wire"
)

// BulkPreview is wire.BulkPreview.
type BulkPreview = wire.BulkPreview

// ErrAppChanged means the app's copy changed after the owner agreed to
// replace it, so the push left it alone.
var ErrAppChanged = errors.New("the app's copy changed since the push was confirmed; check it and push again")

// StalePreviewError means a query push's confirm doesn't match a new
// preview, which it carries.
type StalePreviewError struct{ Preview BulkPreview }

func (e *StalePreviewError) Error() string {
	return "the push would differ from the preview; review the new preview"
}

// PreviewBulkPush counts how the cards or lorebooks stand on the target, for
// the owner to confirm a query push.
func (s *Service) PreviewBulkPush(ctx context.Context, k *library.Kind, instanceID int64, ids []int64) (BulkPreview, error) {
	p, _, err := s.bulkPreview(ctx, targetKind(k), instanceID, ids)
	return p, err
}

// bulkTarget is one bound entity as a query push preview saw it.
type bulkTarget struct {
	ID      int64  `json:"id"`
	Version int64  `json:"version"`
	State   string `json:"state"`
	// AppHash is the app copy the owner agrees to replace: what the last
	// check saw if it found a change, else what the last push or resolve
	// accepted. Empty if the app lost its copy.
	AppHash  string `json:"app_hash"`
	accepted string
	lastPush int64
}

// bulkPreview returns the preview and the bound entities by ID.
func (s *Service) bulkPreview(ctx context.Context, k kind, instanceID int64, ids []int64) (BulkPreview, map[int64]bulkTarget, error) {
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	rows, err := s.pool.Query(ctx, `SELECT b.`+k.FK+`, b.version, b.remote_state, b.accepted_hash, b.seen_data, s.status,
			(SELECT max(id) FROM push WHERE binding_id = b.id)
		FROM binding b JOIN binding_status s ON s.binding_id = b.id
		WHERE b.instance_id = $1 AND b.`+k.FK+` = ANY($2) ORDER BY b.`+k.FK, instanceID, ids)
	if err != nil {
		return BulkPreview{}, nil, err
	}
	p := BulkPreview{Total: len(ids)}
	bound := map[int64]bulkTarget{}
	var list []bulkTarget
	var t bulkTarget
	var seen []byte
	var status string
	if _, err := pgx.ForEachRow(rows, []any{&t.ID, &t.Version, &t.State, &t.accepted, &seen, &status, &t.lastPush}, func() error {
		t.AppHash = t.accepted
		switch {
		case t.State == remoteMissing:
			t.AppHash = ""
		case t.State == remoteChanged && seen != nil:
			h, err := cardhash.ContentHash(seen)
			if err != nil {
				return err
			}
			t.AppHash = h
		}
		switch status {
		case "missing":
			p.New++
		case "remote_changed", "diverged":
			p.Drifted++
		case "ahead":
			p.Ahead++
		case "in_sync":
			p.InSync++
		case "cant_check":
			p.CantCheck++
		}
		bound[t.ID] = t
		list = append(list, t)
		return nil
	}); err != nil {
		return BulkPreview{}, nil, err
	}
	p.New += len(ids) - len(bound)
	raw, err := json.Marshal(map[string]any{"instance": instanceID, "kind": k.Name, "ids": ids, "bound": list})
	if err != nil {
		return BulkPreview{}, nil, err
	}
	sum := sha256.Sum256(raw)
	p.Confirm = hex.EncodeToString(sum[:])
	return p, bound, nil
}
