package targets

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func (e *env) newLorebook(name string) int64 {
	e.t.Helper()
	data := fmt.Sprintf(`{"name": %q, "description": "Forge lore.", "scan_depth": 4,
		"entries": [{"keys": ["forge"], "content": "The forge burns hot."}]}`, name)
	lb, _, err := e.lib.Lorebooks.CreateFromName(e.ctx, name, "upload", library.Edit{Data: json.RawMessage(data), Source: "ui"})
	if err != nil {
		e.t.Fatal(err)
	}
	return lb.ID
}

func (e *env) lorebookHead(id int64) library.Revision {
	e.t.Helper()
	lb, err := e.lib.Lorebooks.Get(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	rev, err := e.lib.Lorebooks.Revision(e.ctx, lb.HeadRevisionID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rev
}

func (e *env) updateLorebook(id int64, change func(*library.Edit)) {
	e.t.Helper()
	rev := e.lorebookHead(id)
	ed := library.Edit{Data: rev.Data, Overrides: rev.Overrides, Source: "ui"}
	change(&ed)
	if _, err := e.lib.Lorebooks.Update(e.ctx, id, rev.ID, ed); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) pushLorebook(id int64) Binding {
	e.t.Helper()
	b, err := e.s.PushLorebook(e.ctx, e.inst, id, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) lorebookStatus(id int64) string {
	e.t.Helper()
	list, err := e.s.LorebookBindings(e.ctx, id)
	if err != nil {
		e.t.Fatal(err)
	}
	if len(list) != 1 {
		e.t.Fatalf("lorebook %d has %d bindings", id, len(list))
	}
	return list[0].Status
}

// remoteBook is the lorebook object of the app's copy.
func (e *env) remoteBook(id string) map[string]any {
	e.t.Helper()
	book, _ := e.remote(id)["lorebook"].(map[string]any)
	return book
}

var newEntries = []any{map[string]any{"keys": []any{"anvil"}, "content": "The anvil rings."}}

func TestPushLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	b := e.pushLorebook(lb)
	if b.RemoteID != "l1" || b.LorebookID != lb || b.CardID != 0 || b.Status != "in_sync" {
		t.Fatalf("push: %+v", b)
	}
	if book := e.remoteBook("l1"); book["name"] != "Forge lore" || book["scan_depth"] != nil {
		t.Fatalf("the app got %v", book)
	}
	e.updateLorebook(lb, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "Hotter lore.") })
	if s := e.lorebookStatus(lb); s != "ahead" {
		t.Fatalf("after an edit: %s", s)
	}
	if b = e.pushLorebook(lb); b.RemoteID != "l1" || b.Status != "in_sync" || e.remoteBook("l1")["description"] != "Hotter lore." {
		t.Fatalf("second push: %+v", b)
	}
}

func TestPushLorebookNeedsStandaloneTarget(t *testing.T) {
	e := setup(t, nil)
	if _, err := e.s.PushLorebook(e.ctx, e.inst, e.newLorebook("Forge lore"), false); err == nil {
		t.Fatal("pushed a lorebook to a target that embeds them")
	}
}

func TestCheckAndAdoptLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	b := e.pushLorebook(lb)
	e.editRemote("l1", "entries", newEntries)
	if b = e.check(b); b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "entries" {
		t.Fatalf("check: %+v", b)
	}
	b, err := e.s.Adopt(e.ctx, b.ID, IntoBase, e.lorebookHead(lb).ID)
	if err != nil {
		t.Fatal(err)
	}
	rev := e.lorebookHead(lb)
	var data map[string]any
	_ = json.Unmarshal(rev.Data, &data)
	if rev.Source != "adopt" || fmt.Sprint(data["entries"]) != fmt.Sprint(newEntries) || data["scan_depth"] == nil {
		t.Fatalf("adopted revision: %s %s", rev.Source, rev.Data)
	}
	if b.Status != "ahead" {
		t.Fatalf("after adopt: %+v", b)
	}
}

func TestLorebookOverride(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.updateLorebook(lb, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(fmt.Sprintf(`{%q: {"description": "Lumiverse lore."}}`, e.key()))
	})
	b := e.pushLorebook(lb)
	if e.remoteBook("l1")["description"] != "Lumiverse lore." {
		t.Fatalf("the app got %v", e.remoteBook("l1"))
	}
	e.editRemote("l1", "name", "Forge lore (LV)")
	b = e.check(b)
	if _, err := e.s.Adopt(e.ctx, b.ID, IntoOverride, e.lorebookHead(lb).ID); err != nil {
		t.Fatal(err)
	}
	rev := e.lorebookHead(lb)
	var data map[string]any
	_ = json.Unmarshal(rev.Data, &data)
	if data["name"] != "Forge lore" || data["description"] != "Forge lore." {
		t.Fatalf("the lorebook's own data changed: %s", rev.Data)
	}
	if want := fmt.Sprintf(`{%q: {"description": "Lumiverse lore.", "name": "Forge lore (LV)"}}`, e.key()); !jsonEqual(rev.Overrides, want) {
		t.Fatalf("overrides = %s, want %s", rev.Overrides, want)
	}
}

func jsonEqual(raw json.RawMessage, want string) bool {
	var a, b any
	return json.Unmarshal(raw, &a) == nil && json.Unmarshal([]byte(want), &b) == nil && fmt.Sprint(a) == fmt.Sprint(b)
}
