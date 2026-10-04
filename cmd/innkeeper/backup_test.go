package main

import (
	"os"
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
	cmd := pgDumpCommand("postgres://app:s3cret@db:5432/innkeeper?sslmode=disable", "/backups/x.dump")
	if strings.Contains(strings.Join(cmd.Args, " "), "s3cret") {
		t.Fatalf("password in args: %v", cmd.Args)
	}
	if !slices.Contains(cmd.Env, "PGPASSWORD=s3cret") {
		t.Fatal("PGPASSWORD not set")
	}
	if !slices.Contains(cmd.Args, "--dbname=postgres://app@db:5432/innkeeper?sslmode=disable") {
		t.Fatalf("args = %v", cmd.Args)
	}
}
