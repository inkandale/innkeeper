package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type pluginSourcesOut struct{ Body []plugins.PluginSource }

type pluginSourceIn struct{ Body plugins.PluginSource }

type pluginSourceIDIn struct {
	ID string `path:"id"`
}

type pluginNameIn struct {
	Name string `path:"name"`
}

type installIn struct {
	Name string `path:"name"`
	Body struct {
		Source  string `json:"source" minLength:"1"`
		Version string `json:"version" minLength:"1"`
	}
}

// catalogPluginOut is a plugin a source offers: its newest version's details,
// and every version it lists.
type catalogPluginOut struct {
	plugins.Entry
	// Versions is newest first.
	Versions []string `json:"versions"`
}

type catalogSourceOut struct {
	Source  plugins.PluginSource     `json:"source"`
	Plugins []catalogPluginOut `json:"plugins"`
	// Error says why the source couldn't be read; Plugins is empty then.
	Error string `json:"error,omitempty"`
}

type pluginCatalogOut struct{ Body []catalogSourceOut }

func (s *Server) pluginStoreRoutes(api huma.API) {
	tags := []string{"Plugins"}
	// Plugins run as code inside Innkeeper, so changing them is owner-level.
	destructive := needs(tier.Destructive)

	huma.Register(api, huma.Operation{OperationID: "list-plugin-sources", Method: http.MethodGet, Path: "/plugin-sources",
		Summary: "List plugin sources", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*pluginSourcesOut, error) {
			list, err := s.Registry.Sources(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &pluginSourcesOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "add-plugin-source", Method: http.MethodPost,
		Path: "/plugin-sources", Summary: "Add a plugin source", Tags: tags, DefaultStatus: http.StatusCreated,
		Description: "A source is the https:// or file:// URL of an index.toml. Its plugins run as code inside " +
			"Innkeeper with their own database role, so add only sources you trust."},
		func(ctx context.Context, in *pluginSourceIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.AddSource(ctx, in.Body))
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "delete-plugin-source", Method: http.MethodDelete,
		Path: "/plugin-sources/{id}", Summary: "Delete a plugin source", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *pluginSourceIDIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.DeleteSource(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "plugin-catalog", Method: http.MethodGet, Path: "/plugin-catalog",
		Summary: "Check sources for plugins", Tags: tags,
		Description: "Reads every source's index. It changes nothing."},
		func(ctx context.Context, _ *struct{}) (*pluginCatalogOut, error) {
			sources, err := s.Registry.Sources(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &pluginCatalogOut{Body: []catalogSourceOut{}}
			// ponytail: sources are read one at a time; fetch in parallel if owners add many.
			for _, src := range sources {
				entries, err := s.Registry.Index(ctx, src.URL)
				c := catalogSourceOut{Source: src, Plugins: catalog(entries)}
				if err != nil {
					c.Error = err.Error()
				}
				out.Body = append(out.Body, c)
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "install-plugin", Method: http.MethodPost,
		Path: "/plugins/{name}/install", Summary: "Install or update a plugin", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Installs a version from a source and makes it current. Running jobs finish on the old version. " +
			"Installing the previous version rolls back without a download."},
		func(ctx context.Context, in *installIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.Install(ctx, in.Body.Source, in.Name, in.Body.Version))
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "uninstall-plugin", Method: http.MethodDelete,
		Path: "/plugins/{name}", Summary: "Uninstall a plugin", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Removes the plugin's code. Its instances and data stay, and come back if it's installed again."},
		func(ctx context.Context, in *pluginNameIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.Uninstall(ctx, in.Name))
		})

	huma.Register(api, huma.Operation{Metadata: destructive, OperationID: "remove-plugin-data", Method: http.MethodDelete,
		Path: "/plugins/{name}/data", Summary: "Remove an uninstalled plugin's data", Tags: tags, DefaultStatus: http.StatusNoContent,
		Description: "Deletes the plugin's instances, database schema, and role. This can't be undone."},
		func(ctx context.Context, in *pluginNameIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.RemoveData(ctx, in.Name))
		})
}

// catalog groups a source's entries by plugin, newest version first.
func catalog(entries []plugins.Entry) []catalogPluginOut {
	byName := map[string]*catalogPluginOut{}
	for _, e := range entries {
		p, ok := byName[e.Name]
		if !ok {
			p = &catalogPluginOut{Entry: e}
			byName[e.Name] = p
		} else if plugins.Newer(e.Version, p.Version) {
			p.Entry = e
		}
		p.Versions = append(p.Versions, e.Version)
	}
	out := []catalogPluginOut{}
	for _, p := range byName {
		slices.SortFunc(p.Versions, func(a, b string) int {
			if plugins.Newer(a, b) {
				return -1
			}
			if plugins.Newer(b, a) {
				return 1
			}
			return 0
		})
		out = append(out, *p)
	}
	slices.SortFunc(out, func(a, b catalogPluginOut) int { return strings.Compare(a.Name, b.Name) })
	return out
}
