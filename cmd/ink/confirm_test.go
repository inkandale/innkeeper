package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestConfirm(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetErr(io.Discard)
	cmd.SetIn(strings.NewReader("y\n"))
	if err := confirm(cmd, true, "Go?"); err != nil {
		t.Fatalf("--yes: %v", err)
	}
	if err := confirm(cmd, false, "Go?"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("without a terminal: %v", err)
	}
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return true }
	t.Cleanup(func() { stdinIsTerminal = old })
	for in, ok := range map[string]bool{"y\n": true, "YES\n": true, "\n": false, "no\n": false, "": false} {
		cmd.SetIn(strings.NewReader(in))
		if err := confirm(cmd, false, "Go?"); (err == nil) != ok {
			t.Errorf("answer %q: %v", in, err)
		}
	}
}

func TestConfirmRefusesWithoutTerminal(t *testing.T) {
	got := stubAPI(t, nil)
	archive := filepath.Join(t.TempDir(), "a.tar.gz")
	if err := os.WriteFile(archive, []byte("ARCHIVE"), 0o600); err != nil {
		t.Fatal(err)
	}
	// /dev/null is a character device but not a terminal.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	for _, args := range [][]string{{"plugins", "purge", "x"}, {"backups", "restore", archive}} {
		if _, err := runInkReader(t, devNull, args...); err == nil || !strings.Contains(err.Error(), "--yes") {
			t.Errorf("%q: err = %v", args, err)
		}
	}
	if len(*got) != 0 {
		t.Errorf("sent %q", *got)
	}
}
