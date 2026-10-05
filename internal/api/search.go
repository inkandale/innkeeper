package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type searchIn struct{ Body library.Query }

type searchOut struct{ Body library.Results }

func (s *Server) searchRoutes(api huma.API) {
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "search-cards", Method: http.MethodPost, Path: "/search",
		Summary: "Search the library", Description: "Send {} to list every card that isn't archived, newest first.",
		Tags: []string{"Search"}},
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			res, err := s.Lib.Search(ctx, in.Body)
			if err != nil {
				return nil, s.fail(err)
			}
			return &searchOut{Body: res}, nil
		})
}
