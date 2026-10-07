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
