package targets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/jobs"
	"github.com/davidgibbons/innkeeper/internal/library"
	"github.com/davidgibbons/innkeeper/internal/plugins"
)

// A resolve outside a sync waits for it; the sync's own resolves don't.
func TestResolveWaitsForSync(t *testing.T) {
	e := setup(t, nil)
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	e.appCard("r2", map[string]any{"name": "Mira", "description": miraText}, nil)
	items := e.reconcile(ReconcileParams{InstanceID: e.inst}, "r1", "r2")
	runCtx, release, err := e.s.lockSync(e.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Resolve(runCtx, Resolution{InstanceID: e.inst, RemoteID: "r2", AppHash: items["r2"].AppHash,
		Action: "import"}); err != nil {
		t.Fatalf("the sync's own resolve: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.s.Resolve(e.ctx, Resolution{InstanceID: e.inst, RemoteID: "r1", AppHash: items["r1"].AppHash,
			Action: "import"})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("resolve finished during a sync: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// addApp adds another fake target app with a dir of its own.
func (e *env) addApp() (int64, string) {
	e.t.Helper()
	dir := e.t.TempDir()
	id, err := e.reg.CreateInstance(e.ctx, plugins.NewInstance{Plugin: "fake",
		Name: fmt.Sprintf("App %s", filepath.Base(dir)), Config: map[string]any{"dir": dir}})
	if err != nil {
		e.t.Fatal(err)
	}
	return id, dir
}

// copyItem puts e's app item remoteID into another app's dir too.
func (e *env) copyItem(remoteID, dir string) {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.dir, remoteID+".json"))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, remoteID+".json"), raw, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// cardsNamed counts library cards whose head is named name.
func (e *env) cardsNamed(name string) []int64 {
	e.t.Helper()
	var ids []int64
	rows, err := e.pool.Query(e.ctx, `SELECT c.id FROM card c JOIN card_revision r ON r.id = c.head_revision_id
		WHERE r.data->>'name' = $1 ORDER BY c.id`, name)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			e.t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// A sync runs every app in turn, so a card in two apps ends up as one card.
func TestSyncEveryApp(t *testing.T) {
	e := setup(t, nil)
	inst2, dir2 := e.addApp()
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	e.copyItem("r1", dir2)
	raw, _ := json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0",
		"data": map[string]any{"name": "Mira", "description": miraText}})
	if err := os.WriteFile(filepath.Join(dir2, "r2.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Sync(e.ctx, SyncParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Apps) != 2 || res.Apps[0].InstanceID != e.inst || res.Apps[1].InstanceID != inst2 {
		t.Fatalf("apps = %+v", res.Apps)
	}
	lia := e.cardsNamed("Lia")
	if len(lia) != 1 || len(e.cardsNamed("Mira")) != 1 {
		t.Fatalf("Lia %v, Mira %v", lia, e.cardsNamed("Mira"))
	}
	if bs, err := e.s.Bindings(e.ctx, lia[0]); err != nil || len(bs) != 2 {
		t.Errorf("Lia bindings %d, %v", len(bs), err)
	}
	for _, app := range res.Apps {
		j, err := jobs.New(e.pool).Get(e.ctx, app.JobID)
		var snap Snapshot
		if err != nil || app.Error != "" || j.Kind != ReconcileKind || j.Status != jobs.StatusSucceeded ||
			*j.InstanceID != app.InstanceID || json.Unmarshal(j.Progress, &snap) != nil || len(snap.Items) == 0 {
			t.Errorf("app %d: job %+v, %v", app.InstanceID, j, err)
		}
	}
}

// One app failing doesn't stop the others.
func TestSyncAppFails(t *testing.T) {
	e := setup(t, nil)
	_, dir2 := e.addApp()
	if err := os.WriteFile(filepath.Join(dir2, "r9.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, nil)
	res, err := e.s.Sync(e.ctx, SyncParams{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Apps) != 2 || res.Apps[0].Error != "" || res.Apps[1].Error == "" || len(e.cardsNamed("Lia")) != 1 {
		t.Fatalf("apps = %+v", res.Apps)
	}
	j, err := jobs.New(e.pool).Get(e.ctx, res.Apps[1].JobID)
	if err != nil || j.Status != jobs.StatusFailed {
		t.Errorf("failed app job = %+v, %v", j, err)
	}
}

// Starting a sync while one is queued returns that one.
func TestEnqueueSyncJoins(t *testing.T) {
	e := setup(t, nil)
	q := jobs.New(e.pool)
	a, err := e.s.EnqueueSync(e.ctx, q, SyncParams{})
	if err != nil {
		t.Fatal(err)
	}
	if b, err := e.s.EnqueueSync(e.ctx, q, SyncParams{InstanceID: e.inst}); err != nil || b.ID != a.ID {
		t.Errorf("second start = %d, %v; want %d", b.ID, err, a.ID)
	}
	if _, err := e.s.EnqueueSync(e.ctx, q, SyncParams{Apply: "all"}); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("bad apply: %v", err)
	}
}
