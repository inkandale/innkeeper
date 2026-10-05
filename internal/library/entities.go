package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidgibbons/innkeeper/internal/cardhash"
)

// kind holds what differs between cards and lorebooks. Their tables have the
// same shape, so one implementation serves both.
type kind struct {
	table, revTable, fk string
	simhash             func([]byte) (int64, bool, error)
	// Top-level keys data may not have.
	forbid []string
}

// Entities stores one kind of versioned entity: cards or lorebooks.
type Entities struct {
	pool *pgxpool.Pool
	k    kind
}

// Entity is a card or lorebook's current state.
type Entity struct {
	ID             int64
	Slug           string
	HeadRevisionID int64
	ArchivedAt     *time.Time
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
	// Source is ui, mcp, adopt, or plugin:<name>.
	Source string
}

const entityCols = `id, slug, coalesce(head_revision_id, 0), archived_at, provenance, created_at`

func scanEntity(row pgx.Row) (Entity, error) {
	var e Entity
	err := row.Scan(&e.ID, &e.Slug, &e.HeadRevisionID, &e.ArchivedAt, &e.Provenance, &e.CreatedAt)
	return e, mapErr(err)
}

func (s *Entities) revCols() string {
	return `id, ` + s.k.fk + `, parent_id, data, avatar_blob, overrides, content_hash, text_simhash, message, source, created_at`
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
}

func (s *Entities) prepare(e Edit) (revisionRow, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(e.Data, &top); err != nil || top == nil {
		return revisionRow{}, fmt.Errorf("%w: data must be a JSON object", ErrInvalid)
	}
	for _, k := range s.k.forbid {
		if _, ok := top[k]; ok {
			return revisionRow{}, fmt.Errorf("%w: %s data may not contain %s", ErrInvalid, s.k.table, k)
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
	rev, err := scanRevision(tx.QueryRow(ctx, `INSERT INTO `+s.k.revTable+` (`+s.k.fk+`, parent_id, data, avatar_blob,
		overrides, content_hash, text_simhash, message, source)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING `+s.revCols(),
		id, parent, r.data, r.avatar, r.overrides, r.contentHash, r.simhash, r.message, r.source))
	if err != nil {
		return Revision{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE `+s.k.table+` SET head_revision_id = $1 WHERE id = $2`, rev.ID, id); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

// Create adds a new entity with e as its first revision.
func (s *Entities) Create(ctx context.Context, slug, provenance string, e Edit) (Entity, Revision, error) {
	r, err := s.prepare(e)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	defer tx.Rollback(ctx)
	ent, rev, err := s.create(ctx, tx, slug, provenance, r)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	return ent, rev, tx.Commit(ctx)
}

// create inserts the entity inside a savepoint, so a taken slug leaves tx
// usable for another try.
func (s *Entities) create(ctx context.Context, tx pgx.Tx, slug, provenance string, r revisionRow) (Entity, Revision, error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return Entity{}, Revision{}, err
	}
	defer sp.Rollback(ctx)
	ent, err := scanEntity(sp.QueryRow(ctx, `INSERT INTO `+s.k.table+` (slug, provenance) VALUES ($1, $2) RETURNING `+entityCols,
		slug, provenance))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
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
	r, err := s.prepare(e)
	if err != nil {
		return Revision{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx)
	var head int64
	if err := tx.QueryRow(ctx, `SELECT head_revision_id FROM `+s.k.table+` WHERE id = $1 FOR UPDATE`, id).Scan(&head); err != nil {
		return Revision{}, mapErr(err)
	}
	if head != base {
		return Revision{}, ErrConflict
	}
	rev, err := s.insertRevision(ctx, tx, id, &base, r)
	if err != nil {
		return Revision{}, err
	}
	return rev, tx.Commit(ctx)
}

// Get returns the entity with this ID.
func (s *Entities) Get(ctx context.Context, id int64) (Entity, error) {
	return scanEntity(s.pool.QueryRow(ctx, `SELECT `+entityCols+` FROM `+s.k.table+` WHERE id = $1`, id))
}

// GetBySlug returns the entity with this slug.
func (s *Entities) GetBySlug(ctx context.Context, slug string) (Entity, error) {
	return scanEntity(s.pool.QueryRow(ctx, `SELECT `+entityCols+` FROM `+s.k.table+` WHERE slug = $1`, slug))
}

// Revision returns one revision by its ID.
func (s *Entities) Revision(ctx context.Context, revID int64) (Revision, error) {
	return scanRevision(s.pool.QueryRow(ctx, `SELECT `+s.revCols()+` FROM `+s.k.revTable+` WHERE id = $1`, revID))
}

// History returns every revision of an entity, newest first.
func (s *Entities) History(ctx context.Context, id int64) ([]Revision, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+s.revCols()+` FROM `+s.k.revTable+` WHERE `+s.k.fk+` = $1 ORDER BY id DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Revision, error) { return scanRevision(row) })
}

// SetArchived archives or unarchives an entity.
func (s *Entities) SetArchived(ctx context.Context, id int64, archived bool) error {
	tag, err := s.pool.Exec(ctx, `UPDATE `+s.k.table+`
		SET archived_at = CASE WHEN $2 THEN coalesce(archived_at, now()) END WHERE id = $1`, id, archived)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
