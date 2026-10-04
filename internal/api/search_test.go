package api

import (
	"fmt"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestSearchRoute(t *testing.T) {
	e := newEnv(t)
	a := e.createCard(`{"name": "Brakka", "description": "A dwarf blacksmith."}`)
	e.createCard(`{"name": "Carrow", "description": "A sailor."}`)
	e.call("PUT", fmt.Sprintf("/cards/%d/tags", a.ID), map[string]any{"tags": []string{"fantasy"}}, 200, nil)

	var res library.Results
	e.call("POST", "/search", map[string]any{"text": "blacksmith", "filter": map[string]any{"tag": "fantasy"},
		"include": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != a.ID || res.Hits[0].Fields["name"] != "Brakka" || res.Hits[0].Tags[0] != "fantasy" {
		t.Fatalf("results = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/search", map[string]any{"page": map[string]any{"size": 1, "cursor": nil}}, 200, &res)
	if len(res.Hits) != 1 || res.NextCursor == nil {
		t.Fatalf("first page = %+v", res)
	}

	e.call("POST", "/search", map[string]any{"semantic": true}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"tag": "a", "archived": true}}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"bogus": 1}}, 422, nil)
}
