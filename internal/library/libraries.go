package library

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
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

// CreateLibrary adds an empty library. Its name is trimmed.
func (l *Library) CreateLibrary(ctx context.Context, name string) (LibrarySummary, error) {
	var id int64
	if err := l.pool.QueryRow(ctx, `INSERT INTO library (name) VALUES ($1) RETURNING id`, strings.TrimSpace(name)).Scan(&id); err != nil {
		return LibrarySummary{}, libraryErr(err)
	}
	return l.GetLibrary(ctx, id)
}

// RenameLibrary renames a library. The name is trimmed.
func (l *Library) RenameLibrary(ctx context.Context, id int64, name string) (LibrarySummary, error) {
	tag, err := l.pool.Exec(ctx, `UPDATE library SET name = $2 WHERE id = $1`, id, strings.TrimSpace(name))
	if err != nil {
		return LibrarySummary{}, libraryErr(err)
	}
	if tag.RowsAffected() == 0 {
		return LibrarySummary{}, ErrNotFound
	}
	return l.GetLibrary(ctx, id)
}

// deleteBatch is how many cards or lorebooks one transaction of
// DeleteLibrary or PurgeArchived deletes.
const deleteBatch = 500

// DeleteLibrary deletes a library with its cards, lorebooks, and
// collections. It works in batches, each its own transaction, so a run that
// stops part way can run again. progress hears each batch's kind and size.
// Blobs stay until garbage collection finds them unused.
func (l *Library) DeleteLibrary(ctx context.Context, id int64, progress func(k *Kind, n int)) error {
	if id == DefaultLibrary {
		return fmt.Errorf("%w: the default library can't be deleted", ErrInvalid)
	}
	// An import still writing to the library can add entities after the
	// batches; the library row's delete then fails, and the batches run again.
	for range 3 {
		for _, s := range []*Entities{l.Cards, l.Lorebooks} {
			for {
				n, err := s.deleteSome(ctx, `t.library_id = $1`, id)
				if err != nil {
					return fmt.Errorf("delete %s of library %d: %w", s.k.Plural, id, err)
				}
				if n == 0 {
					break
				}
				progress(s.k, n)
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

// deleteSome deletes up to deleteBatch entities that match where, with arg
// as $1, and what hangs off them, and returns how many it deleted.
func (s *Entities) deleteSome(ctx context.Context, where string, arg any) (int, error) {
	var n int
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		// The lock waits out a concurrent change, then rechecks where on the row as it now is.
		rows, err := tx.Query(ctx, `SELECT id FROM `+s.k.Table+` t WHERE `+where+` ORDER BY id LIMIT $2 FOR UPDATE`, arg, deleteBatch)
		if err != nil {
			return err
		}
		ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(ids) == 0 {
			return err
		}
		n = len(ids)
		return s.deleteIn(ctx, tx, ids)
	})
	return n, err
}

// deleteIn deletes the entities ids and what hangs off them in tx.
func (s *Entities) deleteIn(ctx context.Context, tx pgx.Tx, ids []int64) error {
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
}

// DeleteCards deletes the cards ids with their revisions and what hangs off
// them, including bindings: an app's copy stays in the app, and archiving
// with unpublish is how to remove it first. IDs that don't exist are
// skipped. It returns how many it deleted.
func (l *Library) DeleteCards(ctx context.Context, ids []int64) (int, error) {
	var deleted int
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id FROM card WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
		if err != nil {
			return err
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil || len(found) == 0 {
			return err
		}
		deleted = len(found)
		return l.Cards.deleteIn(ctx, tx, found)
	})
	return deleted, err
}

// Purge predicates, with $1 the days: cards archived more than that long
// ago, and such lorebooks that no live card links to.
const (
	purgeableCards     = `t.archived_at < now() - make_interval(days => $1)`
	purgeableLorebooks = `t.archived_at < now() - make_interval(days => $1)
		AND NOT EXISTS (SELECT 1 FROM card_lorebook cl JOIN card c ON c.id = cl.card_id
			WHERE cl.lorebook_id = t.id AND c.archived_at IS NULL)`
)

// Purgeable counts the cards and lorebooks PurgeArchived would delete for days.
func (l *Library) Purgeable(ctx context.Context, days int) (cards, lorebooks int64, err error) {
	err = l.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM card t WHERE `+purgeableCards+`),
		(SELECT count(*) FROM lorebook t WHERE `+purgeableLorebooks+`)`, days).Scan(&cards, &lorebooks)
	return cards, lorebooks, err
}

// PurgeArchived deletes the cards archived more than days ago, then the
// archived lorebooks no live card links to. Each batch locks and rechecks
// its rows, so one unarchived or linked meanwhile stays. progress hears each
// batch's kind and size.
func (l *Library) PurgeArchived(ctx context.Context, days int, progress func(k *Kind, n int)) error {
	if days < 1 {
		return fmt.Errorf("%w: purge days must be at least 1", ErrInvalid)
	}
	// Cards first, so a lorebook only they linked to qualifies too.
	for _, p := range []struct {
		s     *Entities
		where string
	}{{l.Cards, purgeableCards}, {l.Lorebooks, purgeableLorebooks}} {
		for {
			n, err := p.s.deleteSome(ctx, p.where, days)
			if err != nil {
				return fmt.Errorf("purge archived %s: %w", p.s.k.Plural, err)
			}
			if n == 0 {
				break
			}
			progress(p.s.k, n)
		}
	}
	return nil
}

// deletes are the statements that delete the entities $1 of kind k, and
// what every kind hangs off them, once deleteLinks has run.
func (k *Kind) deletes() []string {
	return []string{
		`DELETE FROM binding WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM ` + k.SourceTable + ` WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM card_lorebook WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM source_detect WHERE kind = '` + k.Name + `' AND entity_id = ANY($1)`,
		// A live variant moves up to its nearest ancestor that survives, so its family stays connected.
		`WITH RECURSIVE up AS (
			SELECT t.id AS child, t.variant_of_id AS anc FROM ` + k.Table + ` t
			WHERE t.variant_of_id = ANY($1) AND NOT t.id = ANY($1)
			UNION ALL
			SELECT up.child, p.variant_of_id FROM up JOIN ` + k.Table + ` p ON p.id = up.anc WHERE up.anc = ANY($1)
		)
		UPDATE ` + k.Table + ` t SET variant_of_id = up.anc
		FROM up WHERE up.child = t.id AND (up.anc IS NULL OR NOT up.anc = ANY($1))`,
		`DELETE FROM ` + k.RevTable + ` WHERE ` + k.FK + ` = ANY($1)`,
		`DELETE FROM ` + k.Table + ` WHERE id = ANY($1)`,
	}
}

// LibrarySetting is library id's override of a core setting, or "" when it
// has none.
func (l *Library) LibrarySetting(ctx context.Context, id int64, key string) (string, error) {
	var v string
	err := l.pool.QueryRow(ctx, `SELECT value FROM library_setting WHERE library_id = $1 AND key = $2`, id, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// SetLibrarySetting stores library id's override of a core setting; ""
// removes it.
func (l *Library) SetLibrarySetting(ctx context.Context, id int64, key, value string) error {
	if value == "" {
		_, err := l.pool.Exec(ctx, `DELETE FROM library_setting WHERE library_id = $1 AND key = $2`, id, key)
		return err
	}
	_, err := l.pool.Exec(ctx, `INSERT INTO library_setting (library_id, key, value) VALUES ($1, $2, $3)
		ON CONFLICT (library_id, key) DO UPDATE SET value = EXCLUDED.value`, id, key, value)
	return mapErr(err)
}
