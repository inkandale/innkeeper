package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
)

// ruleItem is a rule and what it has done.
type ruleItem struct {
	library.Rule
	Affected []library.RuleCount `json:"affected" doc:"How many cards it has archived or collected, by library."`
}

type rulesOut struct{ Body []ruleItem }

type ruleOut struct{ Body ruleItem }

type listRulesIn struct {
	Library int64 `query:"library" doc:"Only the rules that cover this library."`
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
		out[i] = ruleItem{Rule: r, Affected: []library.RuleCount{}}
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
