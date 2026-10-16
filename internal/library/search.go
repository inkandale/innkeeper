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

// Query is a structured card search, as POST /cards/search takes it. Bulk
// actions select cards with it too.
type Query struct {
	Text     string   `json:"text,omitempty" doc:"Words to match in name, description, personality, scenario, creator notes, and first message. Supports \"quoted phrases\", or, and -excluded words."`
	Semantic bool     `json:"semantic,omitempty" doc:"Not available yet: needs an embedding enricher."`
	Filter   *Filter  `json:"filter,omitempty"`
	Sources  []string `json:"sources,omitempty" doc:"library, or catalog:<instance id> to also search a catalog by text alone. Default: library."`
	Include  []string `json:"include,omitempty" doc:"Extra data per hit: tags, lorebooks, targets, enrichments (the score instance's current values of its score keys and grid key). Lorebooks take cards and targets."`
	Fields   []string `json:"fields,omitempty" doc:"Card data keys to return per hit, plus avatar. Default: name."`
	Sort     string   `json:"sort,omitempty" pattern:"^(relevance|updated|name|created|enrichment:[a-z][a-z0-9_]*)$" doc:"relevance, updated, name, created, or enrichment:<key> for the score instance's value of key, highest first and cards without one last. Default: relevance with text, newest first without."`
	Facets   []string `json:"facets,omitempty" doc:"Counts to return over every match: tags, collections, targets, sources. Lorebooks take targets and sources."`
	Page     Page     `json:"page,omitempty"`
	Group    string   `json:"group,omitempty" enum:"family" doc:"family returns one hit per family of variants: its face, with the family's matches. Paging and total count families; facets count cards or lorebooks."`

	// ScoreInstance is the enrich instance whose values enrichment filters,
	// sort, and include use, or 0 for none. The caller resolves it from the
	// score_instance setting.
	ScoreInstance int64 `json:"-"`
}

// Page selects one page of results, by cursor or by number.
type Page struct {
	Size   int     `json:"size,omitempty" minimum:"0" maximum:"500" doc:"Default 50."`
	Cursor *string `json:"cursor,omitempty" nullable:"true" doc:"next_cursor from the previous page."`
	Number int     `json:"number,omitempty" minimum:"0" maximum:"1000000" doc:"1-based page number, instead of cursor."`
}

// Filter is one condition on a card. Set exactly one field. Archived cards
// are left out unless a filter mentions archived.
type Filter struct {
	And           []Filter      `json:"and,omitempty"`
	Or            []Filter      `json:"or,omitempty"`
	Not           *Filter       `json:"not,omitempty"`
	IDs           []int64       `json:"ids,omitempty" maxItems:"10000" doc:"Is one of these cards."`
	Name          *string       `json:"name,omitempty" maxLength:"200" doc:"Name contains this, in any case."`
	Tag           *string       `json:"tag,omitempty" doc:"Has this tag, in any case."`
	Collection    *int64        `json:"collection,omitempty" doc:"Is in this collection."`
	Source        *string       `json:"source,omitempty" doc:"Has a source of this kind, such as chub."`
	Archived      *bool         `json:"archived,omitempty"`
	TargetState   *TargetFilter `json:"target_state,omitempty" doc:"Is pushed to one of instances and stands as one of states there. An empty list matches any value."`
	VariantOfRoot *int64        `json:"variant_of_root,omitempty" doc:"Is this card or lorebook, or a variant below it at any depth."`
	Enrichment    *string       `json:"enrichment,omitempty" doc:"Has a current value for this enrichment key from the score instance. With gte and lte, a number in that range; with missing, no value. Cards only."`
	Gte           *float64      `json:"gte,omitempty" doc:"With enrichment: the value is at least this."`
	Lte           *float64      `json:"lte,omitempty" doc:"With enrichment: the value is at most this."`
	Missing       *bool         `json:"missing,omitempty" doc:"With enrichment: true matches cards without a value."`
}

// TargetFilter matches an entity by its bindings: one on any of Instances
// whose status is any of States.
type TargetFilter struct {
	Instances []int64  `json:"instances,omitempty" maxItems:"100" doc:"Plugin instance IDs"`
	States    []string `json:"states,omitempty" maxItems:"6" doc:"in_sync, ahead, remote_changed, diverged, missing, or cant_check"`
}

// TargetState is a card's status on one target, as GET /cards/{id}/bindings
// shows it.
type TargetState struct {
	Instance int64  `json:"instance" doc:"Plugin instance ID"`
	Is       string `json:"is" enum:"in_sync,ahead,remote_changed,diverged,missing,cant_check"`
}

var targetStatuses = []string{"in_sync", "ahead", "remote_changed", "diverged", "missing", "cant_check"}

// Hit is one matching card.
type Hit struct {
	ID         int64          `json:"id"`
	Slug       string         `json:"slug"`
	RevisionID int64          `json:"revision_id"`
	ArchivedAt *time.Time     `json:"archived_at,omitempty"`
	Fields     map[string]any `json:"fields"`
	Tags       []string       `json:"tags,omitempty"`
	Lorebooks  []int64        `json:"lorebooks,omitempty"`
	Targets    []TargetState  `json:"targets,omitempty" doc:"With include targets: how the card stands on each target it's pushed to."`
	Cards      []int64        `json:"cards,omitempty" doc:"Lorebooks with include cards: IDs of linked cards."`
	Entries    *int           `json:"entries,omitempty" doc:"Lorebooks only: how many entries it has."`
	// Enrichments are keyed by enrichment key.
	Enrichments map[string]HitEnrichment `json:"enrichments,omitempty" doc:"With include enrichments: the score instance's current value of each key."`
	Family      *Family                  `json:"family,omitempty" doc:"With group family: the family this hit is the face of."`
}

// Family is a grouped hit's family of variants.
type Family struct {
	RootID int64 `json:"root_id"`
	// Size counts live members, or every member when the filter mentions archived.
	Size int `json:"size"`
	// Matched are the members the query matched, which may not include the face.
	Matched []int64 `json:"matched"`
}

// HitEnrichment is a card's current value of one enrichment key.
type HitEnrichment struct {
	Value json.RawMessage `json:"value"`
	Stale bool            `json:"stale" doc:"Whether the value is about an older revision than the head."`
}

// Results is one page of hits.
type Results struct {
	Hits       []Hit   `json:"hits"`
	NextCursor *string `json:"next_cursor,omitempty"`
	Total      int     `json:"total" doc:"How many match."`
	Facets     *Facets `json:"facets,omitempty"`
}

// Facets counts the matching cards per value. Only the facets asked for are set.
type Facets struct {
	Tags        []TagCount        `json:"tags,omitempty" doc:"Most used first."`
	Collections []CollectionCount `json:"collections,omitempty"`
	Targets     []TargetCount     `json:"targets,omitempty"`
	Sources     []SourceCount     `json:"sources,omitempty"`
}

// CollectionCount is a collection and how many matching cards it has.
type CollectionCount struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Cards int    `json:"cards"`
}

// TargetCount is how many matching cards stand one way on one target.
type TargetCount struct {
	Instance int64  `json:"instance"`
	Is       string `json:"is"`
	Cards    int    `json:"cards"`
}

// SourceCount is a source kind and how many matching cards have it.
type SourceCount struct {
	Kind  string `json:"kind"`
	Cards int    `json:"cards"`
}

// from joins k's entities c to their head revisions r, as every search
// query names them.
func from(k *Kind) string {
	return k.Table + ` c JOIN ` + k.RevTable + ` r ON r.id = c.head_revision_id`
}

// tagsOf is r's string tags as rows (t.t, in card order as t.ord); non-string
// elements like null would break scans, and a missing or non-list tags is none.
const tagsOf = `LATERAL (SELECT e #>> '{}' AS t, ord FROM jsonb_array_elements(
	CASE jsonb_typeof(r.data->'tags') WHEN 'array' THEN r.data->'tags' ELSE '[]' END) WITH ORDINALITY x(e, ord)
	WHERE jsonb_typeof(e) = 'string') t`

// cardOnly names the facets and includes that need card tags, collections,
// or linked lorebooks.
func cardOnly(name string) bool {
	return name == "tags" || name == "collections" || name == "lorebooks" || name == "enrichments"
}

// errNoScoreInstance refuses an enrichment filter, sort, or include.
var errNoScoreInstance = fmt.Errorf("%w: enrichment filters, sort, and include need a score instance; pick one in Settings under Scores from", ErrInvalid)

// enrichmentSQL is the current value of key from the score instance for card
// c, or NULL, as a jsonb expression.
func (w *where) enrichmentSQL(key string) (string, error) {
	if w.k != Card {
		return "", fmt.Errorf("%w: lorebooks have no enrichments", ErrInvalid)
	}
	if w.score == 0 {
		return "", errNoScoreInstance
	}
	return `(SELECT e.value FROM enrichment e WHERE e.card_id = c.id AND e.key = ` + w.arg(key) +
		` AND e.instance_id = ` + w.arg(w.score) + ` ORDER BY e.revision_id DESC, e.created_at DESC, e.id DESC LIMIT 1)`, nil
}

// enrichmentFilter is f's condition on its enrichment key.
func (w *where) enrichmentFilter(f Filter) (string, error) {
	if f.Missing != nil && (f.Gte != nil || f.Lte != nil) {
		return "", fmt.Errorf("%w: an enrichment filter takes missing or a range, not both", ErrInvalid)
	}
	cur, err := w.enrichmentSQL(*f.Enrichment)
	if err != nil {
		return "", err
	}
	if f.Missing != nil && *f.Missing {
		return cur + ` IS NULL`, nil
	}
	if f.Gte == nil && f.Lte == nil {
		return cur + ` IS NOT NULL`, nil
	}
	num := `CASE WHEN jsonb_typeof(v.value) = 'number' THEN v.value::numeric END`
	conds := []string{num + ` IS NOT NULL`}
	if f.Gte != nil {
		conds = append(conds, num+` >= `+w.arg(*f.Gte))
	}
	if f.Lte != nil {
		conds = append(conds, num+` <= `+w.arg(*f.Lte))
	}
	return `EXISTS (SELECT 1 FROM (SELECT ` + cur + ` AS value) v WHERE ` + strings.Join(conds, " AND ") + `)`, nil
}

// pushedStatus is binding_status for apps the library pushes to; a followed site source isn't a target.
const pushedStatus = `(SELECT bs.* FROM binding_status bs JOIN binding b ON b.id = bs.binding_id WHERE b.push)`

// facetSQL selects each facet's rows for the IDs in match.
var facetSQL = map[string]func(k *Kind, match string) string{
	"tags": func(k *Kind, m string) string {
		return `SELECT min(t.t), count(DISTINCT c.id)::int FROM ` + from(k) + `, ` + tagsOf + `
			WHERE c.id IN (` + m + `) GROUP BY lower(t.t) ORDER BY count(DISTINCT c.id) DESC, lower(t.t)`
	},
	"collections": func(_ *Kind, m string) string {
		return `SELECT co.id, co.name, count(*)::int FROM collection_card cc JOIN collection co ON co.id = cc.collection_id
			WHERE cc.card_id IN (` + m + `) GROUP BY co.id ORDER BY lower(co.name)`
	},
	"targets": func(k *Kind, m string) string {
		return `SELECT instance_id, status, count(*)::int FROM ` + pushedStatus + ` bs
			WHERE ` + k.FK + ` IN (` + m + `) GROUP BY 1, 2 ORDER BY 1, 2`
	},
	"sources": func(k *Kind, m string) string {
		return `SELECT kind, count(DISTINCT ` + k.FK + `)::int FROM ` + k.SourceTable + `
			WHERE removed_at IS NULL AND ` + k.FK + ` IN (` + m + `) GROUP BY kind ORDER BY kind`
	},
}

// facets counts the matches of cond for each facet in names.
func (l *Library) facets(ctx context.Context, k *Kind, names []string, cond string, args []any) (*Facets, error) {
	match := `SELECT c.id FROM ` + from(k) + ` WHERE ` + cond
	f := &Facets{}
	for _, name := range names {
		rows, err := l.pool.Query(ctx, facetSQL[name](k, match), args...)
		if err != nil {
			return nil, err
		}
		switch name {
		case "tags":
			f.Tags, err = pgx.CollectRows(rows, pgx.RowToStructByPos[TagCount])
		case "collections":
			f.Collections, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CollectionCount])
		case "targets":
			f.Targets, err = pgx.CollectRows(rows, pgx.RowToStructByPos[TargetCount])
		case "sources":
			f.Sources, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SourceCount])
		}
		if err != nil {
			return nil, err
		}
	}
	return f, nil
}

// where collects SQL conditions and their arguments.
type where struct {
	k     *Kind
	score int64 // the score instance, or 0
	args  []any
	n     int
}

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return "$" + strconv.Itoa(len(w.args))
}

func (w *where) filter(f Filter) (string, error) {
	// Bounds the SQL size, so a huge filter is a 400 instead of a Postgres error.
	if w.n++; w.n > 200 {
		return "", fmt.Errorf("%w: filter has more than 200 conditions", ErrInvalid)
	}
	set := 0
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.IDs != nil, f.Name != nil, f.Tag != nil, f.Collection != nil, f.Source != nil, f.Archived != nil, f.TargetState != nil, f.Enrichment != nil, f.VariantOfRoot != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return "", fmt.Errorf("%w: each filter needs exactly one condition", ErrInvalid)
	}
	if f.Enrichment == nil && (f.Gte != nil || f.Lte != nil || f.Missing != nil) {
		return "", fmt.Errorf("%w: gte, lte, and missing go with enrichment", ErrInvalid)
	}
	if w.k != Card && (f.Tag != nil || f.Collection != nil) {
		return "", fmt.Errorf("%w: lorebooks have no tags or collections", ErrInvalid)
	}
	switch {
	case f.And != nil:
		return w.join(f.And, " AND ", "TRUE")
	case f.Or != nil:
		return w.join(f.Or, " OR ", "FALSE")
	case f.Not != nil:
		s, err := w.filter(*f.Not)
		return "NOT (" + s + ")", err
	case f.IDs != nil:
		return `c.id = ANY(` + w.arg(f.IDs) + `)`, nil
	case f.Name != nil:
		// strpos, unlike LIKE, needs no escaping of % and _.
		return `strpos(lower(r.data->>'name'), lower(` + w.arg(*f.Name) + `)) > 0`, nil
	case f.Tag != nil:
		return `EXISTS (SELECT 1 FROM ` + tagsOf + ` WHERE lower(t.t) = lower(` + w.arg(*f.Tag) + `))`, nil
	case f.Collection != nil:
		return `EXISTS (SELECT 1 FROM collection_card cc
			WHERE cc.card_id = c.id AND cc.collection_id = ` + w.arg(*f.Collection) + `)`, nil
	case f.Source != nil:
		return `EXISTS (SELECT 1 FROM ` + w.k.SourceTable + ` cs
			WHERE cs.` + w.k.FK + ` = c.id AND cs.kind = ` + w.arg(*f.Source) + ` AND cs.removed_at IS NULL)`, nil
	case f.Enrichment != nil:
		return w.enrichmentFilter(f)
	case f.VariantOfRoot != nil:
		return `c.id IN (WITH RECURSIVE down AS (
				SELECT id FROM ` + w.k.Table + ` WHERE id = ` + w.arg(*f.VariantOfRoot) + `
				UNION ALL
				SELECT x.id FROM ` + w.k.Table + ` x JOIN down ON x.variant_of_id = down.id)
			SELECT id FROM down)`, nil
	case f.TargetState != nil:
		cond := `bs.` + w.k.FK + ` = c.id`
		if len(f.TargetState.Instances) > 0 {
			cond += ` AND bs.instance_id = ANY(` + w.arg(f.TargetState.Instances) + `)`
		}
		for _, s := range f.TargetState.States {
			if !slices.Contains(targetStatuses, s) {
				return "", fmt.Errorf("%w: target_state.states must be from %v", ErrInvalid, targetStatuses)
			}
		}
		if len(f.TargetState.States) > 0 {
			cond += ` AND bs.status = ANY(` + w.arg(f.TargetState.States) + `)`
		}
		return `EXISTS (SELECT 1 FROM ` + pushedStatus + ` bs WHERE ` + cond + `)`, nil
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

// compile returns the WHERE condition for q over k's entity c joined to its
// head revision r, and the placeholder holding the text query, if any.
func compile(k *Kind, q Query) (string, *where, string, error) {
	if q.Semantic {
		return "", nil, "", fmt.Errorf("%w: semantic search needs an embedding enricher, and none is installed", ErrInvalid)
	}
	for _, s := range q.Sources {
		if s != "library" {
			return "", nil, "", fmt.Errorf("%w: unknown source %q", ErrInvalid, s)
		}
	}
	w := &where{k: k, score: q.ScoreInstance}
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

// familiesOf is a WITH clause whose fam holds each family with a match for
// cond: its root, its best match's place under order, and its matches.
func familiesOf(k *Kind, cond, order string) string {
	return `WITH RECURSIVE m AS (
		SELECT c.id, row_number() OVER (ORDER BY ` + order + `) AS pos FROM ` + from(k) + ` WHERE ` + cond + `
	), up AS (
		SELECT m.id AS member, e.id, e.variant_of_id FROM m JOIN ` + k.Table + ` e ON e.id = m.id
		UNION ALL
		SELECT up.member, p.id, p.variant_of_id FROM up JOIN ` + k.Table + ` p ON p.id = up.variant_of_id
	), fam AS (
		SELECT up.id AS root_id, min(m.pos) AS pos, array_agg(m.id ORDER BY m.id) AS matched
		FROM up JOIN m ON m.id = up.member WHERE up.variant_of_id IS NULL GROUP BY up.id
	)`
}

// familyPage follows familiesOf: it selects cols for one page of fam's
// faces, best placed first, then each family's root, size, and matches. The
// face is the shallowest live member, then the oldest, then the lowest ID;
// with no live member, the root. all counts archived members in size.
func familyPage(k *Kind, cols string, all bool, limit, offset string) string {
	size := `count(*) FILTER (WHERE e.archived_at IS NULL)::int`
	if all {
		size = `count(*)::int`
	}
	return `, pg AS (
		SELECT * FROM fam ORDER BY pos LIMIT ` + limit + ` OFFSET ` + offset + `
	), down AS (
		SELECT pg.root_id, pg.root_id AS id, 0 AS depth FROM pg
		UNION ALL
		SELECT down.root_id, e.id, down.depth + 1 FROM down JOIN ` + k.Table + ` e ON e.variant_of_id = down.id
	), face AS (
		SELECT pg.root_id, pg.pos, pg.matched, f.id, f.size FROM pg CROSS JOIN LATERAL (
			SELECT (array_agg(e.id ORDER BY e.archived_at IS NOT NULL, d.depth, e.created_at, e.id))[1] AS id, ` + size + ` AS size
			FROM down d JOIN ` + k.Table + ` e ON e.id = d.id WHERE d.root_id = pg.root_id
		) f
	)
	SELECT ` + cols + `, face.root_id, face.size, face.matched
	FROM face JOIN ` + from(k) + ` ON c.id = face.id ORDER BY face.pos`
}

// Search returns one page of the library's entities of kind k that match q,
// best match first when q has text, newest first otherwise. With q.Group
// family, a page holds one hit per family, placed by its best match.
//
// ponytail: the cursor is an offset, so a page can repeat or skip an item
// that was added or archived since the last page; switch to keyset paging
// if that matters.
func (l *Library) Search(ctx context.Context, k *Kind, q Query) (Results, error) {
	cond, w, text, err := compile(k, q)
	if err != nil {
		return Results{}, err
	}
	size := q.Page.Size
	if size == 0 {
		size = 50
	}
	if size < 0 || size > 500 {
		return Results{}, fmt.Errorf("%w: page size must be 0 to 500", ErrInvalid)
	}
	offset := 0
	switch {
	case q.Page.Cursor != nil && q.Page.Number != 0:
		return Results{}, fmt.Errorf("%w: send a page cursor or a page number, not both", ErrInvalid)
	case q.Page.Cursor != nil:
		if offset, err = strconv.Atoi(*q.Page.Cursor); err != nil || offset < 0 {
			return Results{}, fmt.Errorf("%w: bad cursor", ErrInvalid)
		}
	case q.Page.Number < 0 || q.Page.Number > 1_000_000:
		return Results{}, fmt.Errorf("%w: page number must be 1 to 1000000", ErrInvalid)
	case q.Page.Number > 0:
		offset = (q.Page.Number - 1) * size
	}
	// Includes and sort add arguments that the count and facets don't use.
	condArgs := slices.Clone(w.args)
	fields := q.Fields
	if len(fields) == 0 {
		fields = []string{"name"}
	}
	tags, books, targets, cards, entries, enrichments := `NULL::text[]`, `NULL::bigint[]`, `NULL::jsonb`, `NULL::bigint[]`, `NULL::int`, `NULL::jsonb`
	if k != Card {
		entries = `CASE jsonb_typeof(r.data->'entries') WHEN 'array' THEN jsonb_array_length(r.data->'entries') ELSE 0 END`
	}
	for _, inc := range q.Include {
		if k != Card && cardOnly(inc) || k == Card && inc == "cards" {
			return Results{}, fmt.Errorf("%w: a %s search can't include %q", ErrInvalid, k.Name, inc)
		}
		switch inc {
		case "tags":
			tags = `array(SELECT t.t FROM ` + tagsOf + ` ORDER BY t.ord)`
		case "lorebooks":
			books = `array(SELECT lorebook_id FROM card_lorebook WHERE card_id = c.id ORDER BY lorebook_id)`
		case "cards":
			cards = `array(SELECT card_id FROM card_lorebook WHERE lorebook_id = c.id ORDER BY card_id)`
		case "targets":
			targets = `(SELECT jsonb_agg(jsonb_build_object('instance', instance_id, 'is', status) ORDER BY instance_id)
				FROM ` + pushedStatus + ` bs WHERE ` + k.FK + ` = c.id)`
		case "enrichments":
			if q.ScoreInstance == 0 {
				return Results{}, errNoScoreInstance
			}
			enrichments = `(SELECT jsonb_object_agg(x.key, jsonb_build_object('value', x.value, 'stale', x.revision_id <> c.head_revision_id))
				FROM (SELECT DISTINCT ON (e.key) e.key, e.value, e.revision_id FROM enrichment e
					WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.ScoreInstance) + `
					ORDER BY e.key, e.revision_id DESC, e.created_at DESC, e.id DESC) x)`
		default:
			return Results{}, fmt.Errorf("%w: unknown include %q", ErrInvalid, inc)
		}
	}
	for _, name := range q.Facets {
		if facetSQL[name] == nil || k != Card && cardOnly(name) {
			return Results{}, fmt.Errorf("%w: unknown facet %q", ErrInvalid, name)
		}
	}
	order := "c.id DESC"
	switch q.Sort {
	case "", "relevance":
		if text != "" {
			order = "ts_rank(r.search, websearch_to_tsquery('english', " + text + ")) DESC, c.id DESC"
		}
	case "updated":
		order = "c.head_revision_id DESC, c.id DESC"
	case "name":
		order = "lower(r.data->>'name'), c.id"
	case "created":
	default:
		key, ok := strings.CutPrefix(q.Sort, "enrichment:")
		if !ok {
			return Results{}, fmt.Errorf("%w: unknown sort %q", ErrInvalid, q.Sort)
		}
		cur, err := w.enrichmentSQL(key)
		if err != nil {
			return Results{}, err
		}
		order = cur + " DESC NULLS LAST, c.id DESC"
	}
	if q.Group != "" && q.Group != "family" {
		return Results{}, fmt.Errorf("%w: unknown group %q", ErrInvalid, q.Group)
	}
	res := Results{Hits: []Hit{}}
	count := `SELECT count(*) FROM ` + from(k) + ` WHERE ` + cond
	if q.Group == "family" {
		// The count ignores order, whose sort arguments aren't in condArgs.
		count = familiesOf(k, cond, "c.id") + ` SELECT count(*) FROM fam`
	}
	if err := l.pool.QueryRow(ctx, count, condArgs...).Scan(&res.Total); err != nil {
		return Results{}, fmt.Errorf("count matches: %w", err)
	}
	if len(q.Facets) > 0 {
		if res.Facets, err = l.facets(ctx, k, q.Facets, cond, condArgs); err != nil {
			return Results{}, err
		}
	}
	cols := `c.id, c.slug, r.id, c.archived_at, r.data, r.avatar_blob, ` + tags + `, ` + books + `, ` + targets + `, ` + cards + `, ` + entries + `, ` + enrichments
	limit, skip := w.arg(size+1), w.arg(offset)
	sql := `SELECT ` + cols + `, NULL::bigint, NULL::int, NULL::bigint[] FROM ` + from(k) + `
		WHERE ` + cond + ` ORDER BY ` + order + ` LIMIT ` + limit + ` OFFSET ` + skip
	if q.Group == "family" {
		sql = familiesOf(k, cond, order) + familyPage(k, cols, mentionsArchived(q.Filter), limit, skip)
	}
	rows, err := l.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return Results{}, fmt.Errorf("search %s: %w", k.Plural, err)
	}
	var h Hit
	var data []byte
	var avatar *string
	var rootID *int64
	var famSize *int
	var matched []int64
	_, err = pgx.ForEachRow(rows, []any{&h.ID, &h.Slug, &h.RevisionID, &h.ArchivedAt, &data, &avatar, &h.Tags, &h.Lorebooks, &h.Targets, &h.Cards, &h.Entries, &h.Enrichments, &rootID, &famSize, &matched},
		func() error {
			if rootID != nil {
				h.Family = &Family{RootID: *rootID, Size: *famSize, Matched: matched}
			}
			var all map[string]any
			if err := json.Unmarshal(data, &all); err != nil {
				return err
			}
			h.Fields = map[string]any{}
			for _, f := range fields {
				if f == "avatar" {
					// The avatar is the blob only, never a same-named data key.
					if avatar != nil {
						h.Fields[f] = *avatar
					}
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

// UsesEnrichments reports whether q filters, sorts, or includes by
// enrichment, and so needs ScoreInstance.
func (q Query) UsesEnrichments() bool {
	return strings.HasPrefix(q.Sort, "enrichment:") || slices.Contains(q.Include, "enrichments") || filtersEnrichments(q.Filter)
}

func filtersEnrichments(f *Filter) bool {
	if f == nil {
		return false
	}
	if f.Enrichment != nil || filtersEnrichments(f.Not) {
		return true
	}
	return slices.ContainsFunc(f.And, func(g Filter) bool { return filtersEnrichments(&g) }) ||
		slices.ContainsFunc(f.Or, func(g Filter) bool { return filtersEnrichments(&g) })
}

// SearchIDs returns the IDs of every entity of kind k that q matches,
// ignoring its paging and output options.
func (l *Library) SearchIDs(ctx context.Context, k *Kind, q Query) ([]int64, error) {
	cond, w, _, err := compile(k, q)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM `+from(k)+` WHERE `+cond+` ORDER BY c.id`, w.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
