package pluginhost

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Instance is a configured plugin: what to run and what to initialize it
// with.
type Instance struct {
	ID string
	// Version changes whenever the instance's settings do.
	Version  string
	Manifest *protocol.Manifest
	Params   protocol.InitializeParams
}

// Manager runs at most one process per instance, starting it on first use
// and again after a crash. The API and the worker each have their own
// Manager, so an instance can have one process in each.
type Manager struct {
	core protocol.Handler
	idle time.Duration
	log  *slog.Logger

	mu      sync.Mutex
	running map[string]*entry
	closed  bool
}

type entry struct {
	proc     *Process
	inflight int
	lastUsed time.Time
}

// NewManager returns a Manager whose plugins send their calls to core.
// StopIdle stops plugins unused for longer than idle.
func NewManager(core protocol.Handler, idle time.Duration, log *slog.Logger) *Manager {
	return &Manager{core: core, idle: idle, log: log, running: map[string]*entry{}}
}

// Call sends method to the instance's plugin, starting it if needed.
func (m *Manager) Call(ctx context.Context, inst Instance, method string, params, result any) error {
	e, err := m.acquire(ctx, inst)
	if err != nil {
		return err
	}
	defer m.release(e)
	return e.proc.Call(ctx, method, params, result)
}

func (m *Manager) acquire(ctx context.Context, inst Instance) (*entry, error) {
	// ponytail: one lock across plugin startup, so a slow start (up to the
	// initialize timeout) also delays other plugins' calls, releases, and
	// StopIdle, and waiters ignore their ctx. Per-instance locks if that
	// ever matters.
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, protocol.ErrClosed
	}
	e, ok := m.running[inst.ID]
	if !ok || !e.proc.Alive() {
		params := inst.Params
		params.InstanceID = inst.ID
		p, err := Start(ctx, inst.Manifest, params, m.core, m.log)
		if err != nil {
			return nil, &StartError{Err: err}
		}
		e = &entry{proc: p}
		m.running[inst.ID] = e
	}
	e.inflight++
	e.lastUsed = time.Now()
	return e, nil
}

func (m *Manager) release(e *entry) {
	m.mu.Lock()
	e.inflight--
	e.lastUsed = time.Now()
	m.mu.Unlock()
}

// StopIdle stops plugins that have exited or have had no call in flight for
// longer than the idle timeout. Run it periodically.
func (m *Manager) StopIdle() {
	m.stopWhere(func(e *entry) bool {
		return !e.proc.Alive() || (e.inflight == 0 && time.Since(e.lastUsed) > m.idle)
	})
}

// Close stops every plugin. Later calls fail with protocol.ErrClosed.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stopWhere(func(*entry) bool { return true })
}

func (m *Manager) stopWhere(match func(*entry) bool) {
	m.mu.Lock()
	var stopping []*Process
	for id, e := range m.running {
		if match(e) {
			stopping = append(stopping, e.proc)
			delete(m.running, id)
		}
	}
	m.mu.Unlock()
	for _, p := range stopping {
		if err := p.Stop(); err != nil {
			m.log.Warn("stop plugin", "plugin", p.Manifest.Name, "err", err)
		}
	}
}

// StartError means the plugin failed to start or failed its handshake.
type StartError struct{ Err error }

func (e *StartError) Error() string { return e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }
