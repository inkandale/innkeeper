package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
)

// ruleItem is a rule and what it has done.
// It copies library.Rule's fields rather than embed it: huma can't add $schema
// beside an embedded type that has methods.
type ruleItem struct {
	library.RuleSpec
	ID          int64               `json:"id"`
	SavedSearch string              `json:"saved_search" doc:"Its saved search's name."`
	Affected    []library.RuleCount `json:"affected" doc:"How many cards it has archived or collected, by library."`
}

type rulesOut struct{ Body []ruleItem }

type ruleOut struct{ Body ruleItem }

type listRulesIn struct {
	Library int64 `query:"library" doc:"Only the rules that cover this library."`
}

type previewRuleIn struct {
	Body struct {
		ID   int64            `json:"id,omitempty" doc:"The rule being edited, so the cards it would give back count. Leave it out for a new rule."`
		Rule library.RuleSpec `json:"rule" doc:"The rule as it would be saved. It is previewed enabled."`
		Page int              `json:"page,omitempty" minimum:"0" maximum:"100000" doc:"1-based page of every list. Default 1."`
	}
}

// previewList is how many cards a rule would change one way, and one page of them.
type previewList struct {
	Total int           `json:"total"`
	Hits  []library.Hit `json:"hits" doc:"One page, newest first, with name and avatar."`
}

// rulePreview is what a rule would do in one library.
type rulePreview struct {
	Library  int64       `json:"library"`
	Apply    previewList `json:"apply" doc:"Cards it would archive or add to the collection."`
	GiveBack previewList `json:"give_back" doc:"Cards it would unarchive or take out of the collection."`
}

type rulePreviewOut struct{ Body []rulePreview }

// ptrSlice is p as a slice, so two optional values compare with slices.Equal.
func ptrSlice(p *int64) []int64 {
	if p == nil {
		return nil
	}
	return []int64{*p}
}

// previewPage is how many cards a preview list shows at a time.
const previewPage = 24

// previewList is ids' count and the page of them as hits.
func (s *Server) previewList(ctx context.Context, lib int64, ids []int64, page int) (previewList, error) {
	l := previewList{Total: len(ids), Hits: []library.Hit{}}
	ids = slices.Clone(ids)
	slices.Reverse(ids) // ascending in, newest (highest ID) first out
	start := min((page-1)*previewPage, len(ids))
	shown := ids[start:min(start+previewPage, len(ids))]
	if len(shown) == 0 {
		return l, nil
	}
	res, err := s.Lib.Search(ctx, library.Card, library.Query{Library: lib, Filter: &library.Filter{IDs: shown}, IncludeArchived: true,
		Fields: []string{"name", "avatar"}, Page: library.Page{Size: previewPage}})
	l.Hits = res.Hits
	return l, err
}

// previewChanges is RuleChanges for r in lib, less what the hide job would
// hand to another archive rule: a card a lower-ID rule matches isn't r's to
// archive, and one another rule matches isn't given back. Pending duplicates
// aren't predicted; the cards already archived as duplicates never count.
func (s *Server) previewChanges(ctx context.Context, r library.Rule, lib int64, matches []int64,
	browsable map[string][]library.Scorer) (add, remove []int64, err error) {
	if add, remove, err = s.Lib.RuleChanges(ctx, r, lib, matches, nil); err != nil || r.Action != library.RuleArchive {
		return add, remove, err
	}
	others, err := s.Lib.RulesIn(ctx, lib)
	if err != nil {
		return nil, nil, err
	}
	for _, o := range others {
		if o.ID == r.ID || o.Action != library.RuleArchive || !o.Covers(lib) {
			continue
		}
		m, err := s.Lib.RuleMatches(ctx, lib, o.Filter, browsable, nil)
		if err != nil {
			return nil, nil, err
		}
		taken := make(map[int64]bool, len(m))
		for _, id := range m {
			taken[id] = true
		}
		if r.ID == 0 || o.ID < r.ID { // a new rule gets the highest ID
			add = slices.DeleteFunc(add, func(id int64) bool { return taken[id] })
		}
		remove = slices.DeleteFunc(remove, func(id int64) bool { return taken[id] })
	}
	return add, remove, nil
}

type createRuleIn struct{ Body library.RuleSpec }

type updateRuleIn struct {
	ID   int64 `path:"id"`
	Body library.RuleSpec
}

// ruleItems adds each rule's counts.
func (s *Server) ruleItems(ctx context.Context, rules []library.Rule) ([]ruleItem, error) {
	counts, err := s.Lib.RuleCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ruleItem, len(rules))
	for i, r := range rules {
		out[i] = ruleItem{RuleSpec: r.RuleSpec, ID: r.ID, SavedSearch: r.SavedSearch, Affected: []library.RuleCount{}}
		for _, c := range counts {
			if c.Rule == r.ID {
				out[i].Affected = append(out[i].Affected, c)
			}
		}
	}
	return out, nil
}

func (s *Server) ruleItem(ctx context.Context, r library.Rule) (*ruleOut, error) {
	items, err := s.ruleItems(ctx, []library.Rule{r})
	if err != nil {
		return nil, s.fail(err)
	}
	return &ruleOut{Body: items[0]}, nil
}

// recheckRules reruns the hide job in every library an enabled one of rules
// names, so their change is applied or undone there.
func (s *Server) recheckRules(ctx context.Context, rules ...library.Rule) error {
	var libs []int64
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		scope, err := s.Lib.RuleScope(ctx, r)
		if err != nil {
			return err
		}
		libs = append(libs, scope...)
	}
	slices.Sort(libs)
	var errs []error
	for _, lib := range slices.Compact(libs) {
		if err := s.libSettings().RecheckHide(ctx, s.Queue, lib); err != nil {
			errs = append(errs, fmt.Errorf("queue hide for library %d: %w", lib, err))
		}
	}
	return errors.Join(errs...)
}

// dropRules runs del, which deletes rules, then reruns the hide job where
// they applied so their archived cards come back.
func (s *Server) dropRules(ctx context.Context, rules []library.Rule, del func() error) error {
	if err := del(); err != nil {
		return err
	}
	return s.recheckRules(ctx, rules...)
}

func (s *Server) ruleRoutes(api huma.API) {
	tags := []string{"Rules"}
	huma.Register(api, huma.Operation{OperationID: "list-rules", Method: http.MethodGet, Path: "/rules",
		Summary: "List rules", Description: "Oldest first, each with how many cards it has archived or collected in each library.", Tags: tags},
		func(ctx context.Context, in *listRulesIn) (*rulesOut, error) {
			rules, err := s.Lib.Rules(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			if in.Library != 0 {
				rules = slices.DeleteFunc(rules, func(r library.Rule) bool { return !r.Covers(in.Library) })
			}
			items, err := s.ruleItems(ctx, rules)
			if err != nil {
				return nil, s.fail(err)
			}
			return &rulesOut{Body: orEmpty(items)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "preview-rule", Method: http.MethodPost, Path: "/rules/preview",
		Summary: "Preview a rule", Tags: tags,
		Description: "Returns, for each library the rule would cover or covers now, how many cards it would archive or collect " +
			"and how many it would give back, with a page of each. Changes nothing. An archive count leaves out cards " +
			"another archive rule or an earlier duplicate run already holds; duplicates the hide job hasn't found yet aren't predicted."},
		func(ctx context.Context, in *previewRuleIn) (*rulePreviewOut, error) {
			spec := in.Body.Rule
			spec.Enabled = true
			var old library.Rule
			if in.Body.ID != 0 {
				var err error
				if old, err = s.Lib.GetRule(ctx, in.Body.ID); err != nil {
					return nil, s.fail(err)
				}
				if spec.Action != old.Action || !slices.Equal(ptrSlice(spec.CollectionID), ptrSlice(old.CollectionID)) {
					return nil, huma.Error422UnprocessableEntity("a rule's action and collection can't change; make a new rule")
				}
			}
			if err := s.Lib.CheckRuleSpec(ctx, spec); err != nil {
				return nil, s.fail(err)
			}
			for _, id := range spec.Libraries {
				if _, err := s.checkLibrary(ctx, id); err != nil {
					return nil, err
				}
			}
			search, err := s.Lib.GetSavedSearch(ctx, spec.SavedSearchID)
			if errors.Is(err, library.ErrNotFound) {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("saved search %d doesn't exist", spec.SavedSearchID))
			}
			if err != nil {
				return nil, s.fail(err)
			}
			r := library.Rule{ID: in.Body.ID, RuleSpec: spec, Filter: search.Filter}
			libs, err := s.Lib.RuleScope(ctx, r)
			if err != nil {
				return nil, s.fail(err)
			}
			if in.Body.ID != 0 {
				was, err := s.Lib.RuleScope(ctx, old)
				if err != nil {
					return nil, s.fail(err)
				}
				libs = append(slices.Clone(libs), was...)
			}
			slices.Sort(libs)
			browsable, err := s.libSettings().Browsable(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			page := max(in.Body.Page, 1)
			out := []rulePreview{}
			for _, lib := range slices.Compact(libs) {
				var matches []int64
				if r.Covers(lib) {
					if matches, err = s.Lib.RuleMatches(ctx, lib, r.Filter, browsable, nil); err != nil {
						return nil, s.fail(err)
					}
				}
				add, remove, err := s.previewChanges(ctx, r, lib, matches, browsable)
				if err != nil {
					return nil, s.fail(err)
				}
				p := rulePreview{Library: lib}
				if p.Apply, err = s.previewList(ctx, lib, add, page); err != nil {
					return nil, s.fail(err)
				}
				if p.GiveBack, err = s.previewList(ctx, lib, remove, page); err != nil {
					return nil, s.fail(err)
				}
				out = append(out, p)
			}
			return &rulePreviewOut{Body: out}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-rule", Method: http.MethodGet, Path: "/rules/{id}",
		Summary: "Get a rule", Tags: tags},
		func(ctx context.Context, in *idIn) (*ruleOut, error) {
			r, err := s.Lib.GetRule(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return s.ruleItem(ctx, r)
		})

	huma.Register(api, huma.Operation{OperationID: "create-rule", Method: http.MethodPost, Path: "/rules",
		Summary: "Add a rule", Description: "It starts disabled. Preview it with POST /rules/preview, then enable it with PUT.",
		Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createRuleIn) (*ruleOut, error) {
			r, err := s.Lib.CreateRule(ctx, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return s.ruleItem(ctx, r)
		})

	huma.Register(api, huma.Operation{OperationID: "update-rule", Method: http.MethodPut, Path: "/rules/{id}",
		Summary: "Change a rule", Tags: tags,
		Description: "Replaces the rule. Its action and collection can't change. The hide job reruns in every library it covered or covers, " +
			"applying it and giving back the cards it no longer matches."},
		func(ctx context.Context, in *updateRuleIn) (*ruleOut, error) {
			before, after, err := s.Lib.UpdateRule(ctx, in.ID, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			if err := s.recheckRules(ctx, before, after); err != nil {
				return nil, s.fail(err)
			}
			return s.ruleItem(ctx, after)
		})

	huma.Register(api, huma.Operation{OperationID: "delete-rule", Method: http.MethodDelete, Path: "/rules/{id}",
		Summary: "Delete a rule", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Gives back what it did: its archived cards come back, and the cards it collected leave the collection."},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			r, err := s.Lib.GetRule(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return nil, s.fail(s.dropRules(ctx, []library.Rule{r}, func() error { return s.Lib.DeleteRule(ctx, in.ID) }))
		})
}
