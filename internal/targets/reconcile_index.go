package targets

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// index is the library's unarchived heads of one kind, as one instance sees
// them, for reconcile to match app items against.
//
// ponytail: every head is held in memory and compared pairwise, fine at
// library scale (thousands of cards); move the distance search to SQL if a
// library outgrows it.
type index struct {
	entries []indexEntry
	// byRemote maps the instance's bound remote IDs to their bindings.
	byRemote map[string]indexBinding
	// remoteOf maps bound card or lorebook IDs to their remote IDs.
	remoteOf map[int64]string
}

type indexEntry struct {
	id         int64
	name, norm string
	// data leaves a card's lorebooks out: reconcile matches them as items of their own.
	data           json.RawMessage
	hash           string
	simhash, phash *int64
}

type indexBinding struct {
	id, entityID int64
	accepted     json.RawMessage
}

func (s *Service) loadIndex(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo) (index, error) {
	idx := index{byRemote: map[string]indexBinding{}, remoteOf: map[int64]string{}}
	rows, err := s.pool.Query(ctx, `SELECT e.id, r.data, r.overrides -> $1, r.text_simhash, b.avatar_phash
		FROM `+k.table+` e JOIN `+k.revTable+` r ON r.id = e.head_revision_id
		LEFT JOIN blob b ON b.sha256 = r.avatar_blob
		WHERE e.archived_at IS NULL ORDER BY e.id`, inst.ID)
	if err != nil {
		return idx, err
	}
	var data, override []byte
	var ent indexEntry
	_, err = pgx.ForEachRow(rows, []any{&ent.id, &data, &override, &ent.simhash, &ent.phash}, func() error {
		d := json.RawMessage(data)
		if override != nil {
			if d, err = MergePatch(d, override); err != nil {
				return err
			}
		}
		var named struct{ Name string }
		if err := json.Unmarshal(d, &named); err != nil {
			return err
		}
		ent.name, ent.norm = named.Name, normName(named.Name)
		if ent.data, ent.hash, err = Project(d, k.fields(info)); err != nil {
			return err
		}
		idx.entries = append(idx.entries, ent)
		return nil
	})
	if err != nil {
		return idx, err
	}
	rows, err = s.pool.Query(ctx, `SELECT id, `+k.col+`, remote_id, accepted_data FROM binding
		WHERE instance_id = $1::bigint AND `+k.col+` IS NOT NULL`, inst.ID)
	if err != nil {
		return idx, err
	}
	var b indexBinding
	var remoteID string
	_, err = pgx.ForEachRow(rows, []any{&b.id, &b.entityID, &remoteID, &b.accepted}, func() error {
		idx.byRemote[remoteID] = b
		idx.remoteOf[b.entityID] = remoteID
		return nil
	})
	return idx, err
}

// normName case-folds a name and collapses its whitespace.
func normName(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
