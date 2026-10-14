package library

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/cardhash"
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

// Import adds an entity from a plugin, with a slug made from name, unless an
// entity with the same provenance has ever had this content. It returns the
// entity's ID and whether it created it, so importing the same item twice,
// or again after the owner edited it, adds nothing.
//
// ponytail: a source item whose content changed is imported as a new entity,
// not a new revision of the old one.
func (s *Entities) Import(ctx context.Context, provenance, name string, e Edit) (int64, bool, error) {
	r, err := s.prepare(e)
	if err != nil {
		return 0, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)
	// Two jobs can import the same item at once; without the lock both would
	// miss the lookup and create a card each.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, s.k.Table+":"+provenance); err != nil {
		return 0, false, err
	}
	var id int64
	err = tx.QueryRow(ctx, `SELECT e.id FROM `+s.k.Table+` e WHERE e.provenance = $1 AND EXISTS (
		SELECT 1 FROM `+s.k.RevTable+` r WHERE r.`+s.k.FK+` = e.id AND r.content_hash = $2)
		ORDER BY e.id LIMIT 1`, provenance, r.contentHash).Scan(&id)
	if err == nil {
		// Also on a repeat, so a run that stopped before detecting finishes.
		return id, false, s.DetectSources(ctx, id, e.Data)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	ent, _, err := s.createFromName(ctx, tx, name, provenance, r)
	if err != nil {
		return 0, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, false, err
	}
	return ent.ID, true, s.DetectSources(ctx, ent.ID, e.Data)
}

// CreateFromName creates an entity with a free slug made from name.
func (s *Entities) CreateFromName(ctx context.Context, name, provenance string, e Edit) (Entity, Revision, error) {
	var ent Entity
	var rev Revision
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		ent, rev, err = s.CreateFromNameTx(ctx, tx, name, provenance, e)
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
	return s.createFromName(ctx, tx, name, provenance, r)
}

func (s *Entities) createFromName(ctx context.Context, tx pgx.Tx, name, provenance string, r revisionRow) (Entity, Revision, error) {
	// Retried because another writer can take the same slug first.
	for range 5 {
		slug, err := s.freeSlug(ctx, tx, Slugify(name))
		if err != nil {
			return Entity{}, Revision{}, err
		}
		ent, rev, err := s.create(ctx, tx, slug, provenance, r)
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

// ImportCard adds a CCv3 card from a plugin as Import does, with its
// character_book as a linked lorebook. avatar is a file in the blob store's
// tmp folder, or empty; it is moved into the store. It returns the card's ID
// and whether it created it.
func (l *Library) ImportCard(ctx context.Context, provenance, source string, card json.RawMessage, avatar string) (int64, bool, error) {
	name, data, book, err := SplitCard(card)
	if err != nil {
		return 0, false, err
	}
	var avatarSHA string
	if avatar != "" {
		b, err := l.IngestBlob(ctx, avatar)
		if err != nil {
			return 0, false, fmt.Errorf("avatar: %w", err)
		}
		avatarSHA = b.SHA256
	}
	// The lorebook goes first, and linking is idempotent, so a run
	// interrupted partway leaves nothing a retry can't finish.
	var lorebookID int64
	if book != nil {
		lorebookID, _, err = l.Lorebooks.Import(ctx, provenance, cmp.Or(DataName(book), name+" lorebook"),
			Edit{Data: book, Source: source, Message: "Split from imported card"})
		if err != nil {
			return 0, false, err
		}
	}
	cardID, created, err := l.Cards.Import(ctx, provenance, name,
		Edit{Data: data, AvatarBlob: avatarSHA, Source: source, Message: "Imported"})
	if err != nil {
		return 0, false, err
	}
	if lorebookID != 0 {
		if err := l.LinkLorebook(ctx, cardID, lorebookID); err != nil {
			return 0, false, err
		}
	}
	return cardID, created, nil
}

// PluginItem is a card or lorebook a plugin sent.
type PluginItem struct {
	Kind               *Kind
	Provenance, Source string
	// Data is a CCv3 card or a lorebook.
	Data json.RawMessage
	// Avatar is a card's avatar, a file in the blob store's tmp folder, or empty.
	Avatar string
	// Name is a lorebook's name when its data has none.
	Name string
}

// ImportItem adds a card as ImportCard does, or a lorebook as Import does.
// It returns the entity's ID and whether it created it.
func (l *Library) ImportItem(ctx context.Context, it PluginItem) (int64, bool, error) {
	if it.Kind == Card {
		return l.ImportCard(ctx, it.Provenance, it.Source, it.Data, it.Avatar)
	}
	// An avatar sent with a lorebook stays in blob_tmp until garbage collection.
	return l.Lorebooks.Import(ctx, it.Provenance, cmp.Or(DataName(it.Data), it.Name),
		Edit{Data: it.Data, Source: it.Source, Message: "Imported"})
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
