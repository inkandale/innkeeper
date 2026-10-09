package api

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/tier"
)

type settings struct {
	Theme          string `json:"theme" doc:"The selected theme plugin, or empty for the default theme."`
	OnboardingDone bool   `json:"onboarding_done" doc:"Whether the owner finished or skipped the setup wizard."`
}

type settingsOut struct{ Body settings }

type patchSettingsIn struct {
	Body struct {
		Theme          *string `json:"theme,omitempty" doc:"An installed theme plugin's name, or empty for the default theme."`
		OnboardingDone *bool   `json:"onboarding_done,omitempty" doc:"true once the owner finishes or skips the setup wizard; false shows it again."`
	}
}

type meOut struct {
	Body struct {
		Tier string `json:"tier" doc:"What the caller's credentials may do."`
	}
}

func (s *Server) settings(ctx context.Context) (*settingsOut, error) {
	name, err := s.Registry.Theme(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	// A theme that's been uninstalled counts as the default.
	if m := s.Registry.Manifests()[name]; m == nil || !m.IsTheme() {
		name = ""
	}
	done, err := s.Registry.Setting(ctx, "onboarding_done")
	if err != nil {
		return nil, s.fail(err)
	}
	return &settingsOut{Body: settings{Theme: name, OnboardingDone: done == "true"}}, nil
}

func (s *Server) settingsRoutes(api huma.API) {
	tags := []string{"Settings"}
	huma.Register(api, huma.Operation{OperationID: "get-settings", Method: http.MethodGet, Path: "/settings",
		Summary: "Get settings", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*settingsOut, error) { return s.settings(ctx) })

	huma.Register(api, huma.Operation{OperationID: "update-settings", Method: http.MethodPatch, Path: "/settings",
		Summary: "Change settings", Tags: tags},
		func(ctx context.Context, in *patchSettingsIn) (*settingsOut, error) {
			if in.Body.Theme != nil {
				if err := s.Registry.SetTheme(ctx, *in.Body.Theme); err != nil {
					return nil, s.fail(err)
				}
			}
			if in.Body.OnboardingDone != nil {
				v := ""
				if *in.Body.OnboardingDone {
					v = "true"
				}
				if err := s.Registry.SetSetting(ctx, "onboarding_done", v); err != nil {
					return nil, s.fail(err)
				}
			}
			return s.settings(ctx)
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "get-me", Method: http.MethodGet,
		Path: "/auth/me", Summary: "Check credentials", Description: "Returns 401 without valid credentials.", Tags: []string{"Auth"}},
		func(ctx context.Context, _ *struct{}) (*meOut, error) {
			out := &meOut{}
			out.Body.Tier = callerTier(ctx).String()
			return out, nil
		})
}

// themeRoutes serve theme stylesheets. They're public: the login page uses
// them too, and a stylesheet holds nothing secret.
func (s *Server) themeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /theme.css", func(w http.ResponseWriter, r *http.Request) {
		name, err := s.Registry.Theme(r.Context())
		if err != nil {
			s.Log.Error("read theme setting", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// A theme that's been uninstalled falls back to the default.
		s.serveTheme(w, r, name, false)
	})
	mux.HandleFunc("GET /themes/{file}", func(w http.ResponseWriter, r *http.Request) {
		name, ok := strings.CutSuffix(r.PathValue("file"), ".css")
		if !ok {
			http.NotFound(w, r)
			return
		}
		s.serveTheme(w, r, name, true)
	})
}

// serveTheme writes the named theme's stylesheet. An unknown name is a 404
// when strict, and an empty stylesheet otherwise.
func (s *Server) serveTheme(w http.ResponseWriter, r *http.Request, name string, strict bool) {
	m := s.Registry.Manifests()[name]
	if m == nil || !m.IsTheme() {
		if strict {
			http.NotFound(w, r)
			return
		}
		name = ""
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if name == "" {
		return
	}
	// OpenInRoot keeps a symlink from serving files outside the plugin folder.
	f, err := os.OpenInRoot(m.Dir, m.Stylesheet)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.Log.Error("open theme", "theme", name, "err", err)
		}
		if strict {
			http.NotFound(w, r)
		}
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
}
