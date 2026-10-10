package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCardsExport(t *testing.T) {
	got := stubAPI(t, map[string]string{"/cards/12/export?format=png": "PNG", "/cards/12/export?format=json": "{}"})
	file := filepath.Join(t.TempDir(), "brakka.png")
	if _, err := runInk(t, "cards", "export", "12", file); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "PNG" {
		t.Fatalf("file = %q", b)
	}
	if out, err := runInk(t, "cards", "export", "12"); err != nil || out != "{}" {
		t.Fatalf("stdout = %q, err = %v", out, err)
	}
	if _, err := runInk(t, "cards", "export", "12", "--format", "charx"); err != nil {
		t.Fatal(err)
	}
	if last := (*got)[len(*got)-1]; last != "GET /cards/12/export?format=charx " {
		t.Fatalf("request = %q", last)
	}
}

func TestCardsArchive(t *testing.T) {
	got := stubAPI(t, nil)
	if _, err := runInk(t, "cards", "unarchive", `{"filter": {"archived": true}}`); err != nil {
		t.Fatal(err)
	}
	if want := `POST /bulk {"action":"unarchive","query":{"filter":{"archived":true}}}`; (*got)[0] != want {
		t.Fatalf("request = %s, want %s", (*got)[0], want)
	}
	if _, err := runInk(t, "cards", "archive"); err == nil {
		t.Error("archive without a query succeeded")
	}
}

func TestCardsSources(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/cards/3/sources": `[{"id": 4, "kind": "chub", "ref": "u/a", "linked_by": "auto", "url": "https://chub.ai/characters/u/a"}]`,
	})
	out, err := runInk(t, "cards", "sources", "3")
	if err != nil {
		t.Fatal(err)
	}
	if f := strings.Fields(strings.Split(out, "\n")[1]); !slices.Equal(f, []string{"4", "chub", "u/a", "auto", "https://chub.ai/characters/u/a"}) {
		t.Errorf("row = %q", f)
	}
	for _, args := range [][]string{{"cards", "sources", "add", "3", "https://chub.ai/characters/u/a"}, {"cards", "sources", "remove", "3", "4"}} {
		if _, err := runInk(t, args...); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"GET /cards/3/sources ", `PUT /cards/3/sources {"url":"https://chub.ai/characters/u/a"}`, "DELETE /cards/3/sources/4 "}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q, want %q", *got, want)
	}
	for _, args := range [][]string{{"cards", "sources"}, {"cards", "sources", "add", "3"}, {"cards", "sources", "x"}, {"cards", "sources", "remove", "3", "x"}} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}

func TestCardsSearch(t *testing.T) {
	page := `{"hits": [{"id": 3, "slug": "brakka", "fields": {"name": "Brakka"}, "tags": ["orc", "hero"], "entries": 4}], "total": 2}`
	got := stubAPI(t, map[string]string{"/search": page, "/lorebooks/search": page})
	out, err := runInk(t, "cards", "search", "orc", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[1]); !slices.Equal(f, []string{"3", "brakka", "Brakka", "orc,hero"}) {
		t.Errorf("row = %q", f)
	}
	if !strings.Contains(out, "1 of 2; next: --page 2") {
		t.Errorf("no next-page hint in %q", out)
	}
	for _, args := range [][]string{
		{"cards", "list", "--archived"},
		{"cards", "search", `{"text": "x", "sort": "name"}`, "--page", "3"},
		{"lorebooks", "list"},
	} {
		if _, err := runInk(t, args...); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	want := []string{
		`POST /search {"include":["tags"],"page":{"size":1},"text":"orc"}`,
		`POST /search {"filter":{"archived":true},"include":["tags"]}`,
		`POST /search {"include":["tags"],"page":{"number":3},"sort":"name","text":"x"}`,
		`POST /lorebooks/search {}`,
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q\nwant %q", *got, want)
	}
	if out, _ := runInk(t, "lorebooks", "list"); !strings.Contains(out, "ENTRIES") || !strings.Contains(out, " 4") {
		t.Errorf("lorebooks list = %q", out)
	}
	if out, _ := runInk(t, "cards", "list", "--json"); out != page+"\n" {
		t.Errorf("--json = %q", out)
	}
	if _, err := runInk(t, "cards", "search", "{bad"); err == nil {
		t.Error("bad query JSON succeeded")
	}
}
