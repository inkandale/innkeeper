package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/bulk"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/targets"
	"github.com/davidgibbons/innkeeper/protocol"
)

// target configures a fake target app that keeps its cards in a temp dir,
// and returns the instance and the dir.
func (e *testEnv) target() (int64, string) {
	e.t.Helper()
	dir := e.t.TempDir()
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: "Target",
		Config: map[string]any{"dir": dir}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id, dir
}

func TestPushRoutes(t *testing.T) {
	e := newEnv(t)
	inst, dir := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail.", "scenario": "A forge."}}, 201, &c)
	path := fmt.Sprintf("/cards/%d", c.ID)

	var p targets.Preview
	e.call("GET", fmt.Sprintf("%s/push-preview?instance_id=%d", path, inst), nil, 200, &p)
	if len(p.DroppedFields) != 1 || p.DroppedFields[0] != "scenario" || p.NewCopy {
		t.Fatalf("preview = %+v", p)
	}
	e.call("GET", path+"/push-preview", nil, 422, nil)

	var j jobOut
	e.call("POST", path+"/push", map[string]any{"instance_id": inst}, 202, &j)
	if j.Kind != targets.PushKind || *j.InstanceID != inst {
		t.Fatalf("push job = %+v", j)
	}
	e.call("POST", path+"/push", map[string]any{"instance_id": e.importID + 1000}, 404, nil)
	e.call("POST", "/cards/999/push", map[string]any{"instance_id": inst}, 404, nil)
	e.markBroken(e.importID, "boom")
	e.call("POST", path+"/push", map[string]any{"instance_id": e.importID}, 409, nil)

	// No worker runs here, so push directly.
	if _, err := e.s.Targets.Push(context.Background(), library.Card, inst, c.ID, false); err != nil {
		t.Fatal(err)
	}
	var list []targets.Binding
	e.call("GET", path+"/bindings", nil, 200, &list)
	if len(list) != 1 || list[0].Status != "in_sync" || list[0].RemoteID != "r1" {
		t.Fatalf("bindings = %+v", list)
	}
	bpath := fmt.Sprintf("/bindings/%d", list[0].ID)

	// The owner changes the card in the app.
	if err := os.WriteFile(filepath.Join(dir, "r1.json"), []byte(`{"spec": "chara_card_v3", "data": {"name": "Brakka", "first_mes": "Well met."}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var b targets.Binding
	e.call("POST", bpath+"/check", nil, 200, &b)
	if b.Status != "remote_changed" || len(b.Changes) != 1 {
		t.Fatalf("check = %+v", b)
	}

	var pushes []targets.Push
	e.call("GET", bpath+"/pushes", nil, 200, &pushes)
	if len(pushes) != 1 {
		t.Fatalf("pushes = %+v", pushes)
	}
	e.call("DELETE", fmt.Sprintf("/instances/%d", inst), nil, 409, nil)
	e.call("DELETE", bpath, nil, 204, nil)
	e.call("GET", bpath, nil, 404, nil)
	e.call("DELETE", fmt.Sprintf("/instances/%d", inst), nil, 204, nil)
}

func TestBulkPush(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	for _, name := range []string{"Brakka", "Carrow"} {
		e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": name}}, 201, nil)
	}
	e.call("POST", "/pushes", map[string]any{"instance_id": inst, "query": map[string]any{}}, 400, nil)
	var j jobOut
	e.call("POST", "/pushes", map[string]any{"instance_id": inst, "query": map[string]any{"text": "carrow"}}, 202, &j)
	if j.Kind != bulk.Kind || j.Children["queued"] != 1 {
		t.Fatalf("bulk push = %+v", j)
	}

	body := map[string]any{"instance_id": inst, "query": map[string]any{"text": "brakka"}}
	e.call("POST", "/pushes/preview", map[string]any{"instance_id": inst, "query": map[string]any{}}, 400, nil)
	var p targets.BulkPreview
	e.call("POST", "/pushes/preview", body, 200, &p)
	if p.Total != 1 || p.New != 1 || p.Confirm == "" {
		t.Fatalf("preview = %+v", p)
	}
	body["confirm"] = "stale"
	var conflict struct {
		Errors []struct {
			Value targets.BulkPreview `json:"value"`
		} `json:"errors"`
	}
	e.call("POST", "/pushes", body, 409, &conflict)
	if len(conflict.Errors) != 1 || conflict.Errors[0].Value.Confirm != p.Confirm {
		t.Fatalf("409 = %+v", conflict)
	}
	body["confirm"] = p.Confirm
	e.call("POST", "/pushes", body, 202, &j)
}

func TestFailHidesPluginErrorText(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
	}{
		{&pluginhost.StartError{Err: fmt.Errorf("initialize plugin fake: %w", protocol.NewError(-32000, "cannot open /secret", false))}, 503},
		{fmt.Errorf("target.put: %w", protocol.NewError(-32000, "cannot open /secret", false)), 502},
		{protocol.NewError(-32000, "cannot open /secret", true), 503},
		{protocol.NewError(protocol.CodeInvalidParams, "cannot open /secret", false), 400},
		{protocol.NewError(protocol.CodeRemoteNotFound, "cannot open /secret", false), 404},
		{fmt.Errorf("call: %w", context.DeadlineExceeded), 503},
	} {
		he := (&Server{Log: quiet}).fail(c.err).(huma.StatusError)
		if he.GetStatus() != c.status || strings.Contains(he.Error(), "/secret") {
			t.Errorf("fail(%v) = %d %q, want %d", c.err, he.GetStatus(), he.Error(), c.status)
		}
	}
}

func TestBindingRequestChecks(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka"}}, 201, &c)
	if _, err := e.s.Targets.Push(context.Background(), library.Card, inst, c.ID, false); err != nil {
		t.Fatal(err)
	}
	var list []targets.Binding
	e.call("GET", fmt.Sprintf("/cards/%d/bindings", c.ID), nil, 200, &list)
	bpath := fmt.Sprintf("/bindings/%d", list[0].ID)

	e.call("POST", "/bindings/9999/check", nil, 404, nil)
	e.call("GET", "/cards/9999/push-preview?instance_id=1", nil, 404, nil)

	e.markBroken(inst, "leaky-cause-xyz")
	r := e.call("POST", bpath+"/check", nil, 409, nil)
	if strings.Contains(string(r.Body), "leaky-cause-xyz") {
		t.Fatalf("body leaks the cause: %s", r.Body)
	}
}

func TestLorebookPushRoutes(t *testing.T) {
	e := newEnv(t)
	dir := t.TempDir()
	inst, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: "Target",
		Config: map[string]any{"dir": dir, "lorebooks": "standalone"}})
	if err != nil {
		t.Fatal(err)
	}
	var lb entityOut
	e.call("POST", "/lorebooks", map[string]any{"data": map[string]any{"name": "Forge lore", "entries": []any{}}}, 201, &lb)
	path := fmt.Sprintf("/lorebooks/%d", lb.ID)

	var j jobOut
	e.call("POST", path+"/push", map[string]any{"instance_id": inst}, 202, &j)
	if j.Kind != targets.PushKind {
		t.Fatalf("push job = %+v", j)
	}
	e.call("POST", "/lorebooks/999/push", map[string]any{"instance_id": inst}, 404, nil)

	var p targets.Preview
	e.call("GET", fmt.Sprintf("%s/push-preview?instance_id=%d", path, inst), nil, 200, &p)
	if p.Lorebook == nil || p.Replaces {
		t.Fatalf("preview = %+v", p)
	}
	// No worker runs here, so push directly.
	if _, err := e.s.Targets.Push(context.Background(), library.Lorebook, inst, lb.ID, false); err != nil {
		t.Fatal(err)
	}
	var list []targets.Binding
	e.call("GET", path+"/bindings", nil, 200, &list)
	if len(list) != 1 || list[0].LorebookID != lb.ID || list[0].RemoteID != "l1" || list[0].Status != "in_sync" {
		t.Fatalf("bindings = %+v", list)
	}
	e.call("GET", fmt.Sprintf("%s/push-preview?instance_id=%d", path, inst), nil, 200, &p)
	if !p.Replaces || len(p.Changes) != 0 {
		t.Fatalf("preview after push = %+v", p)
	}
}

func TestBulkLorebookPush(t *testing.T) {
	e := newEnv(t)
	var lb entityOut
	e.call("POST", "/lorebooks", map[string]any{"data": map[string]any{"name": "Forge lore", "entries": []any{}}}, 201, &lb)
	body := func(inst int64) map[string]any {
		return map[string]any{"instance_id": inst, "kind": "lorebook", "query": map[string]any{"text": "forge"}}
	}

	embedded, _ := e.target() // the fake keeps lorebooks inside cards
	e.call("POST", "/pushes", body(embedded), 400, nil)

	standalone, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "fake", Name: "Standalone",
		Config: map[string]any{"dir": t.TempDir(), "lorebooks": "standalone"}})
	if err != nil {
		t.Fatal(err)
	}
	var j jobOut
	e.call("POST", "/pushes", body(standalone), 202, &j)
	var children []jobOut
	e.call("GET", fmt.Sprintf("/jobs?parent=%d", j.ID), nil, 200, &children)
	if len(children) != 1 || !strings.Contains(string(children[0].Payload), fmt.Sprintf(`"lorebook_id":%d`, lb.ID)) {
		t.Fatalf("children = %+v", children)
	}
}

func TestPushConfirm(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail."}}, 201, &c)
	path := fmt.Sprintf("/cards/%d", c.ID)
	var p targets.Preview
	e.call("GET", fmt.Sprintf("%s/push-preview?instance_id=%d", path, inst), nil, 200, &p)
	if p.Confirm == "" {
		t.Fatal("preview has no confirm")
	}
	e.call("POST", path+"/push", map[string]any{"instance_id": inst, "confirm": p.Confirm}, 202, nil)

	// The card changes after the preview, so the old confirm is refused.
	e.call("PUT", path, map[string]any{"base_revision_id": c.Revision.ID,
		"data": map[string]any{"name": "Brakka", "first_mes": "Well met."}}, 200, nil)
	r := e.call("POST", path+"/push", map[string]any{"instance_id": inst, "confirm": p.Confirm}, 409, nil)
	if !strings.Contains(string(r.Body), "Well met.") {
		t.Fatalf("refusal doesn't carry the new preview: %s", r.Body)
	}
}

func TestBindRoutes(t *testing.T) {
	e := newEnv(t)
	inst, dir := e.target()
	if err := os.WriteFile(filepath.Join(dir, "r1.json"), []byte(`{"spec": "chara_card_v3", "data": {"name": "Brakka"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka"}}, 201, &c)
	// The items paths before /instances/{id}/{plural} still answer.
	for _, p := range []string{"cards", "items"} {
		var items []targets.Item
		e.call("GET", fmt.Sprintf("/instances/%d/%s", inst, p), nil, 200, &items)
		if len(items) != 1 || items[0].RemoteID != "r1" || items[0].BindingID != nil {
			t.Fatalf("%s = %+v", p, items)
		}
	}
	e.call("GET", fmt.Sprintf("/instances/%d/lorebooks", inst), nil, 400, nil) // the fake keeps lorebooks inside cards
	e.call("GET", fmt.Sprintf("/instances/%d/lorebook-items", inst), nil, 400, nil)
	bind := map[string]any{"instance_id": inst, "remote_id": "r1"}
	path := fmt.Sprintf("/cards/%d/bind", c.ID)
	var b targets.Binding
	e.call("POST", path, bind, 200, &b)
	if b.RemoteID != "r1" || b.Status != "in_sync" {
		t.Fatalf("bind = %+v", b)
	}
	e.call("POST", path, bind, 409, nil)
	e.call("POST", "/cards/999/bind", bind, 404, nil)
	e.call("POST", "/lorebooks/999/bind", bind, 400, nil) // as for lorebook-items
}

func TestImportRefusesAnApp(t *testing.T) {
	e := newEnv(t)
	inst, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "target", Name: "App",
		Config: map[string]any{"dir": t.TempDir()}})
	if err != nil {
		t.Fatal(err)
	}
	r := e.call("POST", "/imports", map[string]any{"instance_id": inst}, 400, nil)
	if !strings.Contains(string(r.Body), "/reconciles") {
		t.Fatalf("body = %s", r.Body)
	}
}

func TestFailCarriesTheFreshComparison(t *testing.T) {
	stale := &targets.StaleError{Item: "card:r1", Comparison: targets.Comparison{RemoteID: "r1", AppHash: "fresh"}}
	he := (&Server{Log: quiet}).fail(fmt.Errorf("resolve: %w", stale)).(huma.StatusError)
	raw, _ := json.Marshal(he)
	if he.GetStatus() != 409 || !strings.Contains(string(raw), `"app_hash":"fresh"`) || !strings.Contains(string(raw), "card:r1") {
		t.Fatalf("fail = %d %s", he.GetStatus(), raw)
	}
}

func TestBindingAutosyncRoute(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka", "first_mes": "Hail."}}, 201, &c)
	b, err := e.s.Targets.Push(context.Background(), library.Card, inst, c.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/bindings/%d", b.ID)
	var got targets.Binding
	e.call("PATCH", path, map[string]any{"autosync": true}, 200, &got)
	if !got.Autosync {
		t.Fatalf("patched = %+v", got)
	}
	e.call("GET", path, nil, 200, &got)
	if !got.Autosync {
		t.Errorf("read back = %+v", got)
	}
	e.call("PATCH", "/bindings/999999", map[string]any{"autosync": true}, 404, nil)
}

func TestScanRoute(t *testing.T) {
	e := newEnv(t)
	var a, b jobOut
	e.call("POST", "/scans", nil, 202, &a)
	e.call("POST", "/scans", nil, 202, &b)
	if a.Kind != targets.ScanKind || b.ID != a.ID {
		t.Errorf("scans = %+v, %+v", a, b)
	}
}

// upstream installs the fake as a pull-only plugin that follows fake sources,
// with one instance. It returns the instance and the folder of site pages.
// args are extra plugin flags, such as -mode.
func (e *testEnv) upstream(args ...string) (int64, string) {
	e.t.Helper()
	up := *e.manifests["target"]
	up.Name, up.Capabilities, up.SourceKinds = "upstream", []string{"pull"}, []string{"fake"}
	up.Command = append([]string{up.Command[0], "-caps=pull", "-name=upstream"}, args...)
	e.manifests["upstream"] = &up
	dir := e.t.TempDir()
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: "upstream", Name: "Sites",
		Config: map[string]any{"dir": dir, "fields": []string{"name", "first_mes"}, "lorebooks": "standalone"}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id, dir
}

// followedCard creates a card with the fake source ref and publishes its site page with first_mes.
func (e *testEnv) followedCard(dir, ref, firstMes string) entityOut {
	e.t.Helper()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Vyr", "first_mes": "Who wakes me?"}}, 201, &c)
	if _, err := e.s.Lib.Cards.AddSource(context.Background(), c.ID, library.Source{Kind: "fake", Ref: ref}); err != nil {
		e.t.Fatal(err)
	}
	e.sitePage(dir, ref, firstMes)
	return c
}

// sitePage writes a source's page on the fake site, as <dir>/fake:<ref>.json.
func (e *testEnv) sitePage(dir, ref, firstMes string) {
	e.t.Helper()
	page := filepath.Join(dir, "fake:"+ref+".json")
	raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0",
		"data": map[string]any{"name": "Vyr", "first_mes": firstMes}})
	if err := os.MkdirAll(filepath.Dir(page), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(page, raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func TestSourceCheckRoutes(t *testing.T) {
	e := newEnv(t)
	_, dir := e.upstream()
	c := e.followedCard(dir, "vyr", "Begone.")
	var list []targets.Binding
	e.call("POST", fmt.Sprintf("/cards/%d/source-check", c.ID), nil, 200, &list)
	if len(list) != 1 || list[0].Push || list[0].RemoteID != "fake:vyr" || list[0].RemoteState != "changed" || list[0].AppHash == "" {
		t.Fatalf("bindings = %+v", list)
	}
	var ch targets.Changes
	e.call("GET", "/sync/changes?state=update", nil, 200, &ch)
	if len(ch.Rows) != 1 || ch.Rows[0].CardID != c.ID || len(ch.Rows[0].Apps) != 1 ||
		ch.Rows[0].Apps[0].State != targets.ChangeUpdate || ch.Rows[0].Apps[0].AppHash != list[0].AppHash {
		t.Fatalf("updates = %+v", ch)
	}
	path := fmt.Sprintf("/bindings/%d", list[0].ID)
	var b targets.Binding
	e.call("POST", path+"/dismiss", map[string]any{"app_hash": list[0].AppHash}, 200, &b)
	if b.RemoteState != "unchanged" {
		t.Fatalf("dismissed = %+v", b)
	}
	e.call("PATCH", path, map[string]any{"paused": true}, 200, &b)
	if !b.Paused || b.Autosync {
		t.Fatalf("paused = %+v", b)
	}
	var a, again jobOut
	e.call("POST", "/source-checks", nil, 202, &a)
	e.call("POST", "/source-checks", nil, 202, &again)
	if a.ID == 0 || a.ID != again.ID || a.Kind != targets.SourceCheckKind {
		t.Fatalf("jobs = %+v, %+v", a, again)
	}
}

func TestDismissingASourceThatChangedAgainIsStale(t *testing.T) {
	e := newEnv(t)
	_, dir := e.upstream()
	c := e.followedCard(dir, "vyr", "Begone.")
	var list []targets.Binding
	e.call("POST", fmt.Sprintf("/cards/%d/source-check", c.ID), nil, 200, &list)
	// The author changes the page again after the owner saw the first change.
	e.sitePage(dir, "vyr", "Begone, again.")
	path := fmt.Sprintf("/bindings/%d", list[0].ID)
	r := e.call("POST", path+"/dismiss", map[string]any{"app_hash": list[0].AppHash}, 409, nil)
	if !strings.Contains(string(r.Body), "Begone, again.") {
		t.Fatalf("stale reply lacks the fresh comparison: %s", r.Body)
	}
	var b targets.Binding
	e.call("GET", path, nil, 200, &b)
	if b.RemoteState != "changed" {
		t.Fatalf("after a stale dismiss = %+v", b)
	}
}

func TestCheckingSourcesOfABrokenPluginFails(t *testing.T) {
	e := newEnv(t)
	_, dir := e.upstream("-mode=bad-describe")
	c := e.followedCard(dir, "vyr", "Begone.")
	e.call("POST", fmt.Sprintf("/cards/%d/source-check", c.ID), nil, 502, nil)
}

// chub refs hold a slash, so the remote_id reaches compare and resolve as %2F.
func TestReviewingASourceWhoseRefHasASlash(t *testing.T) {
	e := newEnv(t)
	inst, dir := e.upstream()
	// The fake keeps a page as <dir>/<remote_id>.json, so the slash makes a folder.
	c := e.followedCard(dir, "alice/vyr", "Begone.")
	var list []targets.Binding
	e.call("POST", fmt.Sprintf("/cards/%d/source-check", c.ID), nil, 200, &list)
	if len(list) != 1 || list[0].RemoteID != "fake:alice/vyr" || list[0].RemoteState != "changed" {
		t.Fatalf("bindings = %+v", list)
	}
	item := fmt.Sprintf("/instances/%d/cards/%s", inst, url.PathEscape("fake:alice/vyr"))
	var cmp targets.Comparison
	e.call("GET", fmt.Sprintf("%s/compare?card_id=%d", item, c.ID), nil, 200, &cmp)
	if cmp.RemoteID != "fake:alice/vyr" || cmp.Base != "accepted" {
		t.Fatalf("comparison = %+v", cmp)
	}
	e.call("POST", item+"/resolve", map[string]any{"action": "bind", "card_id": c.ID, "app_hash": cmp.AppHash,
		"base_revision_id": cmp.HeadRevisionID, "accepted_hash": cmp.AcceptedHash, "default": "app"}, 200, nil)
	e.call("GET", fmt.Sprintf("/cards/%d/bindings", c.ID), nil, 200, &list)
	if len(list) != 1 || list[0].RemoteState != "unchanged" {
		t.Fatalf("after resolving = %+v", list)
	}
}
