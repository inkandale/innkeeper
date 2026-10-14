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

// sitesPlugin installs the fake sites plugin, which knows https://fake.example/{a}/{b}.
func (e *testEnv) sitesPlugin() {
	sites := *e.manifests["target"]
	sites.Name, sites.Capabilities = "sites", []string{"sites"}
	sites.Command = []string{sites.Command[0], "-caps=sites", "-name=sites"}
	e.manifests["sites"] = &sites
}

func (e *testEnv) addSites() int64 {
	e.t.Helper()
	e.sitesPlugin()
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "sites", Name: "Sites"})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestSourcesAPI(t *testing.T) {
	for _, plural := range []string{"cards", "lorebooks"} {
		t.Run(plural, func(t *testing.T) {
			e := newEnv(t)
			var ent entityResult
			e.call("POST", "/"+plural, map[string]any{"data": json.RawMessage(
				`{"name": "A", "source": ["https://fake.example/u/a"]}`)}, 201, &ent)
			path := fmt.Sprintf("/%s/%d/sources", plural, ent.ID)

			if r := e.call("PUT", path, map[string]any{"url": "https://fake.example/u/a"}, 422, nil); !strings.Contains(string(r.Body), "no installed plugin recognizes that URL") {
				t.Fatalf("without a sites plugin: %s", r.Body)
			}
			e.addSites()

			// Only import detects, so a created item starts with none.
			var list []library.Source
			e.call("GET", path, nil, 200, &list)
			if len(list) != 0 {
				t.Fatalf("sources = %+v", list)
			}
			var added library.Source
			e.call("PUT", path, map[string]any{"url": "https://fake.example/u/a"}, 200, &added)
			if added.Kind != "fake" || added.Ref != "u/a" || added.LinkedBy != "owner" {
				t.Fatalf("added = %+v", added)
			}
			var byRef library.Source
			e.call("PUT", path, map[string]any{"kind": "fake", "ref": "u/b", "external_id": "3"}, 200, &byRef)
			if byRef.URL != "https://fake.example/u/b" || byRef.ExternalID != "3" {
				t.Fatalf("byRef = %+v", byRef)
			}
			e.call("GET", path, nil, 200, &list)
			if len(list) != 2 {
				t.Fatalf("sources = %+v", list)
			}
			e.call("PUT", path, map[string]any{"url": "https://fake.example/u/a", "kind": "fake", "ref": "u/a"}, 422, nil)

			for _, bad := range []map[string]any{
				{"url": "https://example.com/x"}, {"url": "not a url"},
				{"kind": "fake", "ref": "onlyone"}, {"kind": "nope", "ref": "a/b"}, {},
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
			e.call("PUT", fmt.Sprintf("/%s/999/sources", plural), map[string]any{"url": "https://fake.example/a/b"}, 404, nil)
			e.call("DELETE", fmt.Sprintf("/%s/999/sources/1", plural), nil, 404, nil)
		})
	}
}

// Entities queued while no sites plugin worked wait for one.
func TestSitesInstanceQueuesDetection(t *testing.T) {
	e := newEnv(t)
	e.sitesPlugin()
	detectJobs := func() int {
		t.Helper()
		var n int
		if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM job WHERE kind = 'detect-sources'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM job WHERE kind = 'detect-sources'`)
		return n
	}
	var inst plugins.InstanceInfo
	e.call("POST", "/instances", map[string]any{"plugin": "sites", "name": "Sites"}, 201, &inst)
	if n := detectJobs(); n != 1 {
		t.Fatalf("after create: %d detect jobs, want 1", n)
	}
	path := fmt.Sprintf("/instances/%d", inst.ID)
	e.call("PATCH", path, map[string]any{"name": "Sites 2"}, 200, nil)
	if n := detectJobs(); n != 1 {
		t.Fatalf("after edit: %d detect jobs, want 1", n)
	}
	e.call("POST", path+"/check", nil, 200, nil)
	if n := detectJobs(); n != 1 {
		t.Fatalf("after check: %d detect jobs, want 1", n)
	}
	e.call("POST", "/instances", map[string]any{"plugin": "target", "name": "App"}, 201, nil)
	if n := detectJobs(); n != 0 {
		t.Fatalf("a target instance queued %d detect jobs", n)
	}
}

func TestAddSourceThroughSitesPlugin(t *testing.T) {
	e := newEnv(t)
	id := e.addSites()
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
