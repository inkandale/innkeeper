package api

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/protocol"
)

// catalog adds an instance of the fake plugin as a catalog, with config.
func (e *testEnv) catalog(name string, config map[string]any) int64 {
	e.t.Helper()
	if e.manifests["catalog"] == nil {
		m := *e.manifests["fake"]
		m.Name, m.Capabilities = "catalog", []string{"catalog"}
		m.Command = []string{m.Command[0], "-caps=catalog", "-name=catalog"}
		e.manifests["catalog"] = &m
	}
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "catalog", Name: name, Config: config})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func catalogIDs(items []catalogHit) string {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return strings.Join(ids, ",")
}

func TestCatalogRoutes(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	down := e.catalog("Down", map[string]any{"down": true})
	broken := e.catalog("Broken", nil)
	e.markBroken(broken, "bad config")
	base := fmt.Sprintf("/catalogs/%d", inst)

	var list []catalogOut
	e.call("GET", "/catalogs", nil, 200, &list)
	got := map[int64]catalogOut{}
	for _, c := range list {
		got[c.ID] = c
	}
	if len(list) != 3 || got[inst].Name != "Archive" || got[inst].Info == nil || !got[inst].Info.Ingest || got[inst].LastIngest != nil {
		t.Fatalf("catalogs = %+v", list)
	}
	// describe doesn't reach the source, so a catalog that is down still answers it.
	if got[down].Info == nil || got[broken].Info != nil || !strings.Contains(got[broken].Error, "broken") {
		t.Fatalf("down = %+v, broken = %+v", got[down], got[broken])
	}

	var page catalogResults
	e.call("GET", base+"/search?kind=card&size=1", nil, 200, &page)
	if catalogIDs(page.Items) != "brakka" || page.Next == "" || page.CatalogID != inst || page.Items[0].Library == nil {
		t.Fatalf("first page = %+v", page)
	}
	// Decoding keeps fields the response omits, such as next, so start empty.
	next := page.Next
	page = catalogResults{}
	e.call("GET", base+"/search?kind=card&size=1&cursor="+next, nil, 200, &page)
	if catalogIDs(page.Items) != "carrow" || page.Next != "" {
		t.Fatalf("second page = %+v", page)
	}
	e.call("GET", base+"/search?filters="+url.QueryEscape(`{"tag": "dwarf"}`), nil, 200, &page)
	if catalogIDs(page.Items) != "brakka,forge-lore" {
		t.Fatalf("tag filter = %+v", page)
	}
	e.call("GET", base+"/search?filters="+url.QueryEscape(`{"color": "red"}`), nil, 400, nil)
	e.call("GET", base+"/search?filters=red", nil, 400, nil)
	if r := e.call("GET", fmt.Sprintf("/catalogs/%d/search", down), nil, 503, nil); !strings.Contains(string(r.Body), "catalog Down is unavailable") {
		t.Fatalf("down: %s", r)
	}
	e.call("GET", fmt.Sprintf("/catalogs/%d/search", broken), nil, 409, nil)
	e.call("GET", fmt.Sprintf("/catalogs/%d/search", e.importID), nil, 404, nil)

	var item protocol.CatalogGetResult
	e.call("GET", base+"/items/brakka", nil, 200, &item)
	if item.Version != "2" || len(item.Versions) != 2 || item.Avatar != "" {
		t.Fatalf("item = %+v", item)
	}
	e.call("GET", base+"/items/brakka?version=1", nil, 200, &item)
	if item.Version != "1" {
		t.Fatalf("version 1 = %+v", item)
	}
	// An ID with a slash travels escaped and reaches the plugin whole.
	if r := e.call("GET", base+"/items/no%2Fsuch", nil, 404, nil); !strings.Contains(string(r.Body), "no item no/such") {
		t.Fatalf("missing item: %s", r)
	}
}

func TestCatalogIngestRoute(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	static := e.catalog("Static", map[string]any{"no_ingest": true})

	var j jobOut
	e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", inst), nil, 202, &j)
	if j.Kind != importer.IngestKind || j.InstanceID == nil || *j.InstanceID != inst {
		t.Fatalf("job = %+v", j)
	}
	// Nothing runs jobs here, so the first ingest is still queued.
	e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", inst), nil, 409, nil)
	if r := e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", static), nil, 400, nil); !strings.Contains(string(r.Body), "doesn't ingest") {
		t.Fatalf("static: %s", r)
	}

	var list []catalogOut
	e.call("GET", "/catalogs", nil, 200, &list)
	last := map[int64]*jobOut{}
	for _, c := range list {
		last[c.ID] = c.LastIngest
	}
	if last[inst] == nil || last[inst].ID != j.ID || last[static] != nil {
		t.Fatalf("last ingests = %+v", last)
	}
}
