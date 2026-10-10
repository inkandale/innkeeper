package main

import (
	"io"
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
