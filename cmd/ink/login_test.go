package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// browse replaces openBrowser with one that visits the page, then fetches
// what visit returns.
func browse(t *testing.T, visit func(page *url.URL) string) {
	old := openBrowser
	openBrowser = func(page string) error {
		u, err := url.Parse(page)
		if err != nil {
			return err
		}
		go func() {
			if resp, err := http.Get(visit(u)); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { openBrowser = old })
}

func TestLogin(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	browse(t, func(page *url.URL) string { return page.String() })
	out, err := runInk(t, "login", f.URL+"/", "--profile", "home")
	if err != nil || !strings.Contains(out, "Logged in to "+f.URL+" with the write tier as profile home") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	// Logging in again revokes the login it replaces.
	if _, _, revoked := f.snapshot(); len(revoked) != 1 || revoked[0] != "inkc_1 r0" {
		t.Fatalf("revoked %q", revoked)
	}
	c, err := loadConfig()
	if err != nil || c.Default != "home" || c.Profiles["home"] != (profile{URL: f.URL, ClientID: "inkc_1"}) {
		t.Fatalf("config = %+v, %v", c, err)
	}
	f.mu.Lock()
	redirect, resource := f.redirect, f.resource
	f.mu.Unlock()
	if redirect != "http://127.0.0.1/callback" || resource != f.URL+"/api" {
		t.Fatalf("registered %q, asked for %q", redirect, resource)
	}
	if _, err := runInk(t, "call", "GET", "/jobs"); err != nil {
		t.Fatal(err)
	}
	if _, calls, _ := f.snapshot(); len(calls) != 1 || calls[0] != "a1" {
		t.Fatalf("API calls = %q", calls)
	}
}

// wrap puts h in front of f's handler; h returns true if it answered.
func wrap(f *fakeAuth, h func(w http.ResponseWriter, r *http.Request) bool) {
	inner := f.Config.Handler
	f.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h(w, r) {
			inner.ServeHTTP(w, r)
		}
	})
}

// neverOpen fails the test if ink opens a browser.
func neverOpen(t *testing.T) {
	old := openBrowser
	openBrowser = func(page string) error { t.Errorf("opened %q", page); return nil }
	t.Cleanup(func() { openBrowser = old })
}

func TestLoginChecksTheIssuer(t *testing.T) {
	for _, issuer := range []string{"ms-msdt:/id", "http://other.example"} {
		freshConfig(t)
		neverOpen(t)
		f := newFakeAuth(t)
		wrap(f, func(w http.ResponseWriter, r *http.Request) bool {
			if r.URL.Path != "/.well-known/oauth-authorization-server" {
				return false
			}
			fmt.Fprintf(w, `{"issuer": %q, "revocation_endpoint": "x"}`, issuer)
			return true
		})
		_, err := runInk(t, "login", f.URL)
		if err == nil || !strings.Contains(err.Error(), "address") {
			t.Fatalf("issuer %q: err = %v", issuer, err)
		}
		if strings.HasPrefix(issuer, "http") != strings.Contains(err.Error(), "ink login "+issuer) {
			t.Fatalf("issuer %q: err = %v", issuer, err)
		}
		if c, _ := loadConfig(); len(c.Profiles) != 0 {
			t.Fatalf("saved %+v", c.Profiles)
		}
	}
}

func TestLoginNeedsAWebAddress(t *testing.T) {
	freshConfig(t)
	neverOpen(t)
	for _, server := range []string{"ftp://x", "example.com", "https://"} {
		if _, err := runInk(t, "login", server); err == nil || !strings.Contains(err.Error(), "isn't an http or https address") {
			t.Fatalf("%q: err = %v", server, err)
		}
	}
}

func TestLoginSavesBeforeRevoking(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	var saved string
	wrap(f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/oauth/revoke" {
			c, _ := loadCreds("home")
			saved = c.Refresh
		}
		return false
	})
	browse(t, func(page *url.URL) string { return page.String() })
	if _, err := runInk(t, "login", f.URL, "--profile", "home"); err != nil {
		t.Fatal(err)
	}
	if saved != "r1" {
		t.Fatalf("the old login was revoked while %q was saved", saved)
	}
}

func TestLoginIgnoresAForgedState(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	var forged int
	old := openBrowser
	openBrowser = func(page string) error {
		u, _ := url.Parse(page)
		go func() {
			resp, err := http.Get(u.Query().Get("redirect_uri") + "?" + url.Values{"code": {"c1"}, "state": {"forged"}, "iss": {f.URL}}.Encode())
			if err == nil {
				forged = resp.StatusCode
				resp.Body.Close()
			}
			if resp, err := http.Get(page); err == nil {
				resp.Body.Close()
			}
		}()
		return nil
	}
	t.Cleanup(func() { openBrowser = old })
	if _, err := runInk(t, "login", f.URL); err != nil {
		t.Fatalf("err = %v", err)
	}
	if forged != http.StatusBadRequest {
		t.Fatalf("forged callback got %d", forged)
	}
	if c, _ := loadConfig(); c.Profiles["default"].URL != f.URL {
		t.Fatalf("saved %+v", c.Profiles)
	}
}

func TestLoginGivesUpWaiting(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	browse(t, func(page *url.URL) string { return f.URL + "/nowhere" })
	old := loginWait
	loginWait = 100 * time.Millisecond
	t.Cleanup(func() { loginWait = old })
	if _, err := runInk(t, "login", f.URL); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoginRefusesAnotherIssuer(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	browse(t, func(page *url.URL) string {
		return page.Query().Get("redirect_uri") + "?" + url.Values{"code": {"c1"}, "state": {page.Query().Get("state")}, "iss": {"http://other.example"}}.Encode()
	})
	if _, err := runInk(t, "login", f.URL); err == nil || !strings.Contains(err.Error(), "other.example") {
		t.Fatalf("err = %v", err)
	}
	if c, _ := loadConfig(); len(c.Profiles) != 0 {
		t.Fatalf("saved %+v", c.Profiles)
	}
}

func TestLoginRefusesAnEmptyToken(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	wrap(f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/oauth/token" {
			return false
		}
		fmt.Fprint(w, `{"access_token": "", "refresh_token": "r1", "expires_in": 3600}`)
		return true
	})
	browse(t, func(page *url.URL) string { return page.String() })
	if _, err := runInk(t, "login", f.URL); err == nil || !strings.Contains(err.Error(), "access token") {
		t.Fatalf("err = %v", err)
	}
	if c, _ := loadConfig(); len(c.Profiles) != 0 {
		t.Fatalf("saved %+v", c.Profiles)
	}
}

func TestLoginNeedsOAuth(t *testing.T) {
	freshConfig(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if _, err := runInk(t, "login", srv.URL); err == nil || !strings.Contains(err.Error(), "INNKEEPER_PUBLIC_URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestLogout(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	out, err := runInk(t, "logout")
	if err != nil || !strings.Contains(out, "Logged out of "+f.URL) {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if _, _, revoked := f.snapshot(); len(revoked) != 1 || revoked[0] != "inkc_1 r0" {
		t.Fatalf("revoked %q", revoked)
	}
	if c, _ := loadConfig(); len(c.Profiles) != 0 || c.Default != "" {
		t.Fatalf("config = %+v", c)
	}
	if _, err := loadCreds("home"); err == nil {
		t.Fatal("creds outlived logout")
	}
	if _, err := runInk(t, "logout"); err == nil {
		t.Fatal("logged out with no profile")
	}
}

func TestLogoutWhenTheServerFails(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	f.setRevokeFails(true)
	if _, err := runInk(t, "logout"); err == nil || !strings.Contains(err.Error(), "revoke-grant") {
		t.Fatalf("err = %v", err)
	}
	if c, _ := loadConfig(); len(c.Profiles) != 0 {
		t.Fatalf("kept %+v", c.Profiles)
	}
}

func TestLogoutWithoutItsLogin(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	loggedIn(t, f)
	if err := deleteCreds("home"); err != nil {
		t.Fatal(err)
	}
	_, err := runInk(t, "logout")
	if err == nil || !strings.Contains(err.Error(), "revoke-grant") || !strings.Contains(err.Error(), "couldn't read") {
		t.Fatalf("err = %v", err)
	}
	if c, _ := loadConfig(); len(c.Profiles) != 0 {
		t.Fatalf("kept %+v", c.Profiles)
	}
}
