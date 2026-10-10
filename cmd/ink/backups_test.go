package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBackups(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/backups": `[{"name": "assets-20261010", "type": "assets", "created_at": "2026-10-10T10:00:00Z", "size": 2048, "status": "complete"},
			{"type": "config", "created_at": "2026-10-10T09:00:00Z", "status": "failed", "job_id": 7, "error": "disk full"}]`,
		"/backups/assets-20261010": "ARCHIVE",
	})
	out, err := runInk(t, "backups")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); !slices.Equal(f, []string{"NAME", "TYPE", "STATUS", "CREATED", "SIZE", "ERROR"}) {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); len(f) != 6 || f[0] != "assets-20261010" || f[5] != "2048" {
		t.Errorf("row = %q", lines[1])
	}
	if !strings.Contains(lines[2], "disk full") {
		t.Errorf("row = %q", lines[2])
	}

	*got = nil
	if out, err := runInk(t, "backups", "create", "assets"); err != nil || !strings.Contains(out, "ink jobs watch 9") {
		t.Fatalf("create: %q, %v", out, err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "a.tar.gz")
	if _, err := runInk(t, "backups", "download", "assets-20261010", file); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(file); string(b) != "ARCHIVE" {
		t.Errorf("file = %q", b)
	}
	t.Chdir(dir)
	if _, err := runInk(t, "backups", "download", "assets-20261010"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile("assets-20261010.tar.gz"); string(b) != "ARCHIVE" {
		t.Errorf("default file = %q", b)
	}
	if out, err := runInk(t, "backups", "restore", file, "--replace", "--yes"); err != nil || !strings.Contains(out, "ink jobs watch 9") {
		t.Fatalf("restore: %q, %v", out, err)
	}
	if len(*got) != 4 || (*got)[0] != `POST /backups {"type":"assets"}` || (*got)[2] != "GET /backups/assets-20261010 " {
		t.Fatalf("requests = %q", *got)
	}
	restore := (*got)[3]
	for _, want := range []string{"POST /restores ", `name="archive"`, "ARCHIVE", `name="replace"`, "true"} {
		if !strings.Contains(restore, want) {
			t.Errorf("restore request lacks %q: %q", want, restore)
		}
	}

	*got = nil
	for _, args := range [][]string{
		{"create", "bogus"}, {"create"}, {"download"}, {"restore", file}, {"restore", filepath.Join(dir, "missing"), "--yes"},
	} {
		if _, err := runInk(t, append([]string{"backups"}, args...)...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
	if len(*got) != 0 {
		t.Errorf("failed commands sent %q", *got)
	}
}

func TestDownloadRemovesPartialFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, "PART")
	}))
	t.Cleanup(srv.Close)
	t.Setenv("INNKEEPER_URL", srv.URL)
	t.Setenv("INNKEEPER_TOKEN", "tok")
	file := filepath.Join(t.TempDir(), "a.tar.gz")
	if _, err := runInk(t, "backups", "download", "a", file); err == nil {
		t.Fatal("a broken transfer succeeded")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("partial file left: %v", err)
	}
}
