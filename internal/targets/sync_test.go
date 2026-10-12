package targets

import (
	"testing"
	"time"
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
