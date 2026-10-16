package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func core(_ context.Context, method string, _ json.RawMessage) (any, error) {
	if method == "core.ping" {
		return "pong", nil
	}
	return nil, protocol.MethodNotFound(method)
}

func start(t *testing.T, args ...string) (*Process, error) {
	t.Helper()
	m, err := protocol.LoadManifest(testutil.FakePlugin(t, args...))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Start(context.Background(), m, protocol.InitializeParams{InstanceID: "test"}, core, quiet)
	if p != nil {
		t.Cleanup(func() { _ = p.Stop() })
	}
	return p, err
}

func TestStartAndCall(t *testing.T) {
	p, err := start(t)
	if err != nil {
		t.Fatal(err)
	}
	if p.Info.Name != "fake" {
		t.Fatalf("Info.Name = %q, want fake", p.Info.Name)
	}
	var out map[string]string
	if err := p.Call(context.Background(), "echo", map[string]string{"hi": "there"}, &out); err != nil {
		t.Fatal(err)
	}
	if out["hi"] != "there" {
		t.Fatalf("echo returned %v", out)
	}
}

func TestPluginCallsBackIntoCore(t *testing.T) {
	p, err := start(t)
	if err != nil {
		t.Fatal(err)
	}
	var out string
	if err := p.Call(context.Background(), "callback", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out != "pong" {
		t.Fatalf("callback returned %q, want pong", out)
	}
}

func TestPluginGetsMinimalEnvironment(t *testing.T) {
	t.Setenv("INNKEEPER_TEST_SECRET", "leak")
	p, err := start(t)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"INNKEEPER_TEST_SECRET": "", "PATH": os.Getenv("PATH")} {
		var got string
		if err := p.Call(context.Background(), "env", name, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("plugin sees %s=%q, want %q", name, got, want)
		}
	}
}

func TestStderrLoggedAfterLongLine(t *testing.T) {
	var buf syncBuffer
	m, err := protocol.LoadManifest(testutil.FakePlugin(t))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Start(context.Background(), m, protocol.InitializeParams{}, core, slog.New(slog.NewTextHandler(&buf, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	if err := p.Call(context.Background(), "shout", nil, nil); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(2 * time.Second); !strings.Contains(buf.String(), "msg=after"); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("stderr line after a 100KB line was not logged")
		}
	}
}

// syncBuffer is a bytes.Buffer safe for the logger and the test to share.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestRetryableErrorReachesCaller(t *testing.T) {
	p, err := start(t)
	if err != nil {
		t.Fatal(err)
	}
	err = p.Call(context.Background(), "fail", nil, nil)
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || !rpcErr.Retryable() {
		t.Fatalf("got %v, want retryable error", err)
	}
}

func TestStartRejectsMismatchedPlugin(t *testing.T) {
	tests := []struct{ mode, wantErr string }{
		{"bad-protocol", "speaks protocol 99"},
		{"wrong-caps", "reported capabilities [push]"},
		{"wrong-version", `reported version "9.9.9"`},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			_, err := start(t, "-mode="+tt.mode)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestCrashEndsCalls(t *testing.T) {
	p, err := start(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Call(context.Background(), "crash", nil, nil); !errors.Is(err, protocol.ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
	waitDead(t, p)
}

func TestStopKillsPluginThatIgnoresShutdown(t *testing.T) {
	stopTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stopTimeout = 5 * time.Second })
	p, err := start(t, "-mode=no-shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Stop(); err == nil || !strings.Contains(err.Error(), "was killed") {
		t.Fatalf("got %v, want killed error", err)
	}
	if p.Alive() {
		t.Fatal("plugin still alive after Stop")
	}
}

func TestStopKillsPluginThatStoppedReading(t *testing.T) {
	stopTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stopTimeout = 5 * time.Second })
	p, err := start(t)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.conn.Notify("freeze", nil); err != nil {
		t.Fatal(err)
	}
	// Fill the stdin pipe so further writes block.
	go p.Call(context.Background(), "echo", strings.Repeat("x", 1<<20), nil)
	time.Sleep(100 * time.Millisecond)

	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-stopped:
		if err == nil || !strings.Contains(err.Error(), "was killed") {
			t.Fatalf("got %v, want killed error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop hung on a plugin that stopped reading")
	}
}

// startWrapped runs the fake plugin through a shell script that first leaves
// a background child running, as a wrapper script without exec would.
func startWrapped(t *testing.T) *Process {
	t.Helper()
	dir := testutil.FakePlugin(t)
	script := "#!/bin/sh\nsleep 30 &\nexec ./fakeplugin\n"
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	m, err := protocol.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	m.Command = []string{"./run.sh"}
	p, err := Start(context.Background(), m, protocol.InitializeParams{InstanceID: "test"}, core, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

func TestStopWithChildProcess(t *testing.T) {
	p := startWrapped(t)
	stopped := make(chan error, 1)
	go func() { stopped <- p.Stop() }()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Stop hung on a plugin with a child process")
	}
}

func TestCrashWithChildProcessIsNoticed(t *testing.T) {
	p := startWrapped(t)
	if err := p.Call(context.Background(), "crash", nil, nil); !errors.Is(err, protocol.ErrClosed) {
		t.Fatalf("got %v, want ErrClosed", err)
	}
	waitDead(t, p)
}

func waitDead(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin process did not exit")
	}
}

func TestCheckInfoRefusesServicesOutsideCapabilities(t *testing.T) {
	info := func(caps []string, services ...string) protocol.InitializeResult {
		return protocol.InitializeResult{Protocol: protocol.Version, Name: "p", Version: "1", Capabilities: caps, Services: services}
	}
	manifest := func(caps ...string) *protocol.Manifest {
		return &protocol.Manifest{Name: "p", Version: "1", Capabilities: caps}
	}
	if err := checkInfo(manifest("ai"), info([]string{"ai"}, protocol.MethodAIChat)); err != nil {
		t.Fatalf("ai plugin offering ai.chat: %v", err)
	}
	if err := checkInfo(manifest("ai"), info([]string{"ai"}, "ai.draw")); err == nil {
		t.Error("an unknown service passed")
	}
	if err := checkInfo(manifest("action"), info([]string{"action"}, protocol.MethodAIChat)); err == nil {
		t.Error("ai.chat from a plugin without ai passed")
	}
}
