package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// entityData returns the CCv3 data object in raw, which is the object itself
// or a file that wraps it as {"spec": ..., "data": {...}}.
func entityData(raw []byte) (json.RawMessage, error) {
	var f struct {
		Spec string          `json:"spec"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if f.Spec != "" && len(f.Data) > 0 {
		return f.Data, nil
	}
	return raw, nil
}

// readData reads the data object from a file, or stdin for -.
func readData(cmd *cobra.Command, file string) (json.RawMessage, error) {
	var raw []byte
	var err error
	if file == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
	} else {
		raw, err = os.ReadFile(file)
	}
	if err != nil {
		return nil, err
	}
	d, err := entityData(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return d, nil
}

// editor is the command line of the user's editor.
func editor() []string {
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if f := strings.Fields(os.Getenv(v)); len(f) > 0 {
			return f
		}
	}
	if runtime.GOOS == "windows" {
		return []string{"notepad"}
	}
	return []string{"vi"}
}

// editData opens data in the user's editor and returns the file it wrote
// and what it holds.
func editData(data json.RawMessage, name string) (string, []byte, error) {
	f, err := os.CreateTemp("", "ink-"+name+"-*.json")
	if err != nil {
		return "", nil, err
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		buf.Reset()
		buf.Write(data)
	}
	buf.WriteByte('\n')
	_, err = f.Write(buf.Bytes())
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", nil, err
	}
	ed := editor()
	run := exec.Command(ed[0], append(ed[1:], f.Name())...)
	// The editor needs the terminal, not whatever cobra was given.
	run.Stdin, run.Stdout, run.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := run.Run(); err != nil {
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("%s: %w", ed[0], err)
	}
	edited, err := os.ReadFile(f.Name())
	return f.Name(), edited, err
}

// sameJSON reports whether a and b hold the same JSON, ignoring whitespace.
func sameJSON(a, b []byte) bool {
	var ca, cb bytes.Buffer
	return json.Compact(&ca, a) == nil && json.Compact(&cb, b) == nil && bytes.Equal(ca.Bytes(), cb.Bytes())
}

func createCmd(k kind) *cobra.Command {
	var slug, message string
	create := &cobra.Command{
		Use:   "create <file>|-",
		Short: "Create a " + k.singular + " from a JSON file, or stdin",
		Long: `The file holds the CCv3 data object, or a whole CCv3 file with spec and
data. The slug defaults to one made from data.name.`,
		Example: "  ink " + k.plural + " create brakka.json --message 'first draft'",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			data, err := readData(cmd, args[0])
			if err != nil {
				return err
			}
			body, err := json.Marshal(struct {
				Data    json.RawMessage `json:"data"`
				Message string          `json:"message,omitempty"`
				Slug    string          `json:"slug,omitempty"`
			}{data, message, slug})
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			out, err := c.do(cmd.Context(), "POST", "/"+k.plural, body)
			if err != nil {
				return err
			}
			return show(cmd, out, func(w io.Writer, e entity) error {
				_, err := fmt.Fprintf(w, "created %s %d, %s\n", k.singular, e.ID, e.Slug)
				return err
			})
		},
	}
	create.Flags().StringVar(&slug, "slug", "", "the slug, lowercase words joined by -")
	create.Flags().StringVarP(&message, "message", "m", "", "a note on the first revision")
	return create
}

func editCmd(k kind) *cobra.Command {
	var message string
	edit := &cobra.Command{
		Use:   "edit <" + k.singular + "-id> [file]",
		Short: "Edit a " + k.singular + "'s data in $EDITOR, or replace it with a file's",
		Long: `Edit opens the data in $VISUAL or $EDITOR, and saves a revision if it changed.
If the save fails, the edited file stays where the error says. With a file,
edit saves its data without opening an editor.`,
		Example: "  ink " + k.plural + " edit 12 -m 'fix typo'\n  ink " + k.plural + " edit 12 brakka.json",
		Args:    cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			c, err := newClient(cmd)
			if err != nil {
				return err
			}
			path := "/" + k.plural + "/" + id
			out, err := c.do(cmd.Context(), "GET", path, nil)
			if err != nil {
				return err
			}
			var e entity
			if err := json.Unmarshal(out, &e); err != nil {
				return err
			}
			var data json.RawMessage
			kept := ""
			if len(args) == 2 {
				if data, err = readData(cmd, args[1]); err != nil {
					return err
				}
			} else {
				file, edited, err := editData(e.Revision.Data, k.singular+"-"+id)
				if err != nil {
					return err
				}
				kept = file
				if data, err = entityData(edited); err != nil {
					return fmt.Errorf("%w; your edit is in %s", err, kept)
				}
			}
			if sameJSON(data, e.Revision.Data) {
				if kept != "" {
					os.Remove(kept)
				}
				_, err := fmt.Fprintln(cmd.OutOrStdout(), "no changes")
				return err
			}
			body, err := json.Marshal(struct {
				Base    int64           `json:"base_revision_id"`
				Data    json.RawMessage `json:"data"`
				Message string          `json:"message,omitempty"`
			}{e.Revision.ID, data, message})
			if err != nil {
				return err
			}
			out, err = c.do(cmd.Context(), "PUT", path, body)
			if err != nil {
				if kept != "" {
					return fmt.Errorf("%w; your edit is in %s", err, kept)
				}
				return err
			}
			if kept != "" {
				os.Remove(kept)
			}
			return show(cmd, out, func(w io.Writer, e entity) error {
				_, err := fmt.Fprintf(w, "%s %d is at revision %d\n", k.singular, e.ID, e.Revision.ID)
				return err
			})
		},
	}
	edit.Flags().StringVarP(&message, "message", "m", "", "a note on the revision")
	return edit
}
