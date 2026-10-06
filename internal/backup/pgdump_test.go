package backup

import (
	"io"
	"slices"
	"strings"
	"testing"
)

func TestPgDumpKeepsPasswordOffCommandLine(t *testing.T) {
	for _, u := range []string{
		"postgres://app:s3cret@db:5432/innkeeper?sslmode=disable",
		"postgresql://app@db:5432/innkeeper?sslmode=disable&password=s3cret",
	} {
		cmd, err := pgDumpCommand(u, io.Discard, "--format=plain")
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
		if !slices.Contains(cmd.Args, "--format=plain") {
			t.Fatalf("extra args missing: %v", cmd.Args)
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

func TestRunPgDumpReportsStderr(t *testing.T) {
	cmd, err := pgDumpCommand("postgres://app@127.0.0.1:1/x", io.Discard, "--no-such-flag")
	if err != nil {
		t.Fatal(err)
	}
	// sh stands in for pg_dump so the test needs no PostgreSQL client.
	cmd.Path, cmd.Args, cmd.Err = "/bin/sh", []string{"sh", "-c", "echo boom >&2; exit 1"}, nil
	if err := runPgDump(cmd); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want stderr in it", err)
	}
}
