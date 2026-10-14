package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
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

func TestAddSourceThroughSitesPlugin(t *testing.T) {
	e := newEnv(t)
	sites := *e.manifests["target"]
	sites.Name, sites.Capabilities = "sites", []string{"sites"}
	sites.Command = []string{sites.Command[0], "-caps=sites", "-name=sites"}
	e.manifests["sites"] = &sites
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "sites", Name: "Sites"})
	if err != nil {
		t.Fatal(err)
	}
	var ent entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "A", "entries": []}`)}, 201, &ent)
	path := fmt.Sprintf("/lorebooks/%d/sources", ent.ID)

	var added library.Source
	e.call("PUT", path, map[string]any{"url": "https://fake.example/u/a"}, 200, &added)
	if added.Kind != "fake" || added.Ref != "u/a" || added.URL != "https://fake.example/u/a" || added.LinkedBy != "owner" {
		t.Fatalf("added = %+v", added)
	}
	for body, want := range map[string]string{
		`{"url": "https://chub.ai/characters/u/a"}`: "no installed plugin recognizes that URL",
		`{"kind": "chub", "ref": "u/a"}`:            `no installed plugin knows source kind \"chub\"`,
		`{"kind": "fake", "ref": "nope"}`:           "a fake ref is",
	} {
		if r := e.call("PUT", path, json.RawMessage(body), 422, nil); !strings.Contains(string(r.Body), want) {
			t.Errorf("%s: %s, want %q", body, r.Body, want)
		}
	}

	if err := e.s.Registry.UpdateInstance(context.Background(), id, plugins.InstanceEdit{Config: map[string]any{"sites_bad": true}}); err != nil {
		t.Fatal(err)
	}
	if r := e.call("PUT", path, map[string]any{"url": "https://fake.example/u/b"}, 502, nil); !strings.Contains(string(r.Body), "sites") {
		t.Fatalf("invalid source: %s, want a 502 naming the plugin", r.Body)
	}
}
