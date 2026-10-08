package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// oauthEnv serves the test env's API with a public URL, so OAuth is on.
type oauthEnv struct {
	*testEnv
	url     string
	browser *http.Client
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	e := newEnv(t)
	srv := httptest.NewUnstartedServer(nil)
	s := &Server{Lib: e.s.Lib, Queue: e.s.Queue, Registry: e.s.Registry, Auth: e.s.Auth, Targets: e.s.Targets,
		Plugins: e.s.Plugins, Log: e.s.Log, PublicURL: "http://" + srv.Listener.Addr().String()}
	srv.Config.Handler = s.Handler()
	srv.Start()
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	// A browser that doesn't follow redirects, so the test can look at each.
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &oauthEnv{e, s.PublicURL, browser}
}

func (o *oauthEnv) get(path string) (*http.Response, string) {
	o.t.Helper()
	resp, err := o.browser.Get(o.url + path)
	if err != nil {
		o.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (o *oauthEnv) post(path string, form url.Values, headers ...string) (*http.Response, string) {
	o.t.Helper()
	req, _ := http.NewRequest("POST", o.url+path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := o.browser.Do(req)
	if err != nil {
		o.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

const redirect = "http://localhost:9999/callback"

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func TestOAuthFlow(t *testing.T) {
	o := newOAuthEnv(t)

	// Discovery and registration.
	_, meta := o.get("/.well-known/oauth-authorization-server")
	if !strings.Contains(meta, `"S256"`) || !strings.Contains(meta, o.url+"/oauth/token") {
		t.Fatalf("metadata = %s", meta)
	}
	if resp, _ := o.get("/.well-known/oauth-protected-resource/mcp"); resp.StatusCode != 200 {
		t.Fatalf("resource metadata: %d", resp.StatusCode)
	}
	resp, err := http.Post(o.url+"/mcp", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), "/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("401 WWW-Authenticate = %q", resp.Header.Get("WWW-Authenticate"))
	}
	reg, err := http.Post(o.url+"/oauth/register", "application/json",
		strings.NewReader(`{"client_name": "Claude", "redirect_uris": ["`+redirect+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var client struct {
		ClientID string `json:"client_id"`
	}
	_ = json.NewDecoder(reg.Body).Decode(&client)
	if reg.StatusCode != 201 || client.ClientID == "" {
		t.Fatalf("register: %d", reg.StatusCode)
	}
	if r, _ := http.Post(o.url+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris": ["http://evil.example/cb"]}`)); r.StatusCode != 400 {
		t.Fatalf("plain http redirect registered: %d", r.StatusCode)
	}

	verifier := strings.Repeat("x", 50)
	sum := sha256.Sum256([]byte(verifier))
	params := url.Values{"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {redirect},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"xyz"}, "resource": {o.url + "/mcp"}}

	// Refusals before anything is shown.
	bad := url.Values{}
	for k, v := range params {
		bad[k] = v
	}
	bad.Set("redirect_uri", "http://localhost:9999/other")
	if resp, _ := o.get("/oauth/authorize?" + bad.Encode()); resp.StatusCode != 400 || resp.Header.Get("Location") != "" {
		t.Fatalf("unregistered redirect: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	bad.Set("redirect_uri", redirect)
	bad.Del("code_challenge")
	if resp, _ := o.get("/oauth/authorize?" + bad.Encode()); !strings.Contains(resp.Header.Get("Location"), "error=invalid_request") {
		t.Fatalf("no PKCE: %s", resp.Header.Get("Location"))
	}

	// Log in through the only login provider, then consent.
	_, page := o.get("/oauth/authorize?" + params.Encode())
	if !strings.Contains(page, `name="f_password"`) {
		t.Fatalf("login page = %s", page)
	}
	resp, _ = o.post("/oauth/login", url.Values{"provider": {itoa64(o.loginID)}, "return": {params.Encode()}, "f_password": {"letmein"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "/oauth/authorize?") {
		t.Fatalf("login: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	_, page = o.get("/oauth/authorize?" + params.Encode())
	m := csrfField.FindStringSubmatch(page)
	if m == nil || !strings.Contains(page, "Connect Claude?") {
		t.Fatalf("consent page = %s", page)
	}
	decision := url.Values{"csrf": {m[1]}, "tier": {"write"}, "decision": {"approve"}}
	for k, v := range params {
		decision[k] = v
	}
	if resp, _ := o.post("/oauth/authorize", decision, "Sec-Fetch-Site", "cross-site"); resp.StatusCode != 403 {
		t.Fatalf("cross-site approval: %d", resp.StatusCode)
	}
	noCSRF := url.Values{}
	for k, v := range decision {
		noCSRF[k] = v
	}
	noCSRF.Set("csrf", "forged")
	if resp, _ := o.post("/oauth/authorize", noCSRF); resp.StatusCode != 403 {
		t.Fatalf("forged csrf: %d", resp.StatusCode)
	}
	resp, _ = o.post("/oauth/authorize", decision)
	back, _ := url.Parse(resp.Header.Get("Location"))
	code := back.Query().Get("code")
	if code == "" || back.Query().Get("state") != "xyz" || back.Query().Get("iss") != o.url {
		t.Fatalf("approval redirect = %s", back)
	}

	// Tokens.
	tokenReq := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {client.ClientID},
		"redirect_uri": {redirect}, "code_verifier": {verifier}}
	_, body := o.post("/oauth/token", tokenReq)
	var toks struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Scope   string `json:"scope"`
	}
	_ = json.Unmarshal([]byte(body), &toks)
	if toks.Access == "" || toks.Scope != "write" {
		t.Fatalf("token = %s", body)
	}

	// The access token reaches tools at its tier, and nothing else.
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	s, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: o.url + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: toks.Access}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got := tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka"}}, false)
	if !strings.Contains(got, `"source":"mcp"`) {
		t.Fatalf("create_card = %s", got)
	}
	if res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "push", Arguments: map[string]any{}}); err == nil && !res.IsError {
		t.Fatal("a write grant pushed")
	}
	req, _ := http.NewRequest("GET", o.url+"/api/jobs", nil)
	req.Header.Set("Authorization", "Bearer "+toks.Access)
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 401 {
		t.Fatalf("access token on the API: %d", r.StatusCode)
	}
	// A refresh gives new tokens.
	_, body = o.post("/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {toks.Refresh},
		"client_id": {client.ClientID}})
	if !strings.Contains(body, "access_token") {
		t.Fatalf("refresh = %s", body)
	}
	// Reusing the code revokes the grant, so the token stops working.
	if resp, _ := o.post("/oauth/token", tokenReq); resp.StatusCode != 400 {
		t.Fatalf("code reuse: %d", resp.StatusCode)
	}
	if _, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_jobs", Arguments: map[string]any{}}); err == nil {
		t.Fatal("token works after its code's reuse revoked the grant")
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

func TestRedirectAllowed(t *testing.T) {
	registered := []string{"http://127.0.0.1/callback", "https://app.example/cb"}
	for uri, want := range map[string]bool{
		"http://127.0.0.1/callback":                   true,
		"http://127.0.0.1:5555/callback":              true,
		"http://127.0.0.1:5555/other":                 false,
		"http://127.0.0.1:5555/callback?x=1":          false,
		"http://localhost:5555/callback":              false,
		"https://app.example/cb":                      true,
		"https://app.example:8443/cb":                 false,
		"http://127.0.0.1:5555/callback#x":            false,
		"http://evil@127.0.0.1:5555/callback":         false,
		"http://127.0.0.1:5555@evil.example/callback": false,
		"https://127.0.0.1:5555/callback":             false,
		"http://127.0.0.1:5555/call%62ack":            false,
		"http://127.0.0.1:5555/callback?":             false,
	} {
		if got := redirectAllowed(registered, uri); got != want {
			t.Errorf("redirectAllowed(%q) = %v", uri, got)
		}
	}
}

func TestOAuthLoopbackPort(t *testing.T) {
	o := newOAuthEnv(t)
	id := o.register("http://127.0.0.1/callback")
	verifier, params := authParams(id, "http://127.0.0.1:5555/callback", "")
	code := o.approve(params, "read")
	if code == "" {
		t.Fatal("no code for a loopback redirect on an unregistered port")
	}
	o.redeem(id, code, "http://127.0.0.1:5555/callback", verifier)
}

// register registers a client that redirects to redirectURI, and returns
// its ID.
func (o *oauthEnv) register(redirectURI string) string {
	o.t.Helper()
	resp, err := http.Post(o.url+"/oauth/register", "application/json",
		strings.NewReader(`{"client_name": "ink", "redirect_uris": ["`+redirectURI+`"]}`))
	if err != nil {
		o.t.Fatal(err)
	}
	defer resp.Body.Close()
	var c struct {
		ID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&c); err != nil || c.ID == "" {
		o.t.Fatalf("register: %d, %v", resp.StatusCode, err)
	}
	return c.ID
}

// authParams returns a PKCE verifier and an authorization request from
// clientID for resource, or for no resource if it is "".
func authParams(clientID, redirectURI, resource string) (string, url.Values) {
	verifier := strings.Repeat("x", 50)
	sum := sha256.Sum256([]byte(verifier))
	p := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirectURI},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"state": {"xyz"}}
	if resource != "" {
		p.Set("resource", resource)
	}
	return verifier, p
}

// approve logs in if it must, approves params at tier, and returns the code
// sent back, or "" if none was.
func (o *oauthEnv) approve(params url.Values, tier string) string {
	o.t.Helper()
	_, page := o.get("/oauth/authorize?" + params.Encode())
	if strings.Contains(page, `name="f_password"`) {
		o.post("/oauth/login", url.Values{"provider": {itoa64(o.loginID)}, "return": {params.Encode()}, "f_password": {"letmein"}})
		_, page = o.get("/oauth/authorize?" + params.Encode())
	}
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		o.t.Fatalf("consent page = %s", page)
	}
	decision := url.Values{"csrf": {m[1]}, "tier": {tier}, "decision": {"approve"}}
	for k, v := range params {
		decision[k] = v
	}
	resp, _ := o.post("/oauth/authorize", decision)
	back, _ := url.Parse(resp.Header.Get("Location"))
	return back.Query().Get("code")
}

// redeem exchanges code for an access token and a refresh token.
func (o *oauthEnv) redeem(clientID, code, redirectURI, verifier string) (string, string) {
	o.t.Helper()
	_, body := o.post("/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}})
	var toks struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if json.Unmarshal([]byte(body), &toks) != nil || toks.Access == "" {
		o.t.Fatalf("token = %s", body)
	}
	return toks.Access, toks.Refresh
}

// bearerStatus sends method path with token and returns the status.
func (o *oauthEnv) bearerStatus(method, path, token string) int {
	o.t.Helper()
	req, _ := http.NewRequest(method, o.url+path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		o.t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestOAuthForTheAPI(t *testing.T) {
	o := newOAuthEnv(t)
	id := o.register(redirect)
	verifier, params := authParams(id, redirect, o.url+"/api")
	access, _ := o.redeem(id, o.approve(params, "read"), redirect, verifier)
	if s := o.bearerStatus("GET", "/api/jobs", access); s != http.StatusOK {
		t.Fatalf("API token on the API: %d", s)
	}
	if s := o.bearerStatus("POST", "/mcp", access); s != http.StatusUnauthorized {
		t.Fatalf("API token on /mcp: %d", s)
	}
	_, params = authParams(id, redirect, o.url+"/elsewhere")
	if resp, _ := o.get("/oauth/authorize?" + params.Encode()); !strings.Contains(resp.Header.Get("Location"), "error=invalid_target") {
		t.Fatalf("unknown resource: %s", resp.Header.Get("Location"))
	}
}

func TestOAuthRevoke(t *testing.T) {
	o := newOAuthEnv(t)
	if _, meta := o.get("/.well-known/oauth-authorization-server"); !strings.Contains(meta, `"revocation_endpoint":"`+o.url+`/oauth/revoke"`) {
		t.Fatalf("metadata = %s", meta)
	}
	id := o.register(redirect)
	verifier, params := authParams(id, redirect, o.url+"/api")
	access, refresh := o.redeem(id, o.approve(params, "read"), redirect, verifier)
	if resp, _ := o.post("/oauth/revoke", url.Values{"token": {refresh}}); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no client_id: %d", resp.StatusCode)
	}
	if resp, _ := o.post("/oauth/revoke", url.Values{"token": {refresh}, "client_id": {id}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d", resp.StatusCode)
	}
	if s := o.bearerStatus("GET", "/api/jobs", access); s != http.StatusUnauthorized {
		t.Fatalf("access token after revoking its grant: %d", s)
	}
	if resp, _ := o.post("/oauth/revoke", url.Values{"token": {"unknown"}, "client_id": {id}}); resp.StatusCode != http.StatusOK {
		t.Fatalf("unknown token: %d", resp.StatusCode)
	}
}
