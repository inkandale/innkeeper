package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/enrich"
	"github.com/inkandale/innkeeper/internal/importer"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// EnrichmentOut is how a plugin declares an enrichment key.
type EnrichmentOut struct {
	Type  string    `json:"type"`
	Label string    `json:"label"`
	Range []float64 `json:"range,omitempty"`
	Grid  bool      `json:"grid,omitempty" doc:"Whether the library grid shows this key."`
}

type pluginOut struct {
	Name              string                   `json:"name"`
	Title             string                   `json:"title" doc:"The name to show: the manifest's title, or name without one"`
	Version           string                   `json:"version"`
	Description       string                   `json:"description,omitempty"`
	Category          string                   `json:"category,omitempty" doc:"The group the Plugins page shows the plugin under"`
	MultipleInstances bool                     `json:"multiple_instances" doc:"The owner may create more than one instance"`
	Status            string                   `json:"status" enum:"ready,needs_setup,broken,disabled" doc:"Sums up the plugin's instances: broken or needs_setup if any instance is, disabled if every one is, needs_setup with none. A theme is ready."`
	Capabilities      []string                 `json:"capabilities"`
	Needs             []string                 `json:"needs,omitempty" doc:"Capabilities the plugin needs another plugin to offer, such as ai"`
	Secrets           []string                 `json:"secrets"`
	Formats           []string                 `json:"formats,omitempty" doc:"File formats a codec plugin reads and writes"`
	Inputs            []string                 `json:"inputs,omitempty" doc:"What an import plugin takes from the UI: file (params blob and name), url (params url), or both"`
	Config            map[string]any           `json:"config,omitempty" doc:"JSON Schema for an instance's config"`
	Enrichments       map[string]EnrichmentOut `json:"enrichments,omitempty"`
	EnrichMaxCards    int                      `json:"enrich_max_cards,omitempty" doc:"For an enrich plugin: the most cards a run takes without confirm"`
	Records           map[string]recordTypeOut `json:"records,omitempty" doc:"The record types a records plugin keeps, by type name"`
	Source            string                   `json:"source" doc:"Id of the source the plugin was installed from, or builtin"`
	PreviousSource    *string                  `json:"previous_source,omitempty" doc:"Source of the version a rollback returns to"`
	PreviousVersion   *string                  `json:"previous_version,omitempty" doc:"Version a rollback returns to"`
	Builtin           bool                     `json:"builtin" doc:"The image ships this plugin, so uninstalling falls back to that copy"`
	Icon              string                   `json:"icon,omitempty" doc:"URL of the app's logo"`
	SettingsActions   map[string]string        `json:"settings_actions,omitempty" doc:"Buttons the instance form shows: label by action id, run with POST /plugins/{name}/settings/run"`
}

type recordTypeOut struct {
	Label  string         `json:"label"`
	Schema map[string]any `json:"schema" doc:"JSON Schema for one record"`
	Import []string       `json:"import,omitempty" doc:"What Import takes: file, url, or both"`
}

type pluginsOut struct{ Body []pluginOut }

type instancesOut struct{ Body []plugins.InstanceInfo }

type instanceOut struct{ Body plugins.InstanceInfo }

type checkResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty" doc:"Why the instance doesn't work"`
}

type checkOut struct{ Body checkResult }

type createInstanceIn struct {
	Body struct {
		Plugin        string            `json:"plugin"`
		Name          *string           `json:"name,omitempty" minLength:"1" doc:"Tells it apart from the plugin's other instances. A plugin may have one instance without a name; a singleton's is always without one."`
		Config        map[string]any    `json:"config,omitempty"`
		Secrets       map[string]string `json:"secrets,omitempty" doc:"Write-only. Responses list only the names that are set."`
		OwnerSubject  *string           `json:"owner_subject,omitempty" doc:"For a login provider: the subject that logs in as the owner."`
		LibraryAccess bool              `json:"library_access,omitempty" doc:"Lets an action plugin read the library while it runs an action that asks to."`
		DefaultBases  map[string]any    `json:"default_bases,omitempty" doc:"For each JSON pointer the owner just overrode, the default it replaced, so the form can mark it once the default changes."`
	}
}

type updateInstanceIn struct {
	ID   int64 `path:"id"`
	Body struct {
		Name          *string            `json:"name,omitempty" doc:"Empty clears it. A singleton's instance never keeps one."`
		Config        map[string]any     `json:"config,omitempty" doc:"Replaces the whole config."`
		Secrets       map[string]*string `json:"secrets,omitempty" doc:"Sets each secret named; null removes it. Secrets not named stay."`
		OwnerSubject  *string            `json:"owner_subject,omitempty" doc:"Empty clears it, which logs the owner out of this provider."`
		LibraryAccess *bool              `json:"library_access,omitempty" doc:"Lets an action plugin read the library while it runs an action that asks to."`
		Disabled      *bool              `json:"disabled,omitempty" doc:"true keeps the instance but stops everything using it; false turns it back on. Disabling a login provider ends its sessions."`
		SyncLibrary   *int64             `json:"sync_library,omitempty" doc:"For a target app: the library its syncs match against and import new items into."`
		DefaultBases  map[string]any     `json:"default_bases,omitempty" doc:"For each JSON pointer the owner just overrode, the default it replaced, so the form can mark it once the default changes."`
	}
}

type listInstancesIn struct {
	Offers string `query:"offers" doc:"Only instances that offer this service, such as ai.chat"`
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
			installs, err := s.Registry.Installs(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			all, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			byPlugin := map[string][]plugins.InstanceInfo{}
			for _, i := range all {
				byPlugin[i.Plugin] = append(byPlugin[i.Plugin], i)
			}
			out := &pluginsOut{Body: []pluginOut{}}
			for _, m := range s.Registry.Manifests() {
				p := pluginOut{Name: m.Name, Title: m.DisplayName(), Version: m.Version, Capabilities: m.Capabilities, Needs: protocol.Needs(m.Uses),
					Secrets: orEmpty(m.Secrets), Formats: m.Formats, Inputs: m.Inputs, Config: m.Config,
					Source: "builtin", Builtin: s.Registry.IsBuiltin(m.Name),
					Description: m.Description, Category: m.Category, MultipleInstances: m.AllowsManyInstances(),
					Status: plugins.Status(m, byPlugin[m.Name])}
				if m.Has(protocol.CapabilityEnrich) {
					p.EnrichMaxCards = enrich.MaxCards(m)
				}
				if m.Icon != "" {
					p.Icon = "/api/plugins/" + m.Name + "/icon"
				}
				if in, ok := installs[m.Name]; ok {
					p.Source, p.PreviousSource, p.PreviousVersion = in.Source, in.PreviousSource, in.PreviousVersion
				}
				for k, e := range m.Enrichments {
					if p.Enrichments == nil {
						p.Enrichments = map[string]EnrichmentOut{}
					}
					p.Enrichments[k] = toEnrichmentOut(e)
				}
				for k, rt := range m.Records {
					if p.Records == nil {
						p.Records = map[string]recordTypeOut{}
					}
					p.Records[k] = recordTypeOut{rt.Label, rt.Schema, rt.Import}
				}
				for id, a := range m.Settings.Actions {
					if p.SettingsActions == nil {
						p.SettingsActions = map[string]string{}
					}
					p.SettingsActions[id] = a.Label
				}
				out.Body = append(out.Body, p)
			}
			slices.SortFunc(out.Body, func(a, b pluginOut) int { return strings.Compare(a.Name, b.Name) })
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "list-instances", Method: http.MethodGet, Path: "/instances",
		Summary: "List plugin instances", Tags: tags},
		func(ctx context.Context, in *listInstancesIn) (*instancesOut, error) {
			var list []plugins.InstanceInfo
			var err error
			if in.Offers != "" {
				list, err = s.Registry.InstancesOffering(ctx, s.Plugins, in.Offers)
			} else {
				list, err = s.Registry.ListInstances(ctx)
			}
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
				Config: in.Body.Config, Secrets: in.Body.Secrets, OwnerSubject: in.Body.OwnerSubject, LibraryAccess: in.Body.LibraryAccess,
				DefaultBases: in.Body.DefaultBases})
			if err != nil {
				return nil, s.fail(err)
			}
			importer.EnqueueDetectForSites(ctx, s.Queue, s.Registry, s.Log, in.Body.Plugin)
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
				Secrets: in.Body.Secrets, OwnerSubject: in.Body.OwnerSubject, LibraryAccess: in.Body.LibraryAccess, Disabled: in.Body.Disabled, SyncLibrary: in.Body.SyncLibrary,
				DefaultBases: in.Body.DefaultBases})
			if err != nil {
				return nil, s.fail(err)
			}
			importer.EnqueueDetectForSites(ctx, s.Queue, s.Registry, s.Log, cur.Plugin)
			return s.instance(ctx, in.ID)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "check-instance", Method: http.MethodPost, Path: "/instances/{id}/check",
		Summary:     "Check that a plugin instance works",
		Description: "Starts the plugin, even when the instance is broken; a target that pushes and pulls also lists the app's cards. A failed start marks the instance broken, and a good one clears that. An app error is reported but doesn't mark it broken.",
		Tags:        tags},
		func(ctx context.Context, in *idIn) (*checkOut, error) {
			problem, err := s.Registry.Check(ctx, s.Plugins, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			// Report only what GET /instances already shows: the start error
			// it stores as broken_error, or the plugin's own reply.
			var startErr *pluginhost.StartError
			var rpcErr *protocol.Error
			switch {
			case problem == nil:
				if cur, err := s.Registry.GetInstance(ctx, in.ID); err == nil {
					importer.EnqueueDetectForSites(ctx, s.Queue, s.Registry, s.Log, cur.Plugin)
				}
				return &checkOut{Body: checkResult{OK: true}}, nil
			case errors.As(problem, &startErr):
				return &checkOut{Body: checkResult{Error: startErr.Error()}}, nil
			case errors.As(problem, &rpcErr), errors.Is(problem, plugins.ErrUnknownPlugin):
				return &checkOut{Body: checkResult{Error: problem.Error()}}, nil
			}
			return nil, s.fail(problem)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "delete-instance", Method: http.MethodDelete, Path: "/instances/{id}",
		Summary: "Delete a plugin instance", Description: "Its jobs stay, and logins through it end.", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Registry.DeleteInstance(ctx, in.ID))
		})
}

// loginGuard keeps OAuth and MCP clients away from login providers. A
// provider's owner, config, and secrets decide who logs in with every tier,
// so a client that could change them could make itself the owner.
func (s *Server) loginGuard(ctx context.Context, plugin string, owner bool) error {
	client := auth.FromMCP(ctx) || ctx.Value(oauthKey{}) == true
	if client && (owner || s.Registry.HasCapability(plugin, protocol.CapabilityAuth)) {
		return huma.Error403Forbidden("login providers and owners can't be changed by an OAuth or MCP client; use an API token or the web UI")
	}
	return nil
}
