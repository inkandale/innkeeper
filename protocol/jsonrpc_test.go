package protocol

import (
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
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	block := func(context.Context, string, json.RawMessage) (any, error) {
		<-release
		return nil, nil
	}
	a, _, bw := pair(t, nil, block)
	errc := make(chan error, 1)
	go func() { errc <- a.Call(context.Background(), "slow", nil, nil) }()
	time.Sleep(50 * time.Millisecond)
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
