// Package api serves the core's HTTP API, described by one OpenAPI spec at
// /api/openapi.json.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/inkandale/innkeeper/internal/actions"
	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/backup"
	"github.com/inkandale/innkeeper/internal/export"
	"github.com/inkandale/innkeeper/internal/jobs"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/mcp"
	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// Server holds what the API's handlers use.
type Server struct {
	Lib      *library.Library
	Queue    *jobs.Queue
	Registry *plugins.Registry
	Auth     *auth.Auth
	// Targets must use Plugins.
	Targets *targets.Service
	// Plugins runs plugins for interactive calls, such as logins. Its core
	// handler should be NoCore.
	Plugins *pluginhost.Manager
	Log     *slog.Logger
	// PublicURL is where browsers reach the API, such as
	// https://innkeeper.example.com. Optional. Redirect logins need it, and
	// an http:// URL sends cookies without Secure.
	PublicURL string
	// Stopping closes when the server begins shutting down, which ends job
	// streams so they don't hold up shutdown. Nil means never.
	Stopping <-chan struct{}
	// UI serves the web UI for every path the API, OAuth, and MCP don't.
	// Nil serves none.
	UI http.Handler
	// Backups is where backups live and their schedules. Without a Dir, the
	// backup routes return 409.
	Backups backup.Settings

	// loginSlot lets one login run at a time.
	loginSlot chan struct{}
	// setupMu guards setupCode, which claims the server while it has no
	// login provider. Empty until needed.
	setupMu   sync.Mutex
	setupCode string
	// tools, actions, and catalogInfo cache each plugin instance's
	// mcp.tools, action.list, and catalog.describe, by instance version.
	tools, actions, catalogInfo sync.Map
	// lastPrune is when OAuth clients were last pruned, in Unix seconds.
	lastPrune atomic.Int64
}

// NoCore answers plugin callbacks. The API offers plugins none yet.
func NoCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

// public marks an operation that needs no credentials.
var public = map[string]any{"public": true}

// resourceMetadataURL is where MCP clients learn how to get a token, or
// empty without a public URL.
func (s *Server) resourceMetadataURL() string {
	if s.PublicURL == "" {
		return ""
	}
	return s.public() + "/.well-known/oauth-protected-resource/mcp"
}

// needs is an operation's tier. Operations without one need read for GET
// and HEAD, and write for anything else.
func needs(t tier.Tier) map[string]any { return map[string]any{"tier": t} }

// registerMoved registers op, and again at old, the path it had before, for
// clients that still call that one.
func registerMoved[I, O any](api huma.API, op huma.Operation, old string, h func(context.Context, *I) (*O, error)) {
	huma.Register(api, op, h)
	op.OperationID += "-legacy"
	op.Path, op.Deprecated = old, true
	huma.Register(api, op, h)
}

// kindNamed is the library kind a request names: card unless it names
// another.
func kindNamed(name string) *library.Kind {
	if k, ok := library.KindNamed(name); ok {
		return k
	}
	return library.Card
}

func opTier(op *huma.Operation) tier.Tier {
	if t, ok := op.Metadata["tier"].(tier.Tier); ok {
		return t
	}
	if op.Method == http.MethodGet || op.Method == http.MethodHead {
		return tier.Read
	}
	return tier.Write
}

type tierKey struct{}

// callerTier is the tier of the credentials the request came with.
func callerTier(ctx context.Context) tier.Tier {
	t, _ := ctx.Value(tierKey{}).(tier.Tier)
	return t
}

// oauthKey marks a request whose credentials are an OAuth access token.
type oauthKey struct{}

// noAuth shows a public operation in the spec with no security requirement.
var noAuth = []map[string][]string{}

type idIn struct {
	ID int64 `path:"id"`
}

// orEmpty makes a nil slice encode as [] instead of null.
func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// config is the API's Huma config. Its server URL makes the docs page load
// the spec from under /api.
func config() huma.Config {
	cfg := huma.DefaultConfig("Innkeeper", "0.1.0")
	cfg.Servers = []*huma.Server{{URL: "/api"}}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"token":   {Type: "http", Scheme: "bearer"},
		"session": {Type: "apiKey", In: "cookie", Name: sessionCookie},
	}
	cfg.Security = []map[string][]string{{"token": {}}, {"session": {}}}
	return cfg
}

// routes registers every API operation.
func (s *Server) routes(api huma.API) {
	s.authRoutes(api)
	s.setupRoutes(api)
	s.entityRoutes(api, library.Card, "Cards")
	s.cardRoutes(api)
	s.entityRoutes(api, library.Lorebook, "Lorebooks")
	s.organizeRoutes(api)
	s.searchRoutes(api)
	s.libraryRoutes(api)
	s.librarySettingsRoutes(api)
	s.purgeRoutes(api)
	s.hideRoutes(api)
	s.catalogRoutes(api)
	s.instanceRoutes(api)
	s.pluginSettingsRoutes(api)
	s.pluginStoreRoutes(api)
	s.jobRoutes(api)
	s.targetRoutes(api)
	s.reconcileRoutes(api)
	s.toolRoutes(api)
	s.actionRoutes(api)
	s.enrichmentRoutes(api)
	s.recordRoutes(api)
	s.settingsRoutes(api)
	s.backupRoutes(api)
}

// Spec returns the OpenAPI spec as indented JSON, without a database or
// plugins.
func Spec() ([]byte, error) {
	api := humago.New(http.NewServeMux(), config())
	s := &Server{Lib: &library.Library{}}
	s.routes(api)
	return json.MarshalIndent(api.OpenAPI(), "", "  ")
}

// APIHandler serves the API without its /api prefix, for callers in the
// core's own processes, such as the MCP tools.
func (s *Server) APIHandler() *http.ServeMux {
	apiMux, _ := s.apiHandler()
	return apiMux
}

func (s *Server) apiHandler() (*http.ServeMux, huma.API) {
	apiMux := http.NewServeMux()
	api := humago.New(apiMux, config())
	api.UseMiddleware(s.authenticate(api))
	s.routes(api)
	s.fileRoutes(apiMux)
	return apiMux, api
}

// jobRoutes are api's routes that queue a job, for MCP's start_job.
func jobRoutes(api huma.API) []mcp.JobRoute {
	var out []mcp.JobRoute
	for path, item := range api.OpenAPI().Paths {
		op := item.Post
		if op == nil || op.DefaultStatus != http.StatusAccepted || op.Deprecated || strings.Contains(path, "{") {
			continue
		}
		desc := strings.TrimSpace(op.Summary + ". " + op.Description)
		if fields := bodyFields(api, op); fields != "" {
			desc += " Body: " + fields
		}
		out = append(out, mcp.JobRoute{Name: op.OperationID, Path: path, Description: desc, Tier: opTier(op)})
	}
	slices.SortFunc(out, func(a, b mcp.JobRoute) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// bodyFields lists op's JSON body fields with their docs, so an MCP client
// can fill in a body without the OpenAPI spec.
func bodyFields(api huma.API, op *huma.Operation) string {
	if op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil {
		return ""
	}
	schema := op.RequestBody.Content["application/json"].Schema
	if schema.Ref != "" {
		schema = api.OpenAPI().Components.Schemas.SchemaFromRef(schema.Ref)
	}
	var fields []string
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		if name == "$schema" {
			continue
		}
		f := name
		if slices.Contains(schema.Required, name) {
			f += " (required)"
		}
		if d := schema.Properties[name].Description; d != "" {
			f += ": " + d
		}
		fields = append(fields, f)
	}
	return strings.Join(fields, "; ")
}

// Handler returns the HTTP handler for every route: the API under /api,
// OAuth and MCP at the root, and UI for everything else.
func (s *Server) Handler() http.Handler {
	s.loginSlot = make(chan struct{}, 1)
	apiMux, api := s.apiHandler()
	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", apiMux))
	// Browsers arrive here from a login provider; it isn't part of the API.
	mux.HandleFunc("GET /auth/{id}/callback", s.callback)
	s.oauthRoutes(mux)
	mux.Handle("/mcp", mcp.New(apiMux, jobRoutes(api), s.Auth, s.Log, s.resourceMetadataURL()))
	if s.UI != nil {
		// Not "GET /": that conflicts with the method-less "/api/".
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// Reserved prefixes that nothing registered are 404s, not the app shell.
			for _, p := range []string{"/.well-known/", "/oauth/", "/auth/", "/mcp/", "/api/"} {
				if strings.HasPrefix(r.URL.Path, p) {
					http.NotFound(w, r)
					return
				}
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				w.Header().Set("Allow", "GET, HEAD")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			s.UI.ServeHTTP(w, r)
		})
	}
	return mux
}

// fail turns an error into an HTTP error. Unexpected errors are logged and
// hidden from the client. A nil error stays nil.
func (s *Server) fail(err error) error { return s.failAs("plugin", err) }

// failAs is fail, naming the plugin who in the message for a failed call.
func (s *Server) failAs(who string, err error) error {
	var se huma.StatusError
	var rpcErr *protocol.Error
	var startErr *pluginhost.StartError
	var stale *targets.StaleError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se):
		return se
	case errors.As(err, &stale):
		return huma.Error409Conflict(stale.Error(), &huma.ErrorDetail{Location: "body", Value: stale})
	case errors.Is(err, library.ErrNotFound), errors.Is(err, jobs.ErrNotFound),
		errors.Is(err, plugins.ErrNotFound), errors.Is(err, auth.ErrNotFound), errors.Is(err, targets.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, library.ErrConflict), errors.Is(err, library.ErrSlugTaken), errors.Is(err, library.ErrNameTaken),
		errors.Is(err, library.ErrVariantCycle),
		errors.Is(err, plugins.ErrNameTaken), errors.Is(err, plugins.ErrNeedsName), errors.Is(err, plugins.ErrSingleton), errors.Is(err, jobs.ErrFinished), errors.Is(err, jobs.ErrNotRetryable), errors.Is(err, plugins.ErrInUse), errors.Is(err, plugins.ErrLastLogin), errors.Is(err, plugins.ErrSourceInUse),
		errors.Is(err, targets.ErrNewCopy),
		errors.Is(err, targets.ErrBound):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, plugins.ErrDisabled):
		return huma.Error409Conflict("plugin instance is disabled; enable it first")
	case errors.Is(err, plugins.ErrBroken):
		// The stored error can hold paths and plugin output, so it stays out of the response.
		return huma.Error409Conflict("plugin instance is broken; fix its settings first")
	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin),
		errors.Is(err, targets.ErrCannot), errors.Is(err, plugins.ErrNoCodec), errors.Is(err, actions.ErrInvalid),
		errors.Is(err, plugins.ErrIncapable):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, merge.ErrUnknownHunk), errors.Is(err, merge.ErrBadChoice):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, plugins.ErrSource), errors.Is(err, plugins.ErrBadSource):
		return huma.Error502BadGateway(err.Error())
	// A plugin's error text can name files on the server, and read-tier
	// clients see these, so it goes only to the log.
	case errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams:
		s.Log.Warn("plugin rejected the request", "plugin", who, "err", err)
		return huma.Error400BadRequest(who + " rejected the request; see the server log")
	case errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeRemoteNotFound:
		s.Log.Info("plugin has no such item", "plugin", who, "err", err)
		return huma.Error404NotFound(who + " has no such item")
	case errors.As(err, &startErr), errors.Is(err, context.DeadlineExceeded), plugins.Retryable(err):
		s.Log.Warn("plugin unavailable", "plugin", who, "err", err)
		return huma.Error503ServiceUnavailable(who + " is unavailable; see the server log")
	case errors.As(err, &rpcErr):
		s.Log.Error("plugin failed", "plugin", who, "err", err)
		return huma.Error502BadGateway(who + " failed; see the server log")
	case errors.Is(err, targets.ErrBadReply), errors.Is(err, export.ErrBadCodec):
		return huma.Error502BadGateway("plugin: " + err.Error())
	}
	s.Log.Error("request failed", "err", err)
	return huma.Error500InternalServerError("internal error")
}
