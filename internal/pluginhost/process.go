// Package pluginhost starts plugin processes and talks to them over the
// plugin protocol.
package pluginhost

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

const initTimeout = 10 * time.Second

// stopTimeout is how long a plugin gets to exit after shutdown before it is
// killed. A variable so tests can shorten it.
var stopTimeout = 5 * time.Second

// Process is one running plugin.
type Process struct {
	Manifest *protocol.Manifest
	Info     protocol.InitializeResult

	cmd   *exec.Cmd
	stdin io.WriteCloser
	conn  *protocol.Conn
	done  chan struct{}
	log   *slog.Logger
}

// Start launches the plugin, sends initialize, and checks the reply against
// the manifest. core answers calls the plugin makes back to the core.
func Start(ctx context.Context, m *protocol.Manifest, params protocol.InitializeParams, core protocol.Handler, log *slog.Logger) (*Process, error) {
	cmd := exec.Command(m.CommandPath(), m.Command[1:]...)
	cmd.Dir = m.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start plugin %s: %w", m.Name, err)
	}

	p := &Process{
		Manifest: m,
		cmd:      cmd,
		stdin:    stdin,
		conn:     protocol.NewConn(stdout, stdin, core),
		done:     make(chan struct{}),
		log:      log.With("plugin", m.Name),
	}
	p.watch(stderr)

	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()
	params.Protocol = protocol.Version
	if err := p.conn.Call(initCtx, protocol.MethodInitialize, params, &p.Info); err != nil {
		p.kill()
		return nil, fmt.Errorf("initialize plugin %s: %w", m.Name, err)
	}
	if err := checkInfo(m, p.Info); err != nil {
		p.kill()
		return nil, err
	}
	return p, nil
}

// watch logs the plugin's stderr, serves its stdout, and closes p.done once
// the process has exited.
func (p *Process) watch(stderr io.Reader) {
	var logged sync.WaitGroup
	logged.Go(func() {
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			p.log.Info(s.Text(), "stream", "stderr")
		}
		// Keep draining after an over-long line so the plugin never blocks.
		_, _ = io.Copy(io.Discard, stderr)
	})

	runCtx, cancel := context.WithCancel(context.Background())
	go func() {
		defer close(p.done)
		defer cancel()
		if err := p.conn.Run(runCtx); err != nil {
			p.log.Error("plugin sent invalid protocol data; killing it", "err", err)
			_ = p.cmd.Process.Kill()
		}
		// Wait must follow the last read from stdout and stderr.
		logged.Wait()
		if err := p.cmd.Wait(); err != nil {
			p.log.Warn("plugin exited", "err", err)
		}
	}()
}

func checkInfo(m *protocol.Manifest, info protocol.InitializeResult) error {
	if info.Protocol != protocol.Version {
		return fmt.Errorf("plugin %s speaks protocol %d; this core speaks %d", m.Name, info.Protocol, protocol.Version)
	}
	if info.Name != m.Name {
		return fmt.Errorf("plugin %s reported name %q", m.Name, info.Name)
	}
	got, want := slices.Sorted(slices.Values(info.Capabilities)), slices.Sorted(slices.Values(m.Capabilities))
	if !slices.Equal(got, want) {
		return fmt.Errorf("plugin %s reported capabilities %v; manifest declares %v", m.Name, got, want)
	}
	return nil
}

// Call sends a request to the plugin and decodes its result into result.
func (p *Process) Call(ctx context.Context, method string, params, result any) error {
	return p.conn.Call(ctx, method, params, result)
}

// Alive reports whether the plugin process is still running.
func (p *Process) Alive() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

// Stop asks the plugin to shut down, closes its stdin, and kills it if it
// hasn't exited within stopTimeout.
func (p *Process) Stop() error {
	timeout := stopTimeout
	// Shutdown runs separately because a write to a plugin that has stopped
	// reading blocks until the plugin is killed.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		_ = p.conn.Call(ctx, protocol.MethodShutdown, nil, nil)
		_ = p.stdin.Close()
	}()
	select {
	case <-p.done:
		return nil
	case <-time.After(timeout):
		p.kill()
		return fmt.Errorf("plugin %s did not exit after shutdown and was killed", p.Manifest.Name)
	}
}

func (p *Process) kill() {
	_ = p.cmd.Process.Kill()
	<-p.done
}
