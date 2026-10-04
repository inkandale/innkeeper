package pluginhost

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
)

func newManager(t *testing.T, idle time.Duration) (*Manager, Instance) {
	t.Helper()
	m, err := protocol.LoadManifest(testutil.FakePlugin(t))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(core, idle, quiet)
	t.Cleanup(mgr.Close)
	return mgr, Instance{ID: "fake-1", Manifest: m}
}

func (m *Manager) proc(id string) *Process {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.running[id]; ok {
		return e.proc
	}
	return nil
}

func waitInflight(t *testing.T, m *Manager, id string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		m.mu.Lock()
		e, ok := m.running[id]
		busy := ok && e.inflight > 0
		m.mu.Unlock()
		if busy {
			return
		}
	}
	t.Fatal("call never became in flight")
}

func TestManagerReusesProcess(t *testing.T) {
	mgr, inst := newManager(t, time.Minute)
	ctx := context.Background()
	if err := mgr.Call(ctx, inst, "echo", 1, nil); err != nil {
		t.Fatal(err)
	}
	first := mgr.proc(inst.ID)
	if err := mgr.Call(ctx, inst, "echo", 2, nil); err != nil {
		t.Fatal(err)
	}
	if mgr.proc(inst.ID) != first {
		t.Fatal("second call started a new process")
	}
}

func TestManagerRestartsAfterCrash(t *testing.T) {
	mgr, inst := newManager(t, time.Minute)
	ctx := context.Background()
	_ = mgr.Call(ctx, inst, "crash", nil, nil)
	waitDead(t, mgr.proc(inst.ID))
	var out int
	if err := mgr.Call(ctx, inst, "echo", 7, &out); err != nil {
		t.Fatal(err)
	}
	if out != 7 {
		t.Fatalf("echo after restart returned %d, want 7", out)
	}
}

func TestCallAfterCloseFails(t *testing.T) {
	mgr, inst := newManager(t, time.Minute)
	mgr.Close()
	if err := mgr.Call(context.Background(), inst, "echo", 1, nil); !errors.Is(err, protocol.ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
	if mgr.proc(inst.ID) != nil {
		t.Fatal("Call after Close started a plugin")
	}
}

func TestStopIdleStopsUnusedPlugin(t *testing.T) {
	mgr, inst := newManager(t, 0)
	if err := mgr.Call(context.Background(), inst, "echo", 1, nil); err != nil {
		t.Fatal(err)
	}
	p := mgr.proc(inst.ID)
	mgr.StopIdle()
	if p.Alive() || mgr.proc(inst.ID) != nil {
		t.Fatal("idle plugin still running")
	}
}

func TestStopIdleSparesPluginWithCallInFlight(t *testing.T) {
	mgr, inst := newManager(t, 0)
	errc := make(chan error, 1)
	go func() { errc <- mgr.Call(context.Background(), inst, "sleep", 300, nil) }()
	waitInflight(t, mgr, inst.ID)
	mgr.StopIdle()
	if err := <-errc; err != nil {
		t.Fatalf("in-flight call failed: %v", err)
	}
}

func TestCallReportsStartError(t *testing.T) {
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, "-mode=wrong-version"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(core, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	err = mgr.Call(context.Background(), Instance{ID: "fake-1", Manifest: m}, "echo", 1, nil)
	var se *StartError
	if !errors.As(err, &se) {
		t.Fatalf("got %v, want a StartError", err)
	}
	// An error from a running plugin is not a StartError.
	good, inst := newManager(t, time.Minute)
	err = good.Call(context.Background(), inst, "fail", nil, nil)
	if errors.As(err, &se) {
		t.Fatalf("call error %v reported as a StartError", err)
	}
}
