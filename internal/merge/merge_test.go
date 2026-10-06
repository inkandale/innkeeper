package merge

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func compare(t *testing.T, s Schema, lib, app, anc string) []Field {
	t.Helper()
	var a json.RawMessage
	if anc != "" {
		a = json.RawMessage(anc)
	}
	fs, err := Compare(s, json.RawMessage(lib), json.RawMessage(app), a)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range fs {
		for _, h := range f.Hunks {
			if seen[h.ID] {
				t.Errorf("duplicate hunk ID %q", h.ID)
			}
			seen[h.ID] = true
		}
	}
	return fs
}

// changed returns every hunk whose status is not same, keyed by ID.
func changed(fs []Field) map[string]Hunk {
	out := map[string]Hunk{}
	for _, f := range fs {
		for _, h := range f.Hunks {
			if h.Status != Same {
				out[h.ID] = h
			}
		}
	}
	return out
}

func hunk(t *testing.T, fs []Field, id string) Hunk {
	t.Helper()
	for _, f := range fs {
		for _, h := range f.Hunks {
			if h.ID == id {
				return h
			}
		}
	}
	t.Fatalf("no hunk %q", id)
	return Hunk{}
}

func jsonEq(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	if string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

const card = `{
	"name": "Bram",
	"description": "A smith. He is tall.",
	"first_mes": "Hail. The forge is cold. Sit down.",
	"tags": ["a", "b"],
	"extensions": {"depth_prompt": {"prompt": "Be gruff.", "depth": 4}}
}`

func TestCompareEqual(t *testing.T) {
	fs := compare(t, Card, card, card, "")
	if c := changed(fs); len(c) != 0 {
		t.Errorf("changed hunks on equal cards: %v", c)
	}
	var paths []string
	for _, f := range fs {
		paths = append(paths, f.Path)
	}
	want := []string{"description", "extensions", "first_mes", "name", "tags"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestCompareProse(t *testing.T) {
	app := strings.Replace(card, "is cold", "is warm", 1)
	fs := compare(t, Card, card, app, "")
	c := changed(fs)
	if len(c) != 1 {
		t.Fatalf("changed = %v", c)
	}
	h, ok := c["first_mes#1"]
	if !ok || h.Status != Conflict {
		t.Fatalf("want conflict first_mes#1, got %v", c)
	}
	if hunk(t, fs, "first_mes#0").Status != Same || hunk(t, fs, "first_mes#2").Status != Same {
		t.Error("sentences around the change should be same")
	}
	jsonEq(t, h.Library, `"The forge is cold. "`)
	jsonEq(t, h.App, `"The forge is warm. "`)
	text := map[string]string{"library": "The forge is cold. ", "app": "The forge is warm. "}
	want := map[string]string{"library": "cold. ", "app": "warm. "}
	if len(h.Words) != 2 {
		t.Fatalf("words = %v", h.Words)
	}
	for _, sp := range h.Words {
		if got := text[sp.Side][sp.Start:sp.End]; got != want[sp.Side] {
			t.Errorf("%s span selects %q, want %q", sp.Side, got, want[sp.Side])
		}
	}
}

func TestCompareList(t *testing.T) {
	app := strings.Replace(card, `"b"]`, `"c"]`, 1)
	c := changed(compare(t, Card, card, app, ""))
	if len(c) != 1 {
		t.Fatalf("changed = %v", c)
	}
	h := c["tags#1"]
	if h.Status != Conflict {
		t.Fatalf("tags#1 = %v", h)
	}
	jsonEq(t, h.Library, `["b"]`)
	jsonEq(t, h.App, `["c"]`)
}

func TestCompareExtensions(t *testing.T) {
	app := strings.Replace(card,
		`{"depth_prompt": {"prompt": "Be gruff.", "depth": 4}}`,
		`{"depth_prompt": {"prompt": "Be kind.", "depth": 4}, "lumiverse_ui": {"theme": "dark"}}`, 1)
	fs := compare(t, Card, card, app, "")
	c := changed(fs)
	if len(c) != 2 {
		t.Fatalf("changed = %v", c)
	}
	if h := c["extensions.lumiverse_ui#0"]; h.Status != AppOnly || h.Library != nil {
		t.Errorf("lumiverse_ui = %+v", h)
	} else {
		jsonEq(t, h.App, `{"theme":"dark"}`)
	}
	if h := c["extensions.depth_prompt.prompt#0"]; h.Status != Conflict {
		t.Errorf("prompt = %+v", h)
	}
	h := hunk(t, fs, "extensions.depth_prompt.depth#0")
	if h.Status != Same {
		t.Errorf("depth = %+v", h)
	}
	jsonEq(t, h.Library, `4`)
}

const book = `{"name": "World", "entries": [
	{"comment": "forge", "keys": ["forge"], "content": "Hot. Loud."},
	{"comment": "town", "keys": ["town"], "content": "Small."},
	{"comment": "river", "keys": ["river", "water"], "content": "Wide."}
]}`

func TestCompareLorebook(t *testing.T) {
	app := `{"name": "World", "entries": [
		{"comment": "river", "keys": ["river", "water"], "content": "Wide."},
		{"comment": "forge", "keys": ["forge"], "content": "Hot. Quiet."}
	]}`
	fs := compare(t, Lorebook, book, app, "")
	c := changed(fs)
	if len(c) != 2 {
		t.Fatalf("changed = %v", c)
	}
	if h := c["entries[forge].content#1"]; h.Status != Conflict {
		t.Errorf("forge content = %+v", h)
	}
	h := c["entries[town]#0"]
	if h.Status != LibraryOnly {
		t.Errorf("town = %+v", h)
	}
	jsonEq(t, h.Library, `{"comment":"town","content":"Small.","keys":["town"]}`)
	if fs[0].Path != "entries" || fs[0].Kind != Entries {
		t.Errorf("field = %s %s", fs[0].Path, fs[0].Kind)
	}
}

func TestCompareDuplicateComments(t *testing.T) {
	lib := `{"entries": [
		{"comment": "x", "keys": ["b", "a"], "content": "One."},
		{"comment": "x", "keys": ["c"], "content": "Two."}
	]}`
	app := `{"entries": [
		{"comment": "x", "keys": ["c"], "content": "Two."},
		{"comment": "x", "keys": ["a", "b"], "content": "Uno."}
	]}`
	fs := compare(t, Lorebook, lib, app, "")
	c := changed(fs)
	if len(c) != 1 {
		t.Fatalf("changed = %v", c)
	}
	if h := c["entries[a\x1fb].content#0"]; h.Status != Conflict {
		t.Errorf("changed = %v", c)
	}
	if h := hunk(t, fs, "entries[a\x1fb].keys#0"); h.Status != Same {
		t.Errorf("reordered keys = %+v", h)
	} else {
		jsonEq(t, h.Library, `["b","a"]`)
		jsonEq(t, h.App, `["a","b"]`)
	}
}

func TestCompareAncestor(t *testing.T) {
	lib := strings.Replace(card, "He is tall.", "He is short.", 1)
	anc := strings.Replace(card, `"name"`, `"scenario": "A forge.", "name"`, 1)
	app := strings.Replace(anc, "A forge.", "A cold forge.", 1)
	lib = strings.Replace(lib, `"name"`, `"scenario": "A forge.", "name"`, 1)
	fs := compare(t, Card, lib, app, anc)
	c := changed(fs)
	if len(c) != 2 {
		t.Fatalf("changed = %v", c)
	}
	if h := c["description#1"]; h.Status != AutoLibrary {
		t.Errorf("description = %+v", h)
	} else {
		jsonEq(t, h.Base, `"He is tall."`)
	}
	if h := c["scenario#0"]; h.Status != AutoApp {
		t.Errorf("scenario = %+v", h)
	}
}

func TestCompareDeterministic(t *testing.T) {
	app := `{"name": "World 2", "extensions": {"z": 1, "a": {"b": 2}}, "entries": [
		{"comment": "forge", "keys": ["forge"], "content": "Hot."},
		{"comment": "new", "keys": ["n"], "content": "New."}
	]}`
	a := compare(t, Lorebook, book, app, "")
	b := compare(t, Lorebook, book, app, "")
	if h := hunk(t, a, "entries[new]#0"); h.Status != AppOnly {
		t.Errorf("new entry = %+v", h)
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("runs differ:\n%v\n%v", a, b)
	}
}

func TestCompareJSONShape(t *testing.T) {
	fs := compare(t, Card, `{"tags": ["a", "b"]}`, `{"tags": ["a", "c"], "name": "N"}`, "")
	got, err := json.Marshal(fs)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"path":"name","kind":"scalar","hunks":[{"id":"name#0","status":"app_only","app":"N"}]},` +
		`{"path":"tags","kind":"list","hunks":[{"id":"tags#0","status":"same","library":["a"],"app":["a"]},` +
		`{"id":"tags#1","status":"conflict","library":["b"],"app":["c"]}]}]`
	if string(got) != want {
		t.Errorf("json:\n%s\nwant\n%s", got, want)
	}
}

func TestCompareInvalid(t *testing.T) {
	for _, in := range []string{`[]`, `{`, `"x"`} {
		if _, err := Compare(Card, json.RawMessage(in), json.RawMessage(`{}`), nil); err == nil {
			t.Errorf("Compare(%s) succeeded", in)
		}
	}
}
