package targets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/inkandale/innkeeper/internal/library"
	"github.com/inkandale/innkeeper/internal/merge"
	"github.com/inkandale/innkeeper/internal/plugins"
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
	b, err := e.s.Push(e.ctx, library.Lorebook, e.inst, id, false)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) lorebookStatus(id int64) string {
	e.t.Helper()
	list, err := e.s.Bindings(e.ctx, library.Lorebook, id)
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
	if _, err := e.s.Push(e.ctx, library.Lorebook, e.inst, e.newLorebook("Forge lore"), false); err == nil {
		t.Fatal("pushed a lorebook to a target that embeds them")
	}
}

func TestCheckLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	b := e.pushLorebook(lb)
	e.editRemote("l1", "entries", newEntries)
	if b = e.check(b); b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "entries" {
		t.Fatalf("check: %+v", b)
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
	e.check(b)
	// Both sides renamed it, so keep_in_app, which applies to conflicts, decides the name.
	e.updateLorebook(lb, func(ed *library.Edit) { ed.Data = setField(ed.Data, "name", "Forge lore II") })
	c, err := e.s.CompareItem(e.ctx, library.Lorebook, e.inst, "l1", lb)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Resolve(e.ctx, library.Lorebook, Resolution{InstanceID: e.inst, RemoteID: "l1", AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: lb, Default: merge.ChooseKeepInApp}); err != nil {
		t.Fatal(err)
	}
	rev := e.lorebookHead(lb)
	var data map[string]any
	_ = json.Unmarshal(rev.Data, &data)
	if data["name"] != "Forge lore II" || data["description"] != "Forge lore." {
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

func (e *env) link(card, lorebook int64) {
	e.t.Helper()
	if err := e.lib.LinkLorebook(e.ctx, card, lorebook); err != nil {
		e.t.Fatal(err)
	}
}

func TestCardPushCreatesItsLorebooks(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	a, b := e.newLorebook("Forge lore"), e.newLorebook("Mine lore")
	e.link(e.card, a)
	e.link(e.card, b)
	card := e.push(e.card)
	if e.lorebookStatus(a) != "in_sync" || e.lorebookStatus(b) != "in_sync" {
		t.Fatal("the card push didn't push its lorebooks")
	}
	if got := fmt.Sprint(e.remote(card.RemoteID)["lorebook_remote_ids"]); got != "[l1 l2]" {
		t.Fatalf("the app linked the card to %s", got)
	}
	if got := fmt.Sprint(card.LastPush.LorebookRemoteIDs); got != "[l1 l2]" || card.Status != "in_sync" {
		t.Fatalf("card binding: %+v", card)
	}
	if _, ok := e.remoteData(card.RemoteID)["character_book"]; ok {
		t.Fatal("a standalone target got an embedded lorebook")
	}
}

func TestCardPushLeavesBoundLorebooksAlone(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.link(e.card, lb)
	e.push(e.card)
	e.updateLorebook(lb, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "Hotter lore.") })
	e.push(e.card)
	if e.remoteBook("l1")["description"] != "Forge lore." || e.lorebookStatus(lb) != "ahead" {
		t.Fatal("a card push updated a bound lorebook")
	}
}

func TestCardAheadWhenItsLorebooksChange(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.link(e.card, lb)
	e.push(e.card)
	e.link(e.card, e.newLorebook("Mine lore"))
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after linking a lorebook: %s", s)
	}
	e.push(e.card)
	// The app loses a lorebook; pushing it again gives it a new remote ID.
	if err := os.Remove(filepath.Join(e.dir, "l1.json")); err != nil {
		t.Fatal(err)
	}
	list, _ := e.s.Bindings(e.ctx, library.Lorebook, lb)
	e.check(list[0])
	e.pushLorebook(e.newLorebook("Other lore")) // takes l1 again
	if b := e.pushLorebook(lb); b.RemoteID == "l1" {
		t.Fatalf("re-pushed lorebook kept %s", b.RemoteID)
	}
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("after its lorebook moved: %s", s)
	}
	if e.push(e.card).Status != "in_sync" {
		t.Fatal("pushing the card again didn't bring it in sync")
	}
}

func TestCardPushRerunReusesItsLorebooks(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone", "fail_put": "crash"})
	e.link(e.card, e.newLorebook("Forge lore"))
	if _, err := e.s.push(e.ctx, cardKind, e.inst, e.card, false, "job-1", nil); !errors.Is(err, ErrUncertain) {
		t.Fatalf("first run: %v, want ErrUncertain", err)
	}
	if err := e.reg.UpdateInstance(e.ctx, e.inst, plugins.InstanceEdit{
		Config: map[string]any{"dir": e.dir, "lorebooks": "standalone"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.push(e.ctx, cardKind, e.inst, e.card, false, "job-1", nil); err != nil {
		t.Fatal(err)
	}
	if books, _ := filepath.Glob(filepath.Join(e.dir, "l*.json")); len(books) != 1 {
		t.Fatalf("the app has %d lorebooks, want 1", len(books))
	}
}

// A card push that finds a lorebook bound by a concurrent push uses that copy
// and never updates it.
func TestCreateOnlyLorebookPushLeavesABoundLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.pushLorebook(lb)
	e.updateLorebook(lb, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "Hotter lore.") })
	_, err := e.s.push(e.ctx, lorebookKind, e.inst, lb, false, "k", &pushExpect{createOnly: true})
	if !errors.Is(err, errAlreadyPushed) || e.remoteBook("l1")["description"] != "Forge lore." {
		t.Fatalf("create-only push of a bound lorebook: %v", err)
	}
}
