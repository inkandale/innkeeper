package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runInkReader runs ink with stdin and returns everything it printed.
func runInkReader(t *testing.T, stdin io.Reader, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := newRoot()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(&out)
	root.SetErr(&out)
	err := root.Execute()
	return out.String(), err
}

func TestInstances(t *testing.T) {
	ok := `{"id": 3, "plugin": "lumiverse", "name": "Lumi", "config": {"url": "u"}, "secrets": ["password"], "created_at": "2026-10-07T10:00:00Z"}`
	broken := `{"id": 4, "plugin": "sillytavern", "name": "ST", "config": {}, "secrets": [], "broken_error": "refused", "broken_at": "2026-10-07T10:00:00Z"}`
	got := stubAPI(t, map[string]string{"GET /instances": "[" + ok + "," + broken + "]", "/instances/3": ok, "/instances/4": broken})

	out, err := runInk(t, "instances")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); !slices.Equal(f, []string{"ID", "PLUGIN", "NAME", "STATUS"}) {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[2]); !slices.Equal(f, []string{"4", "sillytavern", "ST", "broken"}) {
		t.Errorf("row = %q", lines[2])
	}
	if out, _ := runInk(t, "instances", "show", "3"); !strings.Contains(out, `{"url":"u"}`) || !strings.Contains(out, "password") {
		t.Errorf("show = %q", out)
	}

	secrets := filepath.Join(t.TempDir(), "s.json")
	if err := os.WriteFile(secrets, []byte(`{"password": "p"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nullSecrets := filepath.Join(t.TempDir(), "null.json")
	if err := os.WriteFile(nullSecrets, []byte(`{"password": null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	*got = nil
	if _, err := runInk(t, "instances", "create", "a", "b", "--secrets", nullSecrets); err == nil || len(*got) != 0 {
		t.Errorf("create with a null secret: %v, sent %q", err, *got)
	}
	if _, err := runInk(t, "instances", "create", "lumiverse", "Lumi", `{"url": "u"}`, "--secrets", secrets); err != nil {
		t.Fatal(err)
	}
	if _, err := runInkReader(t, strings.NewReader(`{"password": null}`), "instances", "configure", "3", "--name", "New", "--secrets", "-"); err != nil {
		t.Fatal(err)
	}
	if _, err := runInk(t, "instances", "delete", "3"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`POST /instances {"config":{"url":"u"},"name":"Lumi","plugin":"lumiverse","secrets":{"password":"p"}}`,
		`PATCH /instances/3 {"name":"New","secrets":{"password":null}}`,
		"DELETE /instances/3 ",
	}
	if !slices.Equal(*got, want) {
		t.Fatalf("requests = %q, want %q", *got, want)
	}

	if out, err := runInk(t, "instances", "check", "3"); err != nil || out != "ok\n" {
		t.Errorf("check ok: %q, %v", out, err)
	}
	if _, err := runInk(t, "instances", "check", "4"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("check broken: %v", err)
	}

	for _, args := range [][]string{
		{"instances", "show", "x"}, {"instances", "create", "lumiverse"}, {"instances", "create", "a", "b", "{bad"},
		{"instances", "create", "a", "b", "--secrets", "/nonexistent"}, {"instances", "configure", "3"}, {"instances", "delete"},
	} {
		if _, err := runInk(t, args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}
