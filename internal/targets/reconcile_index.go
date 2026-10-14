package targets

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/cardhash"
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
	byID    map[int64]indexEntry
	// byRemote maps the instance's bound remote IDs to their bindings. An
	// app can reuse a missing binding's ID; the live binding wins.
	byRemote map[string]indexBinding
	// remoteOf maps bound card or lorebook IDs to their remote IDs.
	remoteOf map[int64]string
}

type indexEntry struct {
	id, head   int64
	name, norm string
	// data leaves a card's lorebooks out: reconcile matches them as items of their own.
	data           json.RawMessage
	hash           string
	simhash, phash *int64
	// avatar is the avatar blob's sha256, or empty.
	avatar string
}

type indexBinding struct {
	id, entityID              int64
	accepted                  json.RawMessage
	acceptedHash, remoteState string
}

func (s *Service) loadIndex(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo) (index, error) {
	idx := index{byID: map[int64]indexEntry{}, byRemote: map[string]indexBinding{}, remoteOf: map[int64]string{}}
	var err error
	if idx.entries, err = s.loadEntries(ctx, k, inst, info, `e.archived_at IS NULL`); err != nil {
		return idx, err
	}
	for _, e := range idx.entries {
		idx.byID[e.id] = e
	}
	rows, err := s.pool.Query(ctx, `SELECT id, `+k.FK+`, remote_id, accepted_data, accepted_hash, remote_state FROM binding
		WHERE instance_id = $1::bigint AND `+k.FK+` IS NOT NULL
		ORDER BY remote_state <> 'missing', id`, inst.ID)
	if err != nil {
		return idx, err
	}
	var b indexBinding
	var remoteID string
	_, err = pgx.ForEachRow(rows, []any{&b.id, &b.entityID, &remoteID, &b.accepted, &b.acceptedHash, &b.remoteState}, func() error {
		idx.byRemote[remoteID] = b
		idx.remoteOf[b.entityID] = remoteID
		return nil
	})
	return idx, err
}

// loadEntries renders and projects the heads that match where, in ID order.
// where's parameters start at $2.
func (s *Service) loadEntries(ctx context.Context, k kind, inst pluginhost.Instance, info protocol.TargetInfo,
	where string, args ...any) ([]indexEntry, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, r.id, r.data, r.overrides -> $1, coalesce(r.avatar_blob, ''), b.avatar_phash
		FROM `+k.Table+` e JOIN `+k.RevTable+` r ON r.id = e.head_revision_id
		LEFT JOIN blob b ON b.sha256 = r.avatar_blob
		WHERE `+where+` ORDER BY e.id`, append([]any{inst.ID}, args...)...)
	if err != nil {
		return nil, err
	}
	var out []indexEntry
	var data, override []byte
	var ent indexEntry
	_, err = pgx.ForEachRow(rows, []any{&ent.id, &ent.head, &data, &override, &ent.avatar, &ent.phash}, func() error {
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
		if ent.simhash, err = textSimhash(k, ent.data); err != nil {
			return err
		}
		out = append(out, ent)
		return nil
	})
	return out, err
}

// textSimhash fingerprints projected data, so both sides of a reconcile
// match on the same fields; nil when the text has no words.
func textSimhash(k kind, data json.RawMessage) (*int64, error) {
	simhash := cardhash.CardSimhash
	if k.isLorebook() {
		simhash = cardhash.LorebookSimhash
	}
	h, ok, err := simhash(data)
	if !ok || err != nil {
		return nil, err
	}
	return &h, nil
}

// normName case-folds a name and collapses its whitespace.
func normName(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
