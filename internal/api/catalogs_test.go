package api

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
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
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "catalog", Name: new(name), Config: config})
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
	off := e.catalog("Off", nil)
	disabled := true
	if err := e.s.Registry.UpdateInstance(context.Background(), off, plugins.InstanceEdit{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/catalogs/%d", inst)

	var list []catalogOut
	e.call("GET", "/catalogs", nil, 200, &list)
	got := map[int64]catalogOut{}
	for _, c := range list {
		got[c.ID] = c
	}
	// describe doesn't reach the source, so a catalog that is down still answers it.
	if len(list) != 2 || got[inst].Name != "Archive" || len(got[inst].Info.Kinds) == 0 || len(got[down].Info.Kinds) == 0 {
		t.Fatalf("catalogs = %+v; want Archive and Down, not the broken or disabled one", list)
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
	failing := e.catalog("Failing", map[string]any{"failing": true})
	if r := e.call("GET", fmt.Sprintf("/catalogs/%d/search", failing), nil, 502, nil); !strings.Contains(string(r.Body), "/srv") {
		t.Fatalf("failing catalog hid its error from the owner: %s", r)
	}
	if r := e.send("GET", fmt.Sprintf("/catalogs/%d/search", failing), nil, "Authorization", "Bearer "+e.tokenWith(tier.Read)); strings.Contains(string(r.Body), "/srv") {
		t.Fatalf("failing catalog leaked its error to a read caller: %s", r)
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
	// An ID with a slash travels escaped and reaches the plugin.
	if r := e.call("GET", base+"/items/no%2Fsuch", nil, 404, nil); !strings.Contains(string(r.Body), "has no such item") {
		t.Fatalf("missing item: %s", r)
	}
}

// A catalog item shows the library cards with its content or near its text.
func TestCatalogSearchMatchesLibrary(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	same := e.createCard(`{"name": "Carrow", "description": "A cheerful elven bard who sings of the sea."}`)
	near := e.createCard(`{"name": "Carrow Two", "description": "A cheerful elven bard who sings of the sea."}`)

	var page catalogResults
	e.call("GET", fmt.Sprintf("/catalogs/%d/search?kind=card", inst), nil, 200, &page)
	got := map[string][]library.HashMatch{}
	for _, it := range page.Items {
		got[it.ID] = it.Library
	}
	want := []library.HashMatch{{ID: same.ID, LibraryID: library.DefaultLibrary, Reason: "content"},
		{ID: near.ID, LibraryID: library.DefaultLibrary, Reason: "text"}}
	if m := got["carrow"]; !slices.Equal(m, want) {
		t.Fatalf("carrow matches = %+v, want %+v", m, want)
	}
	if m := got["brakka"]; len(m) != 0 {
		t.Fatalf("brakka matches = %+v", m)
	}
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
	var later searchResults
	e.call("POST", "/search", map[string]any{"sources": []string{"library", src}, "page": map[string]any{"number": 2}}, 200, &later)
	if later.Catalogs != nil {
		t.Fatalf("page 2 searched catalogs: %+v", later.Catalogs)
	}
	e.call("POST", "/search", map[string]any{"sources": []string{"catalog:999999"}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{"catalog:x"}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{fmt.Sprintf("catalog:%d", e.importID)}}, 400, nil)
	e.call("POST", "/search", map[string]any{"sources": []string{"chub"}}, 400, nil)
}

// A catalog source in a search matches against the searched library.
func TestSearchCatalogSourcesMatchTheQueryLibrary(t *testing.T) {
	e := newEnv(t)
	inst := e.catalog("Archive", nil)
	archive := e.createLibrary("Archive")
	data := map[string]any{"name": "Carrow", "description": "A cheerful elven bard who sings of the sea."}
	e.createCard(`{"name": "Carrow", "description": "A cheerful elven bard who sings of the sea."}`)
	var there entityResult
	e.call("POST", "/cards", map[string]any{"data": data, "library": archive.ID}, 201, &there)

	var res searchResults
	e.call("POST", "/search", map[string]any{"library": archive.ID, "sources": []string{fmt.Sprintf("catalog:%d", inst)}}, 200, &res)
	var carrow []library.HashMatch
	for _, it := range res.Catalogs[0].Items {
		if it.ID == "carrow" {
			carrow = it.Library
		}
	}
	want := []library.HashMatch{{ID: there.ID, LibraryID: archive.ID, Reason: "content"}}
	if !slices.Equal(carrow, want) {
		t.Fatalf("carrow matches = %+v, want %+v", carrow, want)
	}
}
