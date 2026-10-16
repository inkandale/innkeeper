package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	if _, err := runInk(t, "cards", "archive", "true"); err == nil || !strings.Contains(err.Error(), "neither an ID nor a query") {
		t.Errorf("archive true: err = %v", err)
	}
	*got = nil
	for _, args := range [][]string{{"cards", "archive", "3"}, {"lorebooks", "unarchive", "5"}, {"lorebooks", "archive", `{"text": "old"}`}} {
		if _, err := runInk(t, args...); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	want := []string{
		`PUT /cards/3/archived {"archived":true}`,
		`PUT /lorebooks/5/archived {"archived":false}`,
		`POST /bulk {"action":"archive","kind":"lorebook","query":{"text":"old"}}`,
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q\nwant %q", *got, want)
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
	got := stubAPI(t, map[string]string{"/cards/search": page, "/lorebooks/search": page})
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
		{"cards", "search", `{"page": {"size": 1}}`},
	} {
		if _, err := runInk(t, args...); err != nil {
			t.Fatalf("%q: %v", args, err)
		}
	}
	want := []string{
		`POST /cards/search {"include":["tags"],"page":{"size":1},"text":"orc"}`,
		`POST /cards/search {"filter":{"archived":true},"include":["tags"]}`,
		`POST /cards/search {"include":["tags"],"page":{"number":3},"sort":"name","text":"x"}`,
		`POST /lorebooks/search {}`,
		`POST /cards/search {"include":["tags"],"page":{"size":1}}`,
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q\nwant %q", *got, want)
	}
	if out, _ := runInk(t, "lorebooks", "list"); !strings.Contains(out, "ENTRIES") || !strings.Contains(out, " 4") {
		t.Errorf("lorebooks list = %q", out)
	}
	if out, _ := runInk(t, "cards", "search", `{"page": {"size": 1}}`); !strings.Contains(out, "next: --page 2") {
		t.Errorf("a query's page size gave no next-page hint: %q", out)
	}
	if out, _ := runInk(t, "cards", "search", `{"page": {"size": 1}}`, "--page", "2"); strings.Contains(out, "next:") {
		t.Errorf("hinted past the last page: %q", out)
	}
	if out, _ := runInk(t, "cards", "list", "--json"); out != page+"\n" {
		t.Errorf("--json = %q", out)
	}
	if _, err := runInk(t, "cards", "search", "{bad"); err == nil {
		t.Error("bad query JSON succeeded")
	}
}

const brakka = `{"id": 3, "slug": "brakka", "provenance": "api", "created_at": "2026-10-07T10:00:00Z",
	"revision": {"id": 7, "data": {"name": "Brakka", "description": "An orc", "tags": ["greenskin"]}, "created_at": "2026-10-08T10:00:00Z"},
	"lorebooks": [5]}`

func TestCardsShowAndHistory(t *testing.T) {
	stubAPI(t, map[string]string{
		"/cards/3":           brakka,
		"/cards/3/revisions": `[{"id": 7, "source": "ui", "message": "fix typo", "created_at": "2026-10-08T10:00:00Z"}]`,
	})
	out, err := runInk(t, "cards", "show", "3")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Brakka", "brakka", "greenskin", "5", "7"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q: %q", want, out)
		}
	}
	out, err = runInk(t, "cards", "history", "3")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); !slices.Equal(f, []string{"ID", "CREATED", "SOURCE", "MESSAGE"}) {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); f[0] != "7" || f[3] != "ui" || f[4] != "fix" {
		t.Errorf("row = %q", lines[1])
	}
	for _, args := range [][]string{{"cards", "show", "x"}, {"lorebooks", "history"}} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}

func TestCardsCreate(t *testing.T) {
	got := stubAPI(t, map[string]string{"/cards": brakka})
	file := filepath.Join(t.TempDir(), "brakka.json")
	if err := os.WriteFile(file, []byte(`{"spec": "chara_card_v3", "data": {"name": "Brakka"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runInk(t, "cards", "create", file, "--slug", "brakka", "--message", "new")
	if err != nil || out != "created card 3, brakka\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if want := `POST /cards {"data":{"name":"Brakka"},"message":"new","slug":"brakka"}`; (*got)[0] != want {
		t.Fatalf("request = %s, want %s", (*got)[0], want)
	}
	if err := os.WriteFile(file, []byte(`{bad`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runInk(t, "cards", "create", file); err == nil {
		t.Error("created a card from bad JSON")
	}
}

// fakeEditor sets EDITOR to a script that writes content over the file it opens.
func fakeEditor(t *testing.T, content string) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake editor is a shell script")
	}
	script := filepath.Join(t.TempDir(), "ed")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s' '"+content+"' > \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", script)
}

func TestCardsEdit(t *testing.T) {
	got := stubAPI(t, map[string]string{"/cards/3": brakka})
	fakeEditor(t, `{"name": "Brakka II"}`)
	out, err := runInk(t, "cards", "edit", "3", "-m", "rename")
	if err != nil || out != "card 3 is at revision 7\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	want := []string{"GET /cards/3 ", `PUT /cards/3 {"base_revision_id":7,"data":{"name":"Brakka II"},"message":"rename"}`}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q\nwant %q", *got, want)
	}

	*got = nil
	fakeEditor(t, `{"name": "Brakka", "description": "An orc", "tags": ["greenskin"]}`)
	if out, err := runInk(t, "cards", "edit", "3"); err != nil || out != "no changes\n" {
		t.Fatalf("unchanged: out = %q, err = %v", out, err)
	}
	if len(*got) != 1 {
		t.Fatalf("unchanged edit sent %q", *got)
	}

	fakeEditor(t, `{bad`)
	_, err = runInk(t, "cards", "edit", "3")
	if err == nil || !strings.Contains(err.Error(), "your edit is in ") {
		t.Fatalf("bad JSON: err = %v", err)
	}
	kept := err.Error()[strings.LastIndex(err.Error(), " ")+1:]
	if b, _ := os.ReadFile(kept); string(b) != "{bad" {
		t.Errorf("kept file %s holds %q", kept, b)
	}
	os.Remove(kept)

	*got = nil
	file := filepath.Join(t.TempDir(), "b.json")
	if err := os.WriteFile(file, []byte(`{"name": "B"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runInk(t, "cards", "edit", "3", file); err != nil {
		t.Fatal(err)
	}
	if want := `PUT /cards/3 {"base_revision_id":7,"data":{"name":"B"}}`; (*got)[1] != want {
		t.Fatalf("request = %s, want %s", (*got)[1], want)
	}
}

func TestEditKeepsTheFileWhenTheSaveFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			http.Error(w, `{"detail": "stale base revision"}`, http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(brakka))
	}))
	defer srv.Close()
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")
	fakeEditor(t, `{"name": "C"}`)
	_, err := runInk(t, "cards", "edit", "3")
	if err == nil || !strings.Contains(err.Error(), "stale base revision") || !strings.Contains(err.Error(), "your edit is in ") {
		t.Fatalf("err = %v", err)
	}
	os.Remove(err.Error()[strings.LastIndex(err.Error(), " ")+1:])
}

func TestCardsNeedAnID(t *testing.T) {
	stubAPI(t, nil)
	for _, args := range [][]string{{"cards", "edit", "x"}, {"lorebooks", "create"}, {"cards", "bogus"}, {"lorebooks", "bogus", "1"}} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}

func TestLorebooksExportAndSources(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/lorebooks/5":         `{"id": 5, "slug": "orcs", "revision": {"id": 8, "data": {"name": "Orcs", "entries": []}}}`,
		"/lorebooks/5/sources": `[]`,
	})
	file := filepath.Join(t.TempDir(), "orcs.json")
	if _, err := runInk(t, "lorebooks", "export", "5", file); err != nil {
		t.Fatal(err)
	}
	want := `{"spec":"lorebook_v3","data":{"name":"Orcs","entries":[]}}`
	if b, _ := os.ReadFile(file); string(b) != want {
		t.Fatalf("file = %s, want %s", b, want)
	}
	if out, err := runInk(t, "lorebooks", "export", "5"); err != nil || out != want+"\n" {
		t.Fatalf("stdout = %q, err = %v", out, err)
	}
	if _, err := runInk(t, "lorebooks", "sources", "add", "5", "https://chub.ai/lorebooks/u/orcs"); err != nil {
		t.Fatal(err)
	}
	if last := (*got)[len(*got)-1]; last != `PUT /lorebooks/5/sources {"url":"https://chub.ai/lorebooks/u/orcs"}` {
		t.Fatalf("request = %q", last)
	}
	if _, err := runInk(t, "lorebooks", "export", "5", "--format", "png"); err == nil {
		t.Error("lorebooks export took --format")
	}
}

func TestCardsImport(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(body))
		if r.URL.Path == "/api/blobs" {
			_, _ = w.Write([]byte(`{"sha256": "abc"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id": 9}`))
	}))
	defer srv.Close()
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")
	file := filepath.Join(t.TempDir(), "brakka.png")
	if err := os.WriteFile(file, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runInk(t, "lorebooks", "import", "4", file)
	if err != nil || out != "started job 9; follow it with: ink jobs watch 9\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	want := []string{
		"POST /api/blobs application/octet-stream PNG",
		`POST /api/imports application/json {"instance_id":4,"params":{"blob":"abc","name":"brakka.png"}}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("requests = %q\nwant %q", got, want)
	}
	for _, args := range [][]string{{"cards", "import", "x", file}, {"cards", "import", "4", file + ".missing"}} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}

func TestCardsImportURL(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.Path+" "+string(body))
		_, _ = w.Write([]byte(`{"id": 9}`))
	}))
	defer srv.Close()
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")
	out, err := runInk(t, "cards", "import-url", "7", "https://chub.ai/characters/u/brakka")
	if err != nil || out != "started job 9; follow it with: ink jobs watch 9\n" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	want := `POST /api/imports {"instance_id":7,"params":{"url":"https://chub.ai/characters/u/brakka"}}`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("requests = %q\nwant %q", got, want)
	}
	if _, err := runInk(t, "cards", "import-url", "x", "https://chub.ai/characters/u/brakka"); err == nil {
		t.Error("a non-numeric instance ID was accepted")
	}
}
