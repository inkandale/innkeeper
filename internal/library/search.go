package library

import (
	"cmp"
	"context"
	"encoding/base64"
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
	Semantic bool     `json:"semantic,omitempty" doc:"Rank cards by meaning instead of matching words. Needs text and an embed instance (Settings, Plugins, Embeddings from); sorts by relevance only. Cards not embedded yet are left out."`
	Filter   *Filter  `json:"filter,omitempty"`
	Sources  []string `json:"sources,omitempty" doc:"library, or catalog:<instance id> to also search a catalog by text alone. Default: library."`
	Include  []string `json:"include,omitempty" doc:"Extra data per hit: tags, lorebooks, targets, enrichments (the score instance's current values of its score keys and grid key). Lorebooks take cards and targets."`
	Fields   []string `json:"fields,omitempty" doc:"Card data keys to return per hit, plus avatar. Default: name."`
	Sort     string   `json:"sort,omitempty" pattern:"^(relevance|updated|name|created|enrichment:([1-9][0-9]*:)?[a-z][a-z0-9_]*)$" doc:"relevance, updated, name, created, or enrichment:<key> for the score instance's value of key, highest first and cards without one last; enrichment:<instance>:<key> reads that enrich instance's value instead. Default: relevance with text, newest first without."`
	Facets   []string `json:"facets,omitempty" doc:"Counts to return over every match: tags, collections, targets, sources, states (each match once per state it has on any target), or states:<instance>,<instance> for those targets only, or enrichment:<key> for the values of a text or list key from the score instance, or enrichment:<instance>:<key> from that enrich instance. Enrichment counts are keyed by what follows enrichment:. Lorebooks take targets and sources."`
	Page     Page     `json:"page,omitempty"`
	// FacetsOnly saves a filter sidebar the count and page queries it would throw away.
	FacetsOnly bool   `json:"facets_only,omitempty" doc:"Return only the facets: no hits, total, or next_cursor."`
	Group      string `json:"group,omitempty" enum:"family" doc:"family returns one hit per family of variants: its face, with the family's matches. Paging and total count families; facets count cards or lorebooks."`

	Library int64 `json:"library,omitempty" doc:"The library to search; default 1."`

	// ScoreInstance is the enrich instance whose values enrichment filters,
	// sort, and include use, or 0 for none. The caller resolves it from the
	// score_instance setting.
	ScoreInstance int64 `json:"-"`

	// Embedding is Text's vector from EmbedInstance, for a semantic search.
	// The caller embeds it.
	Embedding     []float32 `json:"-"`
	EmbedInstance int64     `json:"-"`
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
	SharesSource  *bool         `json:"shares_source,omitempty" doc:"Has a source, by kind and ref, that another card or lorebook of the same kind also has. A fork's link to its original doesn't count."`
	Enrichment    *string       `json:"enrichment,omitempty" doc:"Has a current value for this enrichment key from the score instance. With gte and lte, a number in that range; with missing, no value. Cards only."`
	Gte           *float64      `json:"gte,omitempty" doc:"With enrichment: the value is at least this."`
	Lte           *float64      `json:"lte,omitempty" doc:"With enrichment: the value is at most this."`
	Missing       *bool         `json:"missing,omitempty" doc:"With enrichment: true matches cards without a value."`
	Is            *string       `json:"is,omitempty" doc:"With enrichment: the value is this text, or a list holding it."`
	Instance      *int64        `json:"instance,omitempty" doc:"With enrichment: the enrich instance whose value to read, instead of the score instance."`
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
	ID         int64      `json:"id"`
	Slug       string     `json:"slug"`
	RevisionID int64      `json:"revision_id"`
	ArchivedAt *time.Time `json:"archived_at,omitempty"`
	// ArchivedReason is why the hide job archived it: low_score or duplicate.
	ArchivedReason string         `json:"archived_reason,omitempty"`
	Fields         map[string]any `json:"fields"`
	Tags           []string       `json:"tags,omitempty"`
	Lorebooks      []int64        `json:"lorebooks,omitempty"`
	Targets        []TargetState  `json:"targets,omitempty" doc:"With include targets: how the card stands on each target it's pushed to."`
	Cards          []int64        `json:"cards,omitempty" doc:"Lorebooks with include cards: IDs of linked cards."`
	Entries        *int           `json:"entries,omitempty" doc:"Lorebooks only: how many entries it has."`
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
	States      []StateCount      `json:"states,omitempty"`
	// Enrichments are keyed by enrichment key.
	Enrichments map[string][]ValueCount `json:"enrichments,omitempty" doc:"By key: each text value, or each item of a list value, and how many matching cards have it. Most common first, at most 100."`
}

// ValueCount is an enrichment value and how many matching cards have it.
type ValueCount struct {
	Value string `json:"value"`
	Cards int    `json:"cards"`
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

// StateCount is how many matching cards stand one way on any target.
type StateCount struct {
	Is    string `json:"is" enum:"in_sync,ahead,remote_changed,diverged,missing,cant_check"`
	Cards int    `json:"cards"`
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

// tagsOf is r's string tags as rows (t.t, in card order as t.ord).
const tagsOf = `LATERAL unnest(r.tags) WITH ORDINALITY t(t, ord)`

// cardOnly names the facets and includes that need card tags, collections,
// or linked lorebooks.
func cardOnly(name string) bool {
	return name == "tags" || name == "collections" || name == "lorebooks" || name == "enrichments"
}

// errNoScoreInstance refuses an enrichment filter, sort, or include.
var errNoScoreInstance = fmt.Errorf("%w: enrichment filters, sort, and include need a score instance; pick one in Settings under Scores from", ErrInvalid)

// enrichmentSQL is the current value of key from enrich instance inst for
// card c, or NULL, as a jsonb expression. Instance 0 means the score instance.
func (w *where) enrichmentSQL(key string, inst int64) (string, error) {
	if w.k != Card {
		return "", fmt.Errorf("%w: lorebooks have no enrichments", ErrInvalid)
	}
	inst = cmp.Or(inst, w.score)
	if inst == 0 {
		return "", errNoScoreInstance
	}
	return `(SELECT e.value FROM enrichment e WHERE e.card_id = c.id AND e.key = ` + w.arg(key) +
		` AND e.instance_id = ` + w.arg(inst) + ` ORDER BY e.revision_id DESC, e.created_at DESC, e.id DESC LIMIT 1)`, nil
}

// enrichmentRef reads a sort or facet's reference to an enrichment key:
// key for the score instance's values, or <instance>:<key> for that enrich
// instance's. Instance 0 means the score instance.
func enrichmentRef(ref string) (int64, string, error) {
	id, key, ok := strings.Cut(ref, ":")
	if !ok {
		return 0, ref, nil
	}
	inst, err := strconv.ParseInt(id, 10, 64)
	if err != nil || inst <= 0 {
		return 0, "", fmt.Errorf("%w: %q names no enrich instance", ErrInvalid, ref)
	}
	return inst, key, nil
}

// enrichmentFilter is f's condition on its enrichment key.
func (w *where) enrichmentFilter(f Filter) (string, error) {
	set := 0
	for _, ok := range []bool{f.Missing != nil, f.Gte != nil || f.Lte != nil, f.Is != nil} {
		if ok {
			set++
		}
	}
	if set > 1 {
		return "", fmt.Errorf("%w: an enrichment filter takes one of missing, a range, or is", ErrInvalid)
	}
	var inst int64
	if f.Instance != nil {
		inst = *f.Instance
	}
	cur, err := w.enrichmentSQL(*f.Enrichment, inst)
	if err != nil {
		return "", err
	}
	if f.Missing != nil && *f.Missing {
		return cur + ` IS NULL`, nil
	}
	if f.Is != nil {
		is := w.arg(*f.Is)
		return `EXISTS (SELECT 1 FROM (SELECT ` + cur + ` AS value) v WHERE v.value = to_jsonb(` + is + `::text)
			OR jsonb_typeof(v.value) = 'array' AND v.value @> jsonb_build_array(` + is + `::text))`, nil
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

// matching selects the IDs of k's entities that match cond.
func matching(k *Kind, cond string) string { return `SELECT c.id FROM ` + from(k) + ` WHERE ` + cond }

// facetSQL selects each facet's rows for the entities that match cond.
var facetSQL = map[string]func(k *Kind, cond string) string{
	// A revision keeps one tag per case (cardhash.UniqueTags), so count(*)
	// counts cards without the sort a count(DISTINCT) needs.
	"tags": func(k *Kind, cond string) string {
		return `SELECT min(t.t), count(*)::int FROM ` + from(k) + `, ` + tagsOf + `
			WHERE ` + cond + ` GROUP BY lower(t.t) ORDER BY count(*) DESC, lower(t.t)`
	},
	"collections": func(k *Kind, cond string) string {
		return `SELECT co.id, co.name, count(*)::int FROM collection_card cc JOIN collection co ON co.id = cc.collection_id
			WHERE cc.card_id IN (` + matching(k, cond) + `) GROUP BY co.id ORDER BY lower(co.name)`
	},
	"targets": func(k *Kind, cond string) string {
		return `SELECT instance_id, status, count(*)::int FROM ` + pushedStatus + ` bs
			WHERE ` + k.FK + ` IN (` + matching(k, cond) + `) GROUP BY 1, 2 ORDER BY 1, 2`
	},
	"sources": func(k *Kind, cond string) string {
		return `SELECT kind, count(DISTINCT ` + k.FK + `)::int FROM ` + k.SourceTable + `
			WHERE removed_at IS NULL AND kind <> '` + ForkSourceKind + `' AND ` + k.FK + ` IN (` + matching(k, cond) + `) GROUP BY kind ORDER BY kind`
	},
}

// enrichmentFacetPrefix starts a facet on an enrichment key's values.
const enrichmentFacetPrefix = "enrichment:"

// enrichmentFacetSQL counts the matches of cond per string value, or list
// item, of the current value of the key and instance in args $key and $inst.
func enrichmentFacetSQL(k *Kind, cond, key, inst string) string {
	return `SELECT x.v, count(*)::int FROM (SELECT (SELECT e.value FROM enrichment e WHERE e.card_id = c.id AND e.key = ` + key +
		` AND e.instance_id = ` + inst + ` ORDER BY e.revision_id DESC, e.created_at DESC, e.id DESC LIMIT 1) AS value
			FROM ` + from(k) + ` WHERE ` + cond + `) m,
		LATERAL (SELECT m.value #>> '{}' AS v WHERE jsonb_typeof(m.value) = 'string'
			UNION SELECT i #>> '{}' FROM jsonb_array_elements(CASE jsonb_typeof(m.value) WHEN 'array' THEN m.value ELSE '[]' END) i
			WHERE jsonb_typeof(i) = 'string') x
		GROUP BY x.v ORDER BY count(*) DESC, x.v LIMIT 100`
}

// statesFacetPrefix starts a states facet that names its targets.
const statesFacetPrefix = "states:"

// statesFacetInstances returns the targets a states facet counts on, none
// for all of them, and whether name is a states facet at all.
func statesFacetInstances(name string) ([]int64, bool, error) {
	if name == "states" {
		return []int64{}, true, nil
	}
	list, ok := strings.CutPrefix(name, statesFacetPrefix)
	if !ok {
		return nil, false, nil
	}
	var ids []int64
	for s := range strings.SplitSeq(list, ",") {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil || id <= 0 {
			return nil, true, fmt.Errorf("%w: facet %q must name instance IDs", ErrInvalid, name)
		}
		ids = append(ids, id)
	}
	return ids, true, nil
}

// statesFacetSQL counts the matches of cond per state they have on a
// target, on the instances in arg $inst only when it isn't empty.
func statesFacetSQL(k *Kind, cond, inst string) string {
	return `SELECT status, count(DISTINCT ` + k.FK + `)::int FROM ` + pushedStatus + ` bs
		WHERE ` + k.FK + ` IN (` + matching(k, cond) + `) AND (cardinality(` + inst + `::bigint[]) = 0 OR instance_id = ANY(` + inst + `))
		GROUP BY status ORDER BY status`
}

// facets counts the matches of cond for each facet in names; an enrichment
// facet reads its named instance's values, or score's.
func (l *Library) facets(ctx context.Context, k *Kind, names []string, cond string, args []any, score int64) (*Facets, error) {
	f := &Facets{}
	for _, name := range names {
		if instances, ok, err := statesFacetInstances(name); ok {
			if err != nil {
				return nil, err
			}
			rows, err := l.pool.Query(ctx, statesFacetSQL(k, cond, "$"+strconv.Itoa(len(args)+1)), append(slices.Clone(args), instances)...)
			if err != nil {
				return nil, err
			}
			if f.States, err = pgx.CollectRows(rows, pgx.RowToStructByPos[StateCount]); err != nil {
				return nil, err
			}
			continue
		}
		if ref, ok := strings.CutPrefix(name, enrichmentFacetPrefix); ok {
			inst, key, err := enrichmentRef(ref)
			if err != nil {
				return nil, err
			}
			n := len(args)
			rows, err := l.pool.Query(ctx, enrichmentFacetSQL(k, cond, "$"+strconv.Itoa(n+1), "$"+strconv.Itoa(n+2)), append(slices.Clone(args), key, cmp.Or(inst, score))...)
			if err != nil {
				return nil, err
			}
			counts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ValueCount])
			if err != nil {
				return nil, err
			}
			if f.Enrichments == nil {
				f.Enrichments = map[string][]ValueCount{}
			}
			f.Enrichments[ref] = counts
			continue
		}
		rows, err := l.pool.Query(ctx, facetSQL[name](k, cond), args...)
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
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.IDs != nil, f.Name != nil, f.Tag != nil, f.Collection != nil, f.Source != nil, f.Archived != nil, f.TargetState != nil, f.Enrichment != nil, f.VariantOfRoot != nil, f.SharesSource != nil} {
		if ok {
			set++
		}
	}
	if set != 1 {
		return "", fmt.Errorf("%w: each filter needs exactly one condition", ErrInvalid)
	}
	if f.Enrichment == nil && (f.Gte != nil || f.Lte != nil || f.Missing != nil || f.Is != nil || f.Instance != nil) {
		return "", fmt.Errorf("%w: gte, lte, missing, is, and instance go with enrichment", ErrInvalid)
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
		// The card_revision_tag_keys index serves this.
		return `card_tag_keys(r.data) @> ARRAY[lower(` + w.arg(*f.Tag) + `)]`, nil
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
				UNION
				SELECT x.id FROM ` + w.k.Table + ` x JOIN down ON x.variant_of_id = down.id)
			SELECT id FROM down)`, nil
	case f.SharesSource != nil:
		return `(EXISTS (SELECT 1 FROM ` + w.k.SourceTable + ` s1 JOIN ` + w.k.SourceTable + ` s2
			ON s2.kind = s1.kind AND s2.ref = s1.ref AND s2.` + w.k.FK + ` <> s1.` + w.k.FK + ` AND s2.removed_at IS NULL
			WHERE s1.` + w.k.FK + ` = c.id AND s1.removed_at IS NULL AND s1.kind <> '` + ForkSourceKind + `')) = ` + w.arg(*f.SharesSource), nil
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
	for _, s := range q.Sources {
		if s != "library" {
			return "", nil, "", fmt.Errorf("%w: unknown source %q", ErrInvalid, s)
		}
	}
	w := &where{k: k, score: q.ScoreInstance}
	conds := []string{"c.library_id = " + w.arg(cmp.Or(q.Library, DefaultLibrary))}
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
	switch {
	case q.Semantic:
		s, err := w.semantic(q)
		if err != nil {
			return "", nil, "", err
		}
		conds = append(conds, s)
	case q.Text != "":
		text = w.arg(q.Text)
		conds = append(conds, "r.search @@ websearch_to_tsquery('english', "+text+")")
	}
	return strings.Join(conds, " AND "), w, text, nil
}

// semantic is the condition that a card has a vector from q's embed
// instance that compares with q's, which has to be the same length.
func (w *where) semantic(q Query) (string, error) {
	switch {
	case w.k != Card:
		return "", fmt.Errorf("%w: only cards have semantic search", ErrInvalid)
	case q.Text == "":
		return "", fmt.Errorf("%w: semantic search needs text", ErrInvalid)
	case q.Embedding == nil:
		// Only Search embeds the query; bulk selections never do.
		return "", fmt.Errorf("%w: semantic search lists cards but can't select them for an action", ErrInvalid)
	}
	return `EXISTS (SELECT 1 FROM card_embedding e WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.EmbedInstance) +
		` AND vector_dims(e.embedding) = ` + w.arg(len(q.Embedding)) + `)`, nil
}

// similarity is a card's cosine similarity to q's vector, as a real so a
// cursor holds it exactly. It is null for another length, which <=> would
// refuse even where the cursor predicate runs before compile's condition.
func (w *where) similarity(q Query) string {
	return `(SELECT CASE WHEN vector_dims(e.embedding) = ` + w.arg(len(q.Embedding)) +
		` THEN (1 - (e.embedding <=> ` + w.arg(vectorText(q.Embedding)) + `::vector))::real END
		FROM card_embedding e WHERE e.card_id = c.id AND e.instance_id = ` + w.arg(q.EmbedInstance) + `)`
}

// familyWalk is a WITH clause whose m holds the matches of cond, with cols,
// and whose up holds each variant match's ancestors. Most matches are their
// own root, so only variants walk up.
func familyWalk(k *Kind, cols, cond string) string {
	return `WITH RECURSIVE m AS (
		SELECT c.id, c.variant_of_id` + cols + ` FROM ` + from(k) + ` WHERE ` + cond + `
	), up AS (
		SELECT m.id AS member, p.id, p.variant_of_id FROM m JOIN ` + k.Table + ` p ON p.id = m.variant_of_id
		UNION
		SELECT up.member, p.id, p.variant_of_id FROM up JOIN ` + k.Table + ` p ON p.id = up.variant_of_id
	)`
}

// familyCount counts the families with a match for cond, for a page with
// none to count them by.
func familyCount(k *Kind, cond string) string {
	return familyWalk(k, "", cond) + ` SELECT count(*) FROM (
		SELECT id FROM m WHERE variant_of_id IS NULL UNION SELECT id FROM up WHERE variant_of_id IS NULL) roots`
}

// familiesOf is a WITH clause whose fam holds each family with a match for
// cond: its root, its best match's place under o, whether every match comes
// after the page cursor (after), and whether the root itself matches (own).
func familiesOf(k *Kind, cond string, o order, after string) string {
	return familyWalk(k, `, row_number() OVER (ORDER BY `+o.sql()+`) AS pos, `+after+` AS after, `+o.cursor()+` AS cur`, cond) + `, fam AS (
		SELECT root_id, min(pos) AS pos, bool_and(after) AS after, bool_or(own) AS own
		FROM (SELECT m.id AS root_id, m.pos, m.after, TRUE AS own FROM m WHERE m.variant_of_id IS NULL
			UNION ALL
			SELECT up.id, m.pos, m.after, FALSE FROM up JOIN m ON m.id = up.member WHERE up.variant_of_id IS NULL) x
		GROUP BY root_id
	)`
}

// familyPage follows familiesOf: it selects cols for one page of fam's
// faces after the cursor, best placed first, then each family's root, size,
// matches, and cursor, and how many families there are. Only the page's
// families get their matches and cursor, as every family's would cost a sort. The face is the shallowest live member, then the
// oldest, then the lowest ID; with no live member, the root. all counts
// archived members in size.
func familyPage(k *Kind, cols string, all bool, limit, offset string) string {
	size := `count(*) FILTER (WHERE d.archived_at IS NULL)::int`
	if all {
		size = `count(*)::int`
	}
	return `, pg AS (
		SELECT f.root_id, f.pos, m.cur, array(SELECT f.root_id WHERE f.own
			UNION SELECT up.member FROM up WHERE up.id = f.root_id AND up.variant_of_id IS NULL ORDER BY 1) AS matched
		FROM (SELECT * FROM fam WHERE after ORDER BY pos LIMIT ` + limit + ` OFFSET ` + offset + `) f JOIN m ON m.pos = f.pos
	), down AS (
		SELECT pg.root_id, e.id, 0 AS depth, e.archived_at, e.created_at FROM pg JOIN ` + k.Table + ` e ON e.id = pg.root_id
		UNION ALL
		SELECT down.root_id, e.id, down.depth + 1, e.archived_at, e.created_at FROM down JOIN ` + k.Table + ` e ON e.variant_of_id = down.id
	) CYCLE id SET is_cycle USING path, face AS (
		SELECT pg.root_id, pg.pos, pg.cur, pg.matched, f.id, f.size FROM pg CROSS JOIN LATERAL (
			SELECT (array_agg(d.id ORDER BY d.archived_at IS NOT NULL, d.depth, d.created_at, d.id))[1] AS id, ` + size + ` AS size
			FROM down d WHERE d.root_id = pg.root_id
		) f
	)
	SELECT ` + cols + `, face.root_id, face.size, face.matched, face.cur, (SELECT count(*) FROM fam)
	FROM face JOIN ` + from(k) + ` ON c.id = face.id ORDER BY face.pos`
}

// sortKey is an expression a search sorts by, and the SQL type a cursor
// value is cast back to.
type sortKey struct{ expr, typ string }

// order sorts by keys and then c.id, all descending or all ascending, so
// one row comparison resumes after a cursor. Keys are never null, since a
// null would make the comparison null.
type order struct {
	keys []sortKey
	desc bool
}

func (o order) all() []sortKey { return append(slices.Clone(o.keys), sortKey{"c.id", "bigint"}) }

func (o order) sql() string {
	dir := ""
	if o.desc {
		dir = " DESC"
	}
	parts := []string{}
	for _, k := range o.all() {
		parts = append(parts, k.expr+dir)
	}
	return strings.Join(parts, ", ")
}

// cursor is a row's position under o, as a jsonb array of its keys.
func (o order) cursor() string {
	parts := []string{}
	for _, k := range o.all() {
		parts = append(parts, k.expr)
	}
	return "jsonb_build_array(" + strings.Join(parts, ", ") + ")"
}

var errBadCursor = fmt.Errorf("%w: bad cursor; send next_cursor from a search with the same sort", ErrInvalid)

// after is the condition that a row comes after cursor under o.
func (o order) after(w *where, cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	var vals []json.RawMessage
	keys := o.all()
	if err != nil || json.Unmarshal(raw, &vals) != nil || len(vals) != len(keys) {
		return "", errBadCursor
	}
	c := w.arg(string(raw))
	lhs, rhs := []string{}, []string{}
	for i, k := range keys {
		if !cursorValueIs(k.typ, vals[i]) {
			return "", errBadCursor
		}
		lhs = append(lhs, k.expr)
		if k.typ == "jsonb" {
			rhs = append(rhs, fmt.Sprintf("(%s::jsonb -> %d)", c, i))
		} else {
			rhs = append(rhs, fmt.Sprintf("(%s::jsonb ->> %d)::%s", c, i, k.typ))
		}
	}
	op := " > "
	if o.desc {
		op = " < "
	}
	return "(" + strings.Join(lhs, ", ") + ")" + op + "(" + strings.Join(rhs, ", ") + ")", nil
}

// cursorValueIs reports whether v casts to typ. Only jsonb may be JSON null.
func cursorValueIs(typ string, v json.RawMessage) bool {
	if typ == "jsonb" {
		return true
	}
	if string(v) == "null" {
		return false
	}
	var dst any
	switch typ {
	case "real":
		var f float64
		if json.Unmarshal(v, &f) != nil {
			return false
		}
		// Postgres refuses a real that overflows or rounds to zero.
		f32, err := strconv.ParseFloat(string(v), 32)
		return err == nil && (f32 != 0 || f == 0)
	case "bigint":
		dst = new(int64)
	case "boolean":
		dst = new(bool)
	case "text":
		dst = new(string)
	}
	return json.Unmarshal(v, dst) == nil
}

// Search returns one page of the library's entities of kind k that match q,
// best match first when q has text, by meaning when q is semantic, newest
// first otherwise. With q.Group family, a page holds one hit per family,
// placed by its best match.
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
			tags = `r.tags`
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
		if _, ok, err := statesFacetInstances(name); ok {
			if err != nil {
				return Results{}, err
			}
			continue
		}
		if ref, ok := strings.CutPrefix(name, enrichmentFacetPrefix); ok {
			if k != Card {
				return Results{}, fmt.Errorf("%w: lorebooks have no enrichments", ErrInvalid)
			}
			inst, _, err := enrichmentRef(ref)
			if err != nil {
				return Results{}, err
			}
			if inst == 0 && q.ScoreInstance == 0 {
				return Results{}, errNoScoreInstance
			}
			continue
		}
		if facetSQL[name] == nil || k != Card && cardOnly(name) {
			return Results{}, fmt.Errorf("%w: unknown facet %q", ErrInvalid, name)
		}
	}
	o := order{desc: true}
	if q.Semantic && q.Sort != "" && q.Sort != "relevance" {
		return Results{}, fmt.Errorf("%w: semantic search sorts by relevance only", ErrInvalid)
	}
	switch q.Sort {
	case "", "relevance":
		switch {
		case q.Semantic:
			o.keys = []sortKey{{w.similarity(q), "real"}}
		case text != "":
			o.keys = []sortKey{{"ts_rank(r.search, websearch_to_tsquery('english', " + text + "))", "real"}}
		}
	case "updated":
		o.keys = []sortKey{{"c.head_revision_id", "bigint"}}
	case "name":
		name := "lower(r.data->>'name')"
		o = order{keys: []sortKey{{name + " IS NULL", "boolean"}, {"coalesce(" + name + ", '')", "text"}}}
	case "created":
	default:
		ref, ok := strings.CutPrefix(q.Sort, "enrichment:")
		if !ok {
			return Results{}, fmt.Errorf("%w: unknown sort %q", ErrInvalid, q.Sort)
		}
		inst, key, err := enrichmentRef(ref)
		if err != nil {
			return Results{}, err
		}
		cur, err := w.enrichmentSQL(key, inst)
		if err != nil {
			return Results{}, err
		}
		o.keys = []sortKey{{cur + " IS NOT NULL", "boolean"}, {"coalesce(" + cur + ", 'null')", "jsonb"}}
	}
	after := "TRUE"
	if q.Page.Cursor != nil {
		if after, err = o.after(w, *q.Page.Cursor); err != nil {
			return Results{}, err
		}
	}
	if q.Group != "" && q.Group != "family" {
		return Results{}, fmt.Errorf("%w: unknown group %q", ErrInvalid, q.Group)
	}
	res := Results{Hits: []Hit{}}
	if len(q.Facets) > 0 {
		if res.Facets, err = l.facets(ctx, k, q.Facets, cond, condArgs, q.ScoreInstance); err != nil {
			return Results{}, err
		}
	}
	if q.FacetsOnly {
		return res, nil
	}
	// A grouped page counts its families itself.
	if q.Group != "family" {
		if err := l.pool.QueryRow(ctx, `SELECT count(*) FROM `+from(k)+` WHERE `+cond, condArgs...).Scan(&res.Total); err != nil {
			return Results{}, fmt.Errorf("count matches: %w", err)
		}
	}
	cols := `c.id, c.slug, r.id, c.archived_at, coalesce(c.archived_reason, ''), r.data, r.avatar_blob, ` + tags + `, ` + books + `, ` + targets + `, ` + cards + `, ` + entries + `, ` + enrichments
	limit, skip := w.arg(size+1), w.arg(offset)
	sql := `SELECT ` + cols + `, NULL::bigint, NULL::int, NULL::bigint[], ` + o.cursor() + `, NULL::bigint FROM ` + from(k) + `
		WHERE ` + cond + ` AND ` + after + ` ORDER BY ` + o.sql() + ` LIMIT ` + limit + ` OFFSET ` + skip
	if q.Group == "family" {
		sql = familiesOf(k, cond, o, after) + familyPage(k, cols, mentionsArchived(q.Filter), limit, skip)
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
	var cur, next string
	var families *int
	_, err = pgx.ForEachRow(rows, []any{&h.ID, &h.Slug, &h.RevisionID, &h.ArchivedAt, &h.ArchivedReason, &data, &avatar, &h.Tags, &h.Lorebooks, &h.Targets, &h.Cards, &h.Entries, &h.Enrichments, &rootID, &famSize, &matched, &cur, &families},
		func() error {
			if len(res.Hits) == size-1 {
				next = base64.RawURLEncoding.EncodeToString([]byte(cur))
			}
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
		res.NextCursor = &next
	}
	switch {
	case families != nil:
		res.Total = *families
	case q.Group == "family":
		if err := l.pool.QueryRow(ctx, familyCount(k, cond), condArgs...).Scan(&res.Total); err != nil {
			return Results{}, fmt.Errorf("count families: %w", err)
		}
	}
	return res, nil
}

// UsesEnrichments reports whether q filters, sorts, or includes by
// enrichment, and so needs ScoreInstance.
func (q Query) UsesEnrichments() bool {
	return strings.HasPrefix(q.Sort, "enrichment:") || slices.Contains(q.Include, "enrichments") || filtersEnrichments(q.Filter) ||
		slices.ContainsFunc(q.Facets, func(f string) bool { return strings.HasPrefix(f, enrichmentFacetPrefix) })
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
	return l.SearchIDsAfter(ctx, k, q, 0, 0)
}

// SearchIDsAfter is SearchIDs for the IDs above after, in order, at most
// limit of them; limit 0 means all.
func (l *Library) SearchIDsAfter(ctx context.Context, k *Kind, q Query, after int64, limit int) ([]int64, error) {
	cond, w, _, err := compile(k, q)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT c.id FROM `+from(k)+` WHERE `+cond+` AND c.id > `+w.arg(after)+`
		ORDER BY c.id LIMIT nullif(`+w.arg(limit)+`, 0)`, w.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// CountMatches returns how many entities of kind k q matches.
func (l *Library) CountMatches(ctx context.Context, k *Kind, q Query) (int, error) {
	cond, w, _, err := compile(k, q)
	if err != nil {
		return 0, err
	}
	var n int
	err = l.pool.QueryRow(ctx, `SELECT count(*) FROM `+from(k)+` WHERE `+cond, w.args...).Scan(&n)
	return n, err
}
