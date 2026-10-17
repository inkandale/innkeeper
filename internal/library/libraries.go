package library

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/db"
)

// LibrarySummary is a library with how many cards and lorebooks it holds,
// archived ones included.
type LibrarySummary struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Cards     int       `json:"cards"`
	Lorebooks int       `json:"lorebooks"`
}

const librarySummaryCols = `l.id, l.name, l.created_at,
	(SELECT count(*) FROM card WHERE library_id = l.id)::int,
	(SELECT count(*) FROM lorebook WHERE library_id = l.id)::int`

func libraryErr(err error) error {
	switch {
	case db.IsCode(err, db.UniqueViolation):
		return ErrNameTaken
	case db.IsCode(err, db.CheckViolation):
		return fmt.Errorf("%w: library name may not be empty", ErrInvalid)
	}
	return mapErr(err)
}

// Libraries returns every library, oldest first.
func (l *Library) Libraries(ctx context.Context) ([]LibrarySummary, error) {
	rows, err := l.pool.Query(ctx, `SELECT `+librarySummaryCols+` FROM library l ORDER BY l.id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[LibrarySummary])
}

// GetLibrary returns one library.
func (l *Library) GetLibrary(ctx context.Context, id int64) (LibrarySummary, error) {
	rows, err := l.pool.Query(ctx, `SELECT `+librarySummaryCols+` FROM library l WHERE l.id = $1`, id)
	if err != nil {
		return LibrarySummary{}, err
	}
	s, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByPos[LibrarySummary])
	return s, mapErr(err)
}

// HasLibrary says whether library id exists, without GetLibrary's counts.
func (l *Library) HasLibrary(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM library WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

// CreateLibrary adds an empty library.
func (l *Library) CreateLibrary(ctx context.Context, name string) (LibrarySummary, error) {
	var id int64
	if err := l.pool.QueryRow(ctx, `INSERT INTO library (name) VALUES ($1) RETURNING id`, name).Scan(&id); err != nil {
		return LibrarySummary{}, libraryErr(err)
	}
	return l.GetLibrary(ctx, id)
}

// RenameLibrary renames a library.
func (l *Library) RenameLibrary(ctx context.Context, id int64, name string) (LibrarySummary, error) {
	tag, err := l.pool.Exec(ctx, `UPDATE library SET name = $2 WHERE id = $1`, id, name)
	if err != nil {
		return LibrarySummary{}, libraryErr(err)
	}
	if tag.RowsAffected() == 0 {
		return LibrarySummary{}, ErrNotFound
	}
	return l.GetLibrary(ctx, id)
}

// deleteBatch is how many cards or lorebooks one transaction of
// DeleteLibrary deletes.
const deleteBatch = 500

// DeleteLibrary deletes a library with its cards, lorebooks, and
// collections. It works in batches, each its own transaction, so a run that
// stops part way can run again. progress hears the running count of deleted
// cards and lorebooks. Blobs stay until garbage collection finds them unused.
func (l *Library) DeleteLibrary(ctx context.Context, id int64, progress func(deleted int)) error {
	if id == DefaultLibrary {
		return fmt.Errorf("%w: the default library can't be deleted", ErrInvalid)
	}
	deleted := 0
	// An import still writing to the library can add entities after the
	// batches; the library row's delete then fails, and the batches run again.
	for range 3 {
		for _, s := range []*Entities{l.Cards, l.Lorebooks} {
			for {
				n, err := s.deleteSome(ctx, id)
				if err != nil {
					return fmt.Errorf("delete %s of library %d: %w", s.k.Plural, id, err)
				}
				if n == 0 {
					break
				}
				deleted += n
				progress(deleted)
			}
		}
		var found bool
		err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `DELETE FROM collection WHERE library_id = $1`, id); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `DELETE FROM library WHERE id = $1`, id)
			found = tag.RowsAffected() > 0
			return err
		})
		switch {
		case db.IsCode(err, db.ForeignKeyViolation):
			continue
		case err != nil:
			return err
		case !found:
			return ErrNotFound
		}
		return nil
	}
	return fmt.Errorf("library %d keeps gaining cards or lorebooks; stop what writes to it and delete it again", id)
}

// deleteSome deletes up to deleteBatch entities of library and what hangs off
// them, and returns how many it deleted.
func (s *Entities) deleteSome(ctx context.Context, library int64) (int, error) {
	var n int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM `+s.k.Table+` WHERE library_id = $1 ORDER BY id LIMIT $2`, library, deleteBatch)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(ids) == 0 {
			return err
		}
		n = len(ids)
		// Entities and revisions point at each other, so they are checked at commit.
		if _, err := tx.Exec(ctx, `SET LOCAL innkeeper.delete = 'on'; SET CONSTRAINTS ALL DEFERRED`); err != nil {
			return err
		}
		for _, q := range append(slices.Clone(s.k.deleteLinks), s.k.deletes()...) {
			if _, err := tx.Exec(ctx, q, ids); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// deletes are the statements that delete the entities $1 of kind k, and
// what every kind hangs off them, once deleteLinks has run.
func (k *Kind) deletes() []string {
	return []string{
		`DELETE FROM binding WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM ` + k.SourceTable + ` WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM card_lorebook WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM source_detect WHERE kind = '` + k.Name + `' AND entity_id = ANY($1)`,
		`UPDATE ` + k.Table + ` SET variant_of_id = NULL WHERE variant_of_id = ANY($1)`,
		`DELETE FROM ` + k.RevTable + ` WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM ` + k.Table + ` WHERE id = ANY($1)`,
	}
}
