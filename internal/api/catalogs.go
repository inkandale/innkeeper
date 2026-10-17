package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// catalogTimeout bounds each call the API makes to a catalog.
const catalogTimeout = 30 * time.Second

// describeTimeout is shorter: describe never reaches the source, and GET /catalogs waits on every catalog.
const describeTimeout = 5 * time.Second

// catalogMatchLimit is the most items each catalog returns for one card or
// lorebook.
const catalogMatchLimit = 20

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
	Error     string       `json:"error,omitempty" doc:"In POST /cards/search and POST /lorebooks/search: why this catalog returned nothing"`
}

type catalogResultsOut struct{ Body catalogResults }

type catalogItemIn struct {
	ID      int64  `path:"id"`
	Item    string `path:"item" doc:"The catalog's item ID, with / escaped as %2F"`
	Version string `query:"version" doc:"Default: the latest"`
}

type catalogItemOut struct{ Body protocol.CatalogGetResult }

type adoptCatalogOut struct {
	Status int
	Body   entityOut
}

type catalogMatch struct {
	CatalogID int64                  `json:"catalog_id"`
	Catalog   string                 `json:"catalog"`
	Items     []protocol.CatalogItem `json:"items"`
}

type catalogRef struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

type catalogMatches struct {
	Matches     []catalogMatch `json:"matches" doc:"One per catalog with matches"`
	Unreachable []catalogRef   `json:"unreachable" doc:"Catalogs that didn't answer, which may hold more matches"`
}

type catalogMatchesOut struct{ Body catalogMatches }

// catalogCall sends method to catalog instance id with a timeout, and maps
// its errors to HTTP ones. It returns the instance's settings.
func (s *Server) catalogCall(ctx context.Context, id int64, method string, params, result any) (plugins.InstanceInfo, error) {
	info, err := s.Registry.GetInstance(ctx, id)
	if err != nil {
		return info, s.fail(err)
	}
	inst, err := s.Registry.InstanceWith(ctx, id, "catalog")
	if errors.Is(err, plugins.ErrIncapable) || errors.Is(err, plugins.ErrUnknownPlugin) {
		return info, huma.Error404NotFound(fmt.Sprintf("instance %d is not a catalog", id))
	}
	if err != nil {
		return info, s.fail(err)
	}
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	err = s.Registry.Call(ctx, s.Plugins, inst, method, params, result)
	if errors.Is(err, context.Canceled) {
		// The client went away; there is no one to answer.
		return info, err
	}
	return info, s.failAs("catalog "+info.Name, err)
}

// catalogs lists every catalog instance with its description and newest
// ingest. A catalog that can't describe itself gets an error instead.
func (s *Server) catalogs(ctx context.Context) ([]catalogOut, error) {
	list, err := s.Registry.InstancesWith(ctx, "catalog")
	if err != nil {
		return nil, err
	}
	out := []catalogOut{}
	for _, i := range list {
		c := catalogOut{ID: i.ID, Name: i.Name, Plugin: i.Plugin}
		var info protocol.CatalogInfo
		dctx, cancel := context.WithTimeout(ctx, describeTimeout)
		_, err := s.catalogCall(dctx, i.ID, protocol.MethodCatalogDescribe, nil, &info)
		cancel()
		if err != nil {
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

// withMatches adds each item's library matches, with one query per kind.
func (s *Server) withMatches(ctx context.Context, items []protocol.CatalogItem) ([]catalogHit, error) {
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
		matches, err := s.Lib.Entities(k).MatchHashes(ctx, library.DefaultLibrary, hashes, library.NearDistance)
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
			// Only adopt asks for an avatar; never hand a client a server path.
			out.Body.Avatar = ""
			return out, nil
		})

	// Listing 200 stops huma adding its default error response, so add it here.
	schemas := api.OpenAPI().Components.Schemas
	body := func(mediaType string, t reflect.Type) map[string]*huma.MediaType {
		return map[string]*huma.MediaType{mediaType: {Schema: schemas.Schema(t, true, "")}}
	}
	huma.Register(api, huma.Operation{OperationID: "adopt-catalog-item", Method: http.MethodPost, Path: "/catalogs/{id}/items/{item}/adopt",
		Summary: "Adopt a catalog item into the library",
		Description: "Copies one version in as a new card or lorebook; a card's character_book becomes a linked lorebook. " +
			"Returns 201 with the new entry, or 200 with the one an earlier adopt of the same version made.",
		Tags: tags, DefaultStatus: http.StatusCreated,
		Responses: map[string]*huma.Response{
			"200":     {Description: "An earlier adopt's entry", Content: body("application/json", reflect.TypeFor[entityOut]())},
			"default": {Description: "Error", Content: body("application/problem+json", reflect.TypeFor[huma.ErrorModel]())}}},
		s.adoptCatalogItem)

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
			return s.enqueue(ctx, jobs.Spec{Kind: importer.IngestKind, InstanceID: &in.ID})
		})

	for _, k := range library.Kinds {
		huma.Register(api, huma.Operation{OperationID: "find-" + k.Name + "-in-catalogs", Method: http.MethodGet,
			Path: "/" + k.Plural + "/{id}/catalog-matches", Summary: "Find a " + k.Name + " in the catalogs",
			Description: "Asks every catalog that finds similar items for the same content, near-identical text, or a similar avatar.",
			Tags:        tags},
			func(ctx context.Context, in *idIn) (*catalogMatchesOut, error) {
				return s.findInCatalogs(ctx, k, in.ID)
			})
	}
}

// adoptCatalogItem copies one version of an item into the library. Import
// dedupes on provenance and content, so adopting a version twice finds the
// first copy.
func (s *Server) adoptCatalogItem(ctx context.Context, in *catalogItemIn) (*adoptCatalogOut, error) {
	var res protocol.CatalogGetResult
	info, err := s.catalogCall(ctx, in.ID, protocol.MethodCatalogGet,
		protocol.CatalogGetParams{ID: in.Item, Version: in.Version, Avatar: true}, &res)
	if err != nil {
		return nil, err
	}
	k, ok := library.KindNamed(res.Item.Kind)
	if !ok {
		return nil, huma.Error400BadRequest(fmt.Sprintf("can't adopt a %s; the library holds cards and lorebooks", res.Item.Kind))
	}
	id, created, err := s.Lib.ImportItem(ctx, library.PluginItem{Kind: k,
		Provenance: fmt.Sprintf("catalog:%d:%s@%s", in.ID, in.Item, res.Version), Source: "plugin:" + info.Plugin,
		Data: res.Data, Avatar: res.Avatar, Name: res.Item.Name})
	// The error names server paths, so log it and keep it out of the body.
	if errors.Is(err, blobstore.ErrInvalid) {
		s.Log.Error("catalog sent a bad avatar", "catalog", info.Name, "err", err)
		return nil, huma.Error502BadGateway(fmt.Sprintf("catalog %s sent an avatar the server can't accept", info.Name))
	}
	if errors.Is(err, library.ErrInvalid) {
		return nil, huma.Error502BadGateway(fmt.Sprintf("catalog %s sent an item the library can't take: %v", info.Name, err))
	}
	if err != nil {
		return nil, s.fail(err)
	}
	importer.EnqueueDetect(ctx, s.Queue, s.Log)
	out, err := s.entity(ctx, s.Lib.Entities(k), id)
	if err != nil {
		return nil, err
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return &adoptCatalogOut{Status: status, Body: out.Body}, nil
}

// findInCatalogs asks every catalog that answers catalog.similar for an
// entity's head revision.
//
// ponytail: asks one catalog at a time; ask in parallel if many catalogs make this slow.
func (s *Server) findInCatalogs(ctx context.Context, k *library.Kind, id int64) (*catalogMatchesOut, error) {
	ents := s.Lib.Entities(k)
	e, err := ents.Get(ctx, id)
	if err != nil {
		return nil, s.fail(err)
	}
	rev, err := ents.Revision(ctx, e.HeadRevisionID)
	if err != nil {
		return nil, s.fail(err)
	}
	h := protocol.CatalogHashes{ContentHash: rev.ContentHash, TextSimhash: rev.TextSimhash}
	if rev.AvatarBlob != nil {
		b, err := s.Lib.Blob(ctx, *rev.AvatarBlob)
		if err != nil {
			return nil, s.fail(err)
		}
		h.ImagePhash = b.AvatarPhash
	}
	cats, err := s.catalogs(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	out := &catalogMatchesOut{Body: catalogMatches{Matches: []catalogMatch{}, Unreachable: []catalogRef{}}}
	for _, c := range cats {
		if c.Info == nil {
			out.Body.Unreachable = append(out.Body.Unreachable, catalogRef{c.ID, c.Name, c.Error})
			continue
		}
		if !c.Info.Similar || !slices.Contains(c.Info.Kinds, k.Name) {
			continue
		}
		var res protocol.CatalogSimilarResult
		params := protocol.CatalogSimilarParams{Kind: k.Name, Hashes: h, MaxDistance: library.NearDistance, Limit: catalogMatchLimit}
		if _, err := s.catalogCall(ctx, c.ID, protocol.MethodCatalogSimilar, params, &res); err != nil {
			out.Body.Unreachable = append(out.Body.Unreachable, catalogRef{c.ID, c.Name, err.Error()})
			continue
		}
		if len(res.Items) > 0 {
			out.Body.Matches = append(out.Body.Matches, catalogMatch{c.ID, c.Name, res.Items})
		}
	}
	return out, nil
}
