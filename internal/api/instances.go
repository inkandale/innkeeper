package api

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
)

type enrichmentOut struct {
	Type  string    `json:"type"`
	Label string    `json:"label"`
	Range []float64 `json:"range,omitempty"`
}

type pluginOut struct {
	Name         string                   `json:"name"`
	Version      string                   `json:"version"`
	Capabilities []string                 `json:"capabilities"`
	Secrets      []string                 `json:"secrets"`
	Formats      []string                 `json:"formats,omitempty" doc:"File formats a codec plugin reads and writes"`
	Config       map[string]any           `json:"config,omitempty" doc:"JSON Schema for an instance's config"`
	Enrichments  map[string]enrichmentOut `json:"enrichments,omitempty"`
}

type pluginsOut struct{ Body []pluginOut }

type instancesOut struct{ Body []plugins.InstanceInfo }

type instanceOut struct{ Body plugins.InstanceInfo }

type createInstanceIn struct {
	Body struct {
		Plugin       string            `json:"plugin"`
		Name         string            `json:"name" minLength:"1"`
		Config       map[string]any    `json:"config,omitempty"`
		Secrets      map[string]string `json:"secrets,omitempty" doc:"Write-only. Responses list only the names that are set."`
		OwnerSubject *string           `json:"owner_subject,omitempty" doc:"For a login provider: the subject that logs in as the owner."`
	}
}

type updateInstanceIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name         *string            `json:"name,omitempty" minLength:"1"`
		Config       map[string]any     `json:"config,omitempty" doc:"Replaces the whole config."`
		Secrets      map[string]*string `json:"secrets,omitempty" doc:"Sets each secret named; null removes it. Secrets not named stay."`
		OwnerSubject *string            `json:"owner_subject,omitempty" doc:"Empty clears it, which logs the owner out of this provider."`
	}
}

func (s *Server) instance(ctx context.Context, id int64) (*instanceOut, error) {
	i, err := s.Registry.GetInstance(ctx, id)
	if err != nil {
		return nil, s.fail(err)
	}
	return &instanceOut{Body: i}, nil
}

func (s *Server) instanceRoutes(api huma.API) {
	tags := []string{"Plugins"}
	huma.Register(api, huma.Operation{OperationID: "list-plugins", Method: http.MethodGet, Path: "/plugins",
		Summary: "List installed plugins", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*pluginsOut, error) {
			out := &pluginsOut{Body: []pluginOut{}}
			for _, m := range s.Registry.Manifests() {
				p := pluginOut{Name: m.Name, Version: m.Version, Capabilities: m.Capabilities,
					Secrets: orEmpty(m.Secrets), Formats: m.Formats, Config: m.Config}
				for k, e := range m.Enrichments {
					if p.Enrichments == nil {
						p.Enrichments = map[string]enrichmentOut{}
					}
					p.Enrichments[k] = enrichmentOut{e.Type, e.Label, e.Range}
				}
				out.Body = append(out.Body, p)
			}
			slices.SortFunc(out.Body, func(a, b pluginOut) int { return strings.Compare(a.Name, b.Name) })
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-instances", Method: http.MethodGet, Path: "/instances",
		Summary: "List plugin instances", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*instancesOut, error) {
			list, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &instancesOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "create-instance", Method: http.MethodPost, Path: "/instances",
		Summary: "Configure a plugin instance", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createInstanceIn) (*instanceOut, error) {
			if err := s.loginGuard(ctx, in.Body.Plugin, in.Body.OwnerSubject != nil); err != nil {
				return nil, err
			}
			id, err := s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: in.Body.Plugin, Name: in.Body.Name,
				Config: in.Body.Config, Secrets: in.Body.Secrets, OwnerSubject: in.Body.OwnerSubject})
			if err != nil {
				return nil, s.fail(err)
			}
			return s.instance(ctx, id)
		})

	huma.Register(api, huma.Operation{OperationID: "get-instance", Method: http.MethodGet, Path: "/instances/{id}",
		Summary: "Get a plugin instance", Tags: tags},
		func(ctx context.Context, in *idIn) (*instanceOut, error) {
			return s.instance(ctx, in.ID)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "update-instance", Method: http.MethodPatch, Path: "/instances/{id}",
		Summary: "Change a plugin instance", Description: "Clears a broken instance, and its plugin restarts with the new settings.", Tags: tags},
		func(ctx context.Context, in *updateInstanceIn) (*instanceOut, error) {
			cur, err := s.Registry.GetInstance(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			if err := s.loginGuard(ctx, cur.Plugin, in.Body.OwnerSubject != nil); err != nil {
				return nil, err
			}
			err = s.Registry.UpdateInstance(ctx, in.ID, plugins.InstanceEdit{Name: in.Body.Name, Config: in.Body.Config,
				Secrets: in.Body.Secrets, OwnerSubject: in.Body.OwnerSubject})
			if err != nil {
				return nil, s.fail(err)
			}
			return s.instance(ctx, in.ID)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "delete-instance", Method: http.MethodDelete, Path: "/instances/{id}",
		Summary: "Delete a plugin instance", Description: "Its jobs stay, and logins through it end.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.DeleteInstance(ctx, in.ID))
		})
}

// loginGuard keeps MCP clients away from login providers. A provider's
// owner, config, and secrets decide who logs in with every tier, so a
// client that could change them could make itself the owner.
func (s *Server) loginGuard(ctx context.Context, plugin string, owner bool) error {
	if auth.FromMCP(ctx) && (owner || s.Registry.HasCapability(plugin, "auth")) {
		return huma.Error403Forbidden("login providers and owners can't be changed through MCP; use the CLI or the API")
	}
	return nil
}
