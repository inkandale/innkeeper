package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/protocol"
)

func TestRunsRoutesByToken(t *testing.T) {
	ctx := context.Background()
	runs := NewRuns(quiet)
	got := map[string]string{}
	handler := func(name string) protocol.Handler {
		return func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			got[name] = method
			return name, nil
		}
	}
	removeFirst := runs.Add("1.1", handler("first"))
	defer runs.Add("2.1", handler("second"))()

	if res, err := runs.Handle(ctx, "progress", json.RawMessage(`{"run": "1.1"}`)); err != nil || res != "first" {
		t.Fatalf("first run: %v, %v", res, err)
	}
	if res, err := runs.Handle(ctx, "library.call", json.RawMessage(`{"run": "2.1"}`)); err != nil || res != "second" {
		t.Fatalf("second run: %v, %v", res, err)
	}
	if got["first"] != "progress" || got["second"] != "library.call" {
		t.Fatalf("handlers saw %v", got)
	}

	removeFirst()
	for _, params := range []string{`{"run": "1.1"}`, `{}`, `[]`} {
		_, err := runs.Handle(ctx, "progress", json.RawMessage(params))
		var rpcErr *protocol.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeInvalidParams {
			t.Errorf("%s: got %v, want -32602", params, err)
		}
	}
}
