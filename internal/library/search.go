package library

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Query is a structured card search, as POST /search takes it. Bulk actions
// select cards with it too.
type Query struct {
	Text     string   `json:"text,omitempty" doc:"Words to match in name, description, personality, scenario, creator notes, and first message. Supports \"quoted phrases\", or, and -excluded words."`
	Semantic bool     `json:"semantic,omitempty" doc:"Not available yet: needs an embedding enricher."`
	Filter   *Filter  `json:"filter,omitempty"`
	Sources  []string `json:"sources,omitempty" doc:"Only library for now."`
	Include  []string `json:"include,omitempty" doc:"Extra data per hit: tags, lorebooks."`
	Fields   []string `json:"fields,omitempty" doc:"Card data keys to return per hit, plus avatar. Default: name."`
	Page     Page     `json:"page,omitempty"`
}

// Page selects one page of results.
type Page struct {
	Size   int     `json:"size,omitempty" minimum:"0" maximum:"500" doc:"Default 50."`
	Cursor *string `json:"cursor,omitempty" nullable:"true" doc:"next_cursor from the previous page."`
}

// Filter is one condition on a card. Set exactly one field. Archived cards
// are left out unless a filter mentions archived.
type Filter struct {
	And        []Filter `json:"and,omitempty"`
	Or         []Filter `json:"or,omitempty"`
	Not        *Filter  `json:"not,omitempty"`
	Tag        *string  `json:"tag,omitempty" doc:"Has this tag, in any case."`
	Collection *int64   `json:"collection,omitempty" doc:"Is in this collection."`
	Archived   *bool    `json:"archived,omitempty"`
}

// Hit is one matching card.
type Hit struct {
	ID         int64          `json:"id"`
	Slug       string         `json:"slug"`
	RevisionID int64          `json:"revision_id"`
	ArchivedAt *time.Time     `json:"archived_at,omitempty"`
	Fields     map[string]any `json:"fields"`
	Tags       []string       `json:"tags,omitempty"`
	Lorebooks  []int64        `json:"lorebooks,omitempty"`
}

// Results is one page of hits.
type Results struct {
	Hits       []Hit   `json:"hits"`
	NextCursor *string `json:"next_cursor,omitempty"`
}

// where collects SQL conditions and their arguments.
type where struct{ args []any }

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) filter(f Filter) (string, error) {
	set := 0
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.Tag != nil, f.Collection != nil, f.Archived != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return "", fmt.Errorf("%w: each filter needs exactly one condition", ErrInvalid)
	}
	switch {
	case f.And != nil:
		return w.join(f.And, " AND ", "TRUE")
	case f.Or != nil:
		return w.join(f.Or, " OR ", "FALSE")
	case f.Not != nil:
		s, err := w.filter(*f.Not)
		return "NOT (" + s + ")", err
	case f.Tag != nil:
		return `EXISTS (SELECT 1 FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
			WHERE ct.card_id = c.id AND lower(t.name) = lower(` + w.arg(*f.Tag) + `))`, nil
	case f.Collection != nil:
		return `EXISTS (SELECT 1 FROM collection_card cc
			WHERE cc.card_id = c.id AND cc.collection_id = ` + w.arg(*f.Collection) + `)`, nil
	}
	return `((c.archived_at IS NOT NULL) = ` + w.arg(*f.Archived) + `)`, nil
}

func (w *where) join(fs []Filter, op, empty string) (string, error) {
	if len(fs) == 0 {
		return empty, nil
	}
	parts := make([]string, len(fs))
	for i, f := range fs {
		s, err := w.filter(f)
		if err != nil {
			return "", err
		}
		parts[i] = s
	}
	return "(" + strings.Join(parts, op) + ")", nil
}

func mentionsArchived(f *Filter) bool {
	if f == nil {
		return false
	}
	if f.Archived != nil || mentionsArchived(f.Not) {
		return true
	}
	return slices.ContainsFunc(f.And, func(g Filter) bool { return mentionsArchived(&g) }) ||
		slices.ContainsFunc(f.Or, func(g Filter) bool { return mentionsArchived(&g) })
}

// compile returns the WHERE condition for q over card c joined to its head
// revision r, and the placeholder holding the text query, if any.
func compile(q Query) (string, *where, string, error) {
	if q.Semantic {
		return "", nil, "", fmt.Errorf("%w: semantic search needs an embedding enricher, and none is installed", ErrInvalid)
	}
	for _, s := range q.Sources {
		if s != "library" {
			return "", nil, "", fmt.Errorf("%w: unknown source %q", ErrInvalid, s)
		}
	}
	w := &where{}
	conds := []string{"TRUE"}
	if q.Filter != nil {
		s, err := w.filter(*q.Filter)
		if err != nil {
			return "", nil, "", err
		}
		conds = append(conds, s)
	}
	if !mentionsArchived(q.Filter) {
		conds = append(conds, "c.archived_at IS NULL")
	}
	text := ""
	if q.Text != "" {
		text = w.arg(q.Text)
		conds = append(conds, "r.search @@ websearch_to_tsquery('english', "+text+")")
	}
	return strings.Join(conds, " AND "), w, text, nil
}

// Search returns one page of the library's cards that match q, best match
// first when q has text, newest first otherwise.
//
// ponytail: the cursor is an offset, so a page can repeat or skip a card
// that was added or archived since the last page; switch to keyset paging
// if that matters.
func (l *Library) Search(ctx context.Context, q Query) (Results, error) {
	cond, w, text, err := compile(q)
	if err != nil {
		return Results{}, err
	}
	size := q.Page.Size
	if size == 0 {
		size = 50
	}
	offset := 0
	if q.Page.Cursor != nil {
		if offset, err = strconv.Atoi(*q.Page.Cursor); err != nil || offset < 0 {
			return Results{}, fmt.Errorf("%w: bad cursor", ErrInvalid)
		}
	}
	fields := q.Fields
	if len(fields) == 0 {
		fields = []string{"name"}
	}
	tags, books := `NULL::text[]`, `NULL::bigint[]`
	for _, inc := range q.Include {
		switch inc {
		case "tags":
			tags = `array(SELECT t.name FROM card_tag ct JOIN tag t ON t.id = ct.tag_id
				WHERE ct.card_id = c.id ORDER BY lower(t.name))`
		case "lorebooks":
			books = `array(SELECT lorebook_id FROM card_lorebook WHERE card_id = c.id ORDER BY lorebook_id)`
		default:
			return Results{}, fmt.Errorf("%w: unknown include %q", ErrInvalid, inc)
		}
	}
	order := "c.id DESC"
	if text != "" {
		order = "ts_rank(r.search, websearch_to_tsquery('english', " + text + ")) DESC, c.id DESC"
	}
	sql := `SELECT c.id, c.slug, r.id, c.archived_at, r.data, r.avatar_blob, ` + tags + `, ` + books + `
		FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE ` + cond + ` ORDER BY ` + order + ` LIMIT ` + w.arg(size+1) + ` OFFSET ` + w.arg(offset)
	rows, err := l.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return Results{}, err
	}
	res := Results{Hits: []Hit{}}
	var h Hit
	var data []byte
	var avatar *string
	_, err = pgx.ForEachRow(rows, []any{&h.ID, &h.Slug, &h.RevisionID, &h.ArchivedAt, &data, &avatar, &h.Tags, &h.Lorebooks},
		func() error {
			var all map[string]any
			if err := json.Unmarshal(data, &all); err != nil {
				return err
			}
			h.Fields = map[string]any{}
			for _, f := range fields {
				if f == "avatar" && avatar != nil {
					h.Fields[f] = *avatar
				} else if v, ok := all[f]; ok {
					h.Fields[f] = v
				}
			}
			res.Hits = append(res.Hits, h)
			h = Hit{}
			return nil
		})
	if err != nil {
		return Results{}, err
	}
	if len(res.Hits) > size {
		res.Hits = res.Hits[:size]
		next := strconv.Itoa(offset + size)
		res.NextCursor = &next
	}
	return res, nil
}

// SearchIDs returns the IDs of every card q matches, ignoring its paging and
// output options.
func (l *Library) SearchIDs(ctx context.Context, q Query) ([]int64, error) {
	cond, w, _, err := compile(q)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE `+cond+` ORDER BY c.id`, w.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
