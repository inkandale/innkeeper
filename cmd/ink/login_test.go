package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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

func TestLoginRefusesTheWrongState(t *testing.T) {
	freshConfig(t)
	f := newFakeAuth(t)
	// The browser comes back to ink without visiting the server.
	browse(t, func(page *url.URL) string {
		return page.Query().Get("redirect_uri") + "?" + url.Values{"code": {"c1"}, "state": {"forged"}, "iss": {f.URL}}.Encode()
	})
	if _, err := runInk(t, "login", f.URL); err == nil || !strings.Contains(err.Error(), "state") {
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
