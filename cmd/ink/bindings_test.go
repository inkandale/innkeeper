package main

import (
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

// runInkIn runs ink with stdin, as a terminal when tty is true, and returns
// stderr then stdout.
func runInkIn(t *testing.T, stdin string, tty bool, args ...string) (string, error) {
	out, stderr, err := runInkSplit(t, stdin, tty, args...)
	return stderr + out, err
}

// runInkSplit is runInkIn with stdout and stderr apart.
func runInkSplit(t *testing.T, stdin string, tty bool, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	old := stdinIsTerminal
	stdinIsTerminal = func(io.Reader) bool { return tty }
	t.Cleanup(func() { stdinIsTerminal = old })
	var out, errOut bytes.Buffer
	root := newRoot()
	root.SetArgs(args)
	root.SetIn(strings.NewReader(stdin))
	root.SetOut(&out)
	root.SetErr(&errOut)
	err = root.Execute()
	return out.String(), errOut.String(), err
}

const bindingJSON = `{"id": 5, "instance_id": 3, "card_id": 12, "remote_id": "brakka.png", "status": "remote_changed",
	"ahead": false, "remote_state": "changed", "checked_at": "2026-10-10T10:00:00Z",
	"last_push": {"id": 8, "kind": "push", "revision_id": 40, "pushed_hash": "h", "remote_id": "brakka.png", "pushed_at": "2026-10-09T10:00:00Z"},
	"changes": [{"field": "first_mes", "before": "Hail.", "after": "Well met."}], "created_at": "2026-10-01T10:00:00Z"}`

func TestSyncBindings(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/cards/12/bindings":     "[" + bindingJSON + "]",
		"/bindings/5":            bindingJSON,
		"POST /bindings/5/check": bindingJSON,
		"/bindings/5/pushes":     `[{"id": 8, "kind": "push", "revision_id": 40, "remote_id": "brakka.png", "pushed_at": "2026-10-09T10:00:00Z"}]`,
	})
	out, err := runInk(t, "sync", "bindings", "12")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out, "\n")
	if f := strings.Fields(lines[0]); !slices.Equal(f, []string{"ID", "INSTANCE", "REMOTE", "ID", "STATUS", "LAST", "PUSH", "CHECKED"}) {
		t.Errorf("header = %q", lines[0])
	}
	if f := strings.Fields(lines[1]); len(f) < 4 || !slices.Equal(f[:4], []string{"5", "3", "brakka.png", "remote_changed"}) {
		t.Errorf("row = %q", lines[1])
	}
	if out, _ := runInk(t, "sync", "bindings", "12", "--json"); out != "["+bindingJSON+"]\n" {
		t.Errorf("--json = %q", out)
	}
	for _, args := range [][]string{{"bindings", "show", "5"}, {"status", "5"}} {
		out, err := runInk(t, append([]string{"sync"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"remote_changed", "first_mes", "pushed: Hail.", "app:    Well met."} {
			if !strings.Contains(out, want) {
				t.Errorf("%q: missing %q in:\n%s", args, want, out)
			}
		}
	}
	out, err = runInk(t, "sync", "bindings", "pushes", "5")
	if err != nil {
		t.Fatal(err)
	}
	if f := strings.Fields(strings.Split(out, "\n")[1]); len(f) < 4 || !slices.Equal(f[:4], []string{"8", "push", "40", "brakka.png"}) {
		t.Errorf("pushes row = %q", f)
	}
	want := []string{"GET /cards/12/bindings ", "GET /cards/12/bindings ", "GET /bindings/5 ", "POST /bindings/5/check ", "GET /bindings/5/pushes "}
	if !slices.Equal(*got, want) {
		t.Errorf("requests = %q, want %q", *got, want)
	}
	for _, args := range [][]string{{"bindings", "x"}, {"bindings", "show"}, {"status", "x"}, {"bindings", "pushes", "x"}} {
		if _, err := runInk(t, append([]string{"sync"}, args...)...); err == nil {
			t.Errorf("%q succeeded", args)
		}
	}
}

func TestSyncPreview(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/cards/12/push-preview?instance_id=3": `{"replaces": true, "new_copy": false, "dropped_fields": ["alternate_greetings"],
			"changes": [{"field": "first_mes", "before": "Well met.", "after": "Hail."}], "confirm": "c1"}`,
	})
	out, err := runInk(t, "sync", "preview", "3", "12")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"replaces the app's copy", "first_mes", "app:    Well met.", "pushed: Hail.", "dropped: alternate_greetings"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if (*got)[0] != "GET /cards/12/push-preview?instance_id=3 " {
		t.Errorf("request = %q", (*got)[0])
	}
}

func TestSyncUnbind(t *testing.T) {
	got := stubAPI(t, nil)
	if _, err := runInkIn(t, "", false, "sync", "unbind", "5"); err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("unbind without a terminal: err = %v", err)
	}
	if _, err := runInkIn(t, "n\n", true, "sync", "unbind", "5"); err == nil {
		t.Fatal("unbind answered no succeeded")
	}
	if len(*got) != 0 {
		t.Fatalf("refused unbinds sent %q", *got)
	}
	if _, err := runInkIn(t, "y\n", true, "sync", "unbind", "5"); err != nil {
		t.Fatal(err)
	}
	if _, err := runInkIn(t, "", false, "sync", "unbind", "5", "--yes"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"DELETE /bindings/5 ", "DELETE /bindings/5 "}; !slices.Equal(*got, want) {
		t.Errorf("requests = %q, want %q", *got, want)
	}
}
