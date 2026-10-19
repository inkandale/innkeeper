package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
)

type echoState struct{ instance string }

// servePlugin runs a plugin whose echo method returns its instance ID and
// params, over pipes, initialized from validManifest.
func servePlugin(t *testing.T) *Conn {
	t.Helper()
	m, err := LoadManifest(writeManifest(t, validManifest))
	if err != nil {
		t.Fatal(err)
	}
	p := Plugin[echoState]{
		Init: func(_ context.Context, ip InitializeParams) (*echoState, error) {
			return &echoState{instance: ip.InstanceID}, nil
		},
		Methods: map[string]func(*echoState, context.Context, json.RawMessage) (any, error){
			"echo": func(s *echoState, _ context.Context, params json.RawMessage) (any, error) {
				return map[string]any{"instance": s.instance, "params": params}, nil
			},
		},
		Services: func(s *echoState) []string { return []string{"ai.chat"} },
	}
	core, _, _ := pair(t, nil, p.Handler(m))
	return core
}

func rpcCode(err error) int {
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		return rpcErr.Code
	}
	return 0
}

func TestServeAnswersInitializeFromManifest(t *testing.T) {
	core := servePlugin(t)
	var res InitializeResult
	if err := core.Call(context.Background(), MethodInitialize, InitializeParams{InstanceID: "i1"}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Protocol != Version || res.Name != "lumiverse" || res.Version != "0.1.0" ||
		!slices.Equal(res.Capabilities, []string{"push", "pull"}) || !slices.Equal(res.Services, []string{"ai.chat"}) {
		t.Fatalf("initialize = %+v, want the manifest's name, version, and capabilities", res)
	}
}

func TestServeRejectsCallBeforeInitialize(t *testing.T) {
	core := servePlugin(t)
	err := core.Call(context.Background(), "echo", nil, nil)
	if rpcCode(err) != CodeInvalidRequest {
		t.Fatalf("echo before initialize: err = %v, want %d", err, CodeInvalidRequest)
	}
}

func TestServeDispatchesWithInstanceState(t *testing.T) {
	core := servePlugin(t)
	ctx := context.Background()
	if err := core.Call(ctx, MethodInitialize, InitializeParams{InstanceID: "i1"}, nil); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Instance string         `json:"instance"`
		Params   map[string]int `json:"params"`
	}
	if err := core.Call(ctx, "echo", map[string]int{"x": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if out.Instance != "i1" || out.Params["x"] != 1 {
		t.Fatalf("echo = %+v, want instance i1 and params {x: 1}", out)
	}
	if err := core.Call(ctx, MethodShutdown, nil, nil); err != nil {
		t.Fatalf("shutdown: %v, want the default reply", err)
	}
}

func TestServeAnswersUnknownMethodNotFound(t *testing.T) {
	core := servePlugin(t)
	ctx := context.Background()
	if err := core.Call(ctx, MethodInitialize, InitializeParams{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := core.Call(ctx, "nope", nil, nil); rpcCode(err) != CodeMethodNotFound {
		t.Fatalf("unknown method: err = %v, want %d", err, CodeMethodNotFound)
	}
}
