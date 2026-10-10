package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// catalogTimeout bounds each call the API makes to a catalog.
const catalogTimeout = 30 * time.Second

type catalogOut struct {
	ID         int64                 `json:"id"`
	Name       string                `json:"name"`
	Plugin     string                `json:"plugin"`
	Info       *protocol.CatalogInfo `json:"info,omitempty" doc:"The catalog's kinds, filters, and sorts, and whether it ingests and finds similar items"`
	Error      string                `json:"error,omitempty" doc:"Why the catalog couldn't describe itself"`
	LastIngest *jobOut               `json:"last_ingest,omitempty" doc:"The newest catalog.ingest job"`
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
	Error     string       `json:"error,omitempty" doc:"In POST /search: why this catalog returned nothing"`
}

type catalogResultsOut struct{ Body catalogResults }

type catalogItemIn struct {
	ID      int64  `path:"id"`
	Item    string `path:"item" doc:"The catalog's item ID, with / escaped as %2F"`
	Version string `query:"version" doc:"Default: the latest"`
}

type catalogItemOut struct{ Body protocol.CatalogGetResult }

// catalogCall sends method to catalog instance id with a timeout, and maps
// its errors to HTTP ones. It returns the instance's settings.
func (s *Server) catalogCall(ctx context.Context, id int64, method string, params, result any) (plugins.InstanceInfo, error) {
	info, err := s.Registry.GetInstance(ctx, id)
	if err != nil {
		return info, s.fail(err)
	}
	if !s.Registry.HasCapability(info.Plugin, "catalog") {
		return info, huma.Error404NotFound(fmt.Sprintf("instance %d is not a catalog", id))
	}
	inst, err := s.Registry.Instance(ctx, id)
	if err != nil {
		return info, s.fail(err)
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	err = s.Registry.Call(ctx, s.Plugins, inst, method, params, result)
	var rpcErr *protocol.Error
	var startErr *pluginhost.StartError
	switch {
	case err == nil:
		return info, nil
	case errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams:
		return info, huma.Error400BadRequest(rpcErr.Message)
	case errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeRemoteNotFound:
		return info, huma.Error404NotFound(rpcErr.Message)
	case errors.As(err, &startErr), errors.Is(err, protocol.ErrClosed), errors.Is(err, context.DeadlineExceeded),
		errors.As(err, &rpcErr) && rpcErr.Retryable():
		s.Log.Warn("catalog unavailable", "catalog", info.Name, "err", err)
		return info, huma.Error503ServiceUnavailable(fmt.Sprintf("catalog %s is unavailable; see the server log", info.Name))
	}
	return info, s.fail(err)
}

// catalogs lists every catalog instance with its description and newest
// ingest. A catalog that can't describe itself gets an error instead.
func (s *Server) catalogs(ctx context.Context) ([]catalogOut, error) {
	list, err := s.Registry.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	out := []catalogOut{}
	for _, i := range list {
		if !s.Registry.HasCapability(i.Plugin, "catalog") {
			continue
		}
		c := catalogOut{ID: i.ID, Name: i.Name, Plugin: i.Plugin}
		var info protocol.CatalogInfo
		if _, err := s.catalogCall(ctx, i.ID, protocol.MethodCatalogDescribe, nil, &info); err != nil {
			c.Error = err.Error()
		} else {
			c.Info = &info
		}
		if c.LastIngest, err = s.lastIngest(ctx, i.ID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// lastIngest is a catalog's newest ingest job, or nil.
func (s *Server) lastIngest(ctx context.Context, id int64) (*jobOut, error) {
	list, err := s.Queue.List(ctx, jobs.ListOptions{Kind: importer.IngestKind, Instance: id, Limit: 1})
	if err != nil || len(list) == 0 {
		return nil, err
	}
	j := toJob(list[0])
	return &j, nil
}

// kindEntities is where the library keeps kind, or nil if it doesn't.
func (s *Server) kindEntities(kind string) *library.Entities {
	switch kind {
	case protocol.KindCard:
		return s.Lib.Cards
	case protocol.KindLorebook:
		return s.Lib.Lorebooks
	}
	return nil
}

// withMatches adds each item's library matches, with one query per kind.
func (s *Server) withMatches(ctx context.Context, items []protocol.CatalogItem) ([]catalogHit, error) {
	hits := make([]catalogHit, len(items))
	byKind := map[string][]int{}
	for i, it := range items {
		hits[i] = catalogHit{CatalogItem: it, Library: []library.HashMatch{}}
		byKind[it.Kind] = append(byKind[it.Kind], i)
	}
	for kind, idx := range byKind {
		ents := s.kindEntities(kind)
		if ents == nil {
			continue
		}
		hashes := make([]library.Hashes, len(idx))
		for j, i := range idx {
			h := items[i].Hashes
			hashes[j] = library.Hashes{ContentHash: h.ContentHash, TextSimhash: h.TextSimhash, ImagePhash: h.ImagePhash}
		}
		matches, err := ents.MatchHashes(ctx, hashes, library.NearDistance)
		if err != nil {
			return nil, err
		}
		for j, i := range idx {
			hits[i].Library = orEmpty(matches[j])
		}
	}
	return hits, nil
}

// searchCatalog returns one page from a catalog with its library matches.
func (s *Server) searchCatalog(ctx context.Context, id int64, p protocol.CatalogSearchParams) (catalogResults, error) {
	var page protocol.CatalogSearchResult
	if _, err := s.catalogCall(ctx, id, protocol.MethodCatalogSearch, p, &page); err != nil {
		return catalogResults{}, err
	}
	hits, err := s.withMatches(ctx, page.Items)
	if err != nil {
		return catalogResults{}, s.fail(err)
	}
	return catalogResults{CatalogID: id, Items: hits, Next: page.Next}, nil
}

func (s *Server) catalogRoutes(api huma.API) {
	tags := []string{"Catalogs"}
	huma.Register(api, huma.Operation{OperationID: "list-catalogs", Method: http.MethodGet, Path: "/catalogs",
		Summary: "List catalogs", Description: "Every instance of a catalog plugin, with its description and newest ingest job.", Tags: tags},
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
			res, err := s.searchCatalog(ctx, in.ID, p)
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
			if _, err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogGet, params, &out.Body); err != nil {
				return nil, err
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "start-catalog-ingest", Method: http.MethodPost, Path: "/catalogs/{id}/ingest",
		Summary:     "Ingest a catalog's source",
		Description: "Queues a catalog.ingest job. Returns 409 while an earlier ingest of the catalog is unfinished.",
		Tags:        tags, DefaultStatus: http.StatusAccepted},
		func(ctx context.Context, in *idIn) (*jobResp, error) {
			var d protocol.CatalogInfo
			info, err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogDescribe, nil, &d)
			if err != nil {
				return nil, err
			}
			if !d.Ingest {
				return nil, huma.Error400BadRequest(fmt.Sprintf("catalog %s doesn't ingest; it searches its source directly", info.Name))
			}
			// ponytail: two requests at once can both queue one; the plugin's upserts make that harmless.
			last, err := s.lastIngest(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			if last != nil && last.FinishedAt == nil {
				return nil, huma.Error409Conflict("an ingest of this catalog is already queued or running",
					&huma.ErrorDetail{Location: "path.id", Value: last})
			}
			j, err := s.Queue.Enqueue(ctx, jobs.Spec{Kind: importer.IngestKind, InstanceID: &in.ID})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
}
