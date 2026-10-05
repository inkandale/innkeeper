package targets

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
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

// A push of identical content leaves the accepted hash alone, so a check that
// read the binding before it must still lose.
func TestCheckLosesToAPushOfSameContent(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	old, err := e.s.stored(e.ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	e.push(e.card)
	if err := e.s.record(e.ctx, b.ID, old, "missing", nil); err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.Binding(e.ctx, b.ID); err != nil || got.Status != "in_sync" || got.RemoteState != "unchanged" {
		t.Fatalf("binding = %+v, %v", got, err)
	}
}

// A check's write that waits on a push's row lock must lose too: the guard has
// to be re-evaluated against the pushed row, not an old snapshot.
func TestCheckLosesToAPushInFlight(t *testing.T) {
	e := setup(t, nil)
	b := e.push(e.card)
	old, err := e.s.stored(e.ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := e.pool.Begin(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(e.ctx)
	// Stands in for a push's upsert.
	if _, err := tx.Exec(e.ctx, `UPDATE binding SET version = version + 1 WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- e.s.record(e.ctx, b.ID, old, "missing", nil) }()
	time.Sleep(200 * time.Millisecond)
	if err := tx.Commit(e.ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, err := e.s.Binding(e.ctx, b.ID); err != nil || got.RemoteState != "unchanged" {
		t.Fatalf("binding = %+v, %v", got, err)
	}
}

func TestScanChecksBindingsOnceTheirTargetCanPull(t *testing.T) {
	e := setup(t, map[string]any{"pull": false})
	e.push(e.card)
	if p, err := e.s.Scan(e.ctx, func(ScanProgress) {}); err != nil || p.Checked != 0 {
		t.Fatalf("scan before pull = %+v, %v", p, err)
	}
	if err := e.reg.UpdateInstance(e.ctx, e.inst, plugins.InstanceEdit{Config: map[string]any{"dir": e.dir}}); err != nil {
		t.Fatal(err)
	}
	if p, err := e.s.Scan(e.ctx, func(ScanProgress) {}); err != nil || p.Checked != 1 {
		t.Fatalf("scan after pull = %+v, %v", p, err)
	}
	if s := e.status(e.card); s != "in_sync" {
		t.Fatalf("status = %s, want in_sync", s)
	}
}
