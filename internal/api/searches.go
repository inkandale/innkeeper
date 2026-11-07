package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
)

// ruleRef names a rule.
type ruleRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// savedSearchItem is a saved search, the rules on it, and how many cards it matches in the library asked about.
type savedSearchItem struct {
	library.SavedSearch
	Rules []ruleRef `json:"rules,omitempty" doc:"The rules on it. Listing only."`
	Cards *int      `json:"cards,omitempty" doc:"How many unarchived cards it matches in the library asked about. Missing when its filter no longer reads, such as when an instance it names is gone."`
}

type savedSearchesOut struct{ Body []savedSearchItem }

type savedSearchOut struct{ Body savedSearchItem }

type createSavedSearchIn struct {
	Body struct {
		Name   string         `json:"name" minLength:"1"`
		Filter library.Filter `json:"filter"`
		Sort   string         `json:"sort,omitempty" pattern:"^(relevance|updated|name|created|enrichment:([1-9][0-9]*:)?[a-z][a-z0-9_]*)?$" doc:"The sort to open it with, as search takes it."`
	}
}

type updateSavedSearchIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name   *string         `json:"name,omitempty" minLength:"1"`
		Filter *library.Filter `json:"filter,omitempty"`
		Sort   *string         `json:"sort,omitempty" pattern:"^(relevance|updated|name|created|enrichment:([1-9][0-9]*:)?[a-z][a-z0-9_]*)?$"`
	}
}

// checkFilter refuses a filter search would refuse.
func (s *Server) checkFilter(ctx context.Context, f library.Filter) error {
	browsable, err := s.libSettings().Browsable(ctx)
	if err != nil {
		return err
	}
	return library.CheckFilter(f, browsable)
}

// savedSearchItems adds each search's match count in lib.
func (s *Server) savedSearchItems(ctx context.Context, lib int64, searches []library.SavedSearch) ([]savedSearchItem, error) {
	browsable, err := s.libSettings().Browsable(ctx)
	if err != nil {
		return nil, err
	}
	rules, err := s.Lib.Rules(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]savedSearchItem, len(searches))
	for i, ss := range searches {
		out[i].SavedSearch = ss
		for _, r := range rules {
			if r.SavedSearchID == ss.ID {
				out[i].Rules = append(out[i].Rules, ruleRef{r.ID, r.Name})
			}
		}
		n, err := s.Lib.CountMatches(ctx, library.Card, library.Query{Library: lib, Filter: &ss.Filter, Browsable: browsable})
		switch {
		case errors.Is(err, library.ErrInvalid):
		case err != nil:
			return nil, err
		default:
			out[i].Cards = &n
		}
	}
	return out, nil
}

func (s *Server) savedSearchRoutes(api huma.API) {
	tags := []string{"Saved searches"}
	huma.Register(api, huma.Operation{OperationID: "list-saved-searches", Method: http.MethodGet, Path: "/saved-searches",
		Summary: "List saved searches", Description: "By name, each with how many cards it matches in the library.", Tags: tags},
		func(ctx context.Context, in *libraryQuery) (*savedSearchesOut, error) {
			lib, err := s.checkLibrary(ctx, in.Library)
			if err != nil {
				return nil, err
			}
			all, err := s.Lib.SavedSearches(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			items, err := s.savedSearchItems(ctx, lib, all)
			if err != nil {
				return nil, s.fail(err)
			}
			return &savedSearchesOut{Body: orEmpty(items)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-saved-search", Method: http.MethodGet, Path: "/saved-searches/{id}",
		Summary: "Get a saved search", Tags: tags},
		func(ctx context.Context, in *idIn) (*savedSearchOut, error) {
			ss, err := s.Lib.GetSavedSearch(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			return &savedSearchOut{Body: savedSearchItem{SavedSearch: ss}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-saved-search", Method: http.MethodPost, Path: "/saved-searches",
		Summary: "Save a search", Description: "The filter is checked as search checks it.", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createSavedSearchIn) (*savedSearchOut, error) {
			if err := s.checkFilter(ctx, in.Body.Filter); err != nil {
				return nil, s.fail(err)
			}
			ss, err := s.Lib.CreateSavedSearch(ctx, in.Body.Name, in.Body.Filter, in.Body.Sort)
			if err != nil {
				return nil, s.fail(err)
			}
			return &savedSearchOut{Body: savedSearchItem{SavedSearch: ss}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "update-saved-search", Method: http.MethodPatch, Path: "/saved-searches/{id}",
		Summary: "Change a saved search", Tags: tags},
		func(ctx context.Context, in *updateSavedSearchIn) (*savedSearchOut, error) {
			if in.Body.Filter != nil {
				if err := s.checkFilter(ctx, *in.Body.Filter); err != nil {
					return nil, s.fail(err)
				}
			}
			ss, err := s.Lib.UpdateSavedSearch(ctx, in.ID, in.Body.Name, in.Body.Filter, in.Body.Sort)
			if err != nil {
				return nil, s.fail(err)
			}
			if in.Body.Filter != nil {
				rules, err := s.Lib.RulesOn(ctx, in.ID)
				if err != nil {
					return nil, s.fail(err)
				}
				if err := s.recheckRules(ctx, rules...); err != nil {
					return nil, s.fail(err)
				}
			}
			return &savedSearchOut{Body: savedSearchItem{SavedSearch: ss}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "delete-saved-search", Method: http.MethodDelete, Path: "/saved-searches/{id}",
		Summary: "Delete a saved search", Description: "Refused with 409, naming them, while rules use it.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			rules, err := s.Lib.RulesOn(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			if len(rules) > 0 {
				names := make([]string, len(rules))
				for i, r := range rules {
					names[i] = r.Name
				}
				return nil, huma.Error409Conflict("rules use this saved search; delete them first: " + strings.Join(names, ", "))
			}
			return nil, s.fail(s.Lib.DeleteSavedSearch(ctx, in.ID))
		})
}
