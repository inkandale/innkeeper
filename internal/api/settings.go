package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/embed"
	"github.com/inkandale/innkeeper/internal/libsettings"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

type settings struct {
	Theme          string `json:"theme" doc:"The selected theme plugin, or empty for the default theme."`
	OnboardingDone bool   `json:"onboarding_done" doc:"Whether the owner finished or skipped the setup wizard."`
	ScoreInstance  *int64 `json:"score_instance" doc:"The enrich instance whose values the library grid shows and hide_below reads, and that enrichment filters and sort naming no instance use, or null; a library may override it. When null and exactly one enrich instance exists, that one is used."`
	EmbedInstance  *int64 `json:"embed_instance" doc:"The ai instance whose ai.embed every library's card vectors come from, or null for none."`
	// The hide job's defaults; see GET /libraries/{id}/settings.
	HideBelow         *float64 `json:"hide_below"`
	Duplicates        string   `json:"duplicates" enum:"off,exact,near"`
	DuplicateDistance int      `json:"duplicate_distance"`
	DuplicateKeep     string   `json:"duplicate_keep" enum:"best,oldest,newest"`
	DuplicateDecide   *int64   `json:"duplicate_decide"`
}

type settingsOut struct{ Body settings }

type patchSettingsIn struct {
	Body struct {
		Theme          *string         `json:"theme,omitempty" doc:"An installed theme plugin's name, or empty for the default theme."`
		OnboardingDone *bool           `json:"onboarding_done,omitempty" doc:"true once the owner finishes or skips the setup wizard; false shows it again."`
		ScoreInstance  optional[int64] `json:"score_instance,omitempty" doc:"An enrich instance's ID, or null to clear it."`
		EmbedInstance  optional[int64] `json:"embed_instance,omitempty" doc:"An ai instance's ID, or null to clear it. Setting it, even to its current value, embeds every card again."`
		// Null clears a setting: no hide_below, duplicates off, distance 3, keep best, no decide instance.
		HideOptionsIn
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
	out := &settingsOut{Body: settings{Theme: name, OnboardingDone: done == "true"}}
	score, err := s.Registry.Setting(ctx, libsettings.ScoreInstanceKey)
	if err != nil {
		return nil, s.fail(err)
	}
	insts, err := s.libSettings().Enrichers(ctx)
	if err != nil {
		return nil, s.fail(err)
	}
	if i := libsettings.ScoreNamed(score, insts); i != nil {
		out.Body.ScoreInstance = &i.ID
	}
	if id, err := embed.Instance(ctx, s.Registry); err != nil {
		return nil, s.fail(err)
	} else if id != 0 {
		out.Body.EmbedInstance = &id
	}
	b := &out.Body
	if b.HideBelow, err = libsettings.HideBelow.Core(ctx, s.libSettings()); err != nil {
		return nil, s.fail(err)
	}
	dups, err := libsettings.Duplicates.Core(ctx, s.libSettings())
	if err != nil {
		return nil, s.fail(err)
	}
	dist, err := libsettings.DuplicateDistance.Core(ctx, s.libSettings())
	if err != nil {
		return nil, s.fail(err)
	}
	keep, err := libsettings.DuplicateKeep.Core(ctx, s.libSettings())
	if err != nil {
		return nil, s.fail(err)
	}
	b.Duplicates, b.DuplicateDistance, b.DuplicateKeep = *dups, *dist, *keep
	if b.DuplicateDecide, err = libsettings.DuplicateDecide.Core(ctx, s.libSettings()); err != nil {
		return nil, s.fail(err)
	}
	return out, nil
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
			if in.Body.ScoreInstance.Sent {
				if err := s.setScoreInstance(ctx, nil, in.Body.ScoreInstance.Value); err != nil {
					return nil, err
				}
			}
			if in.Body.EmbedInstance.Sent {
				if err := s.setEmbedInstance(ctx, in.Body.EmbedInstance.Value); err != nil {
					return nil, err
				}
			}
			b := in.Body
			if err := b.HideOptionsIn.set(ctx, s, nil); err != nil {
				return nil, err
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

// setEmbedInstance stores the embed instance, or clears it for nil. Either
// drops every vector: only the current instance's are read, and re-setting
// it makes a model changed in its config take effect.
func (s *Server) setEmbedInstance(ctx context.Context, id *int64) error {
	if id == nil {
		if err := s.Lib.ClearEmbeddings(ctx); err != nil {
			return s.fail(err)
		}
		return s.fail(s.Registry.SetSetting(ctx, embed.InstanceKey, ""))
	}
	inst, err := s.Registry.InstanceWith(ctx, *id, protocol.CapabilityAI)
	if err != nil {
		return s.fail(err)
	}
	services, err := s.Registry.Services(ctx, s.Plugins, inst)
	if err != nil {
		return s.failAs(inst.Manifest.Name, err)
	}
	if !slices.Contains(services, protocol.MethodAIEmbed) {
		return huma.Error400BadRequest(fmt.Sprintf("instance %d doesn't offer %s", *id, protocol.MethodAIEmbed))
	}
	if err := s.Lib.ClearEmbeddings(ctx); err != nil {
		return s.fail(err)
	}
	if err := s.Registry.SetSetting(ctx, embed.InstanceKey, strconv.FormatInt(*id, 10)); err != nil {
		return s.fail(err)
	}
	embed.Enqueue(ctx, s.Queue, s.Log)
	return nil
}

// fileRoutes serve theme stylesheets and plugin icons. They're public: the
// login page uses themes too, and neither holds anything secret.
func (s *Server) fileRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /plugins/{name}/icon", s.serveIcon)
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
	if err := servePluginFile(w, r, m.Dir, m.Stylesheet); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.Log.Error("open theme", "theme", name, "err", err)
		}
		if strict {
			http.NotFound(w, r)
		}
	}
}

// serveIcon writes the named plugin's icon. A strict CSP keeps an SVG's
// scripts from running when the URL is opened directly.
func (s *Server) serveIcon(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	m := s.Registry.Manifests()[name]
	if m == nil || m.Icon == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", m.IconType())
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := servePluginFile(w, r, m.Dir, m.Icon); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.Log.Error("open icon", "plugin", name, "err", err)
		}
		http.NotFound(w, r)
	}
}

// servePluginFile writes the regular file rel inside dir, and writes nothing
// when it returns an error. OpenInRoot keeps a symlink from serving files
// outside the plugin folder.
func servePluginFile(w http.ResponseWriter, r *http.Request, dir, rel string) error {
	f, err := os.OpenInRoot(dir, rel)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fs.ErrNotExist
	}
	http.ServeContent(w, r, "", st.ModTime(), f)
	return nil
}
