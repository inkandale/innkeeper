package library

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/db"
)

// SavedSearch is a named filter the owner opens to browse, and rules act on.
type SavedSearch struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Filter Filter `json:"filter"`
	Sort   string `json:"sort" doc:"The sort to open it with, as search takes it; rules ignore it."`
	// InstanceID is the plugin instance that shipped it.
	InstanceID *int64    `json:"instance_id,omitempty" doc:"The plugin instance that shipped it. Deleting the instance deletes it."`
	CreatedAt  time.Time `json:"created_at"`
}

// CheckFilter reports whether search accepts f, with browsable as
// Query.Browsable for the enrichment terms that name no instance.
func CheckFilter(f Filter, browsable map[string][]Scorer) error {
	_, _, _, err := compile(Card, Query{Filter: &f, Browsable: browsable})
	return err
}

const savedSearchCols = `id, name, filter, sort, instance_id, created_at`

func scanSavedSearch(row pgx.Row) (SavedSearch, error) {
	var s SavedSearch
	err := row.Scan(&s.ID, &s.Name, &s.Filter, &s.Sort, &s.InstanceID, &s.CreatedAt)
	return s, err
}

func savedSearchErr(err error) error {
	switch {
	case db.IsCode(err, db.UniqueViolation):
		return ErrNameTaken
	case db.IsCode(err, db.CheckViolation):
		return fmt.Errorf("%w: a saved search needs a name without spaces around it", ErrInvalid)
	}
	return mapErr(err)
}

// CreateSavedSearch saves a search the owner made.
func (l *Library) CreateSavedSearch(ctx context.Context, name string, f Filter, sort string) (SavedSearch, error) {
	s, err := scanSavedSearch(l.pool.QueryRow(ctx, `INSERT INTO saved_search (name, filter, sort) VALUES ($1, $2, $3)
		RETURNING `+savedSearchCols, name, f, sort))
	return s, savedSearchErr(err)
}

// UpdateSavedSearch changes a saved search. Nil leaves a field as it is.
func (l *Library) UpdateSavedSearch(ctx context.Context, id int64, name *string, f *Filter, sort *string) (SavedSearch, error) {
	s, err := scanSavedSearch(l.pool.QueryRow(ctx, `UPDATE saved_search
		SET name = coalesce($2, name), filter = coalesce($3, filter), sort = coalesce($4, sort)
		WHERE id = $1 RETURNING `+savedSearchCols, id, name, f, sort))
	return s, savedSearchErr(err)
}

// DeleteSavedSearch deletes a saved search and the rules on it.
func (l *Library) DeleteSavedSearch(ctx context.Context, id int64) error {
	tag, err := l.pool.Exec(ctx, `DELETE FROM saved_search WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteInstanceSearches deletes the saved searches instance shipped, and the rules on them.
func (l *Library) DeleteInstanceSearches(ctx context.Context, instance int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM saved_search WHERE instance_id = $1`, instance)
	return err
}

// GetSavedSearch returns one saved search.
func (l *Library) GetSavedSearch(ctx context.Context, id int64) (SavedSearch, error) {
	s, err := scanSavedSearch(l.pool.QueryRow(ctx, `SELECT `+savedSearchCols+` FROM saved_search WHERE id = $1`, id))
	return s, mapErr(err)
}

// SavedSearches returns every saved search, by name.
func (l *Library) SavedSearches(ctx context.Context) ([]SavedSearch, error) {
	rows, err := l.pool.Query(ctx, `SELECT `+savedSearchCols+` FROM saved_search ORDER BY lower(name), id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (SavedSearch, error) { return scanSavedSearch(row) })
}
