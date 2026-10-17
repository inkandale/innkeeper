package targets

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

// importedItem reads the copy an import binding holds for remoteID as the
// app's item, since an import plugin can't be asked for it again. Its fields
// are every key of that copy and the accepted one, so review compares them
// whole. id, when set, picks the entity's binding. ok is false when no
// import binding holds the item.
func (s *Service) importedItem(ctx context.Context, q queryRower, k kind, instanceID int64, remoteID string,
	id int64) (inst pluginhost.Instance, info protocol.TargetInfo, it appItem, ok bool, err error) {
	var accepted json.RawMessage
	err = q.QueryRow(ctx, `SELECT coalesce(seen_data, accepted_data), accepted_data, coalesce(seen_avatar, '') FROM binding
		WHERE instance_id = $1 AND remote_id = $2 AND imported AND `+k.FK+` IS NOT NULL AND ($3::bigint = 0 OR `+k.FK+` = $3)
		ORDER BY id LIMIT 1`, instanceID, remoteID, id).Scan(&it.raw, &accepted, &it.avatar)
	if errors.Is(err, pgx.ErrNoRows) {
		return inst, info, it, false, nil
	}
	if err != nil {
		return inst, info, it, false, err
	}
	if inst, err = s.reg.Instance(ctx, instanceID); err != nil {
		return inst, info, it, false, err
	}
	fields := map[string]bool{}
	for _, raw := range []json.RawMessage{it.raw, accepted} {
		obj, err := object(raw)
		if err != nil {
			return inst, info, it, false, err
		}
		for key := range obj {
			fields[key] = true
		}
	}
	keys := slices.Sorted(maps.Keys(fields))
	info = protocol.TargetInfo{Fields: keys, Pull: true, Update: true, Lorebooks: protocol.LorebooksStandalone, LorebookFields: keys}
	it.imported = true
	if it.data, it.hash, err = Project(it.raw, k.fields(info)); err != nil {
		return inst, info, it, false, err
	}
	it.name = library.DataName(it.data)
	if it.simhash, err = textSimhash(k, it.data); err != nil {
		return inst, info, it, false, err
	}
	if it.avatar != "" {
		b, err := s.lib.Blob(ctx, it.avatar)
		if err != nil {
			return inst, info, it, false, err
		}
		it.phash = b.AvatarPhash
	}
	return inst, info, it, true, nil
}

// ApplyUpdate is autosync for one binding: it takes a waiting update whose
// merge has no conflicting hunks, and says whether it did.
func (s *Service) ApplyUpdate(ctx context.Context, id int64) (bool, error) {
	b, err := s.Binding(ctx, id)
	if err != nil || b.RemoteState != remoteChanged {
		return false, err
	}
	return s.applyUpdate(ctx, b)
}
