package api

import (
	"encoding/json"
	"net/http"
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
