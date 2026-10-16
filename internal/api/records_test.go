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

	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/protocol"
)

// newRecordsEnv adds the fake plugin as a records plugin, notes, with one
// instance, and returns the instance and the path of its notes.
func newRecordsEnv(t *testing.T) (*testEnv, int64, string) {
	t.Helper()
	e := newEnv(t)
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"records"}, "-name=notes"))
	if err != nil {
		t.Fatal(err)
	}
	m.Name = "notes"
	e.manifests["notes"] = m
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "notes", Name: "Notes"})
	if err != nil {
		t.Fatal(err)
	}
	return e, id, fmt.Sprintf("/instances/%d/records/notes", id)
}

func recordIDs(list []protocol.RecordSummary) []string {
	var ids []string
	for _, s := range list {
		ids = append(ids, s.ID)
	}
	return ids
}

func TestRecordsRoundTrip(t *testing.T) {
	e, _, notes := newRecordsEnv(t)
	var list []protocol.RecordSummary
	e.call("GET", notes, nil, 200, &list)
	if !slices.Equal(recordIDs(list), []string{"welcome"}) {
		t.Fatalf("list = %+v", list)
	}
	var put recordIDOut
	e.call("POST", notes, map[string]any{"record": map[string]any{"title": "hi", "body": "Hi."}}, 201, &put.Body)
	if put.Body.ID != "hi" {
		t.Fatalf("created %+v", put.Body)
	}
	var got struct {
		ID     string         `json:"id"`
		Record map[string]any `json:"record"`
	}
	e.call("GET", notes+"/hi", nil, 200, &got)
	if got.ID != "hi" || got.Record["body"] != "Hi." {
		t.Fatalf("got %+v", got)
	}
	e.call("PUT", notes+"/hi", map[string]any{"record": map[string]any{"title": "hello"}}, 200, &put.Body)
	if put.Body.ID != "hello" {
		t.Fatalf("replaced as %+v", put.Body)
	}
	e.call("GET", notes+"/hi", nil, 404, nil)
	e.call("DELETE", notes+"/hello", nil, 204, nil)
	e.call("GET", notes, nil, 200, &list)
	if !slices.Equal(recordIDs(list), []string{"welcome"}) {
		t.Fatalf("list after delete = %+v", list)
	}
}

func TestRecordsErrors(t *testing.T) {
	e, _, notes := newRecordsEnv(t)
	e.call("GET", strings.Replace(notes, "/notes", "/skills", 1), nil, 404, nil)
	e.call("GET", notes+"/nothing", nil, 404, nil)
	e.call("PUT", notes+"/nothing", map[string]any{"record": map[string]any{"title": "x"}}, 404, nil)
	e.call("DELETE", notes+"/nothing", nil, 404, nil)
	// The plugin's message for a bad record reaches the owner.
	r := e.call("POST", notes, map[string]any{"record": map[string]any{"body": "untitled"}}, 400, nil)
	if !strings.Contains(string(r.Body), "a note needs a title") {
		t.Fatalf("bad record: %s", r)
	}
	// A create never replaces: the fake derives the ID from the title.
	r = e.call("POST", notes, map[string]any{"record": map[string]any{"title": "welcome"}}, 400, nil)
	if !strings.Contains(string(r.Body), "already exists") {
		t.Fatalf("create over an existing note: %s", r)
	}
	// IDs that would leave the plugin's folder never reach it.
	for _, bad := range []string{"..%2F..%2Fsecret", "%2E%2E", "%2E"} {
		e.call("GET", notes+"/"+bad, nil, 400, nil)
		e.call("PUT", notes+"/"+bad, map[string]any{"record": map[string]any{"title": "x"}}, 400, nil)
		e.call("DELETE", notes+"/"+bad, nil, 400, nil)
	}
	// The importer instance's plugin has no records.
	e.call("GET", fmt.Sprintf("/instances/%d/records/notes", e.importID), nil, 400, nil)
}

func TestImportRecordsFromUpload(t *testing.T) {
	e, id, notes := newRecordsEnv(t)
	var list []protocol.RecordSummary
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

func TestImportRecordsFromURL(t *testing.T) {
	e, _, notes := newRecordsEnv(t)
	var list []protocol.RecordSummary
	e.call("POST", notes+"/import-url", map[string]any{"url": "https://example.com/skills/noir"}, 200, &list)
	if len(list) != 1 || list[0].ID != "noir" {
		t.Fatalf("imported %+v", list)
	}
	// The plugin's reason reaches the owner.
	r := e.call("POST", notes+"/import-url", map[string]any{"url": "https://example.com/"}, 400, nil)
	if !strings.Contains(string(r.Body), "nothing to import") {
		t.Fatalf("bad url: %s", r)
	}
	e.call("POST", notes+"/import", []byte("x"), 422, nil)
	for _, name := range []string{"..%2Fx.md", "a%5Cb.md", "a%00.md"} {
		e.call("POST", notes+"/import?name="+name, []byte("x"), 400, nil)
	}
	e.call("POST", notes+"/import-url", map[string]any{"url": "file:///etc/passwd"}, 422, nil)
}

func TestRecordTiers(t *testing.T) {
	e, _, notes := newRecordsEnv(t)
	as := func(tr tier.Tier, method, path string, body any) int {
		return e.send(method, path, body, "Authorization", "Bearer "+e.tokenWith(tr)).Status
	}
	if got := as(tier.Read, "GET", notes+"/welcome", nil); got != 200 {
		t.Fatalf("read token getting a record: %d", got)
	}
	if got := as(tier.Read, "POST", notes, map[string]any{"record": map[string]any{"title": "x"}}); got != 403 {
		t.Fatalf("read token creating a record: %d", got)
	}
	if got := as(tier.Write, "POST", notes, map[string]any{"record": map[string]any{"title": "x"}}); got != 201 {
		t.Fatalf("write token creating a record: %d", got)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		if got := as(tier.Write, method, notes+"/x", map[string]any{"record": map[string]any{"title": "x"}}); got != 403 {
			t.Fatalf("write token %s: %d", method, got)
		}
	}
}

func TestPluginsListRecordTypes(t *testing.T) {
	e, _, _ := newRecordsEnv(t)
	var list []pluginOut
	e.call("GET", "/plugins", nil, 200, &list)
	i := slices.IndexFunc(list, func(p pluginOut) bool { return p.Name == "notes" })
	notes := list[i].Records["notes"]
	if notes.Label != "Notes" || !slices.Equal(notes.Import, []string{"file", "url"}) || notes.Schema["type"] != "object" {
		t.Fatalf("notes = %+v", notes)
	}
	raw, _ := json.Marshal(notes.Schema)
	if !strings.Contains(string(raw), "contentMediaType") {
		t.Fatalf("schema = %s", raw)
	}
}
