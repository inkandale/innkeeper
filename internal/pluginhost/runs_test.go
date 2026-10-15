package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/davidgibbons/innkeeper/protocol"
)

func TestRunsRoutesByToken(t *testing.T) {
	runs := NewRuns(quiet)
	got := map[string]string{}
	handler := func(name string) protocol.Handler {
		return func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			got[name] = method
			return name, nil
		}
	}
	first, removeFirst := runs.Add("1", handler("first"))
	second, removeSecond := runs.Add("2", handler("second"))
	defer removeSecond()
	if first == second || len(first) < 20 {
		t.Fatalf("tokens %q and %q aren't unique and unguessable", first, second)
	}
	call := func(instance, token, method string) (any, error) {
		params, _ := json.Marshal(map[string]string{"run": token})
		return runs.Handle(fromInstance(context.Background(), instance), method, params)
	}

	if res, err := call("1", first, "progress"); err != nil || res != "first" {
		t.Fatalf("first run: %v, %v", res, err)
	}
	if res, err := call("2", second, "library.call"); err != nil || res != "second" {
		t.Fatalf("second run: %v, %v", res, err)
	}
	if got["first"] != "progress" || got["second"] != "library.call" {
		t.Fatalf("handlers saw %v", got)
	}

	refused := func(name string, res any, err error) {
		t.Helper()
		var rpcErr *protocol.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeInvalidParams {
			t.Errorf("%s: got %v, %v; want -32602", name, res, err)
		}
	}
	res, err := call("2", first, "library.call")
	refused("another instance's live token", res, err)
	removeFirst()
	res, err = call("1", first, "progress")
	refused("an ended run", res, err)
	res, err = call("1", "", "progress")
	refused("no token", res, err)
}
