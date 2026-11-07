package library

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/inkandale/innkeeper/internal/db"
)

// Rule actions.
const (
	RuleArchive = "archive"
	RuleCollect = "collect"
)

// RuleSpec is a rule as the owner writes it.
type RuleSpec struct {
	Name          string  `json:"name" minLength:"1"`
	SavedSearchID int64   `json:"saved_search_id"`
	Action        string  `json:"action" enum:"archive,collect" doc:"Can't change once the rule exists."`
	CollectionID  *int64  `json:"collection_id,omitempty" doc:"For collect: the collection matching cards are kept in. The rule must cover that collection's library alone. Can't change once the rule exists."`
	AllLibraries  bool    `json:"all_libraries" doc:"Covers every library, including ones made later; otherwise only libraries."`
	Libraries     []int64 `json:"libraries,omitempty" doc:"With all_libraries false: the libraries it covers."`
	Enabled       bool    `json:"enabled" doc:"Ignored on create; a new rule starts disabled."`
}

// Rule is a saved search with an action the hide job keeps applied.
type Rule struct {
	ID int64 `json:"id"`
	RuleSpec
	SavedSearch string `json:"saved_search" doc:"Its saved search's name."`
	// Filter is its saved search's.
	Filter Filter `json:"-"`
}

// Covers reports whether r applies in library.
func (r Rule) Covers(library int64) bool {
	return r.Enabled && (r.AllLibraries || slices.Contains(r.Libraries, library))
}

// Reason is the archived_reason of the cards an archive rule archives.
func (r Rule) Reason() string { return "rule:" + strconv.FormatInt(r.ID, 10) }

// CheckRuleSpec refuses a spec whose action and collection don't go
// together, and a collect rule whose scope isn't its collection's library.
func (l *Library) CheckRuleSpec(ctx context.Context, spec RuleSpec) error {
	switch {
	case !slices.Contains([]string{RuleArchive, RuleCollect}, spec.Action):
		return fmt.Errorf("%w: a rule's action is archive or collect", ErrInvalid)
	case (spec.Action == RuleCollect) != (spec.CollectionID != nil):
		return fmt.Errorf("%w: a collect rule needs a collection, and only a collect rule takes one", ErrInvalid)
	case spec.Action != RuleCollect:
		return nil
	}
	var lib int64
	err := l.pool.QueryRow(ctx, `SELECT library_id FROM collection WHERE id = $1`, *spec.CollectionID).Scan(&lib)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: collection %d doesn't exist", ErrInvalid, *spec.CollectionID)
	}
	if err != nil {
		return err
	}
	if spec.AllLibraries || !slices.Equal(spec.Libraries, []int64{lib}) {
		return fmt.Errorf("%w: a collect rule covers its collection's library alone, library %d", ErrInvalid, lib)
	}
	return nil
}

func ruleErr(err error) error {
	switch {
	case db.IsCode(err, db.UniqueViolation):
		return ErrNameTaken
	case db.IsCode(err, db.CheckViolation):
		return fmt.Errorf("%w: a rule needs a name, with no spaces around it", ErrInvalid)
	case db.IsCode(err, db.ForeignKeyViolation):
		return fmt.Errorf("%w: its saved search or one of its libraries doesn't exist", ErrInvalid)
	}
	return mapErr(err)
}

// setRuleLibraries stores spec's libraries as rule id's scope.
func setRuleLibraries(ctx context.Context, tx pgx.Tx, id int64, spec RuleSpec) error {
	if _, err := tx.Exec(ctx, `DELETE FROM rule_library WHERE rule_id = $1`, id); err != nil {
		return err
	}
	if spec.AllLibraries || len(spec.Libraries) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO rule_library (rule_id, library_id) SELECT $1, unnest($2::bigint[]) ON CONFLICT DO NOTHING`,
		id, spec.Libraries)
	return err
}

// CreateRule saves a new rule, disabled whatever spec says.
func (l *Library) CreateRule(ctx context.Context, spec RuleSpec) (Rule, error) {
	if err := l.CheckRuleSpec(ctx, spec); err != nil {
		return Rule{}, err
	}
	var id int64
	err := pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO rule (name, saved_search_id, action, collection_id, all_libraries)
			VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			spec.Name, spec.SavedSearchID, spec.Action, spec.CollectionID, spec.AllLibraries).Scan(&id); err != nil {
			return err
		}
		return setRuleLibraries(ctx, tx, id, spec)
	})
	if err != nil {
		return Rule{}, ruleErr(err)
	}
	return l.GetRule(ctx, id)
}

// UpdateRule replaces rule id's spec, and returns the rule as it was and as
// it is. Its action and collection can't change, so it never loses track of
// what it did.
func (l *Library) UpdateRule(ctx context.Context, id int64, spec RuleSpec) (before, after Rule, err error) {
	if before, err = l.GetRule(ctx, id); err != nil {
		return before, after, err
	}
	if spec.Action != before.Action || !equalPtr(spec.CollectionID, before.CollectionID) {
		return before, after, fmt.Errorf("%w: a rule's action and collection can't change; make a new rule", ErrInvalid)
	}
	if err := l.CheckRuleSpec(ctx, spec); err != nil {
		return before, after, err
	}
	err = pgx.BeginFunc(ctx, l.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `UPDATE rule SET name = $2, saved_search_id = $3, all_libraries = $4, enabled = $5 WHERE id = $1`,
			id, spec.Name, spec.SavedSearchID, spec.AllLibraries, spec.Enabled); err != nil {
			return err
		}
		return setRuleLibraries(ctx, tx, id, spec)
	})
	if err != nil {
		return before, after, ruleErr(err)
	}
	after, err = l.GetRule(ctx, id)
	return before, after, err
}

func equalPtr[T comparable](a, b *T) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// DeleteRule deletes a rule and the memberships it added.
func (l *Library) DeleteRule(ctx context.Context, id int64) error {
	tag, err := l.pool.Exec(ctx, `DELETE FROM rule WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

const ruleCols = `r.id, r.name, r.saved_search_id, r.action, r.collection_id, r.all_libraries,
	array(SELECT library_id FROM rule_library WHERE rule_id = r.id ORDER BY library_id), r.enabled, s.name, s.filter
	FROM rule r JOIN saved_search s ON s.id = r.saved_search_id`

func scanRule(row pgx.Row) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.Name, &r.SavedSearchID, &r.Action, &r.CollectionID, &r.AllLibraries, &r.Libraries,
		&r.Enabled, &r.SavedSearch, &r.Filter)
	return r, err
}

func (l *Library) rulesWhere(ctx context.Context, where string, args ...any) ([]Rule, error) {
	rows, err := l.pool.Query(ctx, `SELECT `+ruleCols+` WHERE `+where+` ORDER BY r.id`, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Rule, error) { return scanRule(row) })
}

// GetRule returns one rule.
func (l *Library) GetRule(ctx context.Context, id int64) (Rule, error) {
	r, err := scanRule(l.pool.QueryRow(ctx, `SELECT `+ruleCols+` WHERE r.id = $1`, id))
	return r, mapErr(err)
}

// Rules returns every rule, oldest first.
func (l *Library) Rules(ctx context.Context) ([]Rule, error) { return l.rulesWhere(ctx, `TRUE`) }

// RulesOn returns the rules on saved search id.
func (l *Library) RulesOn(ctx context.Context, savedSearch int64) ([]Rule, error) {
	return l.rulesWhere(ctx, `s.id = $1`, savedSearch)
}

// RulesOfInstance returns the rules on the saved searches instance shipped.
func (l *Library) RulesOfInstance(ctx context.Context, instance int64) ([]Rule, error) {
	return l.rulesWhere(ctx, `s.instance_id = $1`, instance)
}

// RulesIn returns the rules the hide job applies in library, oldest first:
// those that cover it, and those that collected cards there before they
// stopped covering it.
func (l *Library) RulesIn(ctx context.Context, library int64) ([]Rule, error) {
	all, err := l.Rules(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := l.pool.Query(ctx, `SELECT DISTINCT cc.rule_id FROM collection_card cc JOIN card c ON c.id = cc.card_id
		WHERE cc.rule_id IS NOT NULL AND c.library_id = $1`, library)
	if err != nil {
		return nil, err
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(r Rule) bool { return !r.Covers(library) && !slices.Contains(applied, r.ID) }), nil
}

// HasRuleWork reports whether the hide job has rule work in library: a rule
// to apply, or a card a rule archived there, perhaps before a move.
func (l *Library) HasRuleWork(ctx context.Context, library int64) (bool, error) {
	rules, err := l.RulesIn(ctx, library)
	if err != nil || len(rules) > 0 {
		return len(rules) > 0, err
	}
	var archived bool
	err = l.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM card WHERE library_id = $1 AND archived_reason LIKE 'rule:%')`, library).Scan(&archived)
	return archived, err
}

// RuleScope returns the libraries r names: every library for all_libraries.
func (l *Library) RuleScope(ctx context.Context, r Rule) ([]int64, error) {
	if !r.AllLibraries {
		return r.Libraries, nil
	}
	rows, err := l.pool.Query(ctx, `SELECT id FROM library ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// RuleCount is how many cards a rule has archived or collected in one library.
type RuleCount struct {
	Rule    int64 `json:"-"`
	Library int64 `json:"library"`
	Cards   int   `json:"cards"`
}

// RuleCounts returns every rule's counts, by rule and library.
func (l *Library) RuleCounts(ctx context.Context) ([]RuleCount, error) {
	rows, err := l.pool.Query(ctx, `SELECT rule_id, library_id, sum(n)::int FROM (
			SELECT substr(archived_reason, 6)::bigint AS rule_id, library_id, count(*) AS n FROM card
			WHERE archived_reason LIKE 'rule:%' GROUP BY 1, 2
			UNION ALL SELECT cc.rule_id, c.library_id, count(*) FROM collection_card cc JOIN card c ON c.id = cc.card_id
			WHERE cc.rule_id IS NOT NULL GROUP BY 1, 2) x
		GROUP BY 1, 2 ORDER BY 1, 2`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[RuleCount])
}

// RuleMatches returns the IDs of library's cards that f matches, archived
// ones included, in ID order: all of them, or those in among unless among is
// nil. browsable, as Query.Browsable, resolves enrichment terms that name no
// instance.
func (l *Library) RuleMatches(ctx context.Context, library int64, f Filter, browsable map[string][]Scorer, among []int64) ([]int64, error) {
	if among != nil {
		f = Filter{And: []Filter{f, {IDs: among}}}
	}
	return l.SearchIDs(ctx, Card, Query{Library: library, Filter: &f, Browsable: browsable, IncludeArchived: true})
}

// RuleChanges returns what r would do in library given matches, the cards
// it matches there: add are the cards to archive or collect, remove those to
// give back. Only cards in among are considered, unless among is nil. Cards
// the owner archived or collected never count.
func (l *Library) RuleChanges(ctx context.Context, r Rule, library int64, matches, among []int64) (add, remove []int64, err error) {
	if matches == nil {
		matches = []int64{} // nil would reach SQL as NULL, and NOT (id = ANY(NULL)) matches nothing.
	}
	ids := func(sql string, args ...any) ([]int64, error) {
		rows, err := l.pool.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[int64])
	}
	switch r.Action {
	case RuleArchive:
		if add, err = ids(`SELECT id FROM card WHERE id = ANY($1) AND archived_at IS NULL ORDER BY id`, matches); err != nil {
			return nil, nil, err
		}
		remove, err = ids(`SELECT id FROM card WHERE library_id = $1 AND archived_reason = $2 AND NOT (id = ANY($3))
			AND ($4::bigint[] IS NULL OR id = ANY($4)) ORDER BY id`, library, r.Reason(), matches, among)
	case RuleCollect:
		if add, err = ids(`SELECT m.id FROM unnest($1::bigint[]) m(id) WHERE NOT EXISTS
			(SELECT 1 FROM collection_card cc WHERE cc.collection_id = $2 AND cc.card_id = m.id) ORDER BY m.id`, matches, *r.CollectionID); err != nil {
			return nil, nil, err
		}
		remove, err = ids(`SELECT cc.card_id FROM collection_card cc JOIN card c ON c.id = cc.card_id
			WHERE cc.rule_id = $1 AND c.library_id = $2 AND NOT (cc.card_id = ANY($3))
			AND ($4::bigint[] IS NULL OR cc.card_id = ANY($4)) ORDER BY cc.card_id`, r.ID, library, matches, among)
	default:
		return nil, nil, fmt.Errorf("%w: rule %d has action %q", ErrInvalid, r.ID, r.Action)
	}
	return add, remove, err
}

// CardLibraries returns the library of each of cardIDs that exists, by card ID.
func (l *Library) CardLibraries(ctx context.Context, cardIDs []int64) (map[int64]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT id, library_id FROM card WHERE id = ANY($1)`, cardIDs)
	if err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	var id, lib int64
	_, err = pgx.ForEachRow(rows, []any{&id, &lib}, func() error {
		out[id] = lib
		return nil
	})
	return out, err
}

// Unarchived returns which of cardIDs aren't archived, in ID order.
func (l *Library) Unarchived(ctx context.Context, cardIDs []int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT id FROM card WHERE id = ANY($1) AND archived_at IS NULL ORDER BY id`, cardIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// ArchivedAs returns which of cardIDs the hide job archived with reason,
// card ID to reason, as SetHidden takes them.
func (l *Library) ArchivedAs(ctx context.Context, cardIDs []int64, reason string) (map[int64]string, error) {
	rows, err := l.pool.Query(ctx, `SELECT id FROM card WHERE id = ANY($1) AND archived_reason = $2`, cardIDs, reason)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	out := make(map[int64]string, len(ids))
	for _, id := range ids {
		out[id] = reason
	}
	return out, nil
}
