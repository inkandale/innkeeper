# Plugin Protocol and Host Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the plugin protocol, the host that runs plugins as child processes, and the `innkeeper plugin check` contract kit that every plugin is tested against.

**Architecture:** A public `protocol` package holds the JSON-RPC 2.0 connection (both sides can call each other), the handshake types, and manifest loading. Go plugins import it too. `internal/pluginhost` starts plugin processes, checks the `initialize` reply against the manifest, and keeps one process per instance with crash restart and idle stop. `internal/contract` runs protocol checks against any plugin folder and backs the `innkeeper plugin check` command.

**Tech Stack:** Go 1.27, standard library, `github.com/BurntSushi/toml` v1.6.0.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, sections "Plugin protocol" and "Build order" step 1.

**Kaneo:** project Innkeeper (INN).

---

## Plan series

This is plan 1 of the core. Each plan produces working, tested software.

1. **Plugin protocol and host** (this plan).
2. Library storage: schema, migrations, card and lorebook revisions, blob store, hashes.
3. Jobs and worker: queue, checkpoints, bulk actions, import flow through `import` plugins.
4. HTTP API, auth, and CLI: OpenAPI with Huma, sessions, API tokens, structured search.
5. Sync: bindings, push, drift detection, adopt.
6. v1 plugins in `innkeeper-plugins`: codec, local auth, folder importer, Lumiverse target.
7. MCP server and web UI.

Each later plan adds that capability's methods to `docs/protocol.md` and its checks to the contract kit.

Constraints this plan leaves for later plans:

- **Plan 3:** a plugin crash surfaces to callers as `protocol.ErrClosed`. Treat it as retryable, so the job is re-queued and resumes from its checkpoint.
- **Plans 3 and 5:** notification handlers (such as the core's `emit.card` handler) must not call back into the plugin on the same connection; they run on the reading goroutine.

## File structure

| File | Responsibility |
|---|---|
| `go.mod` | Module `github.com/davidgibbons/innkeeper` |
| `.github/workflows/ci.yml` | `go vet` and `go test -race` on push and PR |
| `protocol/jsonrpc.go` | Bidirectional JSON-RPC 2.0 connection over newline-delimited JSON |
| `protocol/messages.go` | Protocol version, `initialize`/`shutdown`, handshake types |
| `protocol/manifest.go` | `plugin.toml` loading, validation, command resolution |
| `internal/fakeplugin/main.go` | Test plugin with `-mode` flags for misbehavior |
| `internal/testutil/fakeplugin.go` | Builds the fake plugin into a temp plugin folder |
| `internal/pluginhost/process.go` | Start one plugin, handshake, call, stop |
| `internal/pluginhost/manager.go` | One process per instance, restart after crash, idle stop |
| `internal/contract/contract.go` | Protocol checks run against a plugin folder |
| `cmd/innkeeper/main.go` | CLI entry point: `innkeeper plugin check <dir>` |
| `docs/protocol.md` | Protocol reference for plugin authors |

---

### Task 1: Go module and CI

**Files:**
- Create: `go.mod`
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create the module**

Run from the repo root (`~/git/innkeeper`):

```bash
go mod init github.com/davidgibbons/innkeeper
go mod edit -go=1.27
```

Expected: `go.mod` contains `module github.com/davidgibbons/innkeeper` and `go 1.27`.

- [ ] **Step 2: Add CI**

Create `.github/workflows/ci.yml`:

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...
```

- [ ] **Step 3: Commit**

```bash
git add go.mod .github/workflows/ci.yml
git commit -m "Add Go module and CI"
```

---

### Task 2: JSON-RPC connection

**Files:**
- Create: `protocol/jsonrpc_test.go`
- Create: `protocol/jsonrpc.go`

- [ ] **Step 1: Write the failing tests**

Create `protocol/jsonrpc_test.go`:

```go
package protocol

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"
)

// pair connects two Conns with pipes. Closing the returned writer ends a's
// reader, as if the other process exited.
func pair(t *testing.T, ha, hb Handler) (a, b *Conn, bw *io.PipeWriter) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	a = NewConn(ar, aw, ha)
	b = NewConn(br, bw, hb)
	ctx, cancel := context.WithCancel(context.Background())
	go a.Run(ctx)
	go b.Run(ctx)
	t.Cleanup(func() {
		cancel()
		aw.Close()
		bw.Close()
	})
	return a, b, bw
}

func echo(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method == "echo" {
		return params, nil
	}
	return nil, MethodNotFound(method)
}

func TestCallReturnsResult(t *testing.T) {
	a, _, _ := pair(t, nil, echo)
	var out map[string]int
	if err := a.Call(context.Background(), "echo", map[string]int{"x": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if out["x"] != 1 {
		t.Fatalf("got %v, want x=1", out)
	}
}

func TestCallReturnsRetryableError(t *testing.T) {
	failing := func(context.Context, string, json.RawMessage) (any, error) {
		return nil, NewError(-32000, "busy", true)
	}
	a, _, _ := pair(t, nil, failing)
	err := a.Call(context.Background(), "anything", nil, nil)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32000 || !rpcErr.Retryable() {
		t.Fatalf("got %v, want retryable -32000", err)
	}
}

func TestPlainErrorBecomesInternalError(t *testing.T) {
	failing := func(context.Context, string, json.RawMessage) (any, error) {
		return nil, errors.New("boom")
	}
	a, _, _ := pair(t, nil, failing)
	err := a.Call(context.Background(), "anything", nil, nil)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternalError || rpcErr.Retryable() {
		t.Fatalf("got %v, want non-retryable internal error", err)
	}
}

func TestNotificationsArriveInOrder(t *testing.T) {
	got := make(chan int, 100)
	collect := func(_ context.Context, _ string, params json.RawMessage) (any, error) {
		var n int
		_ = json.Unmarshal(params, &n)
		got <- n
		return nil, nil
	}
	a, _, _ := pair(t, nil, collect)
	for i := range 100 {
		if err := a.Notify("n", i); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 100 {
		if n := <-got; n != i {
			t.Fatalf("notification %d arrived as %d", i, n)
		}
	}
}

func TestCallbackDuringCall(t *testing.T) {
	var b *Conn
	outer := func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		var s string
		err := b.Call(ctx, "inner", nil, &s)
		return s, err
	}
	inner := func(context.Context, string, json.RawMessage) (any, error) { return "pong", nil }
	a, bb, _ := pair(t, inner, outer)
	b = bb
	var s string
	if err := a.Call(context.Background(), "outer", nil, &s); err != nil {
		t.Fatal(err)
	}
	if s != "pong" {
		t.Fatalf("got %q, want pong", s)
	}
}

func TestPendingCallFailsWhenPeerGoesAway(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	block := func(context.Context, string, json.RawMessage) (any, error) {
		close(entered)
		<-release
		return nil, nil
	}
	a, _, bw := pair(t, nil, block)
	errc := make(chan error, 1)
	go func() { errc <- a.Call(context.Background(), "slow", nil, nil) }()
	<-entered
	bw.Close()
	select {
	case err := <-errc:
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("got %v, want ErrClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("call did not fail after peer went away")
	}
}

func TestCallAfterCloseFails(t *testing.T) {
	a, _, bw := pair(t, nil, echo)
	bw.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		err := a.Call(context.Background(), "echo", 1, nil)
		if errors.Is(err, ErrClosed) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %v, want ErrClosed", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelledCallForgetsPendingEntry(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	block := func(context.Context, string, json.RawMessage) (any, error) {
		<-release
		return nil, nil
	}
	a, _, _ := pair(t, nil, block)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := a.Call(ctx, "slow", nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want DeadlineExceeded", err)
	}
	a.mu.Lock()
	n := len(a.pending)
	a.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d pending entries left after cancel", n)
	}
}

func TestPanickingHandlerReturnsInternalError(t *testing.T) {
	panicky := func(context.Context, string, json.RawMessage) (any, error) { panic("oops") }
	a, _, _ := pair(t, nil, panicky)
	err := a.Call(context.Background(), "anything", nil, nil)
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeInternalError {
		t.Fatalf("got %v, want internal error", err)
	}
}

func TestPanickingNotificationHandlerKeepsConnection(t *testing.T) {
	handler := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method == "boom" {
			panic("oops")
		}
		return echo(context.Background(), method, params)
	}
	a, _, _ := pair(t, nil, handler)
	if err := a.Notify("boom", nil); err != nil {
		t.Fatal(err)
	}
	var out int
	if err := a.Call(context.Background(), "echo", 5, &out); err != nil || out != 5 {
		t.Fatalf("call after panicking notification: out=%d err=%v", out, err)
	}
}

func TestHandlerContextCancelledWhenPeerGoesAway(t *testing.T) {
	cancelled := make(chan struct{})
	wait := func(ctx context.Context, _ string, _ json.RawMessage) (any, error) {
		<-ctx.Done()
		close(cancelled)
		return nil, ctx.Err()
	}
	pr, pw := io.Pipe()
	c := NewConn(pr, io.Discard, wait)
	go c.Run(context.Background())
	io.WriteString(pw, `{"jsonrpc":"2.0","id":1,"method":"wait"}`+"\n")
	pw.Close()
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("handler ctx not cancelled after EOF")
	}
}

// TestWireFormat feeds raw JSON lines, as a non-Go plugin would send them.
func TestWireFormat(t *testing.T) {
	pr, pw := io.Pipe()
	out, outW := io.Pipe()
	c := NewConn(pr, outW, echo)
	go c.Run(context.Background())
	t.Cleanup(func() { pw.Close(); outW.Close() })
	replies := bufio.NewScanner(out)

	errc := make(chan error, 1)
	go func() { errc <- c.Call(context.Background(), "plugin.method", nil, nil) }()
	if !replies.Scan() {
		t.Fatal("no request written")
	}
	var req struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(replies.Bytes(), &req); err != nil {
		t.Fatal(err)
	}

	// A batch is skipped without closing the connection.
	io.WriteString(pw, `[{"jsonrpc":"2.0","method":"x"}]`+"\n")
	// error.data may be any JSON value, such as a traceback string.
	io.WriteString(pw, `{"jsonrpc":"2.0","id":`+string(req.ID)+`,"error":{"code":-32000,"message":"bad","data":"Traceback..."}}`+"\n")
	var rpcErr *Error
	if err := <-errc; !errors.As(err, &rpcErr) || rpcErr.Retryable() {
		t.Fatalf("got %v, want non-retryable error", err)
	}

	// A request with a string id gets the same id back.
	io.WriteString(pw, `{"jsonrpc":"2.0","id":"abc","method":"echo","params":{"k":1}}`+"\n")
	if !replies.Scan() {
		t.Fatal("no response written")
	}
	if got, want := replies.Text(), `{"jsonrpc":"2.0","id":"abc","result":{"k":1}}`; got != want {
		t.Fatalf("response = %s, want %s", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./protocol/`
Expected: FAIL with build errors such as `undefined: Handler` and `undefined: NewConn`.

- [ ] **Step 3: Implement the connection**

Create `protocol/jsonrpc.go`:

```go
// Package protocol implements the Innkeeper plugin protocol: JSON-RPC 2.0
// messages, one JSON value per line, over a plugin's stdin and stdout.
package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)

// ErrClosed is returned by calls that were pending, or started, after the
// connection's reader stopped.
var ErrClosed = errors.New("protocol: connection closed")

// Error is a JSON-RPC error object. Data may be any JSON value; Innkeeper
// reads a "retryable" field from it when it is an object.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc error %d: %s", e.Code, e.Message) }

// Retryable reports whether the caller may retry the same call later.
func (e *Error) Retryable() bool {
	var d struct {
		Retryable bool `json:"retryable"`
	}
	_ = json.Unmarshal(e.Data, &d)
	return d.Retryable
}

// NewError builds an Error with the retryable flag set.
func NewError(code int, message string, retryable bool) *Error {
	data, _ := json.Marshal(map[string]bool{"retryable": retryable})
	return &Error{Code: code, Message: message, Data: data}
}

// MethodNotFound is the error for a method the receiver doesn't implement.
func MethodNotFound(method string) *Error {
	return &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}

// Handler answers requests and notifications from the other side. For a
// notification the result is discarded. Notification handlers run on the
// reading goroutine, so they must not call Call on the same Conn.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Conn is one end of a bidirectional JSON-RPC connection: both sides can
// send requests and notifications.
type Conn struct {
	dec     *json.Decoder
	enc     *json.Encoder
	handler Handler
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[int64]chan *message
	closed  bool
}

// NewConn returns a connection that reads from r and writes to w. Call Run
// to start reading.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	return &Conn{
		dec:     json.NewDecoder(r),
		enc:     json.NewEncoder(w),
		handler: h,
		pending: map[int64]chan *message{},
	}
}

// Run reads messages until r reaches EOF. It returns nil on EOF. Pending
// and later calls then fail with ErrClosed, and the ctx passed to handlers
// is cancelled.
func (c *Conn) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer c.closePending()
	for {
		var m message
		if err := c.dec.Decode(&m); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			// A well-formed JSON value of the wrong shape (a batch, a
			// non-string method) has been consumed whole; skip it.
			// ponytail: a skipped request or response with an id leaves
			// its caller waiting until ctx expires; reply -32600 if
			// misbehaving peers show up.
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				continue
			}
			return fmt.Errorf("protocol: read: %w", err)
		}
		switch {
		case m.Method != "" && len(m.ID) == 0:
			// Handled inline so notifications such as emit.card and
			// checkpoint are processed in the order they were sent.
			_, _ = c.handle(ctx, m)
		case m.Method != "":
			go c.serve(ctx, m)
		default:
			c.deliver(m)
		}
	}
}

// Call sends a request and waits for its response. result may be nil.
//
// ponytail: ctx doesn't interrupt a blocked write, so a peer that stops
// reading blocks callers until its process is killed. The plugin host kills
// plugins that don't stop in time.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	req := &message{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("protocol: marshal params: %w", err)
		}
		req.Params = raw
	}
	id := c.nextID.Add(1)
	req.ID, _ = json.Marshal(id)
	ch := make(chan *message, 1)

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.write(req); err != nil {
		c.forget(id)
		return err
	}
	select {
	case <-ctx.Done():
		c.forget(id)
		return ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(resp.Result, result)
	}
}

// Notify sends a notification, which gets no response.
func (c *Conn) Notify(method string, params any) error {
	m := &message{JSONRPC: "2.0", Method: method}
	if params != nil {
		raw, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("protocol: marshal params: %w", err)
		}
		m.Params = raw
	}
	return c.write(m)
}

func (c *Conn) serve(ctx context.Context, req message) {
	resp := &message{JSONRPC: "2.0", ID: req.ID}
	result, err := c.handle(ctx, req)
	if err == nil {
		resp.Result, err = json.Marshal(result)
	}
	if err != nil {
		var rpcErr *Error
		if !errors.As(err, &rpcErr) {
			rpcErr = &Error{Code: CodeInternalError, Message: err.Error()}
		}
		resp.Result, resp.Error = nil, rpcErr
	}
	_ = c.write(resp)
}

// handle runs the handler for a message, turning a panic into an internal
// error so a bad message can't crash the process.
func (c *Conn) handle(ctx context.Context, req message) (result any, err error) {
	if c.handler == nil {
		return nil, MethodNotFound(req.Method)
	}
	defer func() {
		if r := recover(); r != nil {
			err = &Error{Code: CodeInternalError, Message: fmt.Sprintf("handler panicked: %v", r)}
		}
	}()
	return c.handler(ctx, req.Method, req.Params)
}

func (c *Conn) deliver(m message) {
	var id int64
	if err := json.Unmarshal(m.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ok {
		ch <- &m
	}
}

func (c *Conn) write(m *message) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.enc.Encode(m); err != nil {
		return fmt.Errorf("protocol: write: %w", err)
	}
	return nil
}

func (c *Conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *Conn) closePending() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./protocol/`
Expected: `ok  	github.com/davidgibbons/innkeeper/protocol`

- [ ] **Step 5: Commit**

```bash
git add protocol/jsonrpc.go protocol/jsonrpc_test.go
git commit -m "Add bidirectional JSON-RPC connection for plugins"
```

---

### Task 3: Handshake types and manifest

**Files:**
- Create: `protocol/manifest_test.go`
- Create: `protocol/messages.go`
- Create: `protocol/manifest.go`
- Modify: `go.mod`, `go.sum` (TOML dependency)

- [ ] **Step 1: Write the failing tests**

Create `protocol/manifest_test.go`:

```go
package protocol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

const validManifest = `
name = "lumiverse"
version = "0.1.0"
protocol = 1
command = ["./lumiverse-plugin", "--quiet"]
capabilities = ["push", "pull", "search"]
secrets = ["api_key"]

[config]
type = "object"

[enrichments]
summary = { type = "text", label = "AI summary" }
quality = { type = "score", range = [1, 5], label = "Quality" }
`

func TestLoadManifest(t *testing.T) {
	dir := writeManifest(t, validManifest)
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "lumiverse" || len(m.Capabilities) != 3 || m.Secrets[0] != "api_key" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if got := m.Enrichments["quality"].Range; len(got) != 2 || got[1] != 5 {
		t.Fatalf("quality range = %v, want [1 5]", got)
	}
	if got, want := m.CommandPath(), filepath.Join(m.Dir, "lumiverse-plugin"); got != want {
		t.Fatalf("CommandPath = %q, want %q", got, want)
	}
}

func TestCommandPath(t *testing.T) {
	tests := []struct{ cmd, want string }{
		{"python3", "python3"},
		{"./run", "/plugins/x/run"},
		{"bin/run", "/plugins/x/bin/run"},
		{"/usr/bin/node", "/usr/bin/node"},
	}
	for _, tt := range tests {
		m := &Manifest{Command: []string{tt.cmd}, Dir: "/plugins/x"}
		if got := m.CommandPath(); got != tt.want {
			t.Errorf("CommandPath(%q) = %q, want %q", tt.cmd, got, tt.want)
		}
	}
}

func TestMissingManifest(t *testing.T) {
	if _, err := LoadManifest(t.TempDir()); err == nil || !strings.Contains(err.Error(), "read manifest") {
		t.Fatalf("got %v, want read manifest error", err)
	}
}

func TestManifestErrors(t *testing.T) {
	tests := []struct {
		name, replace, with, wantErr string
	}{
		{"bad name", `name = "lumiverse"`, `name = "Lumi Verse"`, "name"},
		{"wrong protocol", `protocol = 1`, `protocol = 2`, "protocol 2 is not supported"},
		{"unknown capability", `"search"]`, `"teleport"]`, `unknown capability "teleport"`},
		{"duplicate capability", `"search"]`, `"push"]`, "listed twice"},
		{"score without range", `range = [1, 5], `, ``, "score needs range"},
		{"bad enrichment type", `type = "text"`, `type = "essay"`, `type "essay"`},
		{"unknown key", `secrets = ["api_key"]`, `secret = ["api_key"]`, "unknown keys: secret"},
		{"trailing hyphen", `name = "lumiverse"`, `name = "lumiverse-"`, "name"},
		{"name too long", `name = "lumiverse"`, `name = "` + strings.Repeat("a", 42) + `"`, "at most 41"},
		{"missing version", `version = "0.1.0"`, ``, "version is required"},
		{"empty command", `command = ["./lumiverse-plugin", "--quiet"]`, `command = []`, "command is required"},
		{"command escapes folder", `"./lumiverse-plugin"`, `"./../../evil"`, "outside the plugin folder"},
		{"empty secret", `secrets = ["api_key"]`, `secrets = [""]`, `secret name ""`},
		{"duplicate secret", `secrets = ["api_key"]`, `secrets = ["api_key", "api_key"]`, "listed twice"},
		{"bad enrichment key", `summary = {`, `"bad key!" = {`, `enrichment key "bad key!"`},
		{"nan range", `range = [1, 5]`, `range = [nan, nan]`, "score needs range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := strings.Replace(validManifest, tt.replace, tt.with, 1)
			if body == validManifest {
				t.Fatalf("replace target %q not found", tt.replace)
			}
			_, err := LoadManifest(writeManifest(t, body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./protocol/`
Expected: FAIL with `undefined: LoadManifest` and `undefined: Manifest`.

- [ ] **Step 3: Add the handshake types**

Create `protocol/messages.go`:

```go
package protocol

// Version is the protocol version this core speaks.
const Version = 1

// Methods every plugin implements.
const (
	MethodInitialize = "initialize"
	MethodShutdown   = "shutdown"
)

// InitializeParams is sent by the core as the first request to a plugin.
type InitializeParams struct {
	Protocol    int               `json:"protocol"`
	InstanceID  string            `json:"instance_id"`
	Config      map[string]any    `json:"config"`
	Secrets     map[string]string `json:"secrets"`
	BlobStores  map[string]string `json:"blob_stores"`
	BlobTmp     string            `json:"blob_tmp"`
	DatabaseURL string            `json:"database_url"`
}

// InitializeResult is the plugin's reply to initialize.
type InitializeResult struct {
	Protocol     int      `json:"protocol"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
}
```

- [ ] **Step 4: Implement manifest loading**

Run: `go get github.com/BurntSushi/toml@v1.6.0`

Create `protocol/manifest.go`:

```go
package protocol

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// Capabilities lists every capability a plugin may declare.
var Capabilities = []string{"import", "search", "push", "pull", "action", "enrich", "codec", "auth"}

var enrichmentTypes = []string{"text", "score", "list"}

// Names become PostgreSQL schema names (plugin_<name>, with - as _): 41
// characters keeps "plugin_" + name under Postgres's 63-byte limit.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

const maxNameLen = 41

// Secret names and enrichment keys.
var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Manifest is a plugin's plugin.toml.
type Manifest struct {
	Name         string                `toml:"name"`
	Version      string                `toml:"version"`
	Protocol     int                   `toml:"protocol"`
	Command      []string              `toml:"command"`
	Capabilities []string              `toml:"capabilities"`
	Secrets      []string              `toml:"secrets"`
	Config       map[string]any        `toml:"config"`
	Enrichments  map[string]Enrichment `toml:"enrichments"`

	// Dir is the absolute path of the plugin's folder.
	Dir string `toml:"-"`
}

// Enrichment declares one enrichment key a plugin writes and how the UI
// shows it.
type Enrichment struct {
	Type  string    `toml:"type"`
	Label string    `toml:"label"`
	Range []float64 `toml:"range"`
}

// LoadManifest reads and validates dir/plugin.toml. Unknown keys are errors,
// so typos fail loudly.
func LoadManifest(dir string) (*Manifest, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var m Manifest
	md, err := toml.DecodeFile(filepath.Join(abs, "plugin.toml"), &m)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", abs, strings.Join(keys, ", "))
	}
	m.Dir = abs
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest %s: %w", abs, err)
	}
	return &m, nil
}

// Validate reports every problem with the manifest at once.
func (m *Manifest) Validate() error {
	var errs []error
	if !namePattern.MatchString(m.Name) || len(m.Name) > maxNameLen {
		errs = append(errs, fmt.Errorf("name %q must match %s and be at most %d characters", m.Name, namePattern, maxNameLen))
	}
	if m.Version == "" {
		errs = append(errs, errors.New("version is required"))
	}
	if m.Protocol != Version {
		errs = append(errs, fmt.Errorf("protocol %d is not supported; this core speaks %d", m.Protocol, Version))
	}
	if len(m.Command) == 0 || m.Command[0] == "" {
		errs = append(errs, errors.New("command is required"))
	} else if m.Dir != "" && !filepath.IsAbs(m.Command[0]) && strings.Contains(m.Command[0], "/") {
		if rel, err := filepath.Rel(m.Dir, m.CommandPath()); err != nil || strings.HasPrefix(rel, "..") {
			errs = append(errs, fmt.Errorf("command %q is outside the plugin folder", m.Command[0]))
		}
	}
	if len(m.Capabilities) == 0 {
		errs = append(errs, errors.New("at least one capability is required"))
	}
	seen := map[string]bool{}
	for _, c := range m.Capabilities {
		if !slices.Contains(Capabilities, c) {
			errs = append(errs, fmt.Errorf("unknown capability %q", c))
		}
		if seen[c] {
			errs = append(errs, fmt.Errorf("capability %q listed twice", c))
		}
		seen[c] = true
	}
	seenSecrets := map[string]bool{}
	for _, s := range m.Secrets {
		if !keyPattern.MatchString(s) {
			errs = append(errs, fmt.Errorf("secret name %q must match %s", s, keyPattern))
		}
		if seenSecrets[s] {
			errs = append(errs, fmt.Errorf("secret %q listed twice", s))
		}
		seenSecrets[s] = true
	}
	for key, e := range m.Enrichments {
		if !keyPattern.MatchString(key) {
			errs = append(errs, fmt.Errorf("enrichment key %q must match %s", key, keyPattern))
		}
		if !slices.Contains(enrichmentTypes, e.Type) {
			errs = append(errs, fmt.Errorf("enrichment %q: type %q must be one of %v", key, e.Type, enrichmentTypes))
		}
		if e.Type == "score" && (len(e.Range) != 2 || !(e.Range[0] < e.Range[1])) {
			errs = append(errs, fmt.Errorf("enrichment %q: score needs range = [low, high]", key))
		}
	}
	return errors.Join(errs...)
}

// CommandPath resolves the executable: relative paths containing / are
// inside the plugin folder, absolute paths are used as-is, and bare names
// are looked up on PATH.
func (m *Manifest) CommandPath() string {
	cmd := m.Command[0]
	if !filepath.IsAbs(cmd) && strings.Contains(cmd, "/") {
		return filepath.Join(m.Dir, cmd)
	}
	return cmd
}
```

Then run `go mod tidy`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test -race ./protocol/`
Expected: `ok  	github.com/davidgibbons/innkeeper/protocol`

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum protocol/messages.go protocol/manifest.go protocol/manifest_test.go
git commit -m "Add plugin manifest loading and handshake types"
```

---

### Task 4: Fake plugin for tests

**Files:**
- Create: `internal/fakeplugin/main.go`
- Create: `internal/testutil/fakeplugin.go`

The fake plugin is the test double for every host and contract test. It is a real process speaking the real protocol. `-mode` makes it misbehave: `bad-protocol` reports protocol 99, `wrong-caps` reports fewer capabilities than its manifest, `bad-errors` answers unknown methods with success, `no-shutdown` never exits. A `freeze` notification makes it stop reading stdin, like a hung plugin.

- [ ] **Step 1: Write the fake plugin**

Create `internal/fakeplugin/main.go`:

```go
// Command fakeplugin is a plugin for core tests. The -mode flag makes it
// misbehave in specific ways.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/davidgibbons/innkeeper/protocol"
)

func main() {
	mode := flag.String("mode", "", "bad-protocol | wrong-caps | bad-errors | no-shutdown")
	flag.Parse()

	var conn *protocol.Conn
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			res := protocol.InitializeResult{
				Protocol:     protocol.Version,
				Name:         "fake",
				Version:      "0.0.1",
				Capabilities: []string{"push", "pull"},
			}
			switch *mode {
			case "bad-protocol":
				res.Protocol = 99
			case "wrong-caps":
				res.Capabilities = []string{"push"}
			}
			return res, nil
		case protocol.MethodShutdown:
			if *mode == "no-shutdown" {
				time.Sleep(time.Hour)
			}
			return nil, nil
		case "echo":
			return params, nil
		case "callback":
			var out string
			err := conn.Call(ctx, "core.ping", nil, &out)
			return out, err
		case "fail":
			return nil, protocol.NewError(-32000, "temporary failure", true)
		case "sleep":
			var ms int
			if err := json.Unmarshal(params, &ms); err != nil {
				return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
			}
			time.Sleep(time.Duration(ms) * time.Millisecond)
			return "slept", nil
		case "crash":
			os.Exit(3)
		case "freeze":
			// A notification handler blocks the reader, so the plugin
			// stops reading stdin, like a hung plugin.
			time.Sleep(time.Hour)
		}
		if *mode == "bad-errors" {
			return nil, nil
		}
		return nil, protocol.MethodNotFound(method)
	}

	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *mode == "no-shutdown" {
		time.Sleep(time.Hour)
	}
}
```

- [ ] **Step 2: Write the test helper**

Create `internal/testutil/fakeplugin.go`. The manifest uses a relative `./fakeplugin` command, so every test also exercises `CommandPath`.

```go
// Package testutil holds helpers shared by core tests.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// FakePlugin builds internal/fakeplugin into a temp plugin folder and writes
// a manifest that runs it with args. It returns the folder.
func FakePlugin(t *testing.T, args ...string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fakeplugin")
	build := exec.Command("go", "build", "-o", bin, "github.com/davidgibbons/innkeeper/internal/fakeplugin")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fakeplugin: %v\n%s", err, out)
	}
	cmd := append([]string{"./fakeplugin"}, args...)
	for i, c := range cmd {
		cmd[i] = strconv.Quote(c)
	}
	manifest := fmt.Sprintf(`name = "fake"
version = "0.0.1"
protocol = 1
command = [%s]
capabilities = ["push", "pull"]
`, strings.Join(cmd, ", "))
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
```

- [ ] **Step 3: Verify it builds**

Run: `go vet ./...`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/fakeplugin internal/testutil
git commit -m "Add fake plugin for host and contract tests"
```

---

### Task 5: Plugin process host

**Files:**
- Create: `internal/pluginhost/process_test.go`
- Create: `internal/pluginhost/process.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/pluginhost/process_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/pluginhost/`
Expected: FAIL with `undefined: Process` and `undefined: Start`.

- [ ] **Step 3: Implement the process host**

Create `internal/pluginhost/process.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/pluginhost/`
Expected: `ok  	github.com/davidgibbons/innkeeper/internal/pluginhost`

- [ ] **Step 5: Commit**

```bash
git add internal/pluginhost/process.go internal/pluginhost/process_test.go
git commit -m "Add plugin process host with handshake checks"
```

---

### Task 6: Plugin manager

**Files:**
- Create: `internal/pluginhost/manager_test.go`
- Create: `internal/pluginhost/manager.go`

The manager keeps one process per plugin instance. It starts a plugin on first use and again after a crash, and stops idle plugins, but never one with a call in flight.

- [ ] **Step 1: Write the failing tests**

Create `internal/pluginhost/manager_test.go`:

```go
package pluginhost

import (
	"context"
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
	time.Sleep(100 * time.Millisecond)
	mgr.StopIdle()
	if err := <-errc; err != nil {
		t.Fatalf("in-flight call failed: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/pluginhost/`
Expected: FAIL with `undefined: Manager` and `undefined: Instance`.

- [ ] **Step 3: Implement the manager**

Create `internal/pluginhost/manager.go`:

```go
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
	ID       string
	Manifest *protocol.Manifest
	Params   protocol.InitializeParams
}

// Manager runs at most one process per instance, starting it on first use
// and again after a crash.
type Manager struct {
	core protocol.Handler
	idle time.Duration
	log  *slog.Logger

	mu      sync.Mutex
	running map[string]*entry
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
	// ponytail: one lock across plugin startup; per-instance locks if a slow
	// start ever blocks calls to other plugins.
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.running[inst.ID]
	if !ok || !e.proc.Alive() {
		params := inst.Params
		params.InstanceID = inst.ID
		p, err := Start(ctx, inst.Manifest, params, m.core, m.log)
		if err != nil {
			return nil, err
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

// Close stops every plugin.
func (m *Manager) Close() {
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/pluginhost/`
Expected: `ok  	github.com/davidgibbons/innkeeper/internal/pluginhost`

- [ ] **Step 5: Commit**

```bash
git add internal/pluginhost/manager.go internal/pluginhost/manager_test.go
git commit -m "Add plugin manager with crash restart and idle stop"
```

---

### Task 7: Contract kit

**Files:**
- Create: `internal/contract/contract_test.go`
- Create: `internal/contract/contract.go`

The checks here apply to every plugin. Later plans add per-capability checks.

- [ ] **Step 1: Write the failing tests**

Create `internal/contract/contract_test.go`:

```go
package contract

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/davidgibbons/innkeeper/internal/testutil"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func failed(results []Result) map[string]error {
	out := map[string]error{}
	for _, r := range results {
		if r.Err != nil {
			out[r.Name] = r.Err
		}
	}
	return out
}

func TestWellBehavedPluginPasses(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePlugin(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("ran %d checks, want 3", len(results))
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
}

func TestMisbehavingPluginsFail(t *testing.T) {
	tests := []struct{ mode, check string }{
		{"wrong-caps", "initialize handshake matches manifest"},
		{"bad-errors", "unknown method returns -32601"},
	}
	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			results, err := Check(context.Background(), testutil.FakePlugin(t, "-mode="+tt.mode), quiet)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := failed(results)[tt.check]; !ok {
				t.Fatalf("check %q passed, want failure; results: %v", tt.check, results)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/contract/`
Expected: FAIL with `undefined: Result` and `undefined: Check`.

- [ ] **Step 3: Implement the checks**

Create `internal/contract/contract.go`:

```go
// Package contract checks that a plugin follows the plugin protocol. The
// innkeeper-plugins CI runs it against every plugin via `innkeeper plugin
// check`.
package contract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Result is the outcome of one check. Err is nil when it passed.
type Result struct {
	Name string
	Err  error
}

// Check runs every check against the plugin in dir. It returns an error only
// when the manifest can't be loaded, since nothing else can be checked then.
func Check(ctx context.Context, dir string, log *slog.Logger) ([]Result, error) {
	m, err := protocol.LoadManifest(dir)
	if err != nil {
		return nil, err
	}
	secrets := map[string]string{}
	for _, s := range m.Secrets {
		secrets[s] = "contract-check"
	}
	params := protocol.InitializeParams{InstanceID: "contract-check", Config: map[string]any{}, Secrets: secrets}

	p, err := pluginhost.Start(ctx, m, params, noCore, log)
	results := []Result{{"initialize handshake matches manifest", err}}
	if err != nil {
		return results, nil
	}
	err = p.Call(ctx, "innkeeper.contract.unknown", nil, nil)
	results = append(results, Result{"unknown method returns -32601", expectCode(err, protocol.CodeMethodNotFound)})
	results = append(results, Result{"exits after shutdown", p.Stop()})
	return results, nil
}

// noCore answers plugin callbacks during a check, where no library exists.
func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

func expectCode(err error, code int) error {
	if err == nil {
		return fmt.Errorf("expected error %d, got success", code)
	}
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) {
		return fmt.Errorf("expected error %d, got %w", code, err)
	}
	if rpcErr.Code != code {
		return fmt.Errorf("expected error %d, got %d", code, rpcErr.Code)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/contract/`
Expected: `ok  	github.com/davidgibbons/innkeeper/internal/contract`

- [ ] **Step 5: Commit**

```bash
git add internal/contract
git commit -m "Add plugin contract checks"
```

---

### Task 8: `innkeeper plugin check` command

**Files:**
- Create: `cmd/innkeeper/main.go`

- [ ] **Step 1: Write the command**

Create `cmd/innkeeper/main.go`. Later plans add subcommands to `run`.

```go
// Command innkeeper is the Innkeeper server and CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/davidgibbons/innkeeper/internal/contract"
)

const usage = "usage: innkeeper plugin check <plugin-dir>"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "innkeeper:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 3 && args[0] == "plugin" && args[1] == "check" {
		return pluginCheck(args[2])
	}
	return errors.New(usage)
}

func pluginCheck(dir string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	results, err := contract.Check(context.Background(), dir, log)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL  %s: %v\n", r.Name, r.Err)
		} else {
			fmt.Printf("PASS  %s\n", r.Name)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d checks failed", failed, len(results))
	}
	return nil
}
```

- [ ] **Step 2: Run it against a passing plugin**

```bash
D=$(mktemp -d)
go build -o "$D/fakeplugin" ./internal/fakeplugin
printf 'name = "fake"\nversion = "0.0.1"\nprotocol = 1\ncommand = ["./fakeplugin"]\ncapabilities = ["push", "pull"]\n' > "$D/plugin.toml"
go run ./cmd/innkeeper plugin check "$D"; echo "exit $?"
```

Expected:

```
PASS  initialize handshake matches manifest
PASS  unknown method returns -32601
PASS  exits after shutdown
exit 0
```

- [ ] **Step 3: Run it against a failing plugin**

```bash
sed -i.bak 's/\["\.\/fakeplugin"\]/[".\/fakeplugin", "-mode=bad-errors"]/' "$D/plugin.toml"
go run ./cmd/innkeeper plugin check "$D"; echo "exit $?"
rm -rf "$D"
```

Expected: the second check prints `FAIL  unknown method returns -32601: expected error -32601, got success`, then `innkeeper: 1 of 3 checks failed` and a non-zero exit.

- [ ] **Step 4: Run the full suite**

Run: `go vet ./... && go test -race ./...`
Expected: every package `ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/innkeeper
git commit -m "Add innkeeper plugin check command"
```

---

### Task 9: Protocol reference

**Files:**
- Create: `docs/protocol.md`

Plugin authors build against this document alone, so it must match the code from Tasks 2 to 8. Invoke the `documentation-writing` skill before writing it.

- [ ] **Step 1: Write the reference**

Create `docs/protocol.md`:

````markdown
# Innkeeper plugin protocol

Protocol version 1. The design behind it is in
[the core design spec](superpowers/specs/2026-10-03-innkeeper-core-design.md).

## Transport

- The core starts the manifest's `command` with the plugin folder as its
  working directory.
- Messages are JSON-RPC 2.0, one JSON value per line, on stdin and stdout.
  Write nothing else to stdout.
- Write logs to stderr. The core logs each line under the plugin's name.
- Both sides send requests. Answer every request with the same `id`, which
  may be a number or a string.
- The core handles requests concurrently and notifications in the order they
  arrive, so a plugin can rely on the order of what it sends.

## Manifest

Each plugin folder has a `plugin.toml`:

| Key | Required | Meaning |
|---|---|---|
| `name` | yes | Lowercase letters, digits, and `-`; starts with a letter; at most 41 characters |
| `version` | yes | The plugin's own version |
| `protocol` | yes | Protocol version; must be `1` |
| `command` | yes | Program and arguments. `./` and `../` paths are relative to the plugin folder; bare names are found on `PATH` |
| `capabilities` | yes | Any of `import`, `search`, `push`, `pull`, `action`, `enrich`, `codec`, `auth` |
| `secrets` | no | Names of secrets the owner enters for each instance |
| `config` | no | JSON Schema for instance settings; the UI renders a form from it |
| `enrichments` | no | Keys the plugin writes; `type` is `text`, `score` (needs `range = [low, high]`), or `list` |

Unknown keys are errors.

## Lifecycle

1. **`initialize`**: the core's first request.

   | Param | Meaning |
   |---|---|
   | `protocol` | The core's protocol version |
   | `instance_id` | The configured instance this process serves |
   | `config` | Instance settings |
   | `secrets` | Decrypted secrets, by name |
   | `blob_stores` | Blob store paths, by store name |
   | `blob_tmp` | Folder for new blob files |
   | `database_url` | PostgreSQL DSN for a role limited to the `plugin_<name>` schema |

   Reply with `protocol`, `name`, `version`, and `capabilities`. These must
   match the manifest, or the core stops the plugin.

   Don't contact external services during `initialize`. Report bad
   settings on the first call that needs them.

2. **Calls**: the core sends capability methods as it needs them.

3. **`shutdown`**: reply, then exit when stdin closes. The core kills a
   plugin that is still running 5 seconds after `shutdown`.

The core starts a plugin on first use, stops it when idle, and starts it
again after a crash. The API and the worker may each run the same instance,
so keep anything that must persist in the plugin's schema or the blob store,
not in memory.

## Errors

Use standard JSON-RPC errors. Answer unknown methods with `-32601`.

`data` may be any JSON value. When it is an object, set `data.retryable` to
`true` if the same call might succeed later, such as after a timeout or a
rate limit:

```json
{"jsonrpc": "2.0", "id": 7, "error": {"code": -32000, "message": "rate limited", "data": {"retryable": true}}}
```

The core re-queues jobs that fail with a retryable error and fails the rest.

## Capability methods

Each capability's methods are added here as the core implements them. The
planned set is in the design spec's "Plugin protocol" section.

## Checking a plugin

```bash
innkeeper plugin check path/to/plugin
```

It loads the manifest, then checks that:

- the `initialize` reply matches the manifest
- an unknown method returns `-32601`
- the plugin exits after `shutdown`

It exits non-zero if any check fails.
````

- [ ] **Step 2: Commit**

```bash
git add docs/protocol.md
git commit -m "Add plugin protocol reference"
```

- [ ] **Step 3: Push and confirm CI passes**

```bash
git push
gh run watch --exit-status
```

Expected: the `ci` workflow succeeds.
