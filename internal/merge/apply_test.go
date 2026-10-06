package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
)

func canon(t *testing.T, s string) string {
	t.Helper()
	m, err := decode(json.RawMessage(s))
	if err != nil {
		t.Fatal(err)
	}
	return string(enc(m))
}

func apply(t *testing.T, s Schema, lib, app, anc string, ch map[string]Choice, def Choice) (string, string) {
	t.Helper()
	var a json.RawMessage
	if anc != "" {
		a = json.RawMessage(anc)
	}
	base, over, err := Apply(s, json.RawMessage(lib), json.RawMessage(app), a, ch, def)
	if err != nil {
		t.Fatal(err)
	}
	return string(base), string(over)
}

func cardApp() string {
	app := strings.Replace(card, "is cold", "is warm", 1)
	app = strings.Replace(app, `"b"]`, `"c"]`, 1)
	return strings.Replace(app, `"depth": 4}}`, `"depth": 4}, "lumiverse_ui": {"theme": "dark"}}`, 1)
}

func TestApplyDefaults(t *testing.T) {
	app := cardApp()
	for def, want := range map[Choice]string{ChooseLibrary: card, ChooseApp: app, "": card} {
		base, over := apply(t, Card, card, app, "", nil, def)
		if base != canon(t, want) {
			t.Errorf("def %q: base = %s", def, base)
		}
		if over != "" {
			t.Errorf("def %q: override = %s", def, over)
		}
	}
}

func TestApplyKeepInApp(t *testing.T) {
	app := cardApp()
	base, over := apply(t, Card, card, app, "", map[string]Choice{"first_mes#1": ChooseKeepInApp}, ChooseLibrary)
	if base != canon(t, card) {
		t.Errorf("base = %s", base)
	}
	if want := `{"first_mes":"Hail. The forge is warm. Sit down."}`; over != want {
		t.Errorf("override = %s, want %s", over, want)
	}

	ch := map[string]Choice{"extensions.lumiverse_ui#0": ChooseKeepInApp}
	base, over = apply(t, Card, card, app, "", ch, ChooseLibrary)
	if strings.Contains(base, "lumiverse_ui") {
		t.Errorf("base = %s", base)
	}
	if want := `{"extensions":{"lumiverse_ui":{"theme":"dark"}}}`; over != want {
		t.Errorf("override = %s, want %s", over, want)
	}
}

func TestApplyBoth(t *testing.T) {
	base, _ := apply(t, Card, card, cardApp(), "", map[string]Choice{"tags#1": ChooseBoth}, ChooseLibrary)
	if !strings.Contains(base, `"tags":["a","b","c"]`) {
		t.Errorf("base = %s", base)
	}
	for _, ch := range []map[string]Choice{{"first_mes#1": ChooseBoth}, {"tags#1": "nonsense"}} {
		_, _, err := Apply(Card, json.RawMessage(card), json.RawMessage(cardApp()), nil, ch, ChooseLibrary)
		if !errors.Is(err, ErrBadChoice) {
			t.Errorf("%v: err = %v", ch, err)
		}
	}
	_, _, err := Apply(Card, json.RawMessage(card), json.RawMessage(card), nil, nil, ChooseBoth)
	if !errors.Is(err, ErrBadChoice) {
		t.Errorf("def both: err = %v", err)
	}
}

func TestApplyUnknownHunk(t *testing.T) {
	_, _, err := Apply(Card, json.RawMessage(card), json.RawMessage(cardApp()), nil,
		map[string]Choice{"nope#0": ChooseApp, "zip#3": ChooseApp, "tags#1": ChooseApp}, ChooseLibrary)
	msg := fmt.Sprint(err)
	if !errors.Is(err, ErrUnknownHunk) || !strings.Contains(msg, "nope#0") || !strings.Contains(msg, "zip#3") {
		t.Errorf("err = %v", err)
	}
}

func TestApplyAncestor(t *testing.T) {
	lib := strings.Replace(card, "He is tall.", "He is short.", 1)
	anc := strings.Replace(card, `"name"`, `"scenario": "A forge.", "name"`, 1)
	app := strings.Replace(anc, "A forge.", "A cold forge.", 1)
	lib = strings.Replace(lib, `"name"`, `"scenario": "A forge.", "name"`, 1)
	base, over := apply(t, Card, lib, app, anc, nil, ChooseLibrary)
	want := strings.Replace(lib, "A forge.", "A cold forge.", 1)
	if base != canon(t, want) || over != "" {
		t.Errorf("base = %s, override = %s", base, over)
	}
}

func TestApplyMissingSide(t *testing.T) {
	tests := []struct {
		lib, app, id string
		ch           Choice
		want         string
	}{
		{`{"description": "Hi."}`, `{}`, "description#0", ChooseApp, `{}`},
		{`{}`, `{"tags": ["a"]}`, "tags#0", ChooseLibrary, `{}`},
		{`{}`, `{"tags": ["a"]}`, "tags#0", ChooseApp, `{"tags":["a"]}`},
		{`{"description": ""}`, `{}`, "description#0", ChooseApp, `{"description":""}`},
	}
	for _, tt := range tests {
		base, _ := apply(t, Card, tt.lib, tt.app, "", map[string]Choice{tt.id: tt.ch}, ChooseLibrary)
		if base != tt.want {
			t.Errorf("%s vs %s: base = %s, want %s", tt.lib, tt.app, base, tt.want)
		}
	}
}

func TestApplyDeleteVsModify(t *testing.T) {
	lib, anc := `{"name": "B"}`, `{"name": "A"}`
	base, over := apply(t, Card, lib, `{}`, anc, map[string]Choice{"name#0": ChooseApp}, ChooseLibrary)
	if base != `{}` || over != "" {
		t.Errorf("base = %s, override = %s", base, over)
	}
	base, over = apply(t, Card, lib, `{}`, anc, map[string]Choice{"name#0": ChooseKeepInApp}, ChooseLibrary)
	if base != `{"name":"B"}` || over != `{"name":null}` {
		t.Errorf("base = %s, override = %s", base, over)
	}
}

func TestApplyLorebook(t *testing.T) {
	app := `{"name": "World", "entries": [
		{"comment": "new", "keys": ["n"], "content": "New."},
		{"comment": "river", "keys": ["water", "river"], "content": "Wide."},
		{"comment": "forge", "keys": ["forge"], "content": "Hot. Quiet."}
	]}`
	base, over := apply(t, Lorebook, book, app, "", map[string]Choice{
		"entries[c:town]#0":          ChooseLibrary,
		"entries[c:new]#0":           ChooseLibrary,
		"entries[c:forge].content#1": ChooseApp,
	}, ChooseApp)
	want := `{"entries":[` +
		`{"comment":"forge","content":"Hot. Quiet.","keys":["forge"]},` +
		`{"comment":"town","content":"Small.","keys":["town"]},` +
		`{"comment":"river","content":"Wide.","keys":["river","water"]}],"name":"World"}`
	if base != want || over != "" {
		t.Errorf("base = %s\nwant   %s\noverride = %s", base, want, over)
	}

	// dropping every entry where one side lacked the key drops the key
	base, _ = apply(t, Lorebook, book, `{"name": "World"}`, "", nil, ChooseApp)
	if base != `{"name":"World"}` {
		t.Errorf("base = %s", base)
	}
	a, b := `{"entries": [{"comment": "a"}]}`, `{"entries": [{"comment": "b"}]}`
	base, _ = apply(t, Lorebook, a, b, "", nil, ChooseKeepInApp)
	if base != `{"entries":[{"comment":"a"}]}` {
		t.Errorf("base = %s", base)
	}
}

// TestApplyRoundTrip checks that a default with no choices reproduces that
// side, up to the differences Compare treats as no change.
func TestApplyRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	pick := func(xs ...any) any { return xs[r.Intn(len(xs))] }
	prose := func() any {
		var s string
		for range r.Intn(4) {
			s += pick("Hi. ", "He is tall. ", "Bye.\n", "Oh").(string)
		}
		return s
	}
	list := func(pool ...any) any {
		out := []any{}
		for range r.Intn(4) {
			out = append(out, pick(pool...))
		}
		return out
	}
	maybe := func(m map[string]any, k string, v func() any) {
		if r.Intn(4) > 0 {
			m[k] = v()
		}
	}
	genCard := func() string {
		m := map[string]any{}
		maybe(m, "name", func() any { return pick("A", "B", 1) })
		maybe(m, "description", prose)
		maybe(m, "first_mes", prose)
		maybe(m, "tags", func() any { return list("a", "b", "c") })
		maybe(m, "extensions", func() any {
			e := map[string]any{}
			maybe(e, "x", func() any { return pick(1, "s", map[string]any{"z": 2}, map[string]any{}) })
			maybe(e, "y", func() any { return pick(json.Number("2.50"), []any{"q"}) })
			return e
		})
		return string(enc(m))
	}
	genBook := func() string {
		m := map[string]any{}
		maybe(m, "name", func() any { return pick("W", "V") })
		maybe(m, "entries", func() any {
			es := []any{}
			for range r.Intn(4) {
				es = append(es, map[string]any{
					"comment": pick("a", "b", ""),
					"keys":    list("x", "y"),
					"content": prose(),
				})
			}
			return es
		})
		return string(enc(m))
	}
	for i := range 300 {
		s, gen := Card, genCard
		if i%2 == 1 {
			s, gen = Lorebook, genBook
		}
		lib, app := gen(), gen()
		base, over := apply(t, s, lib, app, "", nil, ChooseLibrary)
		if base != lib || over != "" {
			t.Fatalf("def library:\nlib  %s\napp  %s\nbase %s\nover %s", lib, app, base, over)
		}
		base, over = apply(t, s, lib, app, "", nil, ChooseApp)
		if normal(t, s, base) != normal(t, s, app) || over != "" {
			t.Fatalf("def app:\nlib  %s\napp  %s\nbase %s\nover %s", lib, app, base, over)
		}
	}
}

// normal drops empty prose, list and entries values and ignores entry and key
// order, which Compare does not count as changes.
func normal(t *testing.T, s Schema, raw string) string {
	t.Helper()
	m, err := decode(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range m {
		if (s[k] == Prose || s[k] == List || s[k] == Entries) && empty(v) {
			delete(m, k)
		}
	}
	if es, ok := m["entries"].([]any); ok {
		for _, e := range es {
			e := e.(map[string]any)
			e["keys"] = sorted(e["keys"])
		}
		slices.SortFunc(es, func(x, y any) int { return strings.Compare(string(enc(x)), string(enc(y))) })
	}
	return string(enc(m))
}
