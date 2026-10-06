package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIIsUnderPrefix(t *testing.T) {
	e := newEnv(t)
	var spec struct {
		Servers []struct{ URL string } `json:"servers"`
		Paths   map[string]any         `json:"paths"`
	}
	r := e.send("GET", "/openapi.json", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /api/openapi.json: %s", r)
	}
	if err := json.Unmarshal(r.Body, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Servers) != 1 || spec.Servers[0].URL != "/api" {
		t.Fatalf("servers = %+v", spec.Servers)
	}
	if _, ok := spec.Paths["/cards"]; !ok {
		t.Fatal("spec paths should not include the /api prefix")
	}
	// The docs page must load the spec from under the prefix.
	if docs := e.send("GET", "/docs", nil); !strings.Contains(string(docs.Body), "/api/openapi") {
		t.Fatalf("docs page doesn't reference /api/openapi: %s", docs.Body)
	}
	// The unprefixed path is no longer the API.
	resp, err := client.Get(e.srv.URL + "/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /openapi.json = %d, want 404 with no UI", resp.StatusCode)
	}
}

func TestSpecNeedsNoServices(t *testing.T) {
	b, err := Spec()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Servers []struct{ URL string } `json:"servers"`
		Paths   map[string]any         `json:"paths"`
	}
	if err := json.Unmarshal(b, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Servers[0].URL != "/api" || spec.Paths["/search"] == nil {
		t.Fatalf("spec = %s", b[:min(len(b), 300)])
	}
}

func TestUIServesNonAPIPaths(t *testing.T) {
	e := newEnv(t)
	e.s.UI = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ui")) })
	srv := httptest.NewServer(e.s.Handler())
	defer srv.Close()
	for path, want := range map[string]string{"/cards/12": "ui", "/": "ui", "/login": "ui"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != want {
			t.Errorf("GET %s = %q, want %q", path, body, want)
		}
	}
	// The API and the login callback still win.
	resp, err := client.Get(srv.URL + "/api/cards/12")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/cards/12 = %d, want 401", resp.StatusCode)
	}
	// The UI answers GET only.
	resp, err = client.Post(srv.URL+"/cards/12", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /cards/12 = %d, want 405", resp.StatusCode)
	}
	// Other routes aren't swallowed by the UI.
	for _, path := range []string{"/auth/1/callback", "/mcp"} {
		resp, err := client.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) == "ui" {
			t.Errorf("GET %s fell through to the UI", path)
		}
	}
}
