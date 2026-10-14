package library

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidgibbons/innkeeper/internal/db"
)

// cleanTags trims names and drops empty ones.
func cleanTags(names []string) []string {
	clean := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			clean = append(clean, n)
		}
	}
	return clean
}

// SetCardTags replaces a card's tags. Tag names are trimmed and matched
// case-insensitively; new names create tags.
func (l *Library) SetCardTags(ctx context.Context, cardID int64, names []string) error {
	clean := cleanTags(names)
	tx, err := l.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO tag (name)
		SELECT DISTINCT ON (lower(n)) n FROM unnest($1::text[]) n
		ON CONFLICT (lower(name)) DO NOTHING`, clean); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM card_tag WHERE card_id = $1`, cardID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO card_tag (card_id, tag_id)
		SELECT $1, id FROM tag WHERE lower(name) IN (SELECT lower(n) FROM unnest($2::text[]) n)`,
		cardID, clean); err != nil {
		return mapErr(err)
	}
	return tx.Commit(ctx)
}

// EditCardTags adds and removes names in a card's data.tags as a new
// revision. Names are trimmed and match case-insensitively; an edit that
// changes nothing adds no revision.
func (l *Library) EditCardTags(ctx context.Context, cardID int64, add, remove []string) error {
	add, remove = cleanTags(add), cleanTags(remove)
	return pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		var head int64
		if err := tx.QueryRow(ctx, `SELECT head_revision_id FROM `+l.Cards.Kind().Table+` WHERE id = $1 FOR UPDATE`, cardID).Scan(&head); err != nil {
			return mapErr(err)
		}
		rev, err := scanRevision(tx.QueryRow(ctx, `SELECT `+l.Cards.revCols()+` FROM `+l.Cards.Kind().RevTable+` WHERE id = $1`, head))
		if err != nil {
			return err
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(rev.Data, &data); err != nil {
			return fmt.Errorf("card %d data: %w", cardID, err)
		}
		var old []string
		if raw, ok := data["tags"]; ok {
			if err := json.Unmarshal(raw, &old); err != nil {
				return fmt.Errorf("%w: card %d tags aren't a list of strings", ErrInvalid, cardID)
			}
		}
		tags := editTags(old, add, remove)
		if slices.Equal(tags, old) {
			return nil
		}
		if data["tags"], err = json.Marshal(tags); err != nil {
			return err
		}
		body, err := json.Marshal(data)
		if err != nil {
			return err
		}
		e := Edit{Data: body, Overrides: rev.Overrides, Message: "Edit tags", Source: "ui"}
		if rev.AvatarBlob != nil {
			e.AvatarBlob = *rev.AvatarBlob
		}
		_, err = l.Cards.UpdateTx(ctx, tx, cardID, head, e)
		return err
	})
}

// editTags drops remove from tags, then appends each of add not already
// there. Matching ignores case.
func editTags(tags, add, remove []string) []string {
	out := slices.DeleteFunc(slices.Clone(tags), func(t string) bool { return containsFold(remove, t) })
	for _, a := range add {
		if !containsFold(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}

// CardTags returns a card's tag names in alphabetical order.
func (l *Library) CardTags(ctx context.Context, cardID int64) ([]string, error) {
	rows, err := l.pool.Query(ctx, `SELECT t.name FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
		WHERE ct.card_id = $1 ORDER BY lower(t.name)`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Collection is a named group of cards. A card can be in many.
type Collection struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	// Cards is how many unarchived cards it has. Only Collections sets it.
	Cards int `json:"cards"`
	// Avatars are the first four members' avatar blobs, for a mosaic. Only
	// Collections sets it.
	Avatars []string `json:"avatars,omitempty"`
}

// collectionErr maps constraint violations on collection.
func collectionErr(err error) error {
	switch {
	case db.IsCode(err, db.UniqueViolation):
		return ErrNameTaken
	case db.IsCode(err, db.CheckViolation):
		return fmt.Errorf("%w: collection name may not be empty", ErrInvalid)
	}
	return mapErr(err)
}

// CreateCollection adds an empty collection.
func (l *Library) CreateCollection(ctx context.Context, name, description string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `INSERT INTO collection (name, description) VALUES ($1, $2)
		RETURNING id, name, description, created_at`, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// UpdateCollection renames a collection or changes its description. Nil
// leaves a field as it is.
func (l *Library) UpdateCollection(ctx context.Context, id int64, name, description *string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `UPDATE collection SET name = coalesce($2, name), description = coalesce($3, description)
		WHERE id = $1 RETURNING id, name, description, created_at`, id, name, description).
		Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// DeleteCollection removes a collection. Its cards stay in the library.
func (l *Library) DeleteCollection(ctx context.Context, id int64) error {
	tag, err := l.pool.Exec(ctx, `DELETE FROM collection WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Collections returns every collection with its unarchived card count and mosaic avatars, by name.
func (l *Library) Collections(ctx context.Context) ([]Collection, error) {
	rows, err := l.pool.Query(ctx, `SELECT c.id, c.name, c.description, c.created_at, count(cc.card_id) FILTER (WHERE k.archived_at IS NULL)::int,
			array(SELECT r.avatar_blob FROM collection_card m
				JOIN card k ON k.id = m.card_id JOIN card_revision r ON r.id = k.head_revision_id
				WHERE m.collection_id = c.id AND r.avatar_blob IS NOT NULL AND k.archived_at IS NULL
				ORDER BY m.added_at, m.card_id LIMIT 4)
		FROM collection c LEFT JOIN collection_card cc ON cc.collection_id = c.id
		LEFT JOIN card k ON k.id = cc.card_id
		GROUP BY c.id ORDER BY lower(c.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Collection])
}

// TagCount is a tag and how many cards have it.
type TagCount struct {
	Name  string `json:"name"`
	Cards int    `json:"cards"`
}

// Tags returns every tag with its card count, alphabetically.
func (l *Library) Tags(ctx context.Context) ([]TagCount, error) {
	rows, err := l.pool.Query(ctx, `SELECT t.name, count(ct.card_id)::int
		FROM tag t LEFT JOIN card_tag ct ON ct.tag_id = t.id
		GROUP BY t.id ORDER BY lower(t.name)`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
}

// AddToCollection adds a card to a collection. Adding it twice is a no-op.
func (l *Library) AddToCollection(ctx context.Context, collectionID, cardID int64) error {
	_, err := l.pool.Exec(ctx, `INSERT INTO collection_card (collection_id, card_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, collectionID, cardID)
	return mapErr(err)
}

// RemoveFromCollection removes a card from a collection.
func (l *Library) RemoveFromCollection(ctx context.Context, collectionID, cardID int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM collection_card WHERE collection_id = $1 AND card_id = $2`,
		collectionID, cardID)
	return err
}

// collectionCards returns the IDs of a collection's cards, oldest addition first.
func (l *Library) collectionCards(ctx context.Context, collectionID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT card_id FROM collection_card WHERE collection_id = $1
		ORDER BY added_at, card_id`, collectionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// CardCollections returns the IDs of the collections a card is in.
func (l *Library) CardCollections(ctx context.Context, cardID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT collection_id FROM collection_card WHERE card_id = $1
		ORDER BY collection_id`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// LinkLorebook links a card to a lorebook's head revision. Linking twice is a no-op.
func (l *Library) LinkLorebook(ctx context.Context, cardID, lorebookID int64) error {
	_, err := l.pool.Exec(ctx, linkLorebook, cardID, lorebookID)
	return mapErr(err)
}

// LinkLorebookTx is LinkLorebook inside the caller's transaction.
func (l *Library) LinkLorebookTx(ctx context.Context, tx pgx.Tx, cardID, lorebookID int64) error {
	_, err := tx.Exec(ctx, linkLorebook, cardID, lorebookID)
	return mapErr(err)
}

const linkLorebook = `INSERT INTO card_lorebook (card_id, lorebook_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`

// UnlinkLorebook removes a link between a card and a lorebook.
func (l *Library) UnlinkLorebook(ctx context.Context, cardID, lorebookID int64) error {
	_, err := l.pool.Exec(ctx, `DELETE FROM card_lorebook WHERE card_id = $1 AND lorebook_id = $2`,
		cardID, lorebookID)
	return err
}

// LorebookCards returns the IDs of the cards a lorebook is linked to.
func (l *Library) LorebookCards(ctx context.Context, lorebookID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT card_id FROM card_lorebook WHERE lorebook_id = $1
		ORDER BY card_id`, lorebookID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// CardLorebooks returns the IDs of the lorebooks linked to a card.
func (l *Library) CardLorebooks(ctx context.Context, cardID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT lorebook_id FROM card_lorebook WHERE card_id = $1
		ORDER BY lorebook_id`, cardID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
