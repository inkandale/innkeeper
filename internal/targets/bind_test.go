package targets

import (
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

// appCopy pushes card and unbinds it, leaving a copy the app has but the
// library doesn't know about. It returns the copy's remote ID.
func (e *env) appCopy(card int64) string {
	e.t.Helper()
	b := e.push(card)
	if err := e.s.Unbind(e.ctx, b.ID); err != nil {
		e.t.Fatal(err)
	}
	return b.RemoteID
}

func (e *env) bind(card int64, remoteID string) Binding {
	e.t.Helper()
	b, err := e.s.Bind(e.ctx, e.inst, card, remoteID)
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func TestBindMatchingItem(t *testing.T) {
	e := setup(t, nil)
	b := e.bind(e.card, e.appCopy(e.card))
	if b.Status != "in_sync" || b.LastPush.Kind != "bind" || b.LastPush.RevisionID != e.head(e.card).ID {
		t.Fatalf("binding = %+v", b)
	}
	pushes, err := e.s.Pushes(e.ctx, b.ID)
	if err != nil || len(pushes) != 1 || pushes[0].Kind != "bind" {
		t.Fatalf("pushes = %+v, %v", pushes, err)
	}
}

// boundChanged binds the card to an app copy whose description differs.
func boundChanged(t *testing.T) (*env, Binding) {
	e := setup(t, nil)
	remoteID := e.appCopy(e.card)
	e.editRemote(remoteID, "description", "A tired smith.")
	b := e.bind(e.card, remoteID)
	if b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "description" ||
		string(b.Changes[0].After) != `"A tired smith."` {
		t.Fatalf("binding = %+v", b)
	}
	return e, b
}

func TestBindDifferingItemThenAdopt(t *testing.T) {
	e, b := boundChanged(t)
	b, err := e.s.Adopt(e.ctx, b.ID, IntoBase, e.head(e.card).ID)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(e.head(e.card).Data, `{"name": "Brakka", "description": "A tired smith.", "first_mes": "Hail.",
		"scenario": "A forge."}`) {
		t.Fatalf("head = %s", e.head(e.card).Data)
	}
	// The library changed since the bind; a push would send the same thing.
	if b.Status != "ahead" {
		t.Fatalf("status = %s", b.Status)
	}
}

func TestBindDifferingItemThenPush(t *testing.T) {
	e, b := boundChanged(t)
	p, err := e.s.Preview(e.ctx, e.inst, e.card)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Replaces || len(p.Changes) != 1 || p.Changes[0].Field != "description" ||
		string(p.Changes[0].After) != `"A dwarf smith."` {
		t.Fatalf("preview = %+v", p)
	}
	b = e.push(e.card)
	if b.Status != "in_sync" || b.LastPush.Kind != "push" || e.remoteData(b.RemoteID)["description"] != "A dwarf smith." {
		t.Fatalf("binding = %+v", b)
	}
}

func TestBindThenLibraryChanges(t *testing.T) {
	e := setup(t, nil)
	b := e.bind(e.card, e.appCopy(e.card))
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("status = %s, want ahead", s)
	}
	e.editRemote(b.RemoteID, "description", "A tired smith.")
	if b = e.check(b); b.Status != "diverged" {
		t.Fatalf("status = %s, want diverged", b.Status)
	}
}

func TestBindCardWithStandaloneLorebooks(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.link(e.card, lb)
	// The app links its copy to the lorebook's bound copy, as the library does.
	b := e.bind(e.card, e.appCopy(e.card))
	if b.Status != "in_sync" || len(b.LastPush.LorebookRemoteIDs) != 1 {
		t.Fatalf("binding = %+v", b)
	}
	lorebooks, err := e.s.LorebookBindings(e.ctx, lb)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.Unbind(e.ctx, lorebooks[0].ID); err != nil {
		t.Fatal(err)
	}
	if s := e.status(e.card); s != "ahead" {
		t.Fatalf("card status after unbinding its lorebook = %s, want ahead", s)
	}
	lbb, err := e.s.BindLorebook(e.ctx, e.inst, lb, lorebooks[0].RemoteID)
	if err != nil || lbb.Status != "in_sync" || lbb.LastPush.Kind != "bind" {
		t.Fatalf("lorebook binding = %+v, %v", lbb, err)
	}
	if s := e.status(e.card); s != "in_sync" {
		t.Fatalf("card status = %s, want in_sync", s)
	}
}

func TestBindCardWithoutReportedLinks(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	lb := e.newLorebook("Forge lore")
	e.link(e.card, lb)
	remoteID := e.appCopy(e.card)
	e.forgetLinks(remoteID)
	if b := e.bind(e.card, remoteID); b.Status != "ahead" || b.RemoteState != "unchanged" {
		t.Fatalf("binding = %+v", b)
	}
}

func TestBindRefusals(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Durza")
	free := e.appCopy(other)
	bound := e.push(e.card)
	if _, err := e.s.Bind(e.ctx, e.inst, e.card, free); !errors.Is(err, ErrBound) {
		t.Fatalf("binding a bound card: %v", err)
	}
	if _, err := e.s.Bind(e.ctx, e.inst, other, bound.RemoteID); !errors.Is(err, ErrBound) {
		t.Fatalf("binding to a bound app item: %v", err)
	}
	if _, err := e.s.Bind(e.ctx, e.inst, other, "r99"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("binding to a missing app item: %v", err)
	}
	lb := e.newLorebook("Forge lore")
	if _, err := e.s.BindLorebook(e.ctx, e.inst, lb, free); !errors.Is(err, ErrCannot) {
		t.Fatalf("binding a lorebook on an embedding target: %v", err)
	}

	e = setup(t, map[string]any{"pull": false})
	if _, err := e.s.Bind(e.ctx, e.inst, e.card, e.appCopy(e.card)); !errors.Is(err, ErrCannot) {
		t.Fatalf("binding on a target without pull: %v", err)
	}
	if _, err := e.s.Items(e.ctx, e.inst); !errors.Is(err, ErrCannot) {
		t.Fatalf("listing a target without pull: %v", err)
	}
}

func TestItems(t *testing.T) {
	e := setup(t, nil)
	free := e.appCopy(e.newCard("Durza"))
	b := e.push(e.card)
	items, err := e.s.Items(e.ctx, e.inst)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	for _, it := range items {
		switch {
		case it.RemoteID == free && it.Name == "Durza" && it.BindingID == nil:
		case it.RemoteID == b.RemoteID && it.Name == "Brakka" && it.BindingID != nil && *it.BindingID == b.ID:
		default:
			t.Fatalf("item = %+v", it)
		}
	}
}

func TestBindDetectsSources(t *testing.T) {
	e := setup(t, nil)
	remoteID := e.appCopy(e.card)
	e.editRemote(remoteID, "extensions", map[string]any{"chub": map[string]any{"id": 7, "full_path": "u/a"}})
	e.bind(e.card, remoteID)
	got, err := e.lib.Cards.Sources(e.ctx, e.card)
	if err != nil || len(got) != 1 || got[0].Ref != "u/a" || got[0].ExternalID != "7" {
		t.Fatalf("sources = %+v, %v", got, err)
	}
}
