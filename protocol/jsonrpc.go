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

// methodCancel is the notification a caller sends when it stops waiting for
// a request's reply; Conn handles it itself and never passes it to Handler.
const methodCancel = "cancel"

type cancelParams struct {
	ID json.RawMessage `json:"id"`
}

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
	// served cancels the handler of each request still being answered, by
	// the request's raw JSON id.
	served map[string]context.CancelFunc
}

// NewConn returns a connection that reads from r and writes to w. Call Run
// to start reading.
func NewConn(r io.Reader, w io.Writer, h Handler) *Conn {
	return &Conn{
		dec:     json.NewDecoder(r),
		enc:     json.NewEncoder(w),
		handler: h,
		pending: map[int64]chan *message{},
		served:  map[string]context.CancelFunc{},
	}
}

// Run reads messages until r reaches EOF. It returns nil on EOF. Pending
// and later calls then fail with ErrClosed, and the ctx passed to handlers
// is cancelled. A cancel notification cancels the ctx of one request's
// handler.
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
		case m.Method == methodCancel && len(m.ID) == 0:
			c.cancelServed(m.Params)
		case m.Method != "" && len(m.ID) == 0:
			// Handled inline so notifications such as emit.card and
			// checkpoint are processed in the order they were sent.
			_, _ = c.handle(ctx, m)
		case m.Method != "":
			// Registered before serving so a cancel read next finds it.
			hctx, cancel := context.WithCancel(ctx)
			c.mu.Lock()
			c.served[string(m.ID)] = cancel
			c.mu.Unlock()
			go c.serve(hctx, cancel, m)
		default:
			c.deliver(m)
		}
	}
}

// Call sends a request and waits for its response. result may be nil. If
// ctx ends first, Call tells the peer to cancel the request.
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
		// In a goroutine so a peer that stopped reading can't hold up the caller.
		go c.Notify(methodCancel, cancelParams{ID: req.ID})
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

func (c *Conn) serve(ctx context.Context, cancel context.CancelFunc, req message) {
	resp := &message{JSONRPC: "2.0", ID: req.ID}
	result, err := c.handle(ctx, req)
	c.mu.Lock()
	delete(c.served, string(req.ID))
	c.mu.Unlock()
	cancel()
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

// cancelServed cancels the handler of the request params names. Unknown ids
// are ignored: the request may already be answered.
func (c *Conn) cancelServed(params json.RawMessage) {
	var p cancelParams
	if err := json.Unmarshal(params, &p); err != nil {
		return
	}
	c.mu.Lock()
	cancel, ok := c.served[string(p.ID)]
	c.mu.Unlock()
	if ok {
		cancel()
	}
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
