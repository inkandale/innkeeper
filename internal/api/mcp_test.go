package api

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/inkandale/innkeeper/internal/bulk"
	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/mcp"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/targets"
	"github.com/inkandale/innkeeper/internal/tier"
	"github.com/inkandale/innkeeper/internal/wire"
)

type bearer struct{ scheme, token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", cmp.Or(b.scheme, "Bearer")+" "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// mcpClient connects to /mcp with a token of tier t.
func (e *testEnv) mcpClient(t tier.Tier) *sdk.ClientSession {
	e.t.Helper()
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	s, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{token: e.tokenWith(t)}}}, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { s.Close() })
	return s
}

// tool calls a tool and returns its text, failing the test on a tool error
// unless wantErr is set.
func tool(t *testing.T, s *sdk.ClientSession, name string, args any, wantErr bool) string {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if res.IsError != wantErr {
		t.Fatalf("%s: error %v, want %v: %s", name, res.IsError, wantErr, text)
	}
	return text
}

func TestMCPNeedsAToken(t *testing.T) {
	e := newEnv(t)
	if r := e.send("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); r.Status != http.StatusUnauthorized {
		t.Fatalf("no token: %s", r)
	}
}

func TestMCPToolsByTier(t *testing.T) {
	e := newEnv(t)
	names := func(tr tier.Tier) []string {
		res, err := e.mcpClient(tr).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tl := range res.Tools {
			out = append(out, tl.Name)
			if tl.Name == "push" && (tl.Annotations == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
				t.Error("push isn't marked destructive")
			}
			if tl.Name == "search_cards" && !tl.Annotations.ReadOnlyHint {
				t.Error("search_cards isn't marked read-only")
			}
		}
		return out
	}
	read, write, all := names(tier.Read), names(tier.Write), names(tier.Destructive)
	if !slices.Contains(read, "search_cards") || slices.Contains(read, "create_card") || slices.Contains(write, "push") ||
		!slices.Contains(write, "create_card") || !slices.Contains(all, "push") || builtIn(all) != 39 ||
		!slices.Contains(read, "refresh") || slices.Contains(write, "set_autosync") || !slices.Contains(all, "set_autosync") {
		t.Fatalf("read %v\nwrite %v\nall %v", read, write, all)
	}
}

func TestMCPCards(t *testing.T) {
	e := newEnv(t)
	s := e.mcpClient(tier.Destructive)
	var c entityOut
	if err := json.Unmarshal([]byte(tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka"}}, false)), &c); err != nil {
		t.Fatal(err)
	}
	if c.Revision.Source != "mcp" {
		t.Fatalf("source = %q", c.Revision.Source)
	}
	got := tool(t, s, "get_card", map[string]any{"id": c.ID}, false)
	if !strings.Contains(got, `"bindings":[]`) || !strings.Contains(got, "Brakka") {
		t.Fatalf("get_card = %s", got)
	}
	update := map[string]any{"id": c.ID, "base_revision_id": c.Revision.ID, "data": map[string]any{"name": "Brakka", "first_mes": "Hail."}}
	tool(t, s, "update_card", update, false)
	if got := tool(t, s, "update_card", update, true); !strings.HasPrefix(got, "409") {
		t.Fatalf("stale update = %s", got)
	}
	if got := tool(t, s, "search_cards", map[string]any{"query": map[string]any{"text": "Brakka"}}, false); !strings.Contains(got, "Brakka") {
		t.Fatalf("search = %s", got)
	}
	tool(t, s, "get_history", map[string]any{"kind": "card", "id": c.ID}, false)
	tool(t, s, "restore_revision", map[string]any{"kind": "card", "id": c.ID, "revision_id": c.Revision.ID,
		"base_revision_id": c.Revision.ID + 1}, false)
}

func TestMCPSources(t *testing.T) {
	e := newEnv(t)
	e.addSites()
	s := e.mcpClient(tier.Write)
	var c entityOut
	_ = json.Unmarshal([]byte(tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka"}}, false)), &c)
	if got := tool(t, s, "add_source", map[string]any{"card_id": c.ID, "url": "https://fake.example/u/a"}, false); !strings.Contains(got, `"u/a"`) {
		t.Fatalf("add_source = %s", got)
	}
	var list []struct{ ID int64 }
	_ = json.Unmarshal([]byte(tool(t, s, "list_sources", map[string]any{"card_id": c.ID}, false)), &list)
	if len(list) != 1 {
		t.Fatalf("sources = %v", list)
	}
	tool(t, s, "remove_source", map[string]any{"card_id": c.ID, "source_id": list[0].ID}, false)
	if got := tool(t, s, "list_sources", map[string]any{"card_id": c.ID}, false); !strings.Contains(got, "[]") {
		t.Fatalf("after remove = %s", got)
	}
	tool(t, s, "add_source", map[string]any{"card_id": c.ID, "url": "https://example.com/x"}, true)
	tool(t, s, "list_sources", map[string]any{}, true)
}

func TestMCPPush(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	s := e.mcpClient(tier.Destructive)
	var c entityOut
	_ = json.Unmarshal([]byte(tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka"}}, false)), &c)
	args := map[string]any{"kind": "card", "id": c.ID, "instance_id": inst}
	var p struct{ Confirm string }
	_ = json.Unmarshal([]byte(tool(t, s, "preview_push", args, false)), &p)
	if res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "push", Arguments: args}); err == nil && !res.IsError {
		t.Fatal("push without confirm went ahead")
	}
	args["confirm"] = p.Confirm
	if got := tool(t, s, "push", args, false); !strings.Contains(got, "target.push") {
		t.Fatalf("push = %s", got)
	}
}

// A write token gets the API's 403 even for a tool it can't see.
func TestMCPInstanceToolsNeedDestructive(t *testing.T) {
	e := newEnv(t)
	res, err := e.mcpClient(tier.Write).CallTool(context.Background(), &sdk.CallToolParams{Name: "create_instance",
		Arguments: map[string]any{"plugin": "fake", "name": "x"}})
	if err == nil && !res.IsError {
		t.Fatal("a write token created an instance")
	}
}

func TestMCPOtherTools(t *testing.T) {
	e := newEnv(t)
	inst, dir := e.target()
	s := e.mcpClient(tier.Destructive)
	var c, lb entityOut
	_ = json.Unmarshal([]byte(tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail."}}, false)), &c)
	_ = json.Unmarshal([]byte(tool(t, s, "create_lorebook", map[string]any{"data": map[string]any{"name": "Lore", "entries": []any{}}}, false)), &lb)
	tool(t, s, "link_lorebook", map[string]any{"card_id": c.ID, "lorebook_id": lb.ID}, false)
	tool(t, s, "get_lorebook", map[string]any{"id": lb.ID}, false)
	tool(t, s, "unlink_lorebook", map[string]any{"card_id": c.ID, "lorebook_id": lb.ID}, false)

	if _, err := e.s.Targets.Push(context.Background(), library.Card, inst, c.ID, false); err != nil {
		t.Fatal(err)
	}
	var bindings []struct{ ID int64 }
	_ = json.Unmarshal([]byte(tool(t, s, "list_bindings", map[string]any{"kind": "card", "id": c.ID}, false)), &bindings)
	if len(bindings) != 1 {
		t.Fatalf("bindings = %v", bindings)
	}
	// The owner edits the card in the app.
	if err := os.WriteFile(filepath.Join(dir, "r1.json"), []byte(`{"spec": "chara_card_v3", "data": {"name": "Brakka", "first_mes": "Well met."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tool(t, s, "check_drift", map[string]any{"binding_id": bindings[0].ID}, false); !strings.Contains(got, "remote_changed") {
		t.Fatalf("check_drift = %s", got)
	}
	if got := tool(t, s, "set_autosync", map[string]any{"binding_id": bindings[0].ID, "autosync": true}, false); !strings.Contains(got, `"autosync":true`) {
		t.Fatalf("set_autosync = %s", got)
	}
	if got := tool(t, s, "list_changes", map[string]any{"instance_id": inst, "state": "ahead"}, false); !strings.Contains(got, `"rows"`) {
		t.Fatalf("list_changes = %s", got)
	}
	if got := tool(t, s, "refresh", map[string]any{}, false); !strings.Contains(got, `"id"`) {
		t.Fatalf("refresh = %s", got)
	}

	var job struct{ ID int64 }
	_ = json.Unmarshal([]byte(tool(t, s, "start_import", map[string]any{"instance_id": e.importID}, false)), &job)
	tool(t, s, "get_job", map[string]any{"id": job.ID}, false)
	tool(t, s, "list_jobs", map[string]any{}, false)
	tool(t, s, "cancel_job", map[string]any{"id": job.ID}, false)
	if got := tool(t, s, "start_import", map[string]any{"instance_id": e.importID, "url": "https://chub.ai/characters/u/b"}, false); !strings.Contains(got, `"url":"https://chub.ai/characters/u/b"`) {
		t.Fatalf("start_import with url = %s", got)
	}
	if got := tool(t, s, "list_plugins", map[string]any{}, false); !strings.Contains(got, "fake") {
		t.Fatalf("list_plugins = %s", got)
	}
	if got := tool(t, s, "list_instances", map[string]any{}, false); strings.Contains(got, "letmein") {
		t.Fatal("list_instances shows a secret")
	}
	// The fake plugin can log people in, so MCP can't change it.
	if got := tool(t, s, "update_instance", map[string]any{"id": inst, "name": "Renamed"}, true); !strings.HasPrefix(got, "403") {
		t.Fatalf("update_instance on a login provider = %s", got)
	}
	vault, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "vault", Name: "Vault"})
	if err != nil {
		t.Fatal(err)
	}
	tool(t, s, "update_instance", map[string]any{"id": vault, "name": "Renamed", "secrets": map[string]any{"api_key": "k"}}, false)
}

// The SDK takes any case of "Bearer"; tools must still reach the API.
func TestMCPLowercaseBearer(t *testing.T) {
	e := newEnv(t)
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	s, err := c.Connect(context.Background(), &sdk.StreamableClientTransport{Endpoint: e.srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearer{"bearer", e.tokenWith(tier.Read)}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	tool(t, s, "list_jobs", map[string]any{}, false)
}

// builtIn counts names that aren't the test env's plugin tools.
func builtIn(names []string) int {
	n := 0
	for _, name := range names {
		if !strings.HasPrefix(name, "login_") && !strings.HasPrefix(name, "importer_") {
			n++
		}
	}
	return n
}

func TestMCPPluginTools(t *testing.T) {
	e := newEnv(t)
	for _, name := range []string{"Twin Peaks!", "twin-peaks"} {
		if _, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	s := e.mcpClient(tier.Read)
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	twins := 0
	for _, n := range names {
		if strings.HasPrefix(n, "twin_peaks_") && strings.HasSuffix(n, "_echo") {
			twins++
		}
	}
	if !slices.Contains(names, "importer_echo") || slices.Contains(names, "importer_note") || twins != 2 {
		t.Fatalf("tools = %v", names)
	}
	if got := tool(t, s, "importer_echo", map[string]any{"text": "hi"}, false); got != "echo: hi" {
		t.Fatalf("importer_echo = %q", got)
	}
}

func TestMCPReconcile(t *testing.T) {
	e := newEnv(t)
	e.work()
	inst, dir := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail."}}, 201, &c)
	e.appCard(dir, "rBrakka.png", "Brakka", "Well met.")
	e.appCard(dir, "rCarrow", "Carrow", "Hello.")

	read, write := e.mcpClient(tier.Read), e.mcpClient(tier.Write)
	if _, err := read.CallTool(context.Background(), &sdk.CallToolParams{Name: "start_sync",
		Arguments: map[string]any{"instance_id": inst}}); err == nil {
		t.Fatal("a read token called start_sync")
	}
	var j jobOut
	start := map[string]any{"instance_id": inst, "apply": "none"}
	if err := json.Unmarshal([]byte(tool(t, write, "start_sync", start, false)), &j); err != nil {
		t.Fatal(err)
	}
	if last, r := e.watchJob(j.ID); last.Status != "succeeded" {
		t.Fatalf("sync = %+v\n%s", last, r.Body)
	}
	// The sync's progress names the app's reconcile job, which holds the snapshot.
	var sync struct{ Progress wire.SyncResult }
	if err := json.Unmarshal([]byte(tool(t, read, "get_job", map[string]any{"id": j.ID}, false)), &sync); err != nil ||
		len(sync.Progress.Apps) != 1 || sync.Progress.Apps[0].JobID == 0 {
		t.Fatalf("sync progress = %+v, %v", sync.Progress, err)
	}
	if got := tool(t, read, "get_job", map[string]any{"id": sync.Progress.Apps[0].JobID}, false); !strings.Contains(got, "rCarrow") {
		t.Fatalf("reconcile job = %s", got)
	}
	var cmp targets.Comparison
	got := tool(t, read, "compare_item", map[string]any{"instance_id": inst, "remote_id": "rBrakka.png", "id": c.ID}, false)
	if err := json.Unmarshal([]byte(got), &cmp); err != nil || cmp.AppHash == "" {
		t.Fatalf("compare_item = %s", got)
	}
	if _, err := read.CallTool(context.Background(), &sdk.CallToolParams{Name: "resolve_item", Arguments: map[string]any{}}); err == nil {
		t.Fatal("a read token called resolve_item")
	}

	got = tool(t, write, "resolve_item", map[string]any{"instance_id": inst, "remote_id": "rBrakka.png", "action": "bind", "card_id": c.ID,
		"app_hash": cmp.AppHash, "base_revision_id": cmp.HeadRevisionID, "default": "app"}, false)
	if !strings.Contains(got, `"binding_id"`) {
		t.Fatalf("resolve_item = %s", got)
	}
	got = tool(t, read, "compare_item", map[string]any{"instance_id": inst, "remote_id": "rBrakka.png", "two_way": true}, false)
	if err := json.Unmarshal([]byte(got), &cmp); err != nil || cmp.Base != "none" || cmp.AcceptedHash == "" {
		t.Fatalf("two-way compare_item = %s", got)
	}
	got = tool(t, write, "resolve_item", map[string]any{"instance_id": inst, "remote_id": "rBrakka.png", "action": "bind", "card_id": cmp.CardID,
		"app_hash": cmp.AppHash, "base_revision_id": cmp.HeadRevisionID, "accepted_hash": cmp.AcceptedHash, "base": "none"}, false)
	if !strings.Contains(got, `"binding_id"`) {
		t.Fatalf("two-way resolve_item = %s", got)
	}
	got = tool(t, write, "resolve_items", map[string]any{"resolutions": []map[string]any{
		{"instance_id": inst, "remote_id": "rCarrow", "action": "skip"}}}, false)
	if err := json.Unmarshal([]byte(got), &j); err != nil || j.Kind != bulk.Kind {
		t.Fatalf("resolve_items = %s", got)
	}
}

func TestCheckSourcesTool(t *testing.T) {
	e := newEnv(t)
	s := e.mcpClient(tier.Write)
	if got := tool(t, s, "check_sources", map[string]any{}, false); !strings.Contains(got, `"kind":"target.source-check"`) {
		t.Fatalf("check_sources = %s", got)
	}
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Vyr"}}, 201, &c)
	if got := tool(t, s, "check_sources", map[string]any{"id": c.ID}, false); !strings.HasPrefix(got, "[") {
		t.Fatalf("check_sources for one card = %s", got)
	}
}

func TestMCPCallInProcess(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka"}}, 201, &c)
	h := e.s.APIHandler()

	res, err := mcp.Call(ctx, h, tier.Read, "get_card", json.RawMessage(fmt.Sprintf(`{"id": %d}`, c.ID)))
	if err != nil || res.IsError || len(res.Content) != 1 || !strings.Contains(res.Content[0].Text, "Brakka") {
		t.Fatalf("get_card = %+v, %v", res, err)
	}
	if res, err = mcp.Call(ctx, h, tier.Read, "get_card", json.RawMessage(`{"id": 999999}`)); err != nil || !res.IsError {
		t.Fatalf("get_card of a missing card = %+v, %v; want a tool error", res, err)
	}
	if _, err := mcp.Call(ctx, h, tier.Read, "create_card", json.RawMessage(`{"data": {"name": "X"}}`)); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("create_card with read = %v, want an error naming its tier", err)
	}
	if _, err := mcp.Call(ctx, h, tier.Read, "no_such_tool", nil); err == nil {
		t.Fatal("an unknown tool ran")
	}
}

func TestMCPVariants(t *testing.T) {
	e := newEnv(t)
	s := e.mcpClient(tier.Write)
	var c entityOut
	if err := json.Unmarshal([]byte(tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka"}}, false)), &c); err != nil {
		t.Fatal(err)
	}
	var v entityOut
	if err := json.Unmarshal([]byte(tool(t, s, "fork_card", map[string]any{"id": c.ID}, false)), &v); err != nil {
		t.Fatal(err)
	}
	if v.Slug != "brakka-variant" || v.VariantOf == nil || v.VariantOf.ID != c.ID || v.Revision.Source != "mcp" {
		t.Fatalf("fork_card = %+v", v)
	}
	if got := tool(t, s, "set_variant_of", map[string]any{"id": c.ID, "variant_of": v.ID}, true); !strings.HasPrefix(got, "409") {
		t.Fatalf("cycle = %s", got)
	}
	tool(t, s, "set_variant_of", map[string]any{"kind": "card", "id": v.ID, "variant_of": nil}, false)
	var after entityOut
	e.call("GET", fmt.Sprintf("/cards/%d", v.ID), nil, 200, &after)
	if after.VariantOf != nil {
		t.Fatalf("after unlinking = %+v", after.VariantOf)
	}
}

func TestMCPLibraries(t *testing.T) {
	e := newEnv(t)
	archive := e.createLibrary("Archive")
	s := e.mcpClient(tier.Destructive)
	var libs []library.LibrarySummary
	if err := json.Unmarshal([]byte(tool(t, s, "list_libraries", map[string]any{}, false)), &libs); err != nil || len(libs) != 2 {
		t.Fatalf("list_libraries = %+v, %v", libs, err)
	}
	var c, b, f entityOut
	if err := json.Unmarshal([]byte(tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "Brakka"}, "library": archive.ID}, false)), &c); err != nil || c.LibraryID != archive.ID {
		t.Fatalf("create_card = %+v, %v", c, err)
	}
	if err := json.Unmarshal([]byte(tool(t, s, "create_lorebook", map[string]any{"data": map[string]any{"name": "Lore", "entries": []any{}}, "library": archive.ID}, false)), &b); err != nil || b.LibraryID != archive.ID {
		t.Fatalf("create_lorebook = %+v, %v", b, err)
	}
	if got := tool(t, s, "search_cards", map[string]any{"query": map[string]any{}}, false); strings.Contains(got, "Brakka") {
		t.Fatalf("default search = %s", got)
	}
	if got := tool(t, s, "search_cards", map[string]any{"library": archive.ID}, false); !strings.Contains(got, "Brakka") {
		t.Fatalf("archive search = %s", got)
	}
	if err := json.Unmarshal([]byte(tool(t, s, "fork_card", map[string]any{"id": c.ID, "library": library.DefaultLibrary}, false)), &f); err != nil || f.LibraryID != library.DefaultLibrary {
		t.Fatalf("fork_card = %+v, %v", f, err)
	}
	var j jobOut
	if err := json.Unmarshal([]byte(tool(t, s, "start_import", map[string]any{"instance_id": e.importID, "library": archive.ID}, false)), &j); err != nil ||
		!strings.Contains(string(j.Payload), fmt.Sprintf(`"library":%d`, archive.ID)) {
		t.Fatalf("start_import = %+v, %v", j, err)
	}
	if got := tool(t, s, "create_card", map[string]any{"data": map[string]any{"name": "X"}, "library": 999}, true); !strings.HasPrefix(got, "422") {
		t.Fatalf("missing library = %s", got)
	}
}
