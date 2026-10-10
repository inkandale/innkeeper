package main

import (
	"slices"
	"strings"
	"testing"
)

func TestPlugins(t *testing.T) {
	catalog := `[
		{"source": {"id": "official", "url": "https://o/index.toml"}, "plugins": [
			{"name": "card-import", "version": "0.2.0", "capabilities": ["importer"], "description": "Imports files", "versions": ["0.2.0", "0.1.0"]},
			{"name": "lumiverse", "version": "1.0.0", "capabilities": ["target"], "description": "Lumiverse", "versions": ["1.0.0"]}]},
		{"source": {"id": "mine", "url": "https://m/index.toml"}, "plugins": [
			{"name": "lumiverse", "version": "1.1.0", "capabilities": ["target"], "description": "Fork", "versions": ["1.1.0"]}]},
		{"source": {"id": "bad", "url": "https://b/index.toml"}, "plugins": [], "error": "404 Not Found"}]`
	got := stubAPI(t, map[string]string{
		"/plugins":        `[{"name": "card-codec", "version": "1.0.0", "capabilities": ["codec"], "secrets": [], "source": "builtin", "builtin": true}]`,
		"/plugin-catalog": catalog,
		"/plugin-sources": `[{"id": "official", "url": "https://o/index.toml"}]`,
	})

	out, err := runInk(t, "plugins")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); !slices.Equal(f, []string{"NAME", "VERSION", "SOURCE", "CAPABILITIES"}) {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); !slices.Equal(f, []string{"card-codec", "1.0.0", "builtin", "codec"}) {
		t.Errorf("row = %q", lines[1])
	}
	out, err = runInk(t, "plugins", "catalog")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "0.2.0, 0.1.0") || !strings.Contains(out, "404 Not Found") {
		t.Errorf("catalog = %q", out)
	}
	if out, _ := runInk(t, "plugins", "sources"); !strings.Contains(out, "https://o/index.toml") {
		t.Errorf("sources = %q", out)
	}

	*got = nil
	for _, args := range [][]string{
		{"install", "card-import"},
		{"install", "card-import", "0.1.0", "--source", "official"},
		{"install", "lumiverse", "--source", "mine"},
		{"remove", "card-import"},
		{"purge", "card-import", "--yes"},
		{"sources", "add", "mine", "https://m/index.toml"},
		{"sources", "remove", "mine"},
	} {
		if _, err := runInk(t, append([]string{"plugins"}, args...)...); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	want := []string{
		"GET /plugin-catalog ",
		`POST /plugins/card-import/install {"source":"official","version":"0.2.0"}`,
		`POST /plugins/card-import/install {"source":"official","version":"0.1.0"}`,
		"GET /plugin-catalog ",
		`POST /plugins/lumiverse/install {"source":"mine","version":"1.1.0"}`,
		"DELETE /plugins/card-import ",
		"DELETE /plugins/card-import/data ",
		`POST /plugin-sources {"id":"mine","url":"https://m/index.toml"}`,
		"DELETE /plugin-sources/mine ",
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q, want %q", *got, want)
	}

	*got = nil
	for _, args := range [][]string{
		{"install", "lumiverse"}, // two sources offer it
		{"install", "nothing"},   // none does
		{"install", "card-import", "--source", "mine"},
		{"purge", "card-import"}, // no --yes, no terminal
		{"sources", "add", "mine"},
		{"remove"},
	} {
		if _, err := runInk(t, append([]string{"plugins"}, args...)...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
	for _, r := range *got {
		if !strings.HasPrefix(r, "GET ") {
			t.Errorf("a failed command sent %q", r)
		}
	}
}
