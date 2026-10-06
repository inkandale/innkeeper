// Package api serves the core's HTTP API, described by one OpenAPI spec at
// /api/openapi.json.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/mcp"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/targets"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
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

	// loginSlot lets one login run at a time.
	loginSlot chan struct{}
	// tools caches each plugin instance's MCP tools, by instance version.
	tools sync.Map
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
	s.entityRoutes(api, s.Lib.Cards, "cards", "card", "Cards")
	s.cardRoutes(api)
	s.entityRoutes(api, s.Lib.Lorebooks, "lorebooks", "lorebook", "Lorebooks")
	s.organizeRoutes(api)
	s.searchRoutes(api)
	s.instanceRoutes(api)
	s.jobRoutes(api)
	s.targetRoutes(api)
	s.toolRoutes(api)
}

// Handler returns the HTTP handler for every route: the API under /api,
// OAuth and MCP at the root, and UI for everything else.
func (s *Server) Handler() http.Handler {
	s.loginSlot = make(chan struct{}, 1)
	apiMux := http.NewServeMux()
	api := humago.New(apiMux, config())
	api.UseMiddleware(s.authenticate(api))
	s.routes(api)

	mux := http.NewServeMux()
	mux.Handle("/api/", http.StripPrefix("/api", apiMux))
	// Browsers arrive here from a login provider; it isn't part of the API.
	mux.HandleFunc("GET /auth/{id}/callback", s.callback)
	s.oauthRoutes(mux)
	mux.Handle("/mcp", mcp.New(apiMux, s.Auth, s.Log, s.resourceMetadataURL()))
	if s.UI != nil {
		mux.Handle("/", s.UI)
	}
	return mux
}

// fail turns an error into an HTTP error. Unexpected errors are logged and
// hidden from the client. A nil error stays nil.
func (s *Server) fail(err error) error {
	var se huma.StatusError
	var rpcErr *protocol.Error
	var startErr *pluginhost.StartError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se):
		return se
	case errors.Is(err, library.ErrNotFound), errors.Is(err, jobs.ErrNotFound),
		errors.Is(err, plugins.ErrNotFound), errors.Is(err, auth.ErrNotFound), errors.Is(err, targets.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, library.ErrConflict), errors.Is(err, library.ErrSlugTaken), errors.Is(err, library.ErrNameTaken),
		errors.Is(err, plugins.ErrNameTaken), errors.Is(err, jobs.ErrFinished), errors.Is(err, plugins.ErrInUse),
		errors.Is(err, targets.ErrNewCopy), errors.Is(err, targets.ErrNoChanges), errors.Is(err, targets.ErrMoved),
		errors.Is(err, targets.ErrBound):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, plugins.ErrBroken):
		// The stored error can hold paths and plugin output, so it stays out of the response.
		return huma.Error409Conflict("plugin instance is broken; fix its settings first")
	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin),
		errors.Is(err, targets.ErrCannot), errors.Is(err, plugins.ErrNoCodec):
		return huma.Error400BadRequest(err.Error())
	case errors.As(err, &startErr), errors.Is(err, protocol.ErrClosed):
		s.Log.Error("plugin failed", "err", err)
		return huma.Error502BadGateway("plugin failed; see the server log")
	case errors.As(err, &rpcErr), errors.Is(err, targets.ErrBadReply), errors.Is(err, errBadCodec):
		return huma.Error502BadGateway("plugin: " + err.Error())
	}
	s.Log.Error("request failed", "err", err)
	return huma.Error500InternalServerError("internal error")
}
