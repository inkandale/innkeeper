package library

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/db"
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
			// *string so a null element is seen; []string would turn it into "".
			var elems []*string
			if err := json.Unmarshal(raw, &elems); err != nil || slices.Contains(elems, nil) {
				return fmt.Errorf("%w: card %d tags aren't a list of strings", ErrInvalid, cardID)
			}
			for _, e := range elems {
				old = append(old, *e)
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

// Collection is a named group of cards. A card can be in many.
type Collection struct {
	ID          int64     `json:"id"`
	LibraryID   int64     `json:"library_id"`
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

// CreateCollection adds an empty collection to libraryID.
func (l *Library) CreateCollection(ctx context.Context, libraryID int64, name, description string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `INSERT INTO collection (library_id, name, description) VALUES ($1, $2, $3)
		RETURNING id, library_id, name, description, created_at`, libraryID, name, description).
		Scan(&c.ID, &c.LibraryID, &c.Name, &c.Description, &c.CreatedAt)
	return c, collectionErr(err)
}

// UpdateCollection renames a collection or changes its description. Nil
// leaves a field as it is.
func (l *Library) UpdateCollection(ctx context.Context, id int64, name, description *string) (Collection, error) {
	var c Collection
	err := l.pool.QueryRow(ctx, `UPDATE collection SET name = coalesce($2, name), description = coalesce($3, description)
		WHERE id = $1 RETURNING id, library_id, name, description, created_at`, id, name, description).
		Scan(&c.ID, &c.LibraryID, &c.Name, &c.Description, &c.CreatedAt)
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

// Collections returns libraryID's collections with their unarchived card counts and mosaic avatars, by name.
func (l *Library) Collections(ctx context.Context, libraryID int64) ([]Collection, error) {
	rows, err := l.pool.Query(ctx, `SELECT c.id, c.library_id, c.name, c.description, c.created_at, count(cc.card_id) FILTER (WHERE k.archived_at IS NULL)::int,
			array(SELECT r.avatar_blob FROM collection_card m
				JOIN card k ON k.id = m.card_id JOIN card_revision r ON r.id = k.head_revision_id
				WHERE m.collection_id = c.id AND r.avatar_blob IS NOT NULL AND k.archived_at IS NULL
				ORDER BY m.added_at, m.card_id LIMIT 4)
		FROM collection c LEFT JOIN collection_card cc ON cc.collection_id = c.id
		LEFT JOIN card k ON k.id = cc.card_id
		WHERE c.library_id = $1
		GROUP BY c.id ORDER BY lower(c.name)`, libraryID)
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

// Tags returns every tag on an active card in libraryID with its card count,
// alphabetically. Spellings that differ only in case count as one.
func (l *Library) Tags(ctx context.Context, libraryID int64) ([]TagCount, error) {
	rows, err := l.pool.Query(ctx, `SELECT min(t.t), count(DISTINCT c.id)::int FROM `+from(Card)+`, `+tagsOf+`
		WHERE c.library_id = $1 AND c.archived_at IS NULL GROUP BY lower(t.t) ORDER BY lower(t.t)`, libraryID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
}

// AddToCollection adds a card to a collection of its library. Adding it
// twice is a no-op.
func (l *Library) AddToCollection(ctx context.Context, collectionID, cardID int64) error {
	var same bool
	err := l.pool.QueryRow(ctx, `SELECT co.library_id = c.library_id FROM collection co, card c WHERE co.id = $1 AND c.id = $2`,
		collectionID, cardID).Scan(&same)
	switch {
	case err != nil:
		return mapErr(err)
	case !same:
		return fmt.Errorf("%w: card %d is in another library than collection %d", ErrInvalid, cardID, collectionID)
	}
	_, err = l.pool.Exec(ctx, `INSERT INTO collection_card (collection_id, card_id) VALUES ($1, $2)
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
