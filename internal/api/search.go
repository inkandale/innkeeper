package api

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/embed"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

type searchIn struct{ Body library.Query }

// withEmbedding embeds q's text with the embed instance when q is a
// semantic card search, for Search to rank by. Anything else is left for
// Search to refuse with a 400.
func (s *Server) withEmbedding(ctx context.Context, k *library.Kind, q *library.Query) error {
	if !q.Semantic || q.Text == "" || k != library.Card {
		return nil
	}
	inst, err := embed.Instance(ctx, s.Registry)
	if err != nil {
		return err
	}
	if inst == 0 {
		return library.ErrNoEmbedInstance
	}
	vectors, err := s.Registry.Embed(ctx, s.Plugins, inst, []string{q.Text})
	if err != nil {
		return err
	}
	q.Embedding, q.EmbedInstance = vectors[0], inst
	return nil
}

// searchResults are the library's hits and a page from each catalog source.
type searchResults struct {
	library.Results
	Catalogs []catalogResults `json:"catalogs,omitempty" doc:"One per catalog:<id> source, in order"`
}

type searchOut struct{ Body searchResults }

// search runs q over the library's entities of kind k, if q asks for it, and
// over each catalog source for k.
func (s *Server) search(ctx context.Context, k *library.Kind, q library.Query) (*searchOut, error) {
	var catalogs []int64
	var rest []string
	for _, src := range q.Sources {
		v, ok := strings.CutPrefix(src, "catalog:")
		if !ok {
			rest = append(rest, src)
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, huma.Error400BadRequest(fmt.Sprintf("source %q: want catalog:<instance id>", src))
		}
		catalogs = append(catalogs, id)
	}
	var err error
	if q.Library, err = s.checkLibrary(ctx, q.Library); err != nil {
		return nil, err
	}
	out := &searchOut{Body: searchResults{Results: library.Results{Hits: []library.Hit{}}}}
	if len(rest) > 0 || len(catalogs) == 0 {
		q.Sources = rest
		decl, err := s.withScores(ctx, &q)
		if err != nil {
			return nil, s.fail(err)
		}
		if err := s.withEmbedding(ctx, k, &q); err != nil {
			return nil, s.fail(err)
		}
		search := s.Lib.Search
		if q.FacetsOnly {
			search = s.facetSearch
		}
		res, err := search(ctx, k, q)
		if err != nil {
			return nil, s.fail(err)
		}
		shownOnHits(res.Hits, decl)
		out.Body.Results = res
	}
	// Catalogs return only their first page, so later library pages leave them out.
	if q.Page.Cursor != nil || q.Page.Number > 1 {
		catalogs = nil
	}
	p := protocol.CatalogSearchParams{Q: q.Text, Kind: k.Name, Page: protocol.CatalogPage{Size: min(cmp.Or(q.Page.Size, 50), protocol.MaxCatalogPage)}}
	for _, id := range catalogs {
		res, err := s.searchCatalog(ctx, id, q.Library, p)
		var se huma.StatusError
		switch {
		case errors.As(err, &se) && se.GetStatus() == http.StatusNotFound:
			return nil, huma.Error400BadRequest(fmt.Sprintf("source \"catalog:%d\": %v", id, err))
		case err != nil:
			res = catalogResults{CatalogID: id, Items: []catalogHit{}, Error: err.Error()}
		}
		out.Body.Catalogs = append(out.Body.Catalogs, res)
	}
	return out, nil
}

func (s *Server) searchRoutes(api huma.API) {
	registerMoved(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "search-cards", Method: http.MethodPost, Path: "/cards/search",
		Summary: "Search the library",
		Description: "Send {} to list every card that isn't archived, newest first. " +
			"With catalog:<id> sources, also returns each catalog's first page of cards matching text, on the first library page only.",
		Tags: []string{"Search"}}, "/search",
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			return s.search(ctx, library.Card, in.Body)
		})
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "search-lorebooks", Method: http.MethodPost, Path: "/lorebooks/search",
		Summary: "Search the library's lorebooks",
		Description: "Takes the same query as POST /cards/search. Text matches names, descriptions, entry keys, and entry content. " +
			"Lorebooks have no tags or collections. Send {} to list every lorebook that isn't archived, newest first. " +
			"With catalog:<id> sources, also returns each catalog's first page of lorebooks matching text, on the first library page only.",
		Tags: []string{"Search"}},
		func(ctx context.Context, in *searchIn) (*searchOut, error) {
			return s.search(ctx, library.Lorebook, in.Body)
		})
}
