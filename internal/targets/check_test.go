package targets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/library"
)

func TestCheck(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	// A change in whitespace only isn't a change.
	e.editRemote("r1", "description", "  A dwarf smith.\r\n")
	if b = e.check(b); b.Status != "in_sync" || b.RemoteState != "unchanged" || b.CheckedAt == nil {
		t.Fatalf("after a whitespace edit: %+v", b)
	}
	e.editRemote("r1", "first_mes", "Well met.")
	b = e.check(b)
	if b.Status != "remote_changed" || len(b.Changes) != 1 || b.Changes[0].Field != "first_mes" ||
		string(b.Changes[0].Before) != `"Hail."` || string(b.Changes[0].After) != `"Well met."` {
		t.Fatalf("after an app edit: %+v", b)
	}
	e.update(e.card, func(ed *library.Edit) { ed.Data = setField(ed.Data, "description", "A grumpy dwarf smith.") })
	if s := e.status(e.card); s != "diverged" {
		t.Fatalf("after both changed: %s", s)
	}
}

func TestScan(t *testing.T) {
	e := setup(t, nil)
	other := e.newCard("Carrow")
	e.push(e.card)
	e.push(other)
	e.editRemote("r1", "first_mes", "Well met.")
	if err := os.Remove(filepath.Join(e.dir, "r2.json")); err != nil {
		t.Fatal(err)
	}
	var reports int
	p, err := e.s.Scan(e.ctx, func(ScanProgress) { reports++ })
	if err != nil {
		t.Fatal(err)
	}
	if p.Checked != 2 || p.Changed != 1 || p.Missing != 1 || p.Failed != 0 || reports != 2 {
		t.Fatalf("scan = %+v after %d reports", p, reports)
	}
	if e.status(e.card) != "remote_changed" || e.status(other) != "missing" {
		t.Fatal("scan didn't record what it found")
	}
}
