package api

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/importer"
	"github.com/davidgibbons/innkeeper/internal/library"
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
	// A finished ingest no longer blocks a new one.
	if err := e.s.Queue.Cancel(context.Background(), j.ID); err != nil {
		t.Fatal(err)
	}
	var j2 jobOut
	e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", inst), nil, 202, &j2)
	if j2.ID == j.ID {
		t.Fatalf("second ingest reused job %d", j.ID)
	}
	if r := e.call("POST", fmt.Sprintf("/catalogs/%d/ingest", static), nil, 400, nil); !strings.Contains(string(r.Body), "doesn't ingest") {
		t.Fatalf("static: %s", r)
	}

	var list []catalogOut
	e.call("GET", "/catalogs", nil, 200, &list)
	last := map[int64]*jobOut{}
	for _, c := range list {
		last[c.ID] = c.LastIngest
	}
	if last[inst] == nil || last[inst].ID != j2.ID || last[static] != nil {
		t.Fatalf("last ingests = %+v", last)
	}
}

func TestCatalogAdopt(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	base := fmt.Sprintf("/catalogs/%d", inst)

	var c entityOut
	e.call("POST", base+"/items/brakka/adopt", nil, 201, &c)
	if c.Provenance != fmt.Sprintf("catalog:%d:brakka@2", inst) || c.Revision.Source != "plugin:catalog" ||
		len(c.Lorebooks) != 1 || c.Revision.Avatar == nil {
		t.Fatalf("adopted %+v", c)
	}
	for _, path := range []string{"/items/brakka/adopt", "/items/brakka/adopt?version=2"} {
		var again entityOut
		e.call("POST", base+path, nil, 200, &again)
		if again.ID != c.ID {
			t.Fatalf("%s adopted again as %d, want %d", path, again.ID, c.ID)
		}
	}
	var old entityOut
	e.call("POST", base+"/items/brakka/adopt?version=1", nil, 201, &old)
	if old.ID == c.ID || old.Provenance != fmt.Sprintf("catalog:%d:brakka@1", inst) {
		t.Fatalf("version 1 = %+v", old)
	}
	e.call("POST", base+"/items/brakka/adopt?version=9", nil, 404, nil)
	e.call("POST", base+"/items/nobody/adopt", nil, 404, nil)

	var lb entityOut
	e.call("POST", base+"/items/forge-lore/adopt", nil, 201, &lb)
	e.call("GET", fmt.Sprintf("/lorebooks/%d", lb.ID), nil, 200, nil)

	// near has carrow's text but not its content.
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)
	var page catalogResults
	e.call("GET", base+"/search", nil, 200, &page)
	got := map[string][]library.HashMatch{}
	for _, it := range page.Items {
		got[it.ID] = it.Library
	}
	if m := got["brakka"]; len(m) == 0 || m[0] != (library.HashMatch{ID: c.ID, Reason: "content"}) {
		t.Fatalf("brakka matches = %+v", m)
	}
	if m := got["carrow"]; len(m) != 1 || m[0] != (library.HashMatch{ID: near.ID, Reason: "text"}) {
		t.Fatalf("carrow matches = %+v", m)
	}
	if m := got["forge-lore"]; len(m) != 1 || m[0] != (library.HashMatch{ID: lb.ID, Reason: "content"}) {
		t.Fatalf("forge-lore matches = %+v", m)
	}
}

func TestCatalogMatches(t *testing.T) {
	e := newEnv(t)
	up := e.catalog("Archive", nil)
	down := e.catalog("Down", map[string]any{"down": true})
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)

	var out catalogMatches
	e.call("GET", fmt.Sprintf("/cards/%d/catalog-matches", near.ID), nil, 200, &out)
	if len(out.Matches) != 1 || out.Matches[0].CatalogID != up || out.Matches[0].Catalog != "Archive" ||
		len(out.Matches[0].Items) != 1 || out.Matches[0].Items[0].ID != "carrow" {
		t.Fatalf("matches = %+v", out.Matches)
	}
	if len(out.Unreachable) != 1 || out.Unreachable[0].ID != down || !strings.Contains(out.Unreachable[0].Error, "catalog Down is unavailable") {
		t.Fatalf("unreachable = %+v", out.Unreachable)
	}

	var lb entityOut
	e.call("POST", fmt.Sprintf("/catalogs/%d/items/forge-lore/adopt", up), nil, 201, &lb)
	var lbOut catalogMatches
	e.call("GET", fmt.Sprintf("/lorebooks/%d/catalog-matches", lb.ID), nil, 200, &lbOut)
	if len(lbOut.Matches) != 1 || lbOut.Matches[0].Items[0].ID != "forge-lore" {
		t.Fatalf("lorebook matches = %+v", lbOut.Matches)
	}
	e.call("GET", "/cards/999999/catalog-matches", nil, 404, nil)
}

func TestSearchCatalogSources(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	down := e.catalog("Down", map[string]any{"down": true})
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)
	src := fmt.Sprintf("catalog:%d", inst)

	var res searchResults
	e.call("POST", "/search", map[string]any{"text": "carrow", "sources": []string{"library", src, fmt.Sprintf("catalog:%d", down)}}, 200, &res)
	if res.Total != 1 || res.Hits[0].ID != near.ID || len(res.Catalogs) != 2 {
		t.Fatalf("search = %+v", res)
	}
	if c := res.Catalogs[0]; c.CatalogID != inst || catalogIDs(c.Items) != "carrow" || len(c.Items[0].Library) != 1 || c.Error != "" {
		t.Fatalf("archive = %+v", c)
	}
	if c := res.Catalogs[1]; c.CatalogID != down || len(c.Items) != 0 || !strings.Contains(c.Error, "unavailable") {
		t.Fatalf("down = %+v", c)
	}

	var only searchResults
	e.call("POST", "/search", map[string]any{"sources": []string{src}}, 200, &only)
	if len(only.Hits) != 0 || only.Total != 0 || catalogIDs(only.Catalogs[0].Items) != "brakka,carrow" {
		t.Fatalf("catalog only = %+v", only)
	}
	var lore searchResults
	e.call("POST", "/lorebooks/search", map[string]any{"sources": []string{src}}, 200, &lore)
	if catalogIDs(lore.Catalogs[0].Items) != "forge-lore" {
		t.Fatalf("lorebooks = %+v", lore)
	}
	e.call("POST", "/search", map[string]any{"sources": []string{"catalog:x"}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{fmt.Sprintf("catalog:%d", e.importID)}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{"chub"}}, 400, nil)
}
