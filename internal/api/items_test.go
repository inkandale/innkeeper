package api

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/internal/tier"
	"github.com/davidgibbons/innkeeper/protocol"
)

// newItemsEnv adds the fake plugin as an items plugin, notes, with one
// instance, and returns the instance and the path of its notes.
func newItemsEnv(t *testing.T) (*testEnv, int64, string) {
	t.Helper()
	e := newEnv(t)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"items"}, "-name=notes"))
	if err != nil {
		t.Fatal(err)
	}
	m.Name = "notes"
	e.manifests["notes"] = m
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "notes", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	return e, id, fmt.Sprintf("/instances/%d/items/notes", id)
}

func itemIDs(list []protocol.ItemSummary) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestItemsRoundTrip(t *testing.T) {
	e, _, notes := newItemsEnv(t)
	var list []protocol.ItemSummary
	e.call("GET", notes, nil, 200, &list)
	if !slices.Equal(itemIDs(list), []string{"welcome"}) {
		t.Fatalf("list = %+v", list)
	}
	var put itemIDOut
	e.call("POST", notes, map[string]any{"item": map[string]any{"title": "hi", "body": "Hi."}}, 201, &put.Body)
	if put.Body.ID != "hi" {
		t.Fatalf("created %+v", put.Body)
	}
	var got struct {
		ID   string         `json:"id"`
		Item map[string]any `json:"item"`
	}
	e.call("GET", notes+"/hi", nil, 200, &got)
	if got.ID != "hi" || got.Item["body"] != "Hi." {
		t.Fatalf("got %+v", got)
	}
	e.call("PUT", notes+"/hi", map[string]any{"item": map[string]any{"title": "hello"}}, 200, &put.Body)
	if put.Body.ID != "hello" {
		t.Fatalf("replaced as %+v", put.Body)
	}
	e.call("GET", notes+"/hi", nil, 404, nil)
	e.call("DELETE", notes+"/hello", nil, 204, nil)
	e.call("GET", notes, nil, 200, &list)
	if !slices.Equal(itemIDs(list), []string{"welcome"}) {
		t.Fatalf("list after delete = %+v", list)
	}
}

func TestItemsErrors(t *testing.T) {
	e, _, notes := newItemsEnv(t)
	e.call("GET", strings.Replace(notes, "/notes", "/skills", 1), nil, 404, nil)
	e.call("GET", notes+"/nothing", nil, 404, nil)
	e.call("PUT", notes+"/nothing", map[string]any{"item": map[string]any{"title": "x"}}, 404, nil)
	e.call("DELETE", notes+"/nothing", nil, 404, nil)
	// The plugin's message for a bad item reaches the owner.
	r := e.call("POST", notes, map[string]any{"item": map[string]any{"body": "untitled"}}, 400, nil)
	if !strings.Contains(string(r.Body), "a note needs a title") {
		t.Fatalf("bad item: %s", r)
	}
	// The importer instance's plugin has no items.
	e.call("GET", fmt.Sprintf("/instances/%d/items/notes", e.importID), nil, 400, nil)
}

func TestImportItemsFromUpload(t *testing.T) {
	e, id, notes := newItemsEnv(t)
	var list []protocol.ItemSummary
	e.call("POST", notes+"/import?name=college.md", []byte("# College"), 200, &list)
	if len(list) != 1 || list[0].ID != "college.md" {
		t.Fatalf("imported %+v", list)
	}
	// The fake records the path it read before the file's text.
	path, text, _ := strings.Cut(list[0].Description, "\n")
	inst, err := e.s.Registry.Instance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != inst.Params.BlobTmp || text != "# College" {
		t.Fatalf("plugin read %q from %q; blob_tmp is %s", text, path, inst.Params.BlobTmp)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("upload left behind: %v", err)
	}
}

func TestImportItemsFromURL(t *testing.T) {
	e, _, notes := newItemsEnv(t)
	var list []protocol.ItemSummary
	e.call("POST", notes+"/import-url", map[string]any{"url": "https://example.com/skills/noir"}, 200, &list)
	if len(list) != 1 || list[0].ID != "noir" {
		t.Fatalf("imported %+v", list)
	}
	// The plugin's reason reaches the owner.
	r := e.call("POST", notes+"/import-url", map[string]any{"url": "/"}, 400, nil)
	if !strings.Contains(string(r.Body), "nothing to import") {
		t.Fatalf("bad url: %s", r)
	}
	e.call("POST", notes+"/import", []byte("x"), 422, nil)
}

func TestItemTiers(t *testing.T) {
	e, _, notes := newItemsEnv(t)
	as := func(tr tier.Tier, method, path string, body any) int {
		return e.send(method, path, body, "Authorization", "Bearer "+e.tokenWith(tr)).Status
	}
	if got := as(tier.Read, "GET", notes+"/welcome", nil); got != 200 {
		t.Fatalf("read token getting an item: %d", got)
	}
	if got := as(tier.Read, "POST", notes, map[string]any{"item": map[string]any{"title": "x"}}); got != 403 {
		t.Fatalf("read token creating an item: %d", got)
	}
	if got := as(tier.Write, "POST", notes, map[string]any{"item": map[string]any{"title": "x"}}); got != 201 {
		t.Fatalf("write token creating an item: %d", got)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if got := as(tier.Write, method, notes+"/x", map[string]any{"item": map[string]any{"title": "x"}}); got != 403 {
			t.Fatalf("write token %s: %d", method, got)
		}
	}
}

func TestPluginsListItemTypes(t *testing.T) {
	e, _, _ := newItemsEnv(t)
	var list []pluginOut
	e.call("GET", "/plugins", nil, 200, &list)
	i := slices.IndexFunc(list, func(p pluginOut) bool { return p.Name == "notes" })
	notes := list[i].Items["notes"]
	if notes.Label != "Notes" || !slices.Equal(notes.Import, []string{"file", "url"}) || notes.Schema["type"] != "object" {
		t.Fatalf("notes = %+v", notes)
	}
	raw, _ := json.Marshal(notes.Schema)
	if !strings.Contains(string(raw), "contentMediaType") {
		t.Fatalf("schema = %s", raw)
	}
}
