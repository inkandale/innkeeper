package pluginhost

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

// Instance is a configured plugin: what to run and what to initialize it
// with.
type Instance struct {
	ID string
	// Version is a decimal number that grows whenever the instance's
	// settings or its plugin's install change.
	Version  string
	Manifest *protocol.Manifest
	Params   protocol.InitializeParams
}

func (i Instance) key() string { return i.ID + "@" + i.Version }

// Manager runs at most one process per instance version, starting it on first use
// and again after a crash. The API and the worker each have their own
// Manager, so an instance can have one process in each.
type Manager struct {
	core protocol.Handler
	idle time.Duration
	log  *slog.Logger

	mu      sync.Mutex
	running map[string]*entry
	// latest maps an instance ID to its newest version seen.
	latest map[string]int64
	closed bool
}

type entry struct {
	key      string
	id       string
	version  int64
	proc     *Process
	inflight int
	lastUsed time.Time
}

// NewManager returns a Manager whose plugins send their calls to core.
// StopIdle stops plugins unused for longer than idle.
func NewManager(core protocol.Handler, idle time.Duration, log *slog.Logger) *Manager {
	return &Manager{core: core, idle: idle, log: log, running: map[string]*entry{}, latest: map[string]int64{}}
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

// Start starts the instance's plugin if it isn't running.
func (m *Manager) Start(ctx context.Context, inst Instance) error {
	e, err := m.acquire(ctx, inst)
	if err != nil {
		return err
	}
	m.release(e)
	return nil
}

// Info is the instance's initialize reply, starting its plugin if needed.
func (m *Manager) Info(ctx context.Context, inst Instance) (protocol.InitializeResult, error) {
	e, err := m.acquire(ctx, inst)
	if err != nil {
		return protocol.InitializeResult{}, err
	}
	defer m.release(e)
	return e.proc.Info, nil
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
	key := inst.key()
	// Unparsable versions count as 0.
	version, _ := strconv.ParseInt(inst.Version, 10, 64)
	e, ok := m.running[key]
	if !ok || !e.proc.Alive() {
		params := inst.Params
		params.InstanceID = inst.ID
		// Tagged with the instance, so Runs can refuse another instance's tokens.
		core := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			return m.core(fromInstance(ctx, inst.ID), method, params)
		}
		if m.core == nil {
			core = nil
		}
		p, err := Start(ctx, inst.Manifest, params, core, m.log)
		if err != nil {
			return nil, &StartError{Err: err}
		}
		e = &entry{key: key, id: inst.ID, version: version, proc: p}
		m.running[key] = e
	}
	// A caller holding a stale Instance must not supersede a newer version.
	if cur, ok := m.latest[inst.ID]; !ok || version > cur {
		m.latest[inst.ID] = version
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

// StopIdle stops plugins that have exited, have had no call in flight for
// longer than the idle timeout, or run an old version with no call in
// flight. Run it periodically.
func (m *Manager) StopIdle() {
	m.stopWhere(func(e *entry) bool {
		if !e.proc.Alive() {
			return true
		}
		return e.inflight == 0 && (time.Since(e.lastUsed) > m.idle || e.version < m.latest[e.id])
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
