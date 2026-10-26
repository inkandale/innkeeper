package api

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

const (
	sessionCookie = "innkeeper_session"
	stateCookie   = "innkeeper_login_state"
)

// failedLoginDelay slows password guessing. Tests set it to 0.
var failedLoginDelay = time.Second

// authenticate lets a request through if it is public, or carries a valid
// API token or session cookie whose tier allows the operation.
func (s *Server) authenticate(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if op.Metadata["public"] == true {
			next(ctx)
			return
		}
		caller, oauth, err := s.authorized(ctx)
		if err != nil {
			s.Log.Error("check credentials", "err", err)
			_ = huma.WriteErr(api, ctx, http.StatusInternalServerError, "internal error")
			return
		}
		if caller == 0 {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "log in, or send an API token as Authorization: Bearer <token>")
			return
		}
		if need := opTier(op); !caller.Allows(need) {
			_ = huma.WriteErr(api, ctx, http.StatusForbidden,
				fmt.Sprintf("this token's tier is %s; %s needs %s", caller, op.OperationID, need))
			return
		}
		ctx = huma.WithValue(ctx, tierKey{}, caller)
		if oauth {
			ctx = huma.WithValue(ctx, oauthKey{}, true)
		}
		next(ctx)
	}
}

// authorized returns the tier of the request's credentials, or 0 if it has
// none that are valid, and whether they are an OAuth access token. A
// logged-in browser is the owner, so it has every tier.
func (s *Server) authorized(ctx huma.Context) (t tier.Tier, oauth bool, err error) {
	if t := auth.InProcessTier(ctx.Context()); t != 0 {
		return t, false, nil
	}
	if h := ctx.Header("Authorization"); h != "" {
		token, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return 0, false, nil
		}
		t, err = s.Auth.CheckToken(ctx.Context(), token)
		if t == 0 && err == nil {
			// An MCP access token works here only through /mcp's tools, which
			// call the API in process.
			resource := auth.ResourceAPI
			if auth.FromMCP(ctx.Context()) {
				resource = auth.ResourceMCP
			}
			t, _, err = s.Auth.CheckAccessToken(ctx.Context(), token, resource)
			oauth = t != 0
		}
		return t, oauth, err
	}
	c, err := huma.ReadCookie(ctx, sessionCookie)
	if err != nil {
		return 0, false, nil
	}
	// Browsers attach cookies to requests that other sites start, so only
	// this site's pages may change things.
	if m := ctx.Method(); m != http.MethodGet && m != http.MethodHead {
		if crossSite(ctx.Header("Sec-Fetch-Site")) {
			return 0, false, nil
		}
	}
	ok, err := s.Auth.CheckSession(ctx.Context(), c.Value)
	if !ok || err != nil {
		return 0, false, err
	}
	return tier.Destructive, false, nil
}

// crossSite reports whether a browser says another site started the request.
func crossSite(secFetchSite string) bool {
	return secFetchSite != "" && secFetchSite != "same-origin" && secFetchSite != "none"
}

// cookie returns an HttpOnly cookie for the whole site. A negative maxAge
// deletes it.
func (s *Server) cookie(ctx context.Context, name, value string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: int(maxAge.Seconds()), HttpOnly: true,
		Secure: s.secureCookies(ctx), SameSite: http.SameSiteLaxMode}
}

type httpsKey struct{}

// withScheme records whether the browser reached the server over https,
// directly or through a proxy.
func withScheme(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		https := r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), httpsKey{}, https)))
	})
}

// secureCookies reports whether cookies need Secure. The public URL decides;
// without one the request does, since a browser drops a Secure cookie sent
// over plain http.
func (s *Server) secureCookies(ctx context.Context) bool {
	if s.PublicURL != "" {
		return !strings.HasPrefix(s.PublicURL, "http://")
	}
	https, known := ctx.Value(httpsKey{}).(bool)
	return https || !known
}

func (s *Server) callbackURL(id int64) string {
	if s.PublicURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/auth/%d/callback", strings.TrimSuffix(s.PublicURL, "/"), id)
}

// authInstance returns an instance whose plugin can log people in.
func (s *Server) authInstance(ctx context.Context, id int64) (pluginhost.Instance, error) {
	inst, err := s.Registry.InstanceWith(ctx, id, protocol.CapabilityAuth)
	if errors.Is(err, plugins.ErrBroken) || errors.Is(err, plugins.ErrDisabled) {
		s.Log.Warn("login provider is broken", "instance", id, "err", err)
		return inst, huma.Error503ServiceUnavailable("login provider unavailable")
	}
	if errors.Is(err, plugins.ErrIncapable) {
		return inst, huma.Error404NotFound("no such login provider")
	}
	if err != nil {
		return inst, s.fail(err)
	}
	return inst, nil
}

// callPlugin calls an instance's plugin. A plugin that fails to start marks
// its instance broken.
func (s *Server) callPlugin(ctx context.Context, id int64, inst pluginhost.Instance, method string, params, result any) error {
	err := s.Registry.Call(ctx, s.Plugins, inst, method, params, result)
	var startErr *pluginhost.StartError
	if errors.As(err, &startErr) && ctx.Err() == nil {
		s.Log.Error("login provider failed to start", "instance", id, "err", err)
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
	return []*http.Cookie{s.cookie(ctx, sessionCookie, secret, auth.SessionTTL), s.cookie(ctx, stateCookie, "", -time.Second)}, nil
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
	// A login started by an OAuth client's authorization goes back to it.
	if c, err := r.Cookie(returnCookie); err == nil {
		http.SetCookie(w, s.cookie(r.Context(), returnCookie, "", -time.Second))
		resume(w, r, c.Value)
		return
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

type grantsOut struct{ Body []auth.Grant }

type createTokenIn struct {
	Body struct {
		Name string `json:"name" minLength:"1"`
		Tier string `json:"tier,omitempty" enum:"read,write,destructive" default:"read" doc:"What the token may do. At most the tier of the credentials creating it."`
	}
}

type createdToken struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Tier      string    `json:"tier"`
	CreatedAt time.Time `json:"created_at"`
	Secret    string    `json:"secret" doc:"Send as Authorization: Bearer <secret>. Shown only once."`
}

type createTokenOut struct{ Body createdToken }

func (s *Server) authRoutes(api huma.API) {
	tags := []string{"Auth"}
	huma.Register(api, huma.Operation{OperationID: "list-login-providers", Method: http.MethodGet,
		Path: "/auth/providers", Summary: "List login providers", Tags: tags, Metadata: public, Security: noAuth},
		func(ctx context.Context, _ *struct{}) (*providersOut, error) {
			list, err := s.loginProviders(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &providersOut{Body: orEmpty(list)}, nil
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
			// This is the UI's login; drop a return left by an abandoned OAuth login.
			out := &beginOut{SetCookie: []*http.Cookie{
				s.cookie(ctx, stateCookie, state, 10*time.Minute),
				s.cookie(ctx, returnCookie, "", -time.Second),
			}}
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
			return &cookiesOut{SetCookie: []*http.Cookie{s.cookie(ctx, sessionCookie, "", -time.Second)}}, nil
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

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "create-token", Method: http.MethodPost, Path: "/tokens",
		Summary: "Create an API token", Tags: tags, DefaultStatus: http.StatusCreated},
		func(ctx context.Context, in *createTokenIn) (*createTokenOut, error) {
			want, err := tier.Parse(cmp.Or(in.Body.Tier, "read"))
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if caller := callerTier(ctx); !caller.Allows(want) {
				return nil, huma.Error403Forbidden(fmt.Sprintf("a %s token can't create a %s token", caller, want))
			}
			t, secret, err := s.Auth.CreateToken(ctx, in.Body.Name, want)
			if err != nil {
				return nil, s.fail(err)
			}
			return &createTokenOut{Body: createdToken{ID: t.ID, Name: t.Name, Tier: t.Tier, CreatedAt: t.CreatedAt, Secret: secret}}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "revoke-token", Method: http.MethodDelete, Path: "/tokens/{id}",
		Summary: "Revoke an API token", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Auth.RevokeToken(ctx, in.ID))
		})

	huma.Register(api, huma.Operation{OperationID: "list-grants", Method: http.MethodGet, Path: "/grants",
		Summary: "List the OAuth clients you approved", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*grantsOut, error) {
			list, err := s.Auth.Grants(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			return &grantsOut{Body: orEmpty(list)}, nil
		})

	huma.Register(api, huma.Operation{Metadata: needs(tier.Destructive), OperationID: "revoke-grant", Method: http.MethodDelete,
		Path: "/grants/{id}", Summary: "Revoke an OAuth client's access", Tags: tags, DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *idIn) (*struct{}, error) {
			return nil, s.fail(s.Auth.RevokeGrant(ctx, in.ID))
		})
}
