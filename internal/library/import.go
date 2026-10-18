package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/db"
)

const maxSlugLen = 60

// Slugify turns a name into a slug: lowercase ASCII letters and digits in
// runs joined by single hyphens.
//
// ponytail: other letters are dropped, so a name in another script becomes
// "untitled"; transliterate if that gets common.
func Slugify(name string) string {
	var b strings.Builder
	gap := false
	for _, r := range strings.ToLower(name) {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte('-')
		}
		gap = false
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String()[:min(b.Len(), maxSlugLen)], "-")
	if s == "" {
		return "untitled"
	}
	return s
}

// DataName is data's name field, or "" if it has none.
func DataName(data json.RawMessage) string {
	var named struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal(data, &named)
	return named.Name
}

// freeSlug returns base, or base-N with N one more than the highest suffix
// in use.
func (s *Entities) freeSlug(ctx context.Context, tx pgx.Tx, base string) (string, error) {
	var n int
	err := tx.QueryRow(ctx, `SELECT coalesce(max(CASE WHEN slug = $1 THEN 1
			ELSE substring(slug from '-([0-9]{1,9})$')::int END), 0)
		FROM `+s.k.Table+` WHERE slug = $1 OR slug ~ ('^' || $1 || '-[0-9]{1,9}$')`, base).Scan(&n)
	if err != nil || n == 0 {
		return base, err
	}
	return fmt.Sprintf("%s-%d", base, n+1), nil
}

// CreateFromName creates an entity in libraryID with a free slug made from name.
func (s *Entities) CreateFromName(ctx context.Context, libraryID int64, name, provenance string, e Edit) (Entity, Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	var ent Entity
	var rev Revision
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		ent, rev, err = s.createFree(ctx, tx, Slugify(name), libraryID, provenance, r)
		return err
	})
	return ent, rev, err
}

// CreateFromNameTx is CreateFromName inside the caller's transaction.
func (s *Entities) CreateFromNameTx(ctx context.Context, tx pgx.Tx, name, provenance string, e Edit) (Entity, Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	return s.createFree(ctx, tx, Slugify(name), DefaultLibrary, provenance, r)
}

// createFree creates an entity with base as its slug, or base-N if that's taken.
func (s *Entities) createFree(ctx context.Context, tx pgx.Tx, base string, libraryID int64, provenance string, r revisionRow) (Entity, Revision, error) {
	// Retried because another writer can take the same slug first.
	for range 5 {
		slug, err := s.freeSlug(ctx, tx, base)
		if err != nil {
			return Entity{}, Revision{}, err
		}
		ent, rev, err := s.create(ctx, tx, slug, libraryID, provenance, r)
		if !errors.Is(err, ErrSlugTaken) {
			return ent, rev, err
		}
	}
	return Entity{}, Revision{}, ErrSlugTaken
}

// SplitCard checks that card is a CCv3 card and returns its name, its data
// as cardhash.CardData returns it, and the character_book, or nil.
func SplitCard(card json.RawMessage) (name string, data, book json.RawMessage, err error) {
	var c struct {
		Spec string `json:"spec"`
	}
	if err := json.Unmarshal(card, &c); err != nil {
		return "", nil, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if c.Spec != "chara_card_v3" {
		return "", nil, nil, fmt.Errorf("%w: spec %q is not chara_card_v3", ErrInvalid, c.Spec)
	}
	if data, book, err = cardhash.CardData(card); err != nil {
		return "", nil, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if data == nil {
		return "", nil, nil, fmt.Errorf("%w: card has no data", ErrInvalid)
	}
	return DataName(data), data, book, nil
}

// PluginItem is a card or lorebook a plugin sent.
type PluginItem struct {
	Kind *Kind
	// LibraryID is where the item goes; 0 means DefaultLibrary.
	LibraryID int64
	// InstanceID is the importing instance, which holds the item's import binding.
	InstanceID         int64
	Provenance, Source string
	// Data is a CCv3 card or a lorebook.
	Data json.RawMessage
	// Avatar is a card's avatar, a file in the blob store's tmp folder, or empty.
	Avatar string
	// Name is a lorebook's name when its data has none.
	Name string
	// Version is a new revision's message; empty means "Imported".
	Version string
	// Created is a new revision's created_at; zero means now.
	Created time.Time
	// Values are a card's enrichment values from InstanceID, already checked
	// against its manifest.
	Values map[string]json.RawMessage
	// SkipDetect leaves the entity off the source detection queue.
	SkipDetect bool
}

// Outcome is what an import did with an item.
type Outcome string

const (
	// Unchanged: a revision, or the waiting update, already has this content.
	Unchanged Outcome = "unchanged"
	Created   Outcome = "created"
	// Revised: the entity was as last imported, so the item became its head.
	Revised Outcome = "revised"
	// Pending: the owner edited the entity since the last import, so the item
	// waits in its import binding as a source update.
	Pending Outcome = "pending"
)

// Imported is what importing one item did.
type Imported struct {
	ID      int64
	Outcome Outcome
	// RevisionID is the revision the import wrote, or the head when it wrote none.
	RevisionID int64
	// Autosync lists import bindings, the card's and its lorebook's, holding
	// an update their autosync should try to apply.
	Autosync []int64
}

// ImportItem stores an item a plugin sent, as ImportItemTx does, in its own transaction.
func (l *Library) ImportItem(ctx context.Context, it PluginItem) (Imported, error) {
	var out Imported
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) (err error) {
		out, err = l.ImportItemTx(ctx, tx, it)
		return err
	})
	return out, err
}

// ImportItemTx stores an item a plugin sent as the one card or lorebook with
// its provenance in its library, inside the caller's transaction. A card's
// character_book becomes a linked lorebook with the same provenance, and its
// avatar is moved into the blob store. Data PostgreSQL refuses, such as
// text it can't store, is ErrInvalid, so one bad item doesn't stop an import.
func (l *Library) ImportItemTx(ctx context.Context, tx pgx.Tx, it PluginItem) (Imported, error) {
	out, err := l.importItemTx(ctx, tx, it)
	if db.IsDataError(err) {
		return out, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return out, err
}

func (l *Library) importItemTx(ctx context.Context, tx pgx.Tx, it PluginItem) (Imported, error) {
	it.LibraryID = cmp.Or(it.LibraryID, DefaultLibrary)
	edit := Edit{Data: it.Data, Source: it.Source, Message: cmp.Or(it.Version, "Imported"), Created: it.Created}
	if it.Kind == Lorebook {
		// An avatar sent with a lorebook stays in blob_tmp until garbage collection.
		return l.importEntity(ctx, tx, l.Lorebooks, it, cmp.Or(DataName(it.Data), it.Name), edit)
	}
	name, data, book, err := SplitCard(it.Data)
	if err != nil {
		return Imported{}, err
	}
	if it.Avatar != "" {
		b, err := l.IngestBlob(ctx, it.Avatar)
		if err != nil {
			return Imported{}, fmt.Errorf("avatar: %w", err)
		}
		edit.AvatarBlob = b.SHA256
	}
	var lorebook Imported
	if book != nil {
		lorebook, err = l.importEntity(ctx, tx, l.Lorebooks, it, cmp.Or(DataName(book), name+" lorebook"),
			Edit{Data: book, Source: it.Source, Message: "Split from imported card", Created: it.Created})
		if err != nil {
			return Imported{}, err
		}
	}
	edit.Data = data
	card, err := l.importEntity(ctx, tx, l.Cards, it, name, edit)
	if err != nil {
		return Imported{}, err
	}
	if lorebook.ID != 0 {
		if err := l.LinkLorebookTx(ctx, tx, card.ID, lorebook.ID); err != nil {
			return Imported{}, err
		}
	}
	if card.Outcome != Unchanged && len(it.Values) > 0 {
		vals := make([]NewEnrichment, 0, len(it.Values))
		for _, key := range slices.Sorted(maps.Keys(it.Values)) {
			vals = append(vals, NewEnrichment{CardID: card.ID, RevisionID: card.RevisionID, Key: key, Value: it.Values[key]})
		}
		if err := addEnrichments(ctx, tx, it.InstanceID, vals); err != nil {
			return Imported{}, err
		}
	}
	card.Autosync = append(lorebook.Autosync, card.Autosync...)
	return card, nil
}

// ImportBatch stores items in one transaction, each as ImportItemTx does,
// then runs then in it with what each item did. An item refused as invalid
// fails alone, with its error in errs; any other failure rolls everything back.
func (l *Library) ImportBatch(ctx context.Context, items []PluginItem, then func(tx pgx.Tx, got []Imported, errs []error) error) error {
	got, errs := make([]Imported, len(items)), make([]error, len(items))
	return pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		for i, it := range items {
			// A savepoint, so a refused item leaves the transaction usable.
			err := pgx.BeginFunc(ctx, tx, func(sp pgx.Tx) (err error) {
				got[i], err = l.ImportItemTx(ctx, sp, it)
				return err
			})
			if errors.Is(err, ErrInvalid) || errors.Is(err, blobstore.ErrInvalid) {
				errs[i] = err
				continue
			}
			if err != nil {
				return fmt.Errorf("store %s: %w", it.Provenance, err)
			}
		}
		return then(tx, got, errs)
	})
}

// importBinding is an entity's import binding on the importing instance.
type importBinding struct {
	id                                  int64
	acceptedHash, avatarKey, seenAvatar string
	seen                                json.RawMessage
	autosync                            bool
	// newest is the later of the source's dates for the accepted and waiting copies, if either has one.
	newest *time.Time
}

// importEntity stores e as the entity with it.Provenance in it.LibraryID:
// a new entity, nothing if a revision already has e's content, a new head if
// the entity is as last imported, or else a pending update in its import
// binding. The binding is targets' row, written here so an import is one
// transaction.
func (l *Library) importEntity(ctx context.Context, tx pgx.Tx, s *Entities, it PluginItem, name string, e Edit) (Imported, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Imported{}, err
	}
	// Two jobs can import the same item at once; without the lock both would
	// miss the lookup and create an entity each.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`,
		fmt.Sprintf("%s:%d:%s", s.k.Table, it.LibraryID, it.Provenance)); err != nil {
		return Imported{}, err
	}
	out := Imported{Outcome: Unchanged}
	var headHash, headAvatar string
	var stored bool
	// The newest entity, since earlier cores imported a changed item as a new one.
	err = tx.QueryRow(ctx, `SELECT e.id, e.head_revision_id, hr.content_hash, coalesce(hr.avatar_blob, ''),
			EXISTS (SELECT 1 FROM `+s.k.RevTable+` r WHERE r.`+s.k.FK+` = e.id AND r.content_hash = $3
				AND r.avatar_blob IS NOT DISTINCT FROM $4)
		FROM `+s.k.Table+` e JOIN `+s.k.RevTable+` hr ON hr.id = e.head_revision_id
		WHERE e.library_id = $1 AND e.provenance = $2 ORDER BY e.id DESC LIMIT 1`,
		it.LibraryID, it.Provenance, r.contentHash, r.avatar).Scan(&out.ID, &out.RevisionID, &headHash, &headAvatar, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		ent, rev, err := s.createFree(ctx, tx, Slugify(name), it.LibraryID, it.Provenance, r)
		if err != nil {
			return Imported{}, err
		}
		if _, err := l.bindImport(ctx, tx, s.k, it, ent.ID, rev.ID, e.Data, r.contentHash, e.AvatarBlob, it.Created); err != nil {
			return Imported{}, err
		}
		return Imported{ID: ent.ID, Outcome: Created, RevisionID: rev.ID}, s.detectUnless(ctx, tx, it, ent.ID)
	}
	if err != nil {
		return out, err
	}
	newKey, err := l.AvatarKey(ctx, e.AvatarBlob)
	if err != nil {
		return Imported{}, err
	}
	if stored {
		return out, withdrawUpdate(ctx, tx, s.k, it, out.ID, r.contentHash, newKey)
	}
	b, err := l.importBinding(ctx, tx, s, it, out.ID, out.RevisionID)
	if err != nil {
		return Imported{}, err
	}
	// The accepted copy again, which the card may differ from by the owner's avatar.
	if r.contentHash == b.acceptedHash && newKey == b.avatarKey {
		return out, withdrawUpdate(ctx, tx, s.k, it, out.ID, r.contentHash, newKey)
	}
	// A source may send versions out of order; an older one never replaces a newer.
	if b.newest != nil && !it.Created.IsZero() && it.Created.Before(*b.newest) {
		return out, nil
	}
	if b.seen != nil {
		seenHash, err := cardhash.ContentHash(b.seen)
		if err != nil {
			return Imported{}, err
		}
		if seenHash == r.contentHash && b.seenAvatar == e.AvatarBlob {
			return out, nil
		}
	}
	headKey, err := l.AvatarKey(ctx, headAvatar)
	if err != nil {
		return Imported{}, err
	}
	// Data and avatar merge apart: the owner's avatar survives a text update.
	ownAvatar := headKey != b.avatarKey
	if headHash != b.acceptedHash || (ownAvatar && newKey != b.avatarKey) {
		_, err := tx.Exec(ctx, `UPDATE binding SET seen_data = $2, seen_avatar = nullif($3, ''), seen_created = $4,
			remote_state = 'changed', checked_at = now() WHERE id = $1`, b.id, e.Data, e.AvatarBlob, dated(it.Created))
		out.Outcome = Pending
		if b.autosync {
			out.Autosync = []int64{b.id}
		}
		return out, err
	}
	if ownAvatar {
		r.avatar = nil
		if headAvatar != "" {
			r.avatar = &headAvatar
		}
	}
	rev, err := s.insertRevision(ctx, tx, out.ID, &out.RevisionID, r)
	if err != nil {
		return Imported{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE binding SET accepted_data = $2, accepted_hash = $3, avatar_hash = $4, accepted_created = $5,
			seen_data = NULL, seen_avatar = NULL, seen_created = NULL, remote_state = 'unchanged', checked_at = now(),
			version = version + 1
		WHERE id = $1`, b.id, e.Data, r.contentHash, newKey, dated(it.Created)); err != nil {
		return Imported{}, err
	}
	out.Outcome, out.RevisionID = Revised, rev.ID
	return out, s.detectUnless(ctx, tx, it, out.ID)
}

// withdrawUpdate drops the entity's waiting update when the item is the
// accepted copy again, since the source took its change back.
func withdrawUpdate(ctx context.Context, tx pgx.Tx, k *Kind, it PluginItem, id int64, hash, avatarKey string) error {
	_, err := tx.Exec(ctx, `UPDATE binding SET seen_data = NULL, seen_avatar = NULL, seen_created = NULL, remote_state = 'unchanged',
			checked_at = now()
		WHERE instance_id = $1 AND `+k.FK+` = $2 AND imported AND remote_state = 'changed'
			AND accepted_hash = $3 AND coalesce(avatar_hash, '') = $4`, it.InstanceID, id, hash, avatarKey)
	return err
}

// importBinding reads the entity's import binding, making one for an entity
// imported before they existed: its accepted copy is then the newest
// revision this plugin wrote, else the first.
func (l *Library) importBinding(ctx context.Context, tx pgx.Tx, s *Entities, it PluginItem, id, head int64) (importBinding, error) {
	var b importBinding
	err := tx.QueryRow(ctx, `SELECT id, accepted_hash, coalesce(avatar_hash, ''), seen_data, coalesce(seen_avatar, ''), autosync,
			greatest(accepted_created, seen_created)
		FROM binding WHERE instance_id = $1 AND `+s.k.FK+` = $2 AND imported FOR UPDATE`, it.InstanceID, id).
		Scan(&b.id, &b.acceptedHash, &b.avatarKey, &b.seen, &b.seenAvatar, &b.autosync, &b.newest)
	if !errors.Is(err, pgx.ErrNoRows) {
		return b, mapErr(err)
	}
	var data json.RawMessage
	var avatar string
	err = tx.QueryRow(ctx, `SELECT data, content_hash, coalesce(avatar_blob, '') FROM `+s.k.RevTable+` WHERE `+s.k.FK+` = $1
		ORDER BY source = $2 DESC, CASE WHEN source = $2 THEN id END DESC, id LIMIT 1`, id, it.Source).
		Scan(&data, &b.acceptedHash, &avatar)
	if err != nil {
		return b, err
	}
	if b.avatarKey, err = l.AvatarKey(ctx, avatar); err != nil {
		return b, err
	}
	// No date: an earlier core stored the revision's import time, not the source's.
	b.id, err = l.bindImport(ctx, tx, s.k, it, id, head, data, b.acceptedHash, avatar, time.Time{})
	return b, err
}

// bindImport adds the entity's import binding with data as the accepted copy.
func (l *Library) bindImport(ctx context.Context, tx pgx.Tx, k *Kind, it PluginItem, id, head int64,
	data json.RawMessage, hash, avatar string, created time.Time) (int64, error) {
	key, err := l.AvatarKey(ctx, avatar)
	if err != nil {
		return 0, err
	}
	var bindingID int64
	if err := tx.QueryRow(ctx, `INSERT INTO binding (instance_id, `+k.FK+`, remote_id, remote_state, accepted_data,
			accepted_hash, avatar_hash, accepted_created, checked_at, push, imported)
		VALUES ($1, $2, $3, 'unchanged', $4, $5, $6, $7, now(), false, true) RETURNING id`,
		it.InstanceID, id, it.Provenance, data, hash, key, dated(created)).Scan(&bindingID); err != nil {
		return 0, mapErr(err)
	}
	// binding_status needs a push row; a pull-only binding is never ahead of it.
	_, err = tx.Exec(ctx, `INSERT INTO push (binding_id, kind, revision_id, pushed_hash, remote_id) VALUES ($1, 'bind', $2, $3, $4)`,
		bindingID, head, hash, it.Provenance)
	return bindingID, err
}

// dated is t, or nil for no date.
func dated(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// detectUnless queues the entity for source detection unless it.SkipDetect is set.
func (s *Entities) detectUnless(ctx context.Context, tx pgx.Tx, it PluginItem, id int64) error {
	if it.SkipDetect {
		return nil
	}
	return s.queueDetect(ctx, tx, id)
}

// ImportCardTx adds a CCv3 card as a new card inside the caller's
// transaction, with its character_book as a new linked lorebook. Unlike
// ImportCard it always creates, since the caller asked for a new copy and its
// transaction makes a retry unnecessary. avatar is a blob's sha256, or empty.
// bookSources are recorded on the lorebook. It returns the card's head revision.
func (l *Library) ImportCardTx(ctx context.Context, tx pgx.Tx, provenance, source string, card json.RawMessage,
	avatar string, bookSources []Source) (Revision, error) {
	name, data, book, err := SplitCard(card)
	if err != nil {
		return Revision{}, err
	}
	_, rev, err := l.Cards.CreateFromNameTx(ctx, tx, name, provenance,
		Edit{Data: data, AvatarBlob: avatar, Source: source, Message: "Imported"})
	if err != nil || book == nil {
		return rev, err
	}
	lb, _, err := l.Lorebooks.CreateFromNameTx(ctx, tx, cmp.Or(DataName(book), name+" lorebook"), provenance,
		Edit{Data: book, Source: source, Message: "Split from imported card"})
	if err != nil {
		return Revision{}, err
	}
	if err := l.Lorebooks.RecordSourcesTx(ctx, tx, lb.ID, bookSources); err != nil {
		return Revision{}, err
	}
	return rev, l.LinkLorebookTx(ctx, tx, rev.EntityID, lb.ID)
}
