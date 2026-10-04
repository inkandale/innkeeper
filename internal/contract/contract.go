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
	"time"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/protocol"
)

// callTimeout bounds each check's call so a silent plugin can't hang CI. A
// variable so tests can shorten it.
var callTimeout = 5 * time.Second

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
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	err = p.Call(callCtx, "innkeeper.contract.unknown", nil, nil)
	cancel()
	results = append(results, Result{"unknown method returns -32601", expectCode(err, protocol.CodeMethodNotFound)})

	// A plugin that already died would "exit after shutdown" without
	// handling it.
	if errors.Is(err, protocol.ErrClosed) || !p.Alive() {
		_ = p.Stop()
		dead := errors.New("plugin had already exited before shutdown")
		return append(results, Result{"answers shutdown", dead}, Result{"exits after shutdown", dead}), nil
	}

	callCtx, cancel = context.WithTimeout(ctx, callTimeout)
	err = p.Call(callCtx, protocol.MethodShutdown, nil, nil)
	cancel()
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("no response within %s", callTimeout)
	}
	results = append(results, Result{"answers shutdown", err})
	return append(results, Result{"exits after shutdown", p.Stop()}), nil
}

// noCore answers plugin callbacks during a check, where no library exists.
func noCore(_ context.Context, method string, _ json.RawMessage) (any, error) {
	return nil, protocol.MethodNotFound(method)
}

func expectCode(err error, code int) error {
	if err == nil {
		return fmt.Errorf("expected error %d, got success", code)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("expected error %d, got no response within %s", code, callTimeout)
	}
	if errors.Is(err, protocol.ErrClosed) {
		return fmt.Errorf("expected error %d, but the plugin exited", code)
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
