package protocol

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
)

// Plugin is a plugin for Serve to run. S is one instance's state.
type Plugin[S any] struct {
	// Init builds the instance's state from initialize's params.
	Init func(ctx context.Context, p InitializeParams) (*S, error)
	// Methods answer every call after initialize, by method name. Serve
	// answers shutdown itself when it isn't here. The state comes first so
	// a method expression such as (*instance).get fits.
	Methods map[string]func(s *S, ctx context.Context, params json.RawMessage) (any, error)
	// Services lists the service methods an instance offers, ai's, or
	// vector.query for an enrich plugin; nil for any other.
	Services func(s *S) []string
}

// Serve runs p over stdin and stdout until the core disconnects, answering
// initialize from ./plugin.toml, the plugin folder the core starts it in.
// It exits the process with status 1 on an error.
func Serve[S any](p Plugin[S]) {
	m, err := LoadManifest(".")
	if err == nil {
		err = NewConn(os.Stdin, os.Stdout, p.Handler(m)).Run(context.Background())
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Handler answers initialize with m's name, version, and capabilities, and
// passes every later call to p's Methods. Tests use it to run a plugin over
// pipes; Serve uses it over stdin and stdout.
func (p Plugin[S]) Handler(m *Manifest) Handler {
	// Set by initialize; requests run on other goroutines.
	var cur atomic.Pointer[S]
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if method == MethodInitialize {
			var ip InitializeParams
			if err := json.Unmarshal(params, &ip); err != nil {
				return nil, InvalidParams(err)
			}
			s, err := p.Init(ctx, ip)
			if err != nil {
				return nil, err
			}
			cur.Store(s)
			res := InitializeResult{Protocol: Version, Name: m.Name, Version: m.Version, Capabilities: m.Capabilities}
			if p.Services != nil {
				res.Services = p.Services(s)
			}
			return res, nil
		}
		call, ok := p.Methods[method]
		if !ok {
			if method == MethodShutdown {
				return nil, nil
			}
			return nil, MethodNotFound(method)
		}
		s := cur.Load()
		if s == nil {
			return nil, NewError(CodeInvalidRequest, method+" before initialize", false)
		}
		return call(s, ctx, params)
	}
}
