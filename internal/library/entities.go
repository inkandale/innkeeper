package library

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/inkandale/innkeeper/cardhash"
	"github.com/inkandale/innkeeper/internal/db"
	"github.com/inkandale/innkeeper/internal/merge"
)

// Kind is what differs between cards and lorebooks. Their tables have the
// same shape, so one implementation serves both.
type Kind struct {
	// Name is the kind in the protocol and the API; Plural is its path segment.
	Name, Plural                 string
	Table, RevTable, SourceTable string
	// FK is the entity's ID column in revisions, sources, and bindings.
	FK string
	// Schema is how a diff splits the kind's data into fields.
	Schema  merge.Schema
	simhash func([]byte) (int64, bool, error)
	// Top-level keys data may not have.
	forbid []string
	// forkCopies copy the kind's links from entity $1 to its new variant $2.
	forkCopies []string
	// deleteLinks delete the rows only this kind hangs off entities $1.
	deleteLinks []string
}

var (
	Card = &Kind{Name: "card", Plural: "cards", Table: "card", RevTable: "card_revision", SourceTable: "card_source",
		FK: "card_id", Schema: merge.Card, simhash: cardhash.CardSimhash, forbid: []string{"character_book"},
		forkCopies: []string{
			// Only collections of the variant's library, which a fork into another library doesn't share.
			`INSERT INTO collection_card (collection_id, card_id) SELECT cc.collection_id, $2 FROM collection_card cc
				JOIN collection co ON co.id = cc.collection_id
				WHERE cc.card_id = $1 AND co.library_id = (SELECT library_id FROM card WHERE id = $2)`,
			`INSERT INTO card_lorebook (card_id, lorebook_id) SELECT $2, lorebook_id FROM card_lorebook WHERE card_id = $1`,
		},
		deleteLinks: []string{
			`DELETE FROM collection_card WHERE card_id = ANY($1)`,
		}}
	Lorebook = &Kind{Name: "lorebook", Plural: "lorebooks", Table: "lorebook", RevTable: "lorebook_revision",
		SourceTable: "lorebook_source", FK: "lorebook_id", Schema: merge.Lorebook, simhash: cardhash.LorebookSimhash}
	// Kinds is every kind the library holds.
	Kinds = []*Kind{Card, Lorebook}
)

// KindNamed returns the kind called name, or false if there is none.
func KindNamed(name string) (*Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return nil, false
}

// Entities stores one kind of versioned entity: cards or lorebooks.
type Entities struct {
	pool *pgxpool.Pool
	k    *Kind
}

// Kind is what this store holds.
func (s *Entities) Kind() *Kind { return s.k }

// Entity is a card or lorebook's current state.
type Entity struct {
	ID             int64
	LibraryID      int64
	Slug           string
	HeadRevisionID int64
	ArchivedAt     *time.Time
	// ArchivedReason is why the hide job archived it, or empty.
	ArchivedReason string
	Provenance     string
	CreatedAt      time.Time
}

// Revision is one immutable version of a card or lorebook.
type Revision struct {
	ID          int64
	EntityID    int64
	ParentID    *int64
	Data        json.RawMessage
	AvatarBlob  *string
	Overrides   json.RawMessage
	ContentHash string
	TextSimhash *int64
	Message     string
	Source      string
	CreatedAt   time.Time
}

// Edit is the content of a new revision.
type Edit struct {
	// Data is the CCv3 data object: a card's without character_book, or a
	// character_book itself.
	Data json.RawMessage
	// AvatarBlob is a blob's sha256, or empty for none.
	AvatarBlob string
	// Overrides maps plugin instance IDs to JSON merge patches. Nil means none.
	Overrides json.RawMessage
	Message   string
	// Source is ui, mcp, reconcile, or plugin:<name>.
	Source string
	// Created is the revision's created_at; zero means now.
	Created time.Time
}

const entityCols = `id, library_id, slug, coalesce(head_revision_id, 0), archived_at, coalesce(archived_reason, ''), provenance, created_at`

func scanEntity(row pgx.Row) (Entity, error) {
	var e Entity
	err := row.Scan(&e.ID, &e.LibraryID, &e.Slug, &e.HeadRevisionID, &e.ArchivedAt, &e.ArchivedReason, &e.Provenance, &e.CreatedAt)
	return e, mapErr(err)
}

func (s *Entities) revCols() string {
	return `id, ` + s.k.FK + `, parent_id, data, avatar_blob, overrides, content_hash, text_simhash, message, source, created_at`
}

func scanRevision(row pgx.Row) (Revision, error) {
	var r Revision
	err := row.Scan(&r.ID, &r.EntityID, &r.ParentID, &r.Data, &r.AvatarBlob, &r.Overrides,
		&r.ContentHash, &r.TextSimhash, &r.Message, &r.Source, &r.CreatedAt)
	return r, mapErr(err)
}

// revisionRow is an Edit checked and hashed, ready to insert.
type revisionRow struct {
	data, overrides []byte
	avatar          *string
	contentHash     string
	simhash         *int64
	message, source string
	created         *time.Time
}

func (s *Entities) prepare(e Edit) (revisionRow, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &top); err != nil || top == nil {
		return revisionRow{}, fmt.Errorf("%w: data must be a JSON object", ErrInvalid)
	}
	for _, k := range s.k.forbid {
		if _, ok := top[k]; ok {
			return revisionRow{}, fmt.Errorf("%w: %s data may not contain %s", ErrInvalid, s.k.Table, k)
		}
	}
	r := revisionRow{data: e.Data, overrides: e.Overrides, message: e.Message, source: e.Source}
	if r.overrides == nil {
		r.overrides = []byte(`{}`)
	}
	var overrides map[string]json.RawMessage
	if err := json.Unmarshal(r.overrides, &overrides); err != nil || overrides == nil {
		return revisionRow{}, fmt.Errorf("%w: overrides must be a JSON object", ErrInvalid)
	}
	if e.AvatarBlob != "" {
		r.avatar = &e.AvatarBlob
	}
	if !e.Created.IsZero() {
		r.created = &e.Created
	}
	var err error
	if r.contentHash, err = cardhash.ContentHash(e.Data); err != nil {
		return revisionRow{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	h, ok, err := s.k.simhash(e.Data)
	if err != nil {
		return revisionRow{}, fmt.Errorf("%w: simhash: %w", ErrInvalid, err)
	}
	if ok {
		r.simhash = &h
	}
	return r, nil
}

func (s *Entities) insertRevision(ctx context.Context, tx pgx.Tx, id int64, parent *int64, r revisionRow) (Revision, error) {
	rev, err := scanRevision(tx.QueryRow(ctx, `INSERT INTO `+s.k.RevTable+` (`+s.k.FK+`, parent_id, data, avatar_blob,
		overrides, content_hash, text_simhash, message, source, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, coalesce($10, now())) RETURNING `+s.revCols(),
		id, parent, r.data, r.avatar, r.overrides, r.contentHash, r.simhash, r.message, r.source, r.created))
	if err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE `+s.k.Table+` SET head_revision_id = $1 WHERE id = $2`, rev.ID, id); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

// Create adds a new entity in libraryID with e as its first revision.
func (s *Entities) Create(ctx context.Context, libraryID int64, slug, provenance string, e Edit) (Entity, Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	var ent Entity
	var rev Revision
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		ent, rev, err = s.create(ctx, tx, slug, libraryID, provenance, r)
		return err
	})
	return ent, rev, err
}

// create inserts the entity inside a savepoint, so a taken slug leaves tx
// usable for another try.
func (s *Entities) create(ctx context.Context, tx pgx.Tx, slug string, libraryID int64, provenance string, r revisionRow) (Entity, Revision, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	defer sp.Rollback(ctx)
	ent, err := scanEntity(sp.QueryRow(ctx, `INSERT INTO `+s.k.Table+` (slug, library_id, provenance) VALUES ($1, $2, $3) RETURNING `+entityCols,
		slug, libraryID, provenance))
	if db.IsCode(err, db.UniqueViolation) {
		return Entity{}, Revision{}, ErrSlugTaken
	}
	if err != nil {
		return Entity{}, Revision{}, err
	}
	rev, err := s.insertRevision(ctx, sp, ent.ID, nil, r)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	ent.HeadRevisionID = rev.ID
	return ent, rev, sp.Commit(ctx)
}

// Update adds a revision on top of base. It returns ErrConflict if base is no
// longer the head.
func (s *Entities) Update(ctx context.Context, id, base int64, e Edit) (Revision, error) {
	var rev Revision
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) (err error) {
		rev, err = s.UpdateTx(ctx, tx, id, base, e)
		return err
	})
	return rev, err
}

// UpdateTx is Update inside the caller's transaction, which the caller commits.
func (s *Entities) UpdateTx(ctx context.Context, tx pgx.Tx, id, base int64, e Edit) (Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Revision{}, err
	}
	var head int64
	if err := tx.QueryRow(ctx, `SELECT head_revision_id FROM `+s.k.Table+` WHERE id = $1 FOR UPDATE`, id).Scan(&head); err != nil {
		return Revision{}, mapErr(err)
	}
	if head != base {
		return Revision{}, ErrConflict
	}
	return s.insertRevision(ctx, tx, id, &base, r)
}

// Get returns the entity with this ID.
func (s *Entities) Get(ctx context.Context, id int64) (Entity, error) {
	return scanEntity(s.pool.QueryRow(ctx, `SELECT `+entityCols+` FROM `+s.k.Table+` WHERE id = $1`, id))
}

// Revision returns one revision by its ID.
func (s *Entities) Revision(ctx context.Context, revID int64) (Revision, error) {
	return scanRevision(s.pool.QueryRow(ctx, `SELECT `+s.revCols()+` FROM `+s.k.RevTable+` WHERE id = $1`, revID))
}

// History returns every revision of an entity, newest first.
func (s *Entities) History(ctx context.Context, id int64) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+s.revCols()+` FROM `+s.k.RevTable+` WHERE `+s.k.FK+` = $1 ORDER BY id DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Revision, error) { return scanRevision(row) })
}

// SetArchived archives or unarchives an entity for the owner, which takes it
// out of the hide job's hands.
func (s *Entities) SetArchived(ctx context.Context, id int64, archived bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE `+s.k.Table+`
		SET archived_at = CASE WHEN $2 THEN coalesce(archived_at, now()) END, archived_reason = NULL WHERE id = $1`, id, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
