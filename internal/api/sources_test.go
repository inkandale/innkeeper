package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestSourcesAPI(t *testing.T) {
	for _, plural := range []string{"cards", "lorebooks"} {
		t.Run(plural, func(t *testing.T) {
			e := newEnv(t)
			var ent entityResult
			e.call("POST", "/"+plural, map[string]any{"data": json.RawMessage(
				`{"name": "A", "extensions": {"chub": {"id": 7, "full_path": "u/a"}}}`)}, 201, &ent)
			path := fmt.Sprintf("/%s/%d/sources", plural, ent.ID)
			dir := map[string]string{"cards": "characters", "lorebooks": "lorebooks"}[plural]

			// Only import detects, so a created item starts with none.
			var list []library.Source
			e.call("GET", path, nil, 200, &list)
			if len(list) != 0 {
				t.Fatalf("sources = %+v", list)
			}
			var added library.Source
			e.call("PUT", path, map[string]any{"url": "https://www.chub.ai/" + dir + "/u/a?x=1"}, 200, &added)
			if added.Kind != "chub" || added.Ref != "u/a" || added.LinkedBy != "owner" {
				t.Fatalf("added = %+v", added)
			}
			var byRef library.Source
			e.call("PUT", path, map[string]any{"kind": "chub", "ref": "u/b", "external_id": "3"}, 200, &byRef)
			wantURL := "https://chub.ai/characters/u/b"
			if plural == "lorebooks" {
				wantURL = "https://chub.ai/lorebooks/u/b"
			}
			if byRef.URL != wantURL || byRef.ExternalID != "3" {
				t.Fatalf("byRef = %+v", byRef)
			}
			e.call("GET", path, nil, 200, &list)
			if len(list) != 2 {
				t.Fatalf("sources = %+v", list)
			}

			// A pasted URL is stored with its entity type's directory.
			other := map[string]string{"cards": "lorebooks", "lorebooks": "characters"}[plural]
			var swapped library.Source
			e.call("PUT", path, map[string]any{"url": "https://chub.ai/" + other + "/u/c"}, 200, &swapped)
			if swapped.URL != "https://chub.ai/"+dir+"/u/c" {
				t.Fatalf("swapped = %+v", swapped)
			}
			e.call("PUT", path, map[string]any{"url": "https://chub.ai/" + dir + "/u/a", "kind": "chub", "ref": "u/a"}, 422, nil)
			e.call("DELETE", fmt.Sprintf("%s/%d", path, swapped.ID), nil, 204, nil)

			for _, bad := range []map[string]any{
				{"url": "https://example.com/x"}, {"url": "not a url"},
				{"kind": "chub", "ref": "../b"}, {"kind": "chub", "ref": "onlyone"}, {"kind": "nope", "ref": "a/b"}, {},
			} {
				e.call("PUT", path, bad, 422, nil)
			}

			e.call("DELETE", fmt.Sprintf("%s/%d", path, added.ID), nil, 204, nil)
			e.call("DELETE", fmt.Sprintf("%s/%d", path, added.ID), nil, 404, nil)
			e.call("GET", path, nil, 200, &list)
			if len(list) != 1 || list[0].Ref != "u/b" {
				t.Fatalf("after remove: %+v", list)
			}
			e.call("GET", fmt.Sprintf("/%s/999/sources", plural), nil, 404, nil)
			e.call("PUT", fmt.Sprintf("/%s/999/sources", plural), map[string]any{"url": "https://chub.ai/characters/a/b"}, 404, nil)
			e.call("DELETE", fmt.Sprintf("/%s/999/sources/1", plural), nil, 404, nil)
		})
	}
}
