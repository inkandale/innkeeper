package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuth stands in for an Innkeeper server with one OAuth client,
// inkc_1. It issues access tokens a1, a2, ... and refresh tokens r1, r2, ...
type fakeAuth struct {
	*httptest.Server
	mu          sync.Mutex
	n           int
	access      string
	refresh     string
	challenge   string
	redirect    string   // the redirect URI registered
	resource    string   // the resource authorized
	apiCalls    []string // the token on each API call
	revoked     []string // "client_id token" of each revocation
	revokeFails bool
}

func newFakeAuth(t *testing.T) *fakeAuth {
	f := &fakeAuth{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"issuer": %q, "revocation_endpoint": %q}`, f.URL, f.URL+"/oauth/revoke")
	})
	mux.HandleFunc("POST /oauth/register", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RedirectURIs []string `json:"redirect_uris"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.mu.Lock()
		f.redirect = strings.Join(in.RedirectURIs, " ")
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"client_id": "inkc_1"}`)
	})
	mux.HandleFunc("GET /oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f.mu.Lock()
		f.challenge, f.resource = q.Get("code_challenge"), q.Get("resource")
		f.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		back.RawQuery = url.Values{"code": {"c1"}, "state": {q.Get("state")}, "iss": {f.URL}}.Encode()
		http.Redirect(w, r, back.String(), http.StatusSeeOther)
	})
	mux.HandleFunc("POST /oauth/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		ok := r.PostForm.Get("client_id") == "inkc_1"
		switch r.PostForm.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
			ok = ok && r.PostForm.Get("code") == "c1" && base64.RawURLEncoding.EncodeToString(sum[:]) == f.challenge
		case "refresh_token":
			ok = ok && f.refresh != "" && r.PostForm.Get("refresh_token") == f.refresh
		default:
			ok = false
		}
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error": "invalid_grant"}`)
			return
		}
		f.n++
		f.access, f.refresh = fmt.Sprintf("a%d", f.n), fmt.Sprintf("r%d", f.n)
		fmt.Fprintf(w, `{"access_token": %q, "refresh_token": %q, "expires_in": 3600, "scope": "write"}`, f.access, f.refresh)
	})
	mux.HandleFunc("POST /oauth/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.revokeFails {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		f.revoked = append(f.revoked, r.PostForm.Get("client_id")+" "+r.PostForm.Get("token"))
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if tok != f.access {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.apiCalls = append(f.apiCalls, tok)
		fmt.Fprint(w, `[]`)
	})
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// snapshot returns the refreshes so far, the API calls, and the revocations.
func (f *fakeAuth) snapshot() (int, []string, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.n, append([]string(nil), f.apiCalls...), append([]string(nil), f.revoked...)
}

// loggedIn saves profile home for f as the default, with an access token
// that has expired and refresh token r0.
func loggedIn(t *testing.T, f *fakeAuth) {
	f.mu.Lock()
	f.refresh = "r0"
	f.mu.Unlock()
	c := config{Default: "home", Profiles: map[string]profile{"home": {URL: f.URL, ClientID: "inkc_1"}}}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if _, err := saveCreds("home", creds{Access: "a0", Expires: time.Now(), Refresh: "r0"}); err != nil {
		t.Fatal(err)
	}
}

func TestProfileRefreshesItsToken(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	for range 2 {
		if _, err := runInk(t, "call", "GET", "/jobs"); err != nil {
			t.Fatal(err)
		}
	}
	// The first call refreshed; the second used the new access token.
	if n, calls, _ := f.snapshot(); n != 1 || strings.Join(calls, " ") != "a1 a1" {
		t.Fatalf("refreshes %d, API calls %q", n, calls)
	}
	if c, err := loadCreds("home"); err != nil || c.Refresh != "r1" {
		t.Fatalf("creds = %+v, %v", c, err)
	}

	// A refused refresh means logging in again.
	if _, err := saveCreds("home", creds{Access: "a0", Expires: time.Now(), Refresh: "gone"}); err != nil {
		t.Fatal(err)
	}
	if _, err := runInk(t, "call", "GET", "/jobs"); err == nil || !strings.Contains(err.Error(), "ink login --profile home "+f.URL) {
		t.Fatalf("err = %v", err)
	}
	if _, err := runInk(t, "call", "GET", "/jobs", "--profile", "work"); err == nil || !strings.Contains(err.Error(), `no profile "work"`) {
		t.Fatalf("unknown profile: %v", err)
	}
}

func TestEnvironmentOverridesProfile(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	got := stubAPI(t, map[string]string{"/jobs": "[]"})
	if _, err := runInk(t, "call", "GET", "/jobs"); err != nil {
		t.Fatal(err)
	}
	if n, _, _ := f.snapshot(); n != 0 || len(*got) != 1 {
		t.Fatalf("refreshes %d, stub calls %q", n, *got)
	}
}

func TestProfiles(t *testing.T) {
	freshConfig(t)
	c := config{Default: "home", Profiles: map[string]profile{
		"home": {URL: "https://home.example"}, "work": {URL: "https://work.example"}}}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	if out, err := runInk(t, "profiles"); err != nil || !strings.Contains(out, "home  https://home.example  yes") {
		t.Fatalf("out = %q, %v", out, err)
	}
	if _, err := runInk(t, "profiles", "use", "work"); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadConfig(); c.Default != "work" {
		t.Fatalf("default = %q", c.Default)
	}
	if _, err := runInk(t, "profiles", "use", "nope"); err == nil {
		t.Fatal("used an unknown profile")
	}
}
