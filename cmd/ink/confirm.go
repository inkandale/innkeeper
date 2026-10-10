package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// stdinIsTerminal reports whether r is a terminal; tests replace it.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// confirm asks question unless yes is set. Without a terminal it fails
// instead, so a script can't wait forever for an answer.
func confirm(cmd *cobra.Command, yes bool, question string) error {
	if yes {
		return nil
	}
	in := cmd.InOrStdin()
	if !stdinIsTerminal(in) {
		return errors.New("stdin isn't a terminal, so nothing can confirm this; pass --yes")
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)
	answer, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return nil
	}
	return errors.New("cancelled")
}
