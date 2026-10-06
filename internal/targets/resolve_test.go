package targets

import (
	"encoding/json"
	"errors"
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
	return e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: c.RemoteID, AppHash: c.AppHash, Action: "bind",
		CardID: c.CardID, Choices: choices, Default: def})
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
	other, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "fake", Name: "Other app",
		Config: map[string]any{"dir": t.TempDir(), "fields": []string{"name", "description", "first_mes", "extensions"}}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.s.Preview(e.ctx, other, card)
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
