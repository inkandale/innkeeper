package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

const (
	sessionCookie = "innkeeper_session"
	stateCookie   = "innkeeper_login_state"
)

// failedLoginDelay slows password guessing. Tests set it to 0.
var failedLoginDelay = time.Second

// authenticate lets a request through if it is public or carries a valid API
// token or session cookie.
func (s *Server) authenticate(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if ctx.Operation().Metadata["public"] == true {
			next(ctx)
			return
		}
		ok, err := s.authorized(ctx)
		if err != nil {
			s.Log.Error("check credentials", "err", err)
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "internal error")
			return
		}
		if !ok {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "log in, or send an API token as Authorization: Bearer <token>")
			return
		}
		next(ctx)
	}
}

func (s *Server) authorized(ctx huma.Context) (bool, error) {
	if h := ctx.Header("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return false, nil
		}
		return s.Auth.CheckToken(ctx.Context(), token)
	}
	c, err := huma.ReadCookie(ctx, sessionCookie)
	if err != nil {
		return false, nil
	}
	// Browsers attach cookies to requests that other sites start, so only
	// this site's pages may change things.
	if m := ctx.Method(); m != http.MethodGet && m != http.MethodHead {
		if crossSite(ctx.Header("Sec-Fetch-Site")) {
			return false, nil
		}
	}
	return s.Auth.CheckSession(ctx.Context(), c.Value)
}

// crossSite reports whether a browser says another site started the request.
func crossSite(secFetchSite string) bool {
	return secFetchSite != "" && secFetchSite != "same-origin" && secFetchSite != "none"
}

// cookie returns an HttpOnly cookie for the whole site. A negative maxAge
// deletes it.
func (s *Server) cookie(name, value string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()), HttpOnly: true,
		Secure: !strings.HasPrefix(s.PublicURL, "http://"), SameSite: http.SameSiteLaxMode}
}

func (s *Server) callbackURL(id int64) string {
	if s.PublicURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/auth/%d/callback", strings.TrimSuffix(s.PublicURL, "/"), id)
}

// authInstance returns an instance whose plugin can log people in.
func (s *Server) authInstance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	inst, err := s.Registry.Instance(ctx, id)
	if errors.Is(err, plugins.ErrBroken) {
		s.Log.Warn("login provider is broken", "instance", id, "err", err)
		return inst, huma.Error503ServiceUnavailable("login provider unavailable")
	}
	if err != nil {
		return inst, s.fail(err)
	}
	if !slices.Contains(inst.Manifest.Capabilities, "auth") {
		return inst, huma.Error404NotFound("no such login provider")
	}
	return inst, nil
}

// callPlugin calls an instance's plugin. A plugin that fails to start marks
// its instance broken.
func (s *Server) callPlugin(ctx context.Context, id int64, inst pluginhost.Instance, method string, params, result any) error {
	err := s.Plugins.Call(ctx, inst, method, params, result)
	var startErr *pluginhost.StartError
	if errors.As(err, &startErr) && ctx.Err() == nil {
		if merr := s.Registry.MarkBroken(context.WithoutCancel(ctx), id, err); merr != nil {
			s.Log.Error("mark plugin instance broken", "instance", id, "err", merr)
		}
		return huma.Error503ServiceUnavailable("login provider unavailable")
	}
	return err
}

// login completes a login and returns the cookies to set: the new session,
// and the deleted login state.
func (s *Server) login(ctx context.Context, id int64, state string, params map[string]string) ([]*http.Cookie, error) {
	inst, err := s.authInstance(ctx, id)
	if err != nil {
		return nil, err
	}
	// ponytail: logins are serialized, so failures cap guessing at ~1/second; per-provider slots if that's too slow.
	select {
	case s.loginSlot <- struct{}{}:
		defer func() { <-s.loginSlot }()
	case <-ctx.Done():
		return nil, huma.Error503ServiceUnavailable("login busy, try again")
	}
	decoded, _ := base64.RawURLEncoding.DecodeString(state)
	var res protocol.AuthCompleteResult
	err = s.callPlugin(ctx, id, inst, protocol.MethodAuthComplete,
		protocol.AuthCompleteParams{Params: params, State: string(decoded), CallbackURL: s.callbackURL(id)}, &res)
	var secret string
	if err == nil {
		secret, err = s.Auth.CreateSession(ctx, id, res.Subject)
	}
	var rpcErr *protocol.Error
	if errors.As(err, &rpcErr) || errors.Is(err, auth.ErrNotOwner) {
		s.Log.Warn("login failed", "instance", id, "err", err)
		time.Sleep(failedLoginDelay)
		return nil, huma.Error401Unauthorized("login failed")
	}
	if err != nil {
		return nil, s.fail(err)
	}
	return []*http.Cookie{s.cookie(sessionCookie, secret, auth.SessionTTL), s.cookie(stateCookie, "", -time.Second)}, nil
}

// callback finishes a redirect login: the provider sends the browser here
// with its result in the query string.
func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		params[k] = v[0]
	}
	state := ""
	if c, err := r.Cookie(stateCookie); err == nil {
		state = c.Value
	}
	cookies, err := s.login(r.Context(), id, state, params)
	if err != nil {
		status := http.StatusInternalServerError
		var se huma.StatusError
		if errors.As(err, &se) {
			status = se.GetStatus()
		}
		http.Error(w, http.StatusText(status), status)
		return
	}
	for _, c := range cookies {
		http.SetCookie(w, c)
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type provider struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Plugin string `json:"plugin"`
}

type providersOut struct{ Body []provider }

type beginOut struct {
	SetCookie []*http.Cookie `header:"Set-Cookie"`
	Body      struct {
		Redirect string          `json:"redirect,omitempty" doc:"Send the browser here."`
		Form     json.RawMessage `json:"form,omitempty" doc:"JSON Schema of the fields to ask for. Send them to complete-login."`
	}
}

type completeIn struct {
	ID    int64  `path:"id"`
	State string `cookie:"innkeeper_login_state"`
	Body  struct {
		Params map[string]string `json:"params"`
	}
}

type cookiesOut struct {
	SetCookie []*http.Cookie `header:"Set-Cookie"`
}

type logoutIn struct {
	Session      string `cookie:"innkeeper_session"`
	SecFetchSite string `header:"Sec-Fetch-Site"`
}

type tokensOut struct{ Body []auth.Token }

type createTokenIn struct {
	Body struct {
		Name string `json:"name" minLength:"1"`
	}
}

type createdToken struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	Secret    string    `json:"secret" doc:"Send as Authorization: Bearer <secret>. Shown only once."`
}

type createTokenOut struct{ Body createdToken }

func (s *Server) authRoutes(api huma.API) {
	tags := []string{"Auth"}
	huma.Register(api, huma.Operation{OperationID: "list-login-providers", Method: http.MethodGet,
		Path: "/auth/providers", Summary: "List login providers", Tags: tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, _ *struct{}) (*providersOut, error) {
			insts, err := s.Registry.ListInstances(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out := &providersOut{Body: []provider{}}
			for _, i := range insts {
				m := s.Registry.Manifests()[i.Plugin]
				if m != nil && slices.Contains(m.Capabilities, "auth") && i.OwnerSubject != nil && i.BrokenError == nil {
					out.Body = append(out.Body, provider{i.ID, i.Name, i.Plugin})
				}
			}
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "begin-login", Method: http.MethodPost,
		Path: "/auth/{id}/begin", Summary: "Start a login",
		Description: "Returns a form to fill in or a URL to send the browser to, and sets a 10-minute login state cookie.",
		Tags:        tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, in *idIn) (*beginOut, error) {
			inst, err := s.authInstance(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			var res protocol.AuthBeginResult
			if err := s.callPlugin(ctx, in.ID, inst, protocol.MethodAuthBegin,
				protocol.AuthBeginParams{CallbackURL: s.callbackURL(in.ID)}, &res); err != nil {
				return nil, s.fail(err)
			}
			// Encoded because plugins may use characters cookies can't hold.
			state := base64.RawURLEncoding.EncodeToString([]byte(res.State))
			out := &beginOut{SetCookie: []*http.Cookie{s.cookie(stateCookie, state, 10*time.Minute)}}
			out.Body.Redirect, out.Body.Form = res.Redirect, res.Form
			return out, nil
		})

	huma.Register(api, huma.Operation{OperationID: "complete-login", Method: http.MethodPost,
		Path: "/auth/{id}/complete", Summary: "Finish a login", Description: "Sets the session cookie.", DefaultStatus: http.StatusOK,
		Tags: tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, in *completeIn) (*cookiesOut, error) {
			cookies, err := s.login(ctx, in.ID, in.State, in.Body.Params)
			if err != nil {
				return nil, err
			}
			return &cookiesOut{SetCookie: cookies}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout",
		Summary: "Log out", Tags: tags, Metadata: public, Security: noAuth, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *logoutIn) (*cookiesOut, error) {
			// Stops other sites from logging the owner out.
			if crossSite(in.SecFetchSite) {
				return nil, huma.Error403Forbidden("cross-site logout refused")
			}
			if in.Session != "" {
				if err := s.Auth.DeleteSession(ctx, in.Session); err != nil {
					return nil, s.fail(err)
				}
			}
			return &cookiesOut{SetCookie: []*http.Cookie{s.cookie(sessionCookie, "", -time.Second)}}, nil
		})

	tags = []string{"Tokens"}
	huma.Register(api, huma.Operation{OperationID: "list-tokens", Method: http.MethodGet, Path: "/tokens",
		Summary: "List API tokens", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*tokensOut, error) {
			list, err := s.Auth.Tokens(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &tokensOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "create-token", Method: http.MethodPost, Path: "/tokens",
		Summary: "Create an API token", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createTokenIn) (*createTokenOut, error) {
			t, secret, err := s.Auth.CreateToken(ctx, in.Body.Name)
			if err != nil {
				return nil, s.fail(err)
			}
			return &createTokenOut{Body: createdToken{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, Secret: secret}}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "revoke-token", Method: http.MethodDelete, Path: "/tokens/{id}",
		Summary: "Revoke an API token", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Auth.RevokeToken(ctx, in.ID))
		})
}
