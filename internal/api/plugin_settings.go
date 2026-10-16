package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// DraftIn is the instance form's unsaved state. Huma reads only exported
// embedded structs.
type DraftIn struct {
	Instance *int64             `json:"instance,omitempty" doc:"An instance of this plugin whose saved secrets the draft starts from"`
	Config   map[string]any     `json:"config,omitempty" doc:"The form's unsaved config"`
	Secrets  map[string]*string `json:"secrets,omitempty" doc:"Typed secrets; null removes a saved one. Write-only."`
}

func (d DraftIn) draft() plugins.Draft {
	return plugins.Draft{Instance: d.Instance, Config: d.Config, Secrets: d.Secrets}
}

type defaultsIn struct {
	Name     string `path:"name"`
	Instance int64  `query:"instance" doc:"Also list this instance's overrides whose default changed"`
}

type defaultsOut struct{ Body plugins.Defaults }

type optionsIn struct {
	Name string `path:"name"`
	Body struct {
		DraftIn
		Field string `json:"field" doc:"JSON pointer to the config field"`
	}
}

type optionsOut struct {
	Body protocol.SettingsOptionsResult
}

type runSettingIn struct {
	Name string `path:"name"`
	Body struct {
		DraftIn
		Action string `json:"action" doc:"One of the plugin's settings_actions"`
	}
}

type runSettingOut struct{ Body protocol.SettingsRunResult }

func (s *Server) pluginSettingsRoutes(api huma.API) {
	tags := []string{"Plugins"}
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "get-plugin-defaults", Method: http.MethodGet,
		Path: "/plugins/{name}/settings/defaults", Summary: "Get a plugin's config defaults", Tags: tags},
		func(ctx context.Context, in *defaultsIn) (*defaultsOut, error) {
			var inst *int64
			if in.Instance != 0 {
				inst = &in.Instance
			}
			d, err := s.Registry.Defaults(ctx, s.Plugins, in.Name, inst)
			if err != nil {
				return nil, s.settingsFail(err)
			}
			return &defaultsOut{Body: d}, nil
		})

	// A draft can name any address and send the saved secrets there, which
	// is what changing the instance can do; so these need that tier.
	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "get-plugin-options", Method: http.MethodPost,
		Path: "/plugins/{name}/settings/options", Summary: "List the choices for a config field", Tags: tags},
		func(ctx context.Context, in *optionsIn) (*optionsOut, error) {
			if err := s.draftGuard(ctx, in.Body.DraftIn); err != nil {
				return nil, err
			}
			res, err := s.Registry.Options(ctx, s.Plugins, in.Name, in.Body.Field, in.Body.draft())
			if err != nil {
				return nil, s.settingsFail(err)
			}
			return &optionsOut{Body: res}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "run-plugin-setting", Method: http.MethodPost,
		Path: "/plugins/{name}/settings/run", Summary: "Run a button on the instance form", Description: "Such as Test connection, against the unsaved draft.", Tags: tags},
		func(ctx context.Context, in *runSettingIn) (*runSettingOut, error) {
			if err := s.draftGuard(ctx, in.Body.DraftIn); err != nil {
				return nil, err
			}
			res, err := s.Registry.RunSetting(ctx, s.Plugins, in.Name, in.Body.Action, in.Body.draft())
			if err != nil {
				return nil, s.settingsFail(err)
			}
			return &runSettingOut{Body: res}, nil
		})
}

// draftGuard is loginGuard for a draft that starts from a saved instance,
// since the draft can send that instance's secrets anywhere.
func (s *Server) draftGuard(ctx context.Context, d DraftIn) error {
	if d.Instance == nil {
		return nil
	}
	cur, err := s.Registry.GetInstance(ctx, *d.Instance)
	if err != nil {
		return s.fail(err)
	}
	return s.loginGuard(ctx, cur.Plugin, false)
}

// settingsFail is fail, except a plugin's own error message reaches the
// owner: only the destructive tier can make these calls.
func (s *Server) settingsFail(err error) error {
	var rpcErr *protocol.Error
	switch {
	case errors.Is(err, plugins.ErrIncapable):
		return huma.Error400BadRequest(err.Error())
	case errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound:
		return huma.Error502BadGateway(rpcErr.Message)
	}
	return s.fail(err)
}
