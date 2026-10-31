package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
)

// DiscoverPageIn is exported because huma skips unexported embedded structs.
type DiscoverPageIn struct {
	ID     int64  `path:"id"`
	Cursor string `query:"cursor" doc:"next_cursor from the previous page."`
	Limit  int    `query:"limit" default:"20" minimum:"1" maximum:"100"`
}

func (in DiscoverPageIn) page() library.Page {
	p := library.Page{Size: in.Limit}
	if in.Cursor != "" {
		p.Cursor = &in.Cursor
	}
	return p
}

type similarCardsIn struct {
	DiscoverPageIn
	Scope string `query:"scope" enum:"library,all" default:"library" doc:"library looks in the card's library; all, in every library."`
}

type similarCardsOut struct{ Body library.SimilarPage }

type relatedCardsIn struct {
	DiscoverPageIn
	Group string `query:"group" enum:"creator,lorebook,collection" doc:"One group to page through; without it, the first page of every group with results. A cursor needs it."`
}

type relatedCardsOut struct {
	Body struct {
		Groups []library.RelatedPage `json:"groups"`
	}
}

func (s *Server) discoverRoutes(api huma.API) {
	tags := []string{"Cards"}
	huma.Register(api, huma.Operation{OperationID: "list-similar-cards", Method: http.MethodGet, Path: "/cards/{id}/similar",
		Summary: "Find similar cards", Tags: tags,
		Description: "Cards that read like this one, closest first: by meaning when a vector source is set, else by shared tags. Leaves out exact copies, this card's variants, and archived cards."},
		func(ctx context.Context, in *similarCardsIn) (*similarCardsOut, error) {
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			inst, err := s.Registry.VectorInstance(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			res, err := s.Lib.SimilarCards(ctx, in.ID, library.SimilarOptions{Instance: inst, AllLibraries: in.Scope == "all", Page: in.page()})
			if err != nil {
				return nil, s.fail(err)
			}
			return &similarCardsOut{Body: res}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-related-cards", Method: http.MethodGet, Path: "/cards/{id}/related",
		Summary: "Find related cards", Tags: tags,
		Description: "Cards linked to this one: by the same creator, a shared lorebook, or a shared collection. Leaves out this card's variants and archived cards."},
		func(ctx context.Context, in *relatedCardsIn) (*relatedCardsOut, error) {
			if in.Cursor != "" && in.Group == "" {
				return nil, huma.Error422UnprocessableEntity("a cursor needs a group")
			}
			if _, err := s.Lib.Cards.Get(ctx, in.ID); err != nil {
				return nil, s.fail(err)
			}
			out := &relatedCardsOut{}
			out.Body.Groups = []library.RelatedPage{}
			groups := library.RelatedGroups
			if in.Group != "" {
				groups = []string{in.Group}
			}
			for _, g := range groups {
				res, err := s.Lib.RelatedCards(ctx, in.ID, g, in.page())
				if err != nil {
					return nil, s.fail(err)
				}
				if in.Group != "" || len(res.Items) > 0 {
					out.Body.Groups = append(out.Body.Groups, res)
				}
			}
			return out, nil
		})
}
