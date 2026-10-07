package targets

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/merge"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

const (
	libGreeting = "Hail, traveler. The forge is hot today. Mind the sparks."
	appGreeting = "Hail, traveler. The forge is cold today. Mind the sparks."
)

// resolveEnv has a library card and an unbound app item "lv1" that differs
// from it in one first_mes sentence and an app-only extensions.lumiverse_ui.
func resolveEnv(t *testing.T) (*env, int64) {
	t.Helper()
	e := setup(t, map[string]any{"fields": []string{"name", "description", "first_mes", "extensions"}})
	card := e.libCard(map[string]any{"name": "Tova", "description": "A smith.", "first_mes": libGreeting})
	e.appCard("lv1", map[string]any{"name": "Tova", "description": "A smith.", "first_mes": appGreeting,
		"extensions": map[string]any{"lumiverse_ui": map[string]any{"theme": "dark"}}}, nil)
	return e, card
}

func (e *env) compareItem(remoteID string, card int64) Comparison {
	e.t.Helper()
	c, err := e.s.CompareItem(e.ctx, e.inst, remoteID, card)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

// hunk is the ID of the first hunk of field path that isn't same.
func hunk(t *testing.T, c Comparison, path string) string {
	t.Helper()
	for _, f := range c.Fields {
		for _, h := range f.Hunks {
			if f.Path == path && h.Status != merge.Same {
				return h.ID
			}
		}
	}
	t.Fatalf("no %s hunk in %+v", path, c.Fields)
	return ""
}

func (e *env) resolve(c Comparison, choices map[string]merge.Choice, def merge.Choice) ResolveResult {
	e.t.Helper()
	res, err := e.tryResolve(c, choices, def)
	if err != nil {
		e.t.Fatal(err)
	}
	return res
}

func (e *env) tryResolve(c Comparison, choices map[string]merge.Choice, def merge.Choice) (ResolveResult, error) {
	return e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: c.RemoteID, AppHash: c.AppHash,
		BaseRevisionID: c.HeadRevisionID, AcceptedHash: c.AcceptedHash, Action: "bind", CardID: c.CardID, Choices: choices, Default: def})
}

func (e *env) revisions(card int64) int {
	e.t.Helper()
	h, err := e.lib.Cards.History(e.ctx, card)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(h)
}

func (e *env) accepted(card int64) map[string]any {
	e.t.Helper()
	var m map[string]any
	if err := e.pool.QueryRow(e.ctx, `SELECT accepted_data FROM binding WHERE card_id = $1`, card).Scan(&m); err != nil {
		e.t.Fatal(err)
	}
	return m
}

// override is the head's override for e.inst, or nil.
func (e *env) override(card int64) map[string]any {
	e.t.Helper()
	var all map[string]map[string]any
	if err := json.Unmarshal(e.head(card).Overrides, &all); err != nil {
		e.t.Fatal(err)
	}
	return all[e.key()]
}

func field(t *testing.T, data json.RawMessage, key string) any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m[key]
}

func TestResolveDefaultLibrary(t *testing.T) {
	e, card := resolveEnv(t)
	res := e.resolve(e.compareItem("lv1", card), nil, merge.ChooseLibrary)
	if res.RevisionID != 0 || res.BindingID == 0 || res.CardID != card || e.revisions(card) != 1 {
		t.Fatalf("result = %+v, %d revisions", res, e.revisions(card))
	}
	if a := e.accepted(card); a["first_mes"] != appGreeting || a["extensions"] == nil {
		t.Errorf("accepted = %v", a)
	}
	if s := e.status(card); s != "ahead" {
		t.Fatalf("status = %s, want ahead", s)
	}
	if b := e.push(card); b.Status != "in_sync" || e.remoteData("lv1")["first_mes"] != libGreeting {
		t.Errorf("after push: %s, %v", b.Status, e.remoteData("lv1"))
	}
}

func TestResolveTakesAppHunkAndKeepsInApp(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	res := e.resolve(c, map[string]merge.Choice{hunk(t, c, "first_mes"): merge.ChooseApp,
		hunk(t, c, "extensions"): merge.ChooseKeepInApp}, "")
	head := e.head(card)
	if res.RevisionID != head.ID || head.Source != "reconcile" || head.Message != "Reconcile with Fake app" ||
		field(t, head.Data, "first_mes") != appGreeting || field(t, head.Data, "extensions") != nil {
		t.Fatalf("head = %+v", head)
	}
	if o := e.override(card); string(mustMarshal(o)) != `{"extensions":{"lumiverse_ui":{"theme":"dark"}}}` {
		t.Errorf("override = %v", o)
	}
	if s := e.status(card); s != "in_sync" {
		t.Errorf("status = %s, want in_sync", s)
	}
}

// otherInstance adds a second fake instance that stores the same fields.
func (e *env) otherInstance() int64 {
	e.t.Helper()
	id, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "fake", Name: "Other app",
		Config: map[string]any{"dir": e.t.TempDir(), "fields": []string{"name", "description", "first_mes", "extensions"}}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func TestResolveKeepInAppIsThisInstanceOnly(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	e.resolve(c, map[string]merge.Choice{hunk(t, c, "extensions"): merge.ChooseKeepInApp}, "")
	if o := e.override(card); o == nil || !strings.Contains(string(mustMarshal(o)), "lumiverse_ui") {
		t.Fatalf("override = %v", o)
	}
	if p := e.preview(card); !strings.Contains(string(p.Card), "lumiverse_ui") {
		t.Errorf("this instance's preview = %s", p.Card)
	}
	p, err := e.s.Preview(e.ctx, e.otherInstance(), card)
	if err != nil || strings.Contains(string(p.Card), "lumiverse_ui") {
		t.Errorf("other preview = %s, %v", p.Card, err)
	}
	// The library sentence stays, so this app is still behind.
	if s := e.status(card); s != "ahead" {
		t.Errorf("status = %s, want ahead", s)
	}
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestResolveAvatar(t *testing.T) {
	e, card := resolveEnv(t)
	e.setAvatar(card, patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) }))
	e.appCard("pic", map[string]any{"name": "Tova", "description": "A smith.", "first_mes": libGreeting},
		map[string]any{"avatar": patternPNG(t, func(x, y int) uint8 { return uint8(x + y) })})
	c := e.compareItem("pic", card)
	if c.Avatar == nil {
		t.Fatal("avatars don't differ")
	}
	if _, err := e.tryResolve(c, map[string]merge.Choice{"avatar": merge.ChooseKeepInApp}, ""); !errors.Is(err, merge.ErrBadChoice) {
		t.Errorf("keep_in_app on avatar: %v", err)
	}
	// Keeping the library's avatar leaves the app behind.
	if e.resolve(c, nil, ""); e.status(card) != "ahead" {
		t.Errorf("status = %s, want ahead", e.status(card))
	}
	c = e.compareItem("pic", card)
	e.resolve(c, map[string]merge.Choice{"avatar": merge.ChooseApp}, "")
	if a := e.head(card).AvatarBlob; a == nil || *a != c.Avatar.App {
		t.Errorf("avatar = %v, want %s", a, c.Avatar.App)
	}
	if s := e.status(card); s != "in_sync" {
		t.Errorf("status = %s, want in_sync", s)
	}
}

func TestResolveToAnyCard(t *testing.T) {
	e, _ := resolveEnv(t)
	owl := e.libCard(map[string]any{"name": "Pell", "description": "A clockwork owl that reads the stars."})
	res := e.resolve(e.compareItem("lv1", owl), nil, merge.ChooseApp)
	if res.CardID != owl || field(t, e.head(owl).Data, "first_mes") != appGreeting || e.status(owl) != "in_sync" {
		t.Errorf("result = %+v, head = %s, status %s", res, e.head(owl).Data, e.status(owl))
	}
}

func TestResolveStale(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	e.editRemote("lv1", "first_mes", "Go away.")
	_, err := e.tryResolve(c, nil, "")
	var stale *StaleError
	if !errors.As(err, &stale) || !strings.Contains(string(mustMarshal(stale.Comparison.Fields)), "Go away.") {
		t.Fatalf("err = %v", err)
	}
	if e.revisions(card) != 1 || e.bindingCount() != 0 {
		t.Error("a stale resolve wrote something")
	}
}

func (e *env) bindingCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM binding`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestResolveUnknownHunkWritesNothing(t *testing.T) {
	e, card := resolveEnv(t)
	_, err := e.tryResolve(e.compareItem("lv1", card), map[string]merge.Choice{"description#9": merge.ChooseApp}, "")
	if !errors.Is(err, merge.ErrUnknownHunk) || e.revisions(card) != 1 || e.bindingCount() != 0 {
		t.Fatalf("err = %v, %d revisions, %d bindings", err, e.revisions(card), e.bindingCount())
	}
}

func TestResolveRaceWithAnotherBindRollsBack(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	e.bind(e.card, "lv1")
	_, err := e.tryResolve(c, map[string]merge.Choice{hunk(t, c, "first_mes"): merge.ChooseApp}, "")
	if !errors.Is(err, ErrBound) || e.revisions(card) != 1 {
		t.Fatalf("err = %v, %d revisions", err, e.revisions(card))
	}
}

func TestResolveBoundItemMergesFromAncestor(t *testing.T) {
	e, card := resolveEnv(t)
	// An earlier keep_in_app choice for this instance.
	e.update(card, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"extensions": {"lumiverse_ui": {"theme": "light"}}}}`)
	})
	b := e.push(card)
	e.editRemote(b.RemoteID, "first_mes", appGreeting)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grim smith.") })
	c := e.compareItem(b.RemoteID, 0)
	if c.Base != "accepted" {
		t.Fatalf("base = %s", c.Base)
	}
	res := e.resolve(c, nil, merge.ChooseLibrary)
	head := e.head(card)
	if res.RevisionID != head.ID || field(t, head.Data, "first_mes") != appGreeting ||
		field(t, head.Data, "description") != "A grim smith." {
		t.Fatalf("head = %s", head.Data)
	}
	if o := e.override(card); !strings.Contains(string(mustMarshal(o)), "light") {
		t.Errorf("override = %v", o)
	}
	if s := e.status(card); s != "ahead" {
		t.Errorf("status = %s, want ahead", s)
	}
}

func TestResolveInAPinnedFieldChangesOnlyTheOverride(t *testing.T) {
	e, card := resolveEnv(t)
	pinned := "Hail, friend. The forge is hot today. Mind the sparks."
	e.update(card, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"first_mes": ` + mustJSON(pinned) + `}}`)
	})
	want := "Hail, friend. The forge is cold today. Mind the sparks."
	e.editRemote("lv1", "first_mes", want)
	c := e.compareItem("lv1", card)
	e.resolve(c, map[string]merge.Choice{hunk(t, c, "first_mes"): merge.ChooseApp}, "")
	head := e.head(card)
	if field(t, head.Data, "first_mes") != libGreeting || e.override(card)["first_mes"] != want {
		t.Fatalf("head = %s, override = %v", head.Data, e.override(card))
	}
	p, err := e.s.Preview(e.ctx, e.otherInstance(), card)
	if err != nil || !strings.Contains(string(p.Card), libGreeting) {
		t.Errorf("other preview = %s, %v", p.Card, err)
	}
}

func TestResolveAfterTheHeadMovedIsStale(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grim smith.") })
	_, err := e.tryResolve(c, nil, "")
	var stale *StaleError
	if !errors.As(err, &stale) || stale.Comparison.HeadRevisionID != e.head(card).ID {
		t.Fatalf("err = %v", err)
	}
	if e.revisions(card) != 2 || e.bindingCount() != 0 {
		t.Error("a stale resolve wrote something")
	}
}

func TestResolveNeedsABaseRevision(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	c.HeadRevisionID = 0
	if _, err := e.tryResolve(c, nil, ""); !errors.Is(err, library.ErrInvalid) || e.bindingCount() != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveAvatarChoiceNeedsAnAvatarDiff(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	_, err := e.tryResolve(c, map[string]merge.Choice{"avatar": merge.ChooseApp}, "")
	if !errors.Is(err, merge.ErrUnknownHunk) || e.bindingCount() != 0 {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveWithAnOutdatedBindingIsStale(t *testing.T) {
	for name, between := range map[string]func(e *env, c Comparison, card int64){
		"another tab": func(e *env, c Comparison, _ int64) { e.resolve(c, nil, merge.ChooseLibrary) },
		"same resolve": func(e *env, c Comparison, _ int64) {
			e.resolve(c, map[string]merge.Choice{hunk(t, c, "first_mes"): merge.ChooseApp}, "")
		},
		"old bind": func(e *env, _ Comparison, card int64) { e.bind(card, "lv1") },
	} {
		t.Run(name, func(t *testing.T) {
			e, card := resolveEnv(t)
			c := e.compareItem("lv1", card)
			between(e, c, card)
			revs := e.revisions(card)
			_, err := e.tryResolve(c, map[string]merge.Choice{hunk(t, c, "first_mes"): merge.ChooseApp}, "")
			var stale *StaleError
			if !errors.As(err, &stale) || stale.Comparison.AcceptedHash == "" || e.revisions(card) != revs {
				t.Fatalf("err = %v, %d revisions", err, e.revisions(card))
			}
		})
	}
}

func (e *env) version(bindingID int64) int64 {
	e.t.Helper()
	var v int64
	if err := e.pool.QueryRow(e.ctx, `SELECT version FROM binding WHERE id = $1`, bindingID).Scan(&v); err != nil {
		e.t.Fatal(err)
	}
	return v
}

func TestResolveThatChangesNothingWritesNothing(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	v := e.version(b.ID)
	res := e.resolve(e.compareItem(b.RemoteID, 0), nil, merge.ChooseLibrary)
	pushes, err := e.s.Pushes(e.ctx, b.ID)
	if err != nil || res.BindingID != b.ID || res.RevisionID != 0 || len(pushes) != 1 || e.version(b.ID) != v {
		t.Fatalf("result = %+v, %d pushes, version %d -> %d, %v", res, len(pushes), v, e.version(b.ID), err)
	}
}

func TestResolvePinsOnlyTheOverriddenSetting(t *testing.T) {
	e, _ := resolveEnv(t)
	card := e.libCard(map[string]any{"name": "Ula", "description": "A smith.", "extensions": map[string]any{"depth": 2}})
	e.update(card, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"extensions": {"lumiverse_ui": "light"}}}`)
	})
	e.appCard("lv2", map[string]any{"name": "Ula", "description": "A smith.",
		"extensions": map[string]any{"depth": 4, "lumiverse_ui": "light"}}, nil)
	c := e.compareItem("lv2", card)
	e.resolve(c, map[string]merge.Choice{hunk(t, c, "extensions"): merge.ChooseApp}, "")
	ext, _ := field(t, e.head(card).Data, "extensions").(map[string]any)
	if ext["depth"] != float64(4) || string(mustMarshal(e.override(card))) != `{"extensions":{"lumiverse_ui":"light"}}` {
		t.Fatalf("extensions = %v, override = %v", ext, e.override(card))
	}
}

func TestResolveKeepsInAppOnAPinnedList(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "tags"}})
	for i, explicit := range []bool{true, false} {
		id := fmt.Sprintf("t%d", i)
		card := e.libCard(map[string]any{"name": "Vel" + id, "description": "A bard.", "tags": []string{"a", "b"}})
		e.update(card, func(ed *library.Edit) {
			ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"tags": ["a", "b", "c"]}}`)
		})
		e.appCard(id, map[string]any{"name": "Vel" + id, "description": "A bard.", "tags": []string{"a", "b", "c", "d"}}, nil)
		c := e.compareItem(id, card)
		var choices map[string]merge.Choice
		def := merge.ChooseKeepInApp
		if explicit {
			choices, def = map[string]merge.Choice{hunk(t, c, "tags"): merge.ChooseKeepInApp}, ""
		}
		if _, err := e.tryResolve(c, choices, def); err != nil {
			t.Fatalf("explicit %v: %v", explicit, err)
		}
		tags := string(mustMarshal(field(t, e.head(card).Data, "tags")))
		if over := string(mustMarshal(e.override(card)["tags"])); tags != `["a","b"]` || over != `["a","b","c","d"]` {
			t.Errorf("explicit %v: base tags %s, override tags %s", explicit, tags, over)
		}
	}
}

func TestResolveAppAvatarNeedsOne(t *testing.T) {
	e, card := resolveEnv(t)
	e.setAvatar(card, patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) }))
	c := e.compareItem("lv1", card)
	if c.Avatar == nil {
		t.Fatal("avatars don't differ")
	}
	if _, err := e.tryResolve(c, map[string]merge.Choice{"avatar": merge.ChooseApp}, ""); !errors.Is(err, merge.ErrBadChoice) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveRebindsACardWhoseCopyWentMissing(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET remote_state = 'missing' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	res := e.resolve(e.compareItem("lv1", card), nil, merge.ChooseApp)
	list, err := e.s.Bindings(e.ctx, card)
	if err != nil || res.BindingID != b.ID || len(list) != 1 || list[0].RemoteID != "lv1" || list[0].Status != "in_sync" {
		t.Fatalf("result = %+v, bindings = %+v, %v", res, list, err)
	}
}

func TestResolveGoneItemIsNotFound(t *testing.T) {
	e, card := resolveEnv(t)
	if !errors.Is(ErrNotFound, library.ErrNotFound) {
		t.Error("targets.ErrNotFound isn't a library.ErrNotFound")
	}
	if _, err := e.s.CompareItem(e.ctx, e.inst, "gone", card); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("compare: %v", err)
	}
	c := e.compareItem("lv1", card)
	c.RemoteID = "gone"
	if _, err := e.tryResolve(c, nil, ""); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("resolve: %v", err)
	}
}

func TestResolveKeepsOverridesOfUnstoredFields(t *testing.T) {
	e, card := resolveEnv(t)
	e.update(card, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"scenario": "A cold forge."}}`)
	})
	c := e.compareItem("lv1", card)
	if res := e.resolve(c, map[string]merge.Choice{hunk(t, c, "first_mes"): merge.ChooseApp}, ""); res.RevisionID == 0 {
		t.Fatal("no revision")
	}
	if o := e.override(card); o["scenario"] != "A cold forge." {
		t.Errorf("override = %v", o)
	}
}

func TestResolveClearsAChangedBinding(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	e.editRemote(b.RemoteID, "first_mes", appGreeting)
	if b = e.check(b); b.Status != "remote_changed" {
		t.Fatalf("status = %s", b.Status)
	}
	// The app's edit merges in from the ancestor, so nothing is left to push.
	e.resolve(e.compareItem(b.RemoteID, 0), nil, merge.ChooseLibrary)
	b, err := e.s.Binding(e.ctx, b.ID)
	if err != nil || b.RemoteState != "unchanged" || len(b.Changes) != 0 || b.Status != "in_sync" {
		t.Fatalf("binding = %+v, %v", b, err)
	}
	var seen bool
	if err := e.pool.QueryRow(e.ctx, `SELECT seen_data IS NOT NULL FROM binding WHERE id = $1`, b.ID).Scan(&seen); err != nil || seen {
		t.Errorf("seen_data kept: %v", err)
	}
}

func TestResolveOntoACardBoundElsewhere(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	v := e.version(b.ID)
	_, err := e.tryResolve(e.compareItem("lv1", card), nil, merge.ChooseApp)
	after, berr := e.s.Binding(e.ctx, b.ID)
	if !errors.Is(err, ErrBound) || berr != nil || after.RemoteID != b.RemoteID || e.version(b.ID) != v || e.revisions(card) != 1 {
		t.Fatalf("err = %v, binding = %+v, %v", err, after, berr)
	}
}

func TestResolveLorebook(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	for i, def := range []merge.Choice{merge.ChooseLibrary, merge.ChooseApp, merge.ChooseKeepInApp} {
		lb := e.newLorebook(fmt.Sprintf("Forge %d", i))
		id := fmt.Sprintf("l%d", i)
		e.writeRemote(id, map[string]any{"lorebook": map[string]any{"name": fmt.Sprintf("Forge %d", i),
			"description": "Old lore.", "entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}}})
		c, err := e.s.CompareLorebookItem(e.ctx, e.inst, id, lb)
		if err != nil {
			t.Fatal(err)
		}
		r := Resolution{InstanceID: e.inst, RemoteID: id, AppHash: c.AppHash, BaseRevisionID: c.HeadRevisionID,
			Action: "bind", CardID: lb, Default: def}
		r.Choices = map[string]merge.Choice{"avatar": merge.ChooseApp}
		if _, err := e.s.ResolveLorebook(e.ctx, r); !errors.Is(err, merge.ErrUnknownHunk) {
			t.Errorf("avatar on a lorebook: %v", err)
		}
		r.Choices = nil
		if _, err := e.s.ResolveLorebook(e.ctx, r); err != nil {
			t.Fatalf("%s: %v", def, err)
		}
		head := e.lorebookHead(lb)
		var over map[string]map[string]any
		_ = json.Unmarshal(head.Overrides, &over)
		desc, want, wantOver := field(t, head.Data, "description"), map[merge.Choice]string{merge.ChooseApp: "Old lore."}[def], ""
		if want == "" {
			want = "Forge lore."
		}
		if def == merge.ChooseKeepInApp {
			wantOver = "Old lore."
		}
		got, _ := over[e.key()]["description"].(string)
		if desc != want || got != wantOver {
			t.Errorf("%s: description %v, override %q", def, desc, got)
		}
		if s, want := e.lorebookStatus(lb), map[bool]string{true: "ahead", false: "in_sync"}[def == merge.ChooseLibrary]; s != want {
			t.Errorf("%s: status %s, want %s", def, s, want)
		}
	}
}

func TestResolveRecordsSources(t *testing.T) {
	e, card := resolveEnv(t)
	e.appCard("src", map[string]any{"name": "Tova", "description": "A smith.", "first_mes": libGreeting,
		"extensions": map[string]any{"chub": map[string]any{"full_path": "someone/tova"}}}, nil)
	e.resolve(e.compareItem("src", card), nil, "")
	if srcs, err := e.lib.Cards.Sources(e.ctx, card); err != nil || len(srcs) != 1 || srcs[0].Kind != "chub" {
		t.Fatalf("sources = %+v, %v", srcs, err)
	}
}

func TestResolveRecordsTheAppsLorebookLinks(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	card := e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
	e.appCard("lv1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l2", "l1"}})
	res := e.resolve(e.compareItem("lv1", card), nil, "")
	b, err := e.s.Binding(e.ctx, res.BindingID)
	if err != nil || strings.Join(b.LastPush.LorebookRemoteIDs, ",") != "l1,l2" {
		t.Fatalf("last push = %+v, %v", b.LastPush, err)
	}
}

func TestResolveKeepsInAppOnAListTheOverrideDeletes(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "tags"}})
	card := e.libCard(map[string]any{"name": "Vel", "description": "A bard.", "tags": []string{"a", "b"}})
	e.update(card, func(ed *library.Edit) { ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"tags": null}}`) })
	e.appCard("t", map[string]any{"name": "Vel", "description": "A bard.", "tags": []string{"a", "b", "c"}}, nil)
	c := e.compareItem("t", card)
	if _, err := e.tryResolve(c, map[string]merge.Choice{hunk(t, c, "tags"): merge.ChooseKeepInApp}, ""); err != nil {
		t.Fatal(err)
	}
	tags := string(mustMarshal(field(t, e.head(card).Data, "tags")))
	if over := string(mustMarshal(e.override(card)["tags"])); tags != `["a","b"]` || over != `["a","b","c"]` {
		t.Errorf("base tags %s, override tags %s", tags, over)
	}
}

func TestResolveLeavesUnpinnedListsAlone(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "tags"}})
	card := e.libCard(map[string]any{"name": "Vel", "description": "A bard.", "tags": []string{"a", "b"}})
	e.update(card, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"description": "A loud bard."}}`)
	})
	e.appCard("t", map[string]any{"name": "Vel", "description": "A loud bard.", "tags": []string{"a", "b", "c"}}, nil)
	c := e.compareItem("t", card)
	if _, err := e.tryResolve(c, map[string]merge.Choice{hunk(t, c, "tags"): merge.ChooseKeepInApp}, ""); !errors.Is(err, merge.ErrBadChoice) {
		t.Errorf("explicit keep_in_app: %v", err)
	}
	e.resolve(c, nil, merge.ChooseKeepInApp)
	if tags := string(mustMarshal(field(t, e.head(card).Data, "tags"))); tags != `["a","b"]` || e.override(card)["tags"] != nil {
		t.Errorf("base tags %s, override %v", tags, e.override(card))
	}
}

func TestResolveRestoresAKeyTheOverrideDeletes(t *testing.T) {
	e, _ := resolveEnv(t)
	card := e.libCard(map[string]any{"name": "Ula", "description": "A smith.", "extensions": map[string]any{"x": 1, "depth": 2}})
	e.update(card, func(ed *library.Edit) {
		ed.Overrides = json.RawMessage(`{"` + e.key() + `": {"extensions": {"x": null}}}`)
	})
	e.appCard("lv2", map[string]any{"name": "Ula", "description": "A smith.", "extensions": map[string]any{"x": 5, "depth": 2}}, nil)
	c := e.compareItem("lv2", card)
	e.resolve(c, map[string]merge.Choice{hunk(t, c, "extensions"): merge.ChooseApp}, "")
	ext, _ := field(t, e.head(card).Data, "extensions").(map[string]any)
	over := string(mustMarshal(e.override(card)))
	if ext["x"] != float64(1) || over != `{"extensions":{"x":5}}` {
		t.Errorf("base extensions %v, override %s", ext, over)
	}
}

func TestResolveRefusedRecordsNoSources(t *testing.T) {
	e, card := resolveEnv(t)
	e.appCard("src", map[string]any{"name": "Tova", "description": "A smith.", "first_mes": libGreeting,
		"extensions": map[string]any{"chub": map[string]any{"full_path": "someone/tova"}}}, nil)
	c := e.compareItem("src", card)
	if _, err := e.tryResolve(c, map[string]merge.Choice{"nope#0": merge.ChooseApp}, ""); !errors.Is(err, merge.ErrUnknownHunk) {
		t.Fatalf("unknown hunk: %v", err)
	}
	e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grim smith.") })
	var stale *StaleError
	if _, err := e.tryResolve(c, nil, ""); !errors.As(err, &stale) {
		t.Fatalf("moved head: %v", err)
	}
	if srcs, err := e.lib.Cards.Sources(e.ctx, card); err != nil || len(srcs) != 0 {
		t.Errorf("sources = %+v, %v", srcs, err)
	}
}

func TestCompareIgnoresAMissingBindingsCopy(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	if _, err := e.pool.Exec(e.ctx, `UPDATE binding SET remote_state = 'missing' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	// The app gave the ID to another character, which happens to share the greeting's edit.
	e.appCard(b.RemoteID, map[string]any{"name": "Pell", "description": "A clockwork owl.", "first_mes": libGreeting}, nil)
	c := e.compareItem(b.RemoteID, card)
	for _, st := range statuses(c.Fields) {
		for _, s := range st {
			if s == merge.AutoApp || s == merge.AutoLibrary {
				t.Fatalf("hunks against the old copy: %v", statuses(c.Fields))
			}
		}
	}
	if c.Base != "none" || c.AcceptedHash != "" {
		t.Fatalf("comparison = %+v", c)
	}
	e.resolve(c, nil, merge.ChooseLibrary)
}

func (e *env) pushCount(bindingID int64) int {
	e.t.Helper()
	p, err := e.s.Pushes(e.ctx, bindingID)
	if err != nil {
		e.t.Fatal(err)
	}
	return len(p)
}

// Each case leaves a bound card where a resolve needs no revision, but one
// thing the no-op check guards differs, so the resolve still writes.
func TestResolveWritesWhenOnlyTheBindingIsBehind(t *testing.T) {
	cases := map[string]struct {
		config map[string]any
		change func(e *env, card int64, b Binding)
		check  func(e *env, b Binding) bool
	}{
		// A field the target doesn't store, so the app isn't behind.
		"head moved since the push": {change: func(e *env, card int64, _ Binding) {
			e.update(card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "scenario", "A cold forge.") })
		}, check: func(e *env, b Binding) bool {
			return b.LastPush.RevisionID == e.head(b.CardID).ID && b.Status == "in_sync"
		}},
		"app_behind flips": {change: func(e *env, _ int64, _ Binding) { e.markBehind() },
			check: func(_ *env, b Binding) bool { return b.Status == "in_sync" }},
		"a link changed": {config: map[string]any{"lorebooks": "standalone"},
			change: func(e *env, _ int64, b Binding) {
				item := e.remote(b.RemoteID)
				item["lorebook_remote_ids"] = []string{"l1"}
				e.writeRemote(b.RemoteID, item)
			}, check: func(_ *env, b Binding) bool { return strings.Join(b.LastPush.LorebookRemoteIDs, ",") == "l1" }},
		"remote_state changed": {change: func(e *env, _ int64, b Binding) {
			e.editRemote(b.RemoteID, "first_mes", "Go away.")
			e.check(b)
			e.editRemote(b.RemoteID, "first_mes", libGreeting)
		}, check: func(_ *env, b Binding) bool { return b.RemoteState == "unchanged" }},
		"app changed a field resolve doesn't compare": {change: func(e *env, _ int64, b Binding) {
			e.editRemote(b.RemoteID, "character_book", map[string]any{"entries": []any{
				map[string]any{"keys": []any{"forge"}, "content": "The forge is cold."}}})
		}, check: func(e *env, b Binding) bool { return e.accepted(b.CardID)["character_book"] != nil }},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := setup(t, tc.config)
			card := e.libCard(map[string]any{"name": "Tova", "description": "A smith.", "first_mes": libGreeting})
			b := e.push(card)
			tc.change(e, card, b)
			res := e.resolve(e.compareItem(b.RemoteID, 0), nil, merge.ChooseLibrary)
			after, err := e.s.Binding(e.ctx, b.ID)
			if err != nil || res.RevisionID != 0 || e.pushCount(b.ID) != 2 || !tc.check(e, after) {
				t.Fatalf("result = %+v, %d pushes, binding = %+v, %v", res, e.pushCount(b.ID), after, err)
			}
		})
	}
}

// An avatar taken from the app on an item already accepted at the app's copy
// still writes a revision.
func TestResolveAvatarOnAnAcceptedItem(t *testing.T) {
	e, card := resolveEnv(t)
	e.setAvatar(card, patternPNG(t, func(x, _ int) uint8 { return uint8(x / 8 % 2 * 255) }))
	e.appCard("pic", map[string]any{"name": "Tova", "description": "A smith.", "first_mes": appGreeting},
		map[string]any{"avatar": patternPNG(t, func(x, y int) uint8 { return uint8(x + y) })})
	e.resolve(e.compareItem("pic", card), nil, merge.ChooseLibrary)
	c := e.compareItem("pic", card)
	res := e.resolve(c, map[string]merge.Choice{"avatar": merge.ChooseApp}, "")
	if a := e.head(card).AvatarBlob; res.RevisionID == 0 || a == nil || *a != c.Avatar.App {
		t.Fatalf("result = %+v, avatar = %v", res, a)
	}
}

func TestResolveImportMakesANewCard(t *testing.T) {
	e, _ := resolveEnv(t)
	pic := patternPNG(t, func(x, y int) uint8 { return uint8(x + y) })
	data := map[string]any{"name": "Pell", "description": "A clockwork owl.", "first_mes": "Hoo.",
		"extensions": map[string]any{"fav": true}}
	match := e.libCard(map[string]any{"name": "Pell", "description": "A clockwork owl.", "first_mes": "Hoo."})
	e.appCard("owl", data, map[string]any{"avatar": pic})
	c := e.compareItem("owl", match)
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "owl", AppHash: c.AppHash, Action: "import"})
	if err != nil || res.CardID == 0 || res.CardID == match || res.BindingID == 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	head := e.head(res.CardID)
	if head.AvatarBlob == nil || *head.AvatarBlob != c.Avatar.App || field(t, head.Data, "first_mes") != "Hoo." {
		t.Errorf("head = %+v", head)
	}
	// The favorite star is app state: kept for this app, out of the card.
	if ext, _ := field(t, head.Data, "extensions").(map[string]any); ext["fav"] != nil {
		t.Errorf("base extensions = %v", ext)
	}
	if b, err := e.s.Binding(e.ctx, res.BindingID); err != nil || b.RemoteID != "owl" || b.CardID != res.CardID || b.Status != "in_sync" {
		t.Errorf("binding = %+v, %v", b, err)
	}
}

func TestResolveImportSplitsABoundItem(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	e.editRemote(b.RemoteID, "description", "A different smith altogether.")
	c := e.compareItem(b.RemoteID, 0)
	r := Resolution{InstanceID: e.inst, RemoteID: b.RemoteID, AppHash: c.AppHash, BindingID: c.BindingID, Action: "import"}
	res, err := e.s.Resolve(e.ctx, r)
	if err != nil || c.BindingID != b.ID || res.CardID == card {
		t.Fatalf("comparison binding %d, result = %+v, %v", c.BindingID, res, err)
	}
	if old, err := e.s.Binding(e.ctx, b.ID); err != nil || old.Status != "missing" {
		t.Errorf("old binding = %+v, %v", old, err)
	}
	cards := e.cardCount()
	// Sent again, the import names the binding it just let go of.
	var stale *StaleError
	if _, err := e.s.Resolve(e.ctx, r); !errors.As(err, &stale) || stale.Comparison.BindingID != res.BindingID {
		t.Fatalf("again: err = %v", err)
	}
	if n := e.cardCount(); n != cards {
		t.Errorf("%d cards after a repeated import, want %d", n, cards)
	}
}

func TestResolveImportRepeatedIsStale(t *testing.T) {
	e, card := resolveEnv(t)
	c := e.compareItem("lv1", card)
	r := Resolution{InstanceID: e.inst, RemoteID: "lv1", AppHash: c.AppHash, BindingID: c.BindingID, Action: "import"}
	res, err := e.s.Resolve(e.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	cards := e.cardCount()
	var stale *StaleError
	if _, err := e.s.Resolve(e.ctx, r); !errors.As(err, &stale) || stale.Comparison.BindingID != res.BindingID {
		t.Fatalf("again: err = %v", err)
	}
	if b, err := e.s.Binding(e.ctx, res.BindingID); err != nil || b.Status != "in_sync" || e.cardCount() != cards {
		t.Errorf("binding = %+v, %v, %d cards, want %d", b, err, e.cardCount(), cards)
	}
}

func TestResolveImportOfAnItemBoundSinceIsStale(t *testing.T) {
	e, card := resolveEnv(t)
	b := e.push(card)
	cards := e.cardCount()
	_, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: b.RemoteID, AppHash: e.compareItem(b.RemoteID, 0).AppHash,
		Action: "import"})
	var stale *StaleError
	if !errors.As(err, &stale) || stale.Comparison.BindingID != b.ID {
		t.Fatalf("err = %v", err)
	}
	if got, err := e.s.Binding(e.ctx, b.ID); err != nil || got.Status == "missing" || e.cardCount() != cards {
		t.Errorf("binding = %+v, %v, %d cards", got, err, e.cardCount())
	}
}

func TestResolveNestedImportRepeatedIsStale(t *testing.T) {
	e, card, forge := worldBooks(t)
	l2, err := e.s.CompareLorebookItem(e.ctx, e.inst, "l2", forge)
	if err != nil {
		t.Fatal(err)
	}
	r := e.bindTo(false, "c1", card)
	r.Lorebooks = []Resolution{{RemoteID: "l2", Action: "import", AppHash: l2.AppHash}}
	if _, err := e.s.Resolve(e.ctx, r); err != nil {
		t.Fatal(err)
	}
	books := e.lorebookCount()
	r = e.bindTo(false, "c1", card)
	r.Lorebooks = []Resolution{{RemoteID: "l2", Action: "import", AppHash: l2.AppHash}}
	var stale *StaleError
	if _, err := e.s.Resolve(e.ctx, r); !errors.As(err, &stale) || stale.Item != "lorebook:l2" {
		t.Fatalf("again: err = %v", err)
	}
	if n := e.lorebookCount(); n != books {
		t.Errorf("lorebooks %d, want %d", n, books)
	}
}

func (e *env) cardCount() int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM card`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func TestResolveSkipWritesNothing(t *testing.T) {
	e, card := resolveEnv(t)
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "lv1", Action: "skip"})
	if err != nil || res.Action != "skip" || e.revisions(card) != 1 || e.bindingCount() != 0 {
		t.Fatalf("result = %+v, %v", res, err)
	}
}

// worldBooks sets up a standalone target with library card Tova and lorebook
// Forge, and an app card c1 linking world books: l1, a copy of Forge, and l2,
// a new one.
func worldBooks(t *testing.T) (e *env, card, forge int64) {
	// Like the real plugins, a standalone target doesn't list character_book.
	e = setup(t, map[string]any{"lorebooks": "standalone", "fields": []string{"name", "description", "first_mes"}})
	card = e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
	forge = e.newLorebook("Forge")
	entries := []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}
	e.writeRemote("l1", map[string]any{"lorebook": map[string]any{"name": "Forge", "description": "Forge lore.", "entries": entries}})
	e.writeRemote("l2", map[string]any{"lorebook": map[string]any{"name": "Mines", "description": "Deep tunnels.",
		"entries": []any{map[string]any{"keys": []any{"mine"}, "content": "The mines are dark."}}}})
	e.appCard("c1", map[string]any{"name": "Tova", "description": "A smith."},
		map[string]any{"lorebook_remote_ids": []string{"l1", "l2"}})
	return e, card, forge
}

// bindTo is a bind resolution from a fresh comparison.
func (e *env) bindTo(lorebook bool, remoteID string, id int64) Resolution {
	e.t.Helper()
	compare := e.s.CompareItem
	if lorebook {
		compare = e.s.CompareLorebookItem
	}
	c, err := compare(e.ctx, e.inst, remoteID, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return Resolution{InstanceID: e.inst, RemoteID: remoteID, AppHash: c.AppHash, BaseRevisionID: c.HeadRevisionID,
		AcceptedHash: c.AcceptedHash, Action: "bind", CardID: c.CardID}
}

func (e *env) links(card int64) []int64 {
	e.t.Helper()
	ids, err := e.lib.CardLorebooks(e.ctx, card)
	if err != nil {
		e.t.Fatal(err)
	}
	return ids
}

func TestResolveNestedWorldBooks(t *testing.T) {
	e, card, forge := worldBooks(t)
	r := e.bindTo(false, "c1", card)
	r.Lorebooks = []Resolution{e.bindTo(true, "l1", forge), {RemoteID: "l2", Action: "skip"}}
	res, err := e.s.Resolve(e.ctx, r)
	if err != nil || len(res.Lorebooks) != 2 || res.Lorebooks[0].CardID != forge || res.Lorebooks[1].Action != "skip" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if l := e.links(card); len(l) != 1 || l[0] != forge {
		t.Errorf("links = %v", l)
	}
	if e.lorebookStatus(forge) != "in_sync" {
		t.Errorf("forge status = %s", e.lorebookStatus(forge))
	}
}

func TestResolveLeftOutWorldBookTakesItsSuggestion(t *testing.T) {
	e, card, forge := worldBooks(t)
	res, err := e.s.Resolve(e.ctx, e.bindTo(false, "c1", card))
	if err != nil || len(res.Lorebooks) != 2 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	// l1 is an exact copy of Forge, so it binds; l2 matches nothing, so it's imported.
	l1, l2 := res.Lorebooks[0], res.Lorebooks[1]
	if l1.Action != "bind" || l1.CardID != forge || l2.Action != "import" || l2.CardID == 0 {
		t.Fatalf("world books = %+v", res.Lorebooks)
	}
	if l := e.links(card); len(l) != 2 {
		t.Errorf("links = %v", l)
	}
	if s := e.status(card); s != "in_sync" {
		t.Errorf("card status = %s", s)
	}
}

func TestResolveSharedWorldBookOnce(t *testing.T) {
	e, card, forge := worldBooks(t)
	r := e.bindTo(false, "c1", card)
	l1 := e.bindTo(true, "l1", forge)
	// The second would be stale if it ran, since the first binds l1.
	r.Lorebooks = []Resolution{l1, l1, {RemoteID: "l2", Action: "skip"}}
	res, err := e.s.Resolve(e.ctx, r)
	if err != nil || res.Lorebooks[0].BindingID == 0 || res.Lorebooks[0].BindingID != res.Lorebooks[1].BindingID {
		t.Fatalf("result = %+v, %v", res, err)
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM push p JOIN binding b ON b.id = p.binding_id
		WHERE b.lorebook_id = $1`, forge).Scan(&n); err != nil || n != 1 {
		t.Errorf("forge push rows = %d, %v", n, err)
	}
}

func TestResolveRollsBackTogether(t *testing.T) {
	e, card, forge := worldBooks(t)
	r := e.bindTo(false, "c1", card)
	r.Choices = map[string]merge.Choice{}
	stale := Resolution{RemoteID: "l2", Action: "import", AppHash: "old"}
	r.Lorebooks = []Resolution{e.bindTo(true, "l1", forge), stale}
	_, err := e.s.Resolve(e.ctx, r)
	var se *StaleError
	if !errors.As(err, &se) || se.Comparison.RemoteID != "l2" {
		t.Fatalf("err = %v", err)
	}
	var lorebooks int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM lorebook`).Scan(&lorebooks); err != nil {
		t.Fatal(err)
	}
	if e.bindingCount() != 0 || e.revisions(card) != 1 || len(e.links(card)) != 0 || lorebooks != 1 {
		t.Errorf("%d bindings, %d revisions, links %v, %d lorebooks", e.bindingCount(), e.revisions(card), e.links(card), lorebooks)
	}
}

func TestResolveLorebookHasNoNestedWorldBooks(t *testing.T) {
	e, _, forge := worldBooks(t)
	r := e.bindTo(true, "l1", forge)
	r.Lorebooks = []Resolution{{RemoteID: "l2", Action: "skip"}}
	if _, err := e.s.ResolveLorebook(e.ctx, r); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveImportSplitsTheCharacterBook(t *testing.T) {
	e := setup(t, nil)
	e.appCard("owl", map[string]any{"name": "Pell", "description": "A clockwork owl.", "character_book": map[string]any{
		"name": "Owl lore", "entries": []any{map[string]any{"keys": []any{"owl"}, "content": "Owls hoot."}}}}, nil)
	c, err := e.s.CompareItem(e.ctx, e.inst, "owl", e.card)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "owl", AppHash: c.AppHash, Action: "import"})
	if err != nil {
		t.Fatal(err)
	}
	links := e.links(res.CardID)
	if len(links) != 1 || field(t, e.lorebookHead(links[0]).Data, "name") != "Owl lore" {
		t.Fatalf("links = %v", links)
	}
	if s := e.status(res.CardID); s != "in_sync" {
		t.Errorf("status = %s", s)
	}
}

func TestResolveImportOnAStandaloneTargetIgnoresTheEmbeddedBook(t *testing.T) {
	e, _, _ := worldBooks(t)
	e.appCard("c9", map[string]any{"name": "Dain", "description": "A miner.",
		"character_book": map[string]any{"name": "Mines", "entries": []any{map[string]any{"keys": []any{"mine"}, "content": "Dark."}}}},
		map[string]any{"lorebook_remote_ids": []string{"l2"}})
	it, err := e.s.CompareItem(e.ctx, e.inst, "c9", e.card)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "c9", AppHash: it.AppHash, Action: "import"})
	if err != nil {
		t.Fatal(err)
	}
	var mines int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM lorebook l JOIN lorebook_revision r ON r.id = l.head_revision_id
		WHERE r.data ->> 'name' = 'Mines'`).Scan(&mines); err != nil {
		t.Fatal(err)
	}
	if l := e.links(res.CardID); mines != 1 || len(l) != 1 || e.status(res.CardID) != "in_sync" {
		t.Errorf("%d Mines lorebooks, links %v, status %s", mines, l, e.status(res.CardID))
	}
}

func TestResolveSuggestionChangedEarlierInTheCallIsAConflict(t *testing.T) {
	forgeBook := func(desc string) map[string]any {
		return map[string]any{"lorebook": map[string]any{"name": "Forge", "description": desc,
			"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}}}
	}
	for name, l1 := range map[string]struct {
		desc string
		def  merge.Choice
	}{
		"bound without a revision": {"Forge lore.", ""},
		"head moved":               {"Old lore.", merge.ChooseApp},
	} {
		t.Run(name, func(t *testing.T) {
			e := setup(t, map[string]any{"lorebooks": "standalone"})
			card := e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
			forge := e.newLorebook("Forge")
			e.writeRemote("l1", forgeBook(l1.desc))
			e.writeRemote("l3", forgeBook("Forge lore."))
			e.appCard("c1", map[string]any{"name": "Tova", "description": "A smith."},
				map[string]any{"lorebook_remote_ids": []string{"l1", "l3"}})
			r := e.bindTo(false, "c1", card)
			first := e.bindTo(true, "l1", forge)
			first.Default = l1.def
			r.Lorebooks = []Resolution{first}
			_, err := e.s.Resolve(e.ctx, r)
			var stale *StaleError
			if !errors.Is(err, library.ErrConflict) || errors.As(err, &stale) ||
				!strings.Contains(err.Error(), "world book l3") || !strings.Contains(err.Error(), "world book l1") {
				t.Fatalf("err = %v", err)
			}
			// An explicit choice for l3 goes through.
			r.Lorebooks = append(r.Lorebooks, Resolution{RemoteID: "l3", Action: "skip"})
			if _, err := e.s.Resolve(e.ctx, r); err != nil {
				t.Fatalf("with l3 chosen: %v", err)
			}
		})
	}
}

func TestResolveStaleErrorNamesTheItem(t *testing.T) {
	e, card, forge := worldBooks(t)
	r := e.bindTo(false, "c1", card)
	r.Lorebooks = []Resolution{e.bindTo(true, "l1", forge), {RemoteID: "l2", Action: "import", AppHash: "old"}}
	var se *StaleError
	if _, err := e.s.Resolve(e.ctx, r); !errors.As(err, &se) || se.Item != "lorebook:l2" {
		t.Fatalf("err = %v", err)
	}
	r.Lorebooks, r.AppHash = nil, "old"
	if _, err := e.s.Resolve(e.ctx, r); !errors.As(err, &se) || se.Item != "card:c1" {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveNoOpRecordsSources(t *testing.T) {
	e := setup(t, map[string]any{"fields": []string{"name", "description", "extensions"}})
	card := e.libCard(map[string]any{"name": "Tova", "description": "A smith.",
		"extensions": map[string]any{"chub": map[string]any{"full_path": "someone/tova"}}})
	b := e.push(card)
	res := e.resolve(e.compareItem(b.RemoteID, 0), nil, "")
	srcs, err := e.lib.Cards.Sources(e.ctx, card)
	if err != nil || res.RevisionID != 0 || e.pushCount(b.ID) != 1 || len(srcs) != 1 {
		t.Fatalf("result = %+v, %d pushes, sources %+v, %v", res, e.pushCount(b.ID), srcs, err)
	}
}

func TestResolveLeftOutBoundWorldBook(t *testing.T) {
	for _, drift := range []bool{true, false} {
		e := setup(t, map[string]any{"lorebooks": "standalone"})
		card := e.libCard(map[string]any{"name": "Tova", "description": "A smith."})
		forge := e.newLorebook("Forge")
		lb := e.pushLorebook(forge)
		if drift {
			e.editRemote(lb.RemoteID, "description", "Hotter lore.")
		}
		e.appCard("c1", map[string]any{"name": "Tova", "description": "A smith."},
			map[string]any{"lorebook_remote_ids": []string{lb.RemoteID}})
		res, err := e.s.Resolve(e.ctx, e.bindTo(false, "c1", card))
		if err != nil || len(res.Lorebooks) != 1 || res.Lorebooks[0].Action != "bind" || res.Lorebooks[0].CardID != forge {
			t.Fatalf("drift %v: result = %+v, %v", drift, res, err)
		}
		want := map[bool]string{true: "Hotter lore.", false: "Forge lore."}[drift]
		if d := field(t, e.lorebookHead(forge).Data, "description"); d != want || len(e.links(card)) != 1 {
			t.Errorf("drift %v: description %v, links %v", drift, d, e.links(card))
		}
	}
}

func TestResolveImportedWorldBookKeepsItsName(t *testing.T) {
	e, card, _ := worldBooks(t)
	res, err := e.s.Resolve(e.ctx, e.bindTo(false, "c1", card))
	if err != nil {
		t.Fatal(err)
	}
	lb, err := e.lib.Lorebooks.Get(e.ctx, res.Lorebooks[1].CardID)
	if err != nil || lb.Slug != "mines" {
		t.Fatalf("lorebook = %+v, %v", lb, err)
	}
}

func TestResolveImportKeepsTheBookOnAStandaloneTargetThatListsIt(t *testing.T) {
	e := setup(t, map[string]any{"lorebooks": "standalone"})
	e.appCard("c9", map[string]any{"name": "Dain", "description": "A miner.", "character_book": map[string]any{
		"name": "Mines", "entries": []any{map[string]any{"keys": []any{"mine"}, "content": "ONLY COPY."}}}}, nil)
	c, err := e.s.CompareItem(e.ctx, e.inst, "c9", e.card)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "c9", AppHash: c.AppHash, Action: "import"})
	if err != nil {
		t.Fatal(err)
	}
	l := e.links(res.CardID)
	if len(l) != 1 || !strings.Contains(string(e.lorebookHead(l[0]).Data), "ONLY COPY.") {
		t.Fatalf("links = %v", l)
	}
}

func TestResolveTwoExplicitWorldBooksOnOneLorebook(t *testing.T) {
	e, card, forge := worldBooks(t)
	e.writeRemote("l2", map[string]any{"lorebook": map[string]any{"name": "Forge", "description": "Forge lore.",
		"entries": []any{map[string]any{"keys": []any{"forge"}, "content": "The forge burns hot."}}}})
	r := e.bindTo(false, "c1", card)
	r.Lorebooks = []Resolution{e.bindTo(true, "l1", forge), e.bindTo(true, "l2", forge)}
	_, err := e.s.Resolve(e.ctx, r)
	if !errors.Is(err, ErrBound) || errors.Is(err, library.ErrConflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveSuggestionConflictReadsCleanly(t *testing.T) {
	err := error(conflictError("world book l3's suggested match #1 Forge was changed by world book l1 in this request"))
	if !errors.Is(err, library.ErrConflict) || strings.Contains(err.Error(), library.ErrConflict.Error()) {
		t.Errorf("err = %v", err)
	}
}

func TestResolveImportSplitLeavesAWorldBookWithTheSameID(t *testing.T) {
	e, _ := resolveEnv(t)
	lb := e.newLorebook("Forge")
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO binding (instance_id, lorebook_id, remote_id, remote_state, accepted_data,
		accepted_hash) VALUES ($1, $2, 'lv1', 'unchanged', '{}', 'h')`, e.inst, lb); err != nil {
		t.Fatal(err)
	}
	c := e.compareItem("lv1", e.card)
	if _, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "lv1", AppHash: c.AppHash, Action: "import"}); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := e.pool.QueryRow(e.ctx, `SELECT remote_state FROM binding WHERE lorebook_id = $1`, lb).Scan(&state); err != nil || state != "unchanged" {
		t.Errorf("world book binding = %s, %v", state, err)
	}
}
