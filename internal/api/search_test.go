package api

import (
	"encoding/json"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
)

func TestSearchRoute(t *testing.T) {
	e := newEnv(t)
	a := e.createCard(`{"name": "Brakka", "description": "A dwarf blacksmith.", "tags": ["fantasy"]}`)
	e.createCard(`{"name": "Carrow", "description": "A sailor."}`)

	var res library.Results
	e.call("POST", "/search", map[string]any{"text": "blacksmith", "filter": map[string]any{"tag": "fantasy"},
		"include": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != a.ID || res.Hits[0].Fields["name"] != "Brakka" || res.Hits[0].Tags[0] != "fantasy" {
		t.Fatalf("results = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/cards/search", map[string]any{"text": "blacksmith", "filter": map[string]any{"tag": "fantasy"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].ID != a.ID {
		t.Fatalf("/cards/search = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/search", map[string]any{"page": map[string]any{"size": 1, "cursor": nil}}, 200, &res)
	if len(res.Hits) != 1 || res.NextCursor == nil {
		t.Fatalf("first page = %+v", res)
	}

	res = library.Results{}
	e.call("POST", "/search", map[string]any{"sort": "name", "page": map[string]any{"size": 1, "number": 2},
		"facets": []string{"tags"}}, 200, &res)
	if len(res.Hits) != 1 || res.Hits[0].Fields["name"] != "Carrow" || res.Total != 2 ||
		res.Facets == nil || len(res.Facets.Tags) != 1 {
		t.Fatalf("numbered page = %+v", res)
	}
	e.call("POST", "/search", map[string]any{"sort": "size"}, 422, nil)

	e.call("POST", "/search", map[string]any{"semantic": true}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"tag": "a", "archived": true}}, 400, nil)
	e.call("POST", "/search", map[string]any{"filter": map[string]any{"bogus": 1}}, 422, nil)
}

func TestSearchLorebooksRoute(t *testing.T) {
	e := newEnv(t)
	var forge, sea entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": [{"keys": ["anvil"], "content": "Heavy."}]}`)}, 201, &forge)
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Sea", "entries": []}`)}, 201, &sea)

	var res struct {
		Total int `json:"total"`
		Hits  []struct {
			ID      int64 `json:"id"`
			Entries int   `json:"entries"`
		} `json:"hits"`
	}
	e.call("POST", "/lorebooks/search", map[string]any{"text": "anvil"}, 200, &res)
	if res.Total != 1 || res.Hits[0].ID != forge.ID || res.Hits[0].Entries != 1 {
		t.Fatalf("search = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{}, 200, &res)
	if res.Total != 2 {
		t.Fatalf("all = %+v", res)
	}
	e.call("POST", "/lorebooks/search", map[string]any{"facets": []string{"tags"}}, 400, nil)
}
