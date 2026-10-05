package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidgibbons/innkeeper/internal/bulk"
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
	if _, err := e.s.Targets.Push(context.Background(), inst, c.ID, false); err != nil {
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
	e.call("POST", bpath+"/adopt", map[string]any{"into": "base", "base_revision_id": c.Revision.ID + 100}, 409, nil)
	e.call("POST", bpath+"/adopt", map[string]any{"into": "base", "base_revision_id": c.Revision.ID}, 200, &b)
	if b.Status != "ahead" {
		t.Fatalf("adopt = %+v", b)
	}
	e.call("POST", bpath+"/adopt", map[string]any{"into": "ignore"}, 409, nil)
	e.call("GET", path, nil, 200, &c)
	if c.Revision.Source != "adopt" {
		t.Fatalf("card after adopt = %+v", c.Revision)
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
}

func TestFailHidesStartError(t *testing.T) {
	err := &pluginhost.StartError{Err: fmt.Errorf("initialize plugin fake: %w", protocol.NewError(-32000, "cannot open /secret", false))}
	he := (&Server{Log: quiet}).fail(err).(huma.StatusError)
	if he.GetStatus() != 502 || strings.Contains(he.Error(), "/secret") {
		t.Fatalf("fail = %d %q", he.GetStatus(), he.Error())
	}
}

func TestBindingRequestChecks(t *testing.T) {
	e := newEnv(t)
	inst, _ := e.target()
	var c entityOut
	e.call("POST", "/cards", map[string]any{"data": map[string]any{"name": "Brakka"}}, 201, &c)
	if _, err := e.s.Targets.Push(context.Background(), inst, c.ID, false); err != nil {
		t.Fatal(err)
	}
	var list []targets.Binding
	e.call("GET", fmt.Sprintf("/cards/%d/bindings", c.ID), nil, 200, &list)
	bpath := fmt.Sprintf("/bindings/%d", list[0].ID)

	e.call("POST", bpath+"/adopt", map[string]any{"into": "base"}, 400, nil)
	e.call("POST", "/bindings/9999/check", nil, 404, nil)
	e.call("POST", "/bindings/9999/adopt", map[string]any{"into": "ignore"}, 404, nil)
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
	if _, err := e.s.Targets.PushLorebook(context.Background(), inst, lb.ID, false); err != nil {
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
	var items []targets.Item
	e.call("GET", fmt.Sprintf("/instances/%d/items", inst), nil, 200, &items)
	if len(items) != 1 || items[0].RemoteID != "r1" || items[0].BindingID != nil {
		t.Fatalf("items = %+v", items)
	}
	e.call("GET", fmt.Sprintf("/instances/%d/lorebook-items", inst), nil, 400, nil) // the fake keeps lorebooks inside cards
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
