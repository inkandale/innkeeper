package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/auth"
	"github.com/inkandale/innkeeper/internal/plugins"
)

// TestEveryOperationNeedsAuth reads the OpenAPI spec, so it also covers
// operations added later.
func TestEveryOperationNeedsAuth(t *testing.T) {
	e := newEnv(t)
	var spec struct {
		Paths map[string]map[string]struct {
			OperationID string                 `json:"operationId"`
			Security    *[]map[string][]string `json:"security"`
		} `json:"paths"`
	}
	r := e.send("GET", "/openapi.json", nil)
	if err := json.Unmarshal(r.Body, &spec); err != nil {
		t.Fatalf("openapi.json: %s: %v", r, err)
	}
	session := e.login("letmein").cookie(t, sessionCookie)
	cookie := sessionCookie + "=" + session.Value
	param := regexp.MustCompile(`\{[^}]+\}`)
	checked := 0
	var public []string
	for path, ops := range spec.Paths {
		for method, op := range ops {
			if op.Security != nil && len(*op.Security) == 0 {
				public = append(public, op.OperationID)
				continue
			}
			checked++
			method = strings.ToUpper(method)
			url := param.ReplaceAllString(path, "1")
			if r := e.send(method, url, nil); r.Status != http.StatusUnauthorized {
				t.Errorf("%s %s without credentials: got %d, want 401", method, path, r.Status)
			}
			if method == http.MethodGet || method == http.MethodHead {
				continue
			}
			if r := e.send(method, url, nil, "Cookie", cookie, "Sec-Fetch-Site", "cross-site"); r.Status != http.StatusUnauthorized {
				t.Errorf("%s %s cross-site with session: got %d, want 401", method, path, r.Status)
			}
		}
	}
	slices.Sort(public)
	want := []string{"begin-login", "claim-server", "complete-login", "get-setup", "health", "list-login-providers", "logout"}
	if checked == 0 || !slices.Equal(public, want) {
		t.Fatalf("checked %d operations; public = %v, want %v", checked, public, want)
	}
}

func TestBadCredentials(t *testing.T) {
	e := newEnv(t)
	for _, h := range [][]string{
		{"Authorization", "Bearer ink_wrong"},
		{"Authorization", "Basic " + e.token},
		{"Cookie", sessionCookie + "=nope"},
	} {
		if r := e.send("GET", "/tokens", nil, h...); r.Status != http.StatusUnauthorized {
			t.Errorf("%v: got %s, want 401", h, r)
		}
	}
}

func TestTokenRoutes(t *testing.T) {
	e := newEnv(t)
	var created struct {
		ID     int64  `json:"id"`
		Secret string `json:"secret"`
	}
	e.call("POST", "/tokens", map[string]string{"name": "mcp"}, 201, &created)
	if r := e.send("GET", "/tokens", nil, "Authorization", "Bearer "+created.Secret); r.Status != 200 {
		t.Fatalf("new token: %s", r)
	}
	var list []auth.Token
	e.call("GET", "/tokens", nil, 200, &list)
	if len(list) != 2 || list[1].Name != "mcp" {
		t.Fatalf("tokens = %+v", list)
	}
	e.call("DELETE", fmt.Sprintf("/tokens/%d", created.ID), nil, 204, nil)
	if r := e.send("GET", "/tokens", nil, "Authorization", "Bearer "+created.Secret); r.Status != 401 {
		t.Fatalf("revoked token: %s", r)
	}
	e.call("DELETE", fmt.Sprintf("/tokens/%d", created.ID), nil, 404, nil)
	e.call("POST", "/tokens", map[string]string{"name": ""}, 422, nil)
}

// begin starts a login with the fake provider and returns the state cookie
// as a Cookie header value.
func (e *testEnv) begin() string {
	e.t.Helper()
	r := e.send("POST", fmt.Sprintf("/auth/%d/begin", e.loginID), nil)
	if r.Status != 200 || !strings.Contains(string(r.Body), `"password"`) {
		e.t.Fatalf("begin: %s", r)
	}
	c := r.cookie(e.t, stateCookie)
	return c.Name + "=" + c.Value
}

func (e *testEnv) login(password string) result {
	e.t.Helper()
	return e.send("POST", fmt.Sprintf("/auth/%d/complete", e.loginID),
		map[string]any{"params": map[string]string{"password": password}}, "Cookie", e.begin())
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	var providers []provider
	if r := e.send("GET", "/auth/providers", nil); r.Status != 200 || json.Unmarshal(r.Body, &providers) != nil {
		t.Fatalf("providers: %s", r)
	}
	// The importer runs the same plugin but names no owner, so no one can log in with it.
	if len(providers) != 1 || providers[0].ID != e.loginID || providers[0].Name != "Login" {
		t.Fatalf("providers = %+v", providers)
	}

	for _, pw := range []string{"wrong", "stranger"} {
		if r := e.login(pw); r.Status != 401 {
			t.Errorf("password %q: got %s, want 401", pw, r)
		}
	}
	ok := e.login("letmein")
	if ok.Status != 200 {
		t.Fatalf("login: %s", ok)
	}
	session := ok.cookie(t, sessionCookie)
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie = %+v", session)
	}
	cookie := sessionCookie + "=" + session.Value
	if r := e.send("GET", "/tokens", nil, "Cookie", cookie); r.Status != 200 {
		t.Fatalf("GET with session: %s", r)
	}
	body := map[string]string{"name": "x"}
	if r := e.send("POST", "/tokens", body, "Cookie", cookie, "Sec-Fetch-Site", "cross-site"); r.Status != 401 {
		t.Fatalf("cross-site POST with session: %s", r)
	}
	if r := e.send("POST", "/tokens", body, "Cookie", cookie, "Sec-Fetch-Site", "same-origin"); r.Status != 201 {
		t.Fatalf("same-origin POST with session: %s", r)
	}
	if r := e.send("POST", "/auth/logout", nil, "Cookie", cookie); r.Status != 204 {
		t.Fatalf("logout: %s", r)
	}
	if r := e.send("GET", "/tokens", nil, "Cookie", cookie); r.Status != 401 {
		t.Fatalf("GET after logout: %s", r)
	}
}

func TestLoginCallback(t *testing.T) {
	e := newEnv(t)
	r := e.send("GET", fmt.Sprintf("/auth/%d/callback?password=letmein", e.loginID), nil, "Cookie", e.begin())
	if r.Status != http.StatusSeeOther || r.Header.Get("Location") != "/" {
		t.Fatalf("callback: %s %v", r, r.Header)
	}
	session := r.cookie(t, sessionCookie)
	if r := e.send("GET", "/tokens", nil, "Cookie", sessionCookie+"="+session.Value); r.Status != 200 {
		t.Fatalf("GET with session: %s", r)
	}
}

func TestLoginNeedsState(t *testing.T) {
	e := newEnv(t)
	r := e.send("POST", fmt.Sprintf("/auth/%d/complete", e.loginID), map[string]any{"params": map[string]string{"password": "letmein"}})
	if r.Status != 401 {
		t.Fatalf("complete without begin: %s", r)
	}
}

func TestLoginOnlyThroughAuthPlugins(t *testing.T) {
	e := newEnv(t)
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "vault", Name: new("Vault")})
	if err != nil {
		t.Fatal(err)
	}
	if r := e.send("POST", fmt.Sprintf("/auth/%d/begin", id), nil); r.Status != 404 {
		t.Fatalf("begin through a push plugin: %s", r)
	}
}

func TestCrossSiteLogout(t *testing.T) {
	e := newEnv(t)
	cookie := sessionCookie + "=" + e.login("letmein").cookie(t, sessionCookie).Value
	r := e.send("POST", "/auth/logout", nil, "Cookie", cookie, "Sec-Fetch-Site", "cross-site")
	if r.Status != http.StatusForbidden || len(r.Header.Values("Set-Cookie")) != 0 {
		t.Fatalf("cross-site logout: %s %v", r, r.Header.Values("Set-Cookie"))
	}
	if r := e.send("GET", "/tokens", nil, "Cookie", cookie); r.Status != 200 {
		t.Fatalf("GET after cross-site logout: %s", r)
	}
}

func TestBeginLoginClearsOAuthReturn(t *testing.T) {
	e := newEnv(t)
	r := e.send("POST", fmt.Sprintf("/auth/%d/begin", e.loginID), nil)
	if c := r.cookie(t, returnCookie); c.Value != "" || c.MaxAge >= 0 {
		t.Fatalf("return cookie not expired: %+v", c)
	}
}

func TestBrokenLoginProviderHidesError(t *testing.T) {
	e := newEnv(t)
	e.markBroken(e.loginID, "fork/exec /secret/path")
	r := e.send("POST", fmt.Sprintf("/auth/%d/begin", e.loginID), nil)
	if r.Status != http.StatusServiceUnavailable || strings.Contains(string(r.Body), "/secret/path") {
		t.Fatalf("begin with broken provider: %s", r)
	}
}

func TestDisabledLoginProviderIsNotListed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	owner, disabled := "owner", true
	second, err := e.s.Registry.CreateInstance(ctx, plugins.NewInstance{Plugin: "fake", Name: new("Second"), OwnerSubject: &owner})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Registry.UpdateInstance(ctx, e.loginID, plugins.InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	var providers []provider
	if r := e.send("GET", "/auth/providers", nil); r.Status != 200 || json.Unmarshal(r.Body, &providers) != nil {
		t.Fatalf("providers: %s", r)
	}
	if len(providers) != 1 || providers[0].ID != second {
		t.Fatalf("providers = %+v, want only the enabled login %d", providers, second)
	}
}
