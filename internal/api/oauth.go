package api

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/auth"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// maxClients caps registered OAuth clients. Unapproved ones are pruned a
// day after registering.
const maxClients = 1000

const (
	// returnCookie holds the authorize request to resume after a login
	// through a redirect provider.
	returnCookie = "innkeeper_oauth_return"
	csrfCookie   = "innkeeper_oauth_csrf"
)

// authorizeParams are the parameters of an authorization request, kept as
// hidden fields through login and consent.
var authorizeParams = []string{"response_type", "client_id", "redirect_uri", "code_challenge",
	"code_challenge_method", "state", "scope", "resource"}

func (s *Server) public() string { return strings.TrimSuffix(s.PublicURL, "/") }

// resourceName returns the grant resource an authorization request names,
// or "" for an unknown one. Naming none means /mcp, since MCP clients may
// leave it out.
func (s *Server) resourceName(resource string) string {
	switch strings.TrimSuffix(resource, "/") {
	case "", s.public() + "/mcp":
		return auth.ResourceMCP
	case s.public() + "/api":
		return auth.ResourceAPI
	}
	return ""
}

// oauthRoutes serves OAuth for MCP clients. It needs PublicURL, since every
// endpoint is named by an absolute URL.
func (s *Server) oauthRoutes(mux *http.ServeMux) {
	if s.PublicURL == "" {
		return
	}
	resource := func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 s.public() + "/mcp",
			"authorization_servers":    []string{s.public()},
			"scopes_supported":         tier.Names,
			"bearer_methods_supported": []string{"header"},
		})
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", resource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", resource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"issuer":                                         s.public(),
			"authorization_endpoint":                         s.public() + "/oauth/authorize",
			"token_endpoint":                                 s.public() + "/oauth/token",
			"registration_endpoint":                          s.public() + "/oauth/register",
			"response_types_supported":                       []string{"code"},
			"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []string{"S256"},
			"token_endpoint_auth_methods_supported":          []string{"none"},
			"scopes_supported":                               tier.Names,
			"authorization_response_iss_parameter_supported": true,
		})
	})
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("POST /oauth/authorize", s.decide)
	mux.HandleFunc("GET /oauth/login", s.oauthLogin)
	mux.HandleFunc("POST /oauth/login", s.oauthLoginSubmit)
	mux.HandleFunc("POST /oauth/token", s.token)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// validRedirect accepts https URIs, and http ones on a loopback host.
func validRedirect(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Fragment != "" || u.Host == "" {
		return false
	}
	return u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname())
}

func loopback(host string) bool { return host == "localhost" || host == "127.0.0.1" || host == "::1" }

// redirectAllowed reports whether uri is one of registered. A loopback
// redirect may use any port, since a native app learns its port only when
// it starts listening (RFC 8252 §7.3). Nothing else may differ, so the
// match compares the escaped path and rejects userinfo and fragments.
func redirectAllowed(registered []string, uri string) bool {
	if slices.Contains(registered, uri) {
		return true
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || !loopback(u.Hostname()) || u.User != nil || u.Fragment != "" {
		return false
	}
	return slices.ContainsFunc(registered, func(r string) bool {
		ru, err := url.Parse(r)
		return err == nil && ru.Scheme == "http" && ru.Hostname() == u.Hostname() && ru.EscapedPath() == u.EscapedPath() && ru.RawQuery == u.RawQuery && ru.ForceQuery == u.ForceQuery
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body must be a JSON object")
		return
	}
	if len(in.RedirectURIs) == 0 || len(in.RedirectURIs) > 10 || slices.ContainsFunc(in.RedirectURIs, func(u string) bool { return !validRedirect(u) }) {
		oauthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris must be 1 to 10 https URIs, or http on localhost")
		return
	}
	name := strings.TrimSpace(in.ClientName)
	if name == "" || len(name) > 100 {
		name = "Unnamed client"
	}
	// Registration is open, so prune at most hourly and cap the clients.
	if last := s.lastPrune.Load(); time.Since(time.Unix(last, 0)) > time.Hour && s.lastPrune.CompareAndSwap(last, time.Now().Unix()) {
		if err := s.Auth.PruneOAuth(r.Context()); err != nil {
			s.Log.Warn("prune oauth", "err", err)
		}
	}
	if n, err := s.Auth.ClientCount(r.Context()); err != nil || n >= maxClients {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "too many registered clients; try again tomorrow")
		return
	}
	c, err := s.Auth.RegisterClient(r.Context(), name, in.RedirectURIs)
	if err != nil {
		s.Log.Error("register oauth client", "err", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": c.ID, "client_name": c.Name, "redirect_uris": c.RedirectURIs,
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"},
	})
}

// authRequest is a checked authorization request.
type authRequest struct {
	params url.Values
	client auth.Client
}

// checkAuthorize validates an authorization request. A bad client or
// redirect URI shows an error page; other errors go back to the client.
func (s *Server) checkAuthorize(w http.ResponseWriter, r *http.Request, q url.Values) (authRequest, bool) {
	c, err := s.Auth.Client(r.Context(), q.Get("client_id"))
	if err != nil {
		s.page(w, http.StatusBadRequest, errorPage, "This app isn't registered with Innkeeper. Try connecting it again.")
		return authRequest{}, false
	}
	if !redirectAllowed(c.RedirectURIs, q.Get("redirect_uri")) {
		s.page(w, http.StatusBadRequest, errorPage, "This app sent a return address it didn't register.")
		return authRequest{}, false
	}
	fail := func(code, description string) (authRequest, bool) {
		s.redirectBack(w, r, q, url.Values{"error": {code}, "error_description": {description}})
		return authRequest{}, false
	}
	switch {
	case q.Get("response_type") != "code":
		return fail("unsupported_response_type", "response_type must be code")
	case q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43:
		return fail("invalid_request", "PKCE with S256 is required")
	case s.resourceName(q.Get("resource")) == "":
		return fail("invalid_target", "the resource must be "+s.public()+"/mcp or "+s.public()+"/api")
	}
	kept := url.Values{}
	for _, k := range authorizeParams {
		if v := q.Get(k); v != "" {
			kept.Set(k, v)
		}
	}
	return authRequest{kept, c}, true
}

// redirectBack sends the browser to the client's redirect URI with result.
func (s *Server) redirectBack(w http.ResponseWriter, r *http.Request, q, result url.Values) {
	u, _ := url.Parse(q.Get("redirect_uri"))
	v := u.Query()
	for k, vals := range result {
		v[k] = vals
	}
	if st := q.Get("state"); st != "" {
		v.Set("state", st)
	}
	v.Set("iss", s.public())
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}

// session reports whether the request carries a live login session.
func (s *Server) session(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	ok, err := s.Auth.CheckSession(r.Context(), c.Value)
	if err != nil {
		s.Log.Error("check session", "err", err)
	}
	return ok
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	req, ok := s.checkAuthorize(w, r, r.URL.Query())
	if !ok {
		return
	}
	if !s.session(r) {
		s.loginPage(w, r, req.params.Encode(), "")
		return
	}
	csrf := randomString()
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Value: csrf, Path: "/oauth/", MaxAge: 600, HttpOnly: true,
		Secure: s.cookie("", "", 0).Secure, SameSite: http.SameSiteStrictMode})
	s.page(w, http.StatusOK, consentPage, map[string]any{
		"Client": req.client.Name, "Redirect": req.params.Get("redirect_uri"), "Params": req.params, "CSRF": csrf,
	})
}

func randomString() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// sameOrigin refuses form posts that another site started.
func sameOrigin(r *http.Request) bool {
	return !crossSite(r.Header.Get("Sec-Fetch-Site"))
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		s.page(w, http.StatusBadRequest, errorPage, "That request was malformed.")
		return
	}
	c, err := r.Cookie(csrfCookie)
	if !sameOrigin(r) || err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.PostForm.Get("csrf"))) != 1 {
		s.page(w, http.StatusForbidden, errorPage, "This approval didn't come from Innkeeper's own page. Start the connection again.")
		return
	}
	if !s.session(r) {
		s.page(w, http.StatusUnauthorized, errorPage, "Your login ended. Start the connection again.")
		return
	}
	req, ok := s.checkAuthorize(w, r, r.PostForm)
	if !ok {
		return
	}
	http.SetCookie(w, &http.Cookie{Name: csrfCookie, Path: "/oauth/", MaxAge: -1})
	if r.PostForm.Get("decision") != "approve" {
		s.redirectBack(w, r, req.params, url.Values{"error": {"access_denied"}})
		return
	}
	t, err := tier.Parse(r.PostForm.Get("tier"))
	if err != nil {
		s.page(w, http.StatusBadRequest, errorPage, "Choose what the app may do.")
		return
	}
	code, err := s.Auth.Approve(r.Context(), req.client.ID, t, s.resourceName(req.params.Get("resource")),
		req.params.Get("redirect_uri"), req.params.Get("code_challenge"))
	if err != nil {
		s.Log.Error("approve oauth client", "err", err)
		s.redirectBack(w, r, req.params, url.Values{"error": {"server_error"}})
		return
	}
	s.redirectBack(w, r, req.params, url.Values{"code": {code}})
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "the body must be form-encoded")
		return
	}
	f := r.PostForm
	var toks auth.Tokens
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		toks, err = s.Auth.RedeemCode(r.Context(), f.Get("client_id"), f.Get("code"), f.Get("redirect_uri"), f.Get("code_verifier"))
	case "refresh_token":
		toks, err = s.Auth.Refresh(r.Context(), f.Get("client_id"), f.Get("refresh_token"))
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	if errors.Is(err, auth.ErrBadGrant) {
		oauthError(w, http.StatusBadRequest, "invalid_grant", "the code or refresh token is invalid, expired, or used")
		return
	}
	if err != nil {
		s.Log.Error("issue oauth tokens", "err", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": toks.Access, "token_type": "Bearer", "expires_in": int(auth.AccessTTL.Seconds()),
		"refresh_token": toks.Refresh, "scope": toks.Tier.String(),
	})
}

// loginProviders are the auth instances that can log the owner in.
func (s *Server) loginProviders(ctx context.Context) ([]provider, error) {
	insts, err := s.Registry.ListInstances(ctx)
	if err != nil {
		return nil, err
	}
	var out []provider
	for _, i := range insts {
		m := s.Registry.Manifests()[i.Plugin]
		if m != nil && slices.Contains(m.Capabilities, "auth") && i.OwnerSubject != nil && i.BrokenError == nil {
			out = append(out, provider{i.ID, i.Name, i.Plugin})
		}
	}
	return out, nil
}

// loginPage lists the login providers, or starts the only one.
func (s *Server) loginPage(w http.ResponseWriter, r *http.Request, ret, failed string) {
	list, err := s.loginProviders(r.Context())
	if err != nil {
		s.Log.Error("list login providers", "err", err)
		s.page(w, http.StatusInternalServerError, errorPage, "Something went wrong.")
		return
	}
	if len(list) == 0 {
		s.page(w, http.StatusServiceUnavailable, errorPage, "No login provider is set up yet.")
		return
	}
	if len(list) == 1 && failed == "" {
		s.beginLogin(w, r, list[0].ID, ret, "")
		return
	}
	s.page(w, http.StatusOK, providersPage, map[string]any{"Providers": list, "Return": ret, "Failed": failed})
}

func (s *Server) oauthLogin(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("provider"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.beginLogin(w, r, id, r.URL.Query().Get("return"), "")
}

// beginLogin starts a login with provider id, then resumes the
// authorization request ret.
func (s *Server) beginLogin(w http.ResponseWriter, r *http.Request, id int64, ret, failed string) {
	inst, err := s.authInstance(r.Context(), id)
	if err != nil {
		s.page(w, http.StatusNotFound, errorPage, "That login provider isn't available.")
		return
	}
	var res protocol.AuthBeginResult
	if err := s.callPlugin(r.Context(), id, inst, protocol.MethodAuthBegin,
		protocol.AuthBeginParams{CallbackURL: s.callbackURL(id)}, &res); err != nil {
		s.Log.Error("begin login", "instance", id, "err", err)
		s.page(w, http.StatusServiceUnavailable, errorPage, "That login provider isn't answering.")
		return
	}
	http.SetCookie(w, s.cookie(stateCookie, base64.RawURLEncoding.EncodeToString([]byte(res.State)), 10*time.Minute))
	if res.Redirect != "" {
		http.SetCookie(w, s.cookie(returnCookie, ret, 10*time.Minute))
		http.Redirect(w, r, res.Redirect, http.StatusSeeOther)
		return
	}
	var form struct {
		Properties map[string]struct {
			Title  string `json:"title"`
			Format string `json:"format"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(res.Form, &form)
	type field struct{ Name, Title, Type string }
	var fields []field
	for _, name := range slices.Sorted(maps.Keys(form.Properties)) {
		p := form.Properties[name]
		typ := "text"
		if p.Format == "password" {
			typ = "password"
		}
		fields = append(fields, field{name, cmp.Or(p.Title, name), typ})
	}
	s.page(w, http.StatusOK, loginPage, map[string]any{"Provider": id, "Fields": fields, "Return": ret, "Failed": failed})
}

func (s *Server) oauthLoginSubmit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil || !sameOrigin(r) {
		s.page(w, http.StatusBadRequest, errorPage, "That login was malformed.")
		return
	}
	id, err := strconv.ParseInt(r.PostForm.Get("provider"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ret := r.PostForm.Get("return")
	params := map[string]string{}
	for k, v := range r.PostForm {
		if !strings.HasPrefix(k, "f_") {
			continue
		}
		params[strings.TrimPrefix(k, "f_")] = v[0]
	}
	state := ""
	if c, err := r.Cookie(stateCookie); err == nil {
		state = c.Value
	}
	cookies, err := s.login(r.Context(), id, state, params)
	if err != nil {
		var se huma.StatusError
		if errors.As(err, &se) && se.GetStatus() == http.StatusUnauthorized {
			s.beginLogin(w, r, id, ret, "That didn't work. Try again.")
			return
		}
		s.Log.Error("oauth login", "err", err)
		s.page(w, http.StatusServiceUnavailable, errorPage, "Logging in failed. Try again shortly.")
		return
	}
	for _, c := range cookies {
		http.SetCookie(w, c)
	}
	resume(w, r, ret)
}

// resume sends the browser back to the authorization request ret. It only
// ever leads to /oauth/authorize, so it can't redirect anywhere else.
func resume(w http.ResponseWriter, r *http.Request, ret string) {
	q, err := url.ParseQuery(ret)
	if err != nil {
		q = url.Values{}
	}
	http.Redirect(w, r, "/oauth/authorize?"+q.Encode(), http.StatusSeeOther)
}

// page writes one of the OAuth pages. They can't be framed, so another site
// can't trick the owner into clicking approve.
func (s *Server) page(w http.ResponseWriter, status int, t *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.WriteHeader(status)
	if err := t.Execute(w, data); err != nil {
		s.Log.Error("render oauth page", "err", err)
	}
}
