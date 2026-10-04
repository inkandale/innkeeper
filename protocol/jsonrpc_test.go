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
