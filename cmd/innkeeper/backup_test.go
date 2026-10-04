package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyBlobs(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "ab", "cd", "abcd1"), "one")
	write(t, filepath.Join(src, "tmp", "upload"), "unfinished")
	write(t, filepath.Join(src, "staging", "x"), "unfinished")

	n, err := copyBlobs(src, dst)
	if err != nil || n != 1 {
		t.Fatalf("copyBlobs = %d, %v; want 1", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "ab", "cd", "abcd1")); string(b) != "one" {
		t.Fatalf("copied %q", b)
	}
	for _, skipped := range []string{"tmp", "staging"} {
		if _, err := os.Stat(filepath.Join(dst, skipped)); err == nil {
			t.Fatalf("copied %s", skipped)
		}
	}

	write(t, filepath.Join(src, "ef", "01", "ef012"), "two")
	if n, err := copyBlobs(src, dst); err != nil || n != 1 {
		t.Fatalf("second copyBlobs = %d, %v; want only the new blob", n, err)
	}
}

func TestPgDumpKeepsPasswordOffCommandLine(t *testing.T) {
	for _, u := range []string{
		"postgres://app:s3cret@db:5432/innkeeper?sslmode=disable",
		"postgresql://app@db:5432/innkeeper?sslmode=disable&password=s3cret",
	} {
		cmd, err := pgDumpCommand(u, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(cmd.Args, " "), "s3cret") {
			t.Fatalf("password in args: %v", cmd.Args)
		}
		if !slices.Contains(cmd.Env, "PGPASSWORD=s3cret") {
			t.Fatal("PGPASSWORD not set")
		}
		if !slices.Contains(cmd.Args, "--dbname=postgres://app@db:5432/innkeeper?sslmode=disable") &&
			!slices.Contains(cmd.Args, "--dbname=postgresql://app@db:5432/innkeeper?sslmode=disable") {
			t.Fatalf("args = %v", cmd.Args)
		}
	}
}

func TestPgDumpRejectsNonURL(t *testing.T) {
	for _, u := range []string{"host=db password=s3cret dbname=x", "mysql://a@b/c"} {
		if _, err := pgDumpCommand(u, io.Discard); err == nil || strings.Contains(err.Error(), "s3cret") {
			t.Fatalf("%q: err = %v", u, err)
		}
	}
}

func TestCopyBlobsKeepsExisting(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, filepath.Join(src, "ab", "cd", "abcd1"), "new")
	write(t, filepath.Join(dst, "ab", "cd", "abcd1"), "old")
	if n, err := copyBlobs(src, dst); err != nil || n != 0 {
		t.Fatalf("copyBlobs = %d, %v; want 0", n, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "ab", "cd", "abcd1")); string(b) != "old" {
		t.Fatalf("overwrote: %q", b)
	}
}

func TestCopyBlobsPrivate(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "blobs")
	write(t, filepath.Join(src, "ab", "cd", "abcd1"), "one")
	if _, err := copyBlobs(src, dst); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]os.FileMode{
		filepath.Join(dst, "ab"):                0o700,
		filepath.Join(dst, "ab", "cd", "abcd1"): 0o600,
	} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Fatalf("%s: %v, %v; want %v", p, fi, err, want)
		}
	}
}

func TestWriteDumpFailureLeavesNothing(t *testing.T) {
	dir := t.TempDir()
	// A false command stands in for a failing pg_dump.
	if _, err := writeDump(dir, func(w io.Writer) *exec.Cmd { return exec.Command("false") }); err == nil {
		t.Fatal("want error")
	}
	if es, _ := os.ReadDir(dir); len(es) != 0 {
		t.Fatalf("left %v", es)
	}
}

func TestWriteDumpNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	mk := func(w io.Writer) *exec.Cmd { return exec.Command("true") }
	for range 2 {
		if _, err := writeDump(dir, mk); err != nil {
			t.Fatal(err)
		}
	}
	if es, _ := os.ReadDir(dir); len(es) != 2 {
		t.Fatalf("want 2 dumps, got %v", es)
	}
}
