// Package api serves the core's HTTP API, described by one OpenAPI spec at
// /openapi.json.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Server holds what the API's handlers use.
type Server struct {
	Lib      *library.Library
	Queue    *jobs.Queue
	Registry *plugins.Registry
	Auth     *auth.Auth
	// Plugins runs plugins for interactive calls, such as logins. Its core
	// handler should be NoCore.
	Plugins *pluginhost.Manager
	Log     *slog.Logger
	// PublicURL is where browsers reach the API, such as
	// https://innkeeper.example.com. Optional. Redirect logins need it, and
	// an http:// URL sends cookies without Secure.
	PublicURL string

	// loginSlot lets one login run at a time.
	loginSlot chan struct{}
}

// NoCore answers plugin callbacks. The API offers plugins none yet.
func NoCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

// public marks an operation that needs no credentials.
var public = map[string]any{"public": true}

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

// Handler returns the HTTP handler for every route.
func (s *Server) Handler() http.Handler {
	s.loginSlot = make(chan struct{}, 1)
	mux := http.NewServeMux()
	cfg := huma.DefaultConfig("Innkeeper", "0.1.0")
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"token":   {Type: "http", Scheme: "bearer"},
		"session": {Type: "apiKey", In: "cookie", Name: sessionCookie},
	}
	cfg.Security = []map[string][]string{{"token": {}}, {"session": {}}}
	api := humago.New(mux, cfg)
	api.UseMiddleware(s.authenticate(api))
	s.authRoutes(api)
	s.entityRoutes(api, s.Lib.Cards, "cards", "card", "Cards")
	s.cardRoutes(api)
	s.entityRoutes(api, s.Lib.Lorebooks, "lorebooks", "lorebook", "Lorebooks")
	s.organizeRoutes(api)
	s.searchRoutes(api)
	s.instanceRoutes(api)
	// Browsers arrive here from a login provider; it isn't part of the API.
	mux.HandleFunc("GET /auth/{id}/callback", s.callback)
	return mux
}

// fail turns an error into an HTTP error. Unexpected errors are logged and
// hidden from the client. A nil error stays nil.
func (s *Server) fail(err error) error {
	var se huma.StatusError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &se):
		return se
	case errors.Is(err, library.ErrNotFound), errors.Is(err, jobs.ErrNotFound),
		errors.Is(err, plugins.ErrNotFound), errors.Is(err, auth.ErrNotFound):
		return huma.Error404NotFound("not found")
	case errors.Is(err, library.ErrConflict), errors.Is(err, library.ErrSlugTaken), errors.Is(err, library.ErrNameTaken),
		errors.Is(err, plugins.ErrNameTaken), errors.Is(err, jobs.ErrFinished):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin):
		return huma.Error400BadRequest(err.Error())
	}
	s.Log.Error("request failed", "err", err)
	return huma.Error500InternalServerError("internal error")
}
