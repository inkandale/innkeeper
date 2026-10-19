package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// catalogTimeout bounds each call the API makes to a catalog.
const catalogTimeout = 30 * time.Second

// catalogDescribe is a catalog's kinds, filters, and sorts.
var catalogDescribe = listMethod[protocol.CatalogInfo, protocol.CatalogInfo]{name: protocol.MethodCatalogDescribe,
	clean: func(r protocol.CatalogInfo, _ func(string, error)) protocol.CatalogInfo { return r }}

type catalogOut struct {
	ID     int64                `json:"id"`
	Name   string               `json:"name"`
	Plugin string               `json:"plugin"`
	Info   protocol.CatalogInfo `json:"info" doc:"The catalog's kinds, filters, and sorts"`
}

type catalogsOut struct{ Body []catalogOut }

type catalogSearchIn struct {
	ID      int64  `path:"id"`
	Q       string `query:"q" doc:"Search text; empty matches everything"`
	Kind    string `query:"kind" doc:"One of the catalog's kinds; empty for all"`
	Filters string `query:"filters" doc:"A JSON object of filter values by name, as the catalog declares them, such as {\"site\": \"chub\"}"`
	Sort    string `query:"sort" doc:"One of the catalog's sorts; empty for its first"`
	Size    int    `query:"size" minimum:"0" maximum:"100" doc:"Default: the catalog's"`
	Cursor  string `query:"cursor" doc:"next from the previous page"`
}

// catalogHit is a catalog item and the library entries that match it.
type catalogHit struct {
	protocol.CatalogItem
	Library []library.HashMatch `json:"library" doc:"Library entries of the item's kind that match it, closest first. content means the same content."`
}

// catalogResults is one page from one catalog.
type catalogResults struct {
	CatalogID int64        `json:"catalog_id"`
	Items     []catalogHit `json:"items"`
	Next      string       `json:"next,omitempty" doc:"Cursor for the next page, for GET /catalogs/{id}/search"`
	Error     string       `json:"error,omitempty" doc:"In POST /cards/search and POST /lorebooks/search: why this catalog returned nothing"`
}

type catalogResultsOut struct{ Body catalogResults }

type catalogItemIn struct {
	ID      int64  `path:"id"`
	Item    string `path:"item" doc:"The catalog's item ID, with / escaped as %2F"`
	Version string `query:"version" doc:"Default: the latest"`
}

type catalogItemOut struct{ Body protocol.CatalogGetResult }

// catalogCall sends method to catalog instance id with a timeout, and maps
// its errors to HTTP ones.
func (s *Server) catalogCall(ctx context.Context, id int64, method string, params, result any) error {
	inst, err := s.Registry.InstanceWith(ctx, id, protocol.CapabilityCatalog)
	if errors.Is(err, plugins.ErrIncapable) || errors.Is(err, plugins.ErrUnknownPlugin) {
		return huma.Error404NotFound(fmt.Sprintf("instance %d is not a catalog", id))
	}
	if err != nil {
		return s.fail(err)
	}
	callCtx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	err = s.Registry.Call(callCtx, s.Plugins, inst, method, params, result)
	if err == nil || errors.Is(err, context.Canceled) {
		// Canceled: the client went away; there is no one to answer.
		return err
	}
	// The name is read only on failure, so a call that works looks the instance up once.
	name := "instance " + inst.ID
	if info, ierr := s.Registry.GetInstance(ctx, id); ierr == nil {
		name = info.Label
	}
	return s.failAs("catalog "+name, err)
}

// catalogs lists each working catalog instance with its description. One
// that is broken, or doesn't describe itself in time, is left out.
func (s *Server) catalogs(ctx context.Context) ([]catalogOut, error) {
	list, err := s.Registry.InstancesWith(ctx, protocol.CapabilityCatalog)
	if err != nil {
		return nil, err
	}
	out := collect(ctx, s, &s.catalogInfo, catalogDescribe, list, func(i plugins.InstanceInfo, info protocol.CatalogInfo) []catalogOut {
		return []catalogOut{{ID: i.ID, Name: i.Label, Plugin: i.Plugin, Info: info}}
	})
	slices.SortFunc(out, func(a, b catalogOut) int { return cmp.Compare(a.ID, b.ID) })
	return out, nil
}

// withMatches adds each item's matches in library lib, with one query per kind.
func (s *Server) withMatches(ctx context.Context, lib int64, items []protocol.CatalogItem) ([]catalogHit, error) {
	hits := make([]catalogHit, len(items))
	byKind := map[string][]int{}
	for i, it := range items {
		hits[i] = catalogHit{CatalogItem: it, Library: []library.HashMatch{}}
		byKind[it.Kind] = append(byKind[it.Kind], i)
	}
	for kind, idx := range byKind {
		k, ok := library.KindNamed(kind)
		if !ok {
			continue
		}
		hashes := make([]library.Hashes, len(idx))
		for j, i := range idx {
			h := items[i].Hashes
			hashes[j] = library.Hashes{ContentHash: h.ContentHash, TextSimhash: h.TextSimhash, ImagePhash: h.ImagePhash}
		}
		matches, err := s.Lib.Entities(k).MatchHashes(ctx, lib, hashes, library.NearDistance)
		if err != nil {
			return nil, err
		}
		for j, i := range idx {
			hits[i].Library = orEmpty(matches[j])
		}
	}
	return hits, nil
}

// searchCatalog returns one page from a catalog with its matches in library lib.
func (s *Server) searchCatalog(ctx context.Context, id, lib int64, p protocol.CatalogSearchParams) (catalogResults, error) {
	var page protocol.CatalogSearchResult
	if err := s.catalogCall(ctx, id, protocol.MethodCatalogSearch, p, &page); err != nil {
		return catalogResults{}, err
	}
	hits, err := s.withMatches(ctx, lib, page.Items)
	if err != nil {
		return catalogResults{}, s.fail(err)
	}
	return catalogResults{CatalogID: id, Items: hits, Next: page.Next}, nil
}

func (s *Server) catalogRoutes(api huma.API) {
	tags := []string{"Catalogs"}
	huma.Register(api, huma.Operation{OperationID: "list-catalogs", Method: http.MethodGet, Path: "/catalogs",
		Summary: "List catalogs", Description: "Every working instance of a catalog plugin, with its description. " +
			"Instances that are broken, or whose plugin doesn't answer, are left out.", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*catalogsOut, error) {
			list, err := s.catalogs(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &catalogsOut{Body: list}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "search-catalog", Method: http.MethodGet, Path: "/catalogs/{id}/search",
		Summary: "Search a catalog", Description: "Returns one page of items, each with the library entries that match it.", Tags: tags},
		func(ctx context.Context, in *catalogSearchIn) (*catalogResultsOut, error) {
			p := protocol.CatalogSearchParams{Q: in.Q, Kind: in.Kind, Sort: in.Sort,
				Page: protocol.CatalogPage{Size: in.Size, Cursor: in.Cursor}}
			if in.Filters != "" {
				if err := json.Unmarshal([]byte(in.Filters), &p.Filters); err != nil {
					return nil, huma.Error400BadRequest("filters must be a JSON object: " + err.Error())
				}
			}
			res, err := s.searchCatalog(ctx, in.ID, library.DefaultLibrary, p)
			if err != nil {
				return nil, err
			}
			return &catalogResultsOut{Body: res}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "get-catalog-item", Method: http.MethodGet, Path: "/catalogs/{id}/items/{item}",
		Summary: "Get a catalog item", Description: "One version as CCv3, with the item's versions, newest first.", Tags: tags},
		func(ctx context.Context, in *catalogItemIn) (*catalogItemOut, error) {
			out := &catalogItemOut{}
			params := protocol.CatalogGetParams{ID: in.Item, Version: in.Version}
			if err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogGet, params, &out.Body); err != nil {
				return nil, err
			}
			// Never hand a client a server path.
			out.Body.Avatar = ""
			return out, nil
		})
}
