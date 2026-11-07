package api

import (
	"fmt"
	"strings"
	"testing"
)

func TestSavedSearchesCountTheirMatchesInALibrary(t *testing.T) {
	e := newEnrichEnv(t)
	low := e.card("Low", "tiny")
	high := e.card("High", strings.Repeat("x", 90))
	e.evaluate(e.instID, low, high)

	var s savedSearchItem
	e.call("POST", "/saved-searches", map[string]any{"name": "Weak", "filter": map[string]any{"enrichment": "quality", "lte": 30}, "sort": "name"}, 201, &s)
	e.call("POST", "/saved-searches", map[string]any{"name": "Weak", "filter": map[string]any{"tag": "x"}}, 409, nil)
	e.call("POST", "/saved-searches", map[string]any{"name": "Bad", "filter": map[string]any{"tag": "x", "name": "y"}}, 400, nil)
	e.call("POST", "/saved-searches", map[string]any{"name": "Sorted", "filter": map[string]any{"tag": "x"}, "sort": "sideways"}, 422, nil)

	var list []savedSearchItem
	e.call("GET", "/saved-searches?library=1", nil, 200, &list)
	if len(list) != 1 || list[0].Name != "Weak" || list[0].Cards == nil || *list[0].Cards != 1 || list[0].Sort != "name" {
		t.Fatalf("list = %+v", list)
	}

	path := fmt.Sprintf("/saved-searches/%d", s.ID)
	e.call("PATCH", path, map[string]any{"name": "Weakest"}, 200, &s)
	if s.Name != "Weakest" || s.Filter.Enrichment == nil {
		t.Fatalf("patched %+v", s)
	}
	e.call("DELETE", path, nil, 204, nil)
	e.call("GET", path, nil, 404, nil)
}
