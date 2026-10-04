package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper/internal/testutil"
	"github.com/davidgibbons/innkeeper/protocol"
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

func waitDead(t *testing.T, p *Process) {
	t.Helper()
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
		t.Fatal("plugin process did not exit")
	}
}
