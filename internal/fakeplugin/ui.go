package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

// rememberedRun is the run token the remember method kept.
var rememberedRun atomic.Value

// uiCall answers the methods the ui tests drive: echo sends one event and
// replies, huge sends an oversized event, flood outruns any reader and leaves
// a file once it has sent everything, slow waits to be cancelled and leaves a
// file saying so, library sends params as a library.call, remember keeps the
// run token, replay makes a library.call with the kept token, empty replies
// null, fail is a plugin error, leak is an untyped Go error, and die exits.
// Mode ui-accepts-all answers every method with null.
func uiCall(ctx context.Context, conn *protocol.Conn, params json.RawMessage, mode string) (any, error) {
	if mode == "ui-accepts-all" {
		return nil, nil
	}
	var p protocol.UICallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	switch p.Method {
	case "echo":
		_ = conn.Notify(protocol.NotifyUIEvent, protocol.UIEvent{Run: p.Run, Data: p.Params})
		return map[string]string{"status": "ok"}, nil
	case "huge":
		data, _ := json.Marshal(strings.Repeat("x", protocol.MaxUIEvent))
		_ = conn.Notify(protocol.NotifyUIEvent, protocol.UIEvent{Run: p.Run, Data: data})
		return map[string]string{"status": "ok"}, nil
	case "flood":
		data, _ := json.Marshal(strings.Repeat("x", protocol.MaxUIEvent-16))
		for range 1000 {
			_ = conn.Notify(protocol.NotifyUIEvent, protocol.UIEvent{Run: p.Run, Data: data})
		}
		if err := os.WriteFile("flooded", nil, 0o644); err != nil {
			return nil, err
		}
		return map[string]string{"status": "ok"}, nil
	case "slow":
		_ = conn.Notify(protocol.NotifyUIEvent, protocol.UIEvent{Run: p.Run, Data: json.RawMessage(`"started"`)})
		select {
		case <-ctx.Done():
			if err := os.WriteFile("cancelled", nil, 0o644); err != nil {
				return nil, err
			}
			return nil, ctx.Err()
		case <-time.After(30 * time.Second):
			return nil, nil
		}
	case "library":
		var call protocol.LibraryCallParams
		_ = json.Unmarshal(p.Params, &call)
		call.Run = p.Run
		var res protocol.MCPCallResult
		if err := conn.Call(ctx, protocol.MethodLibraryCall, call, &res); err != nil {
			return nil, err
		}
		return res, nil
	case "remember":
		rememberedRun.Store(p.Run)
		return nil, nil
	case "replay":
		run, _ := rememberedRun.Load().(string)
		call := protocol.LibraryCallParams{Run: run, Name: "search_cards"}
		return nil, conn.Call(ctx, protocol.MethodLibraryCall, call, nil)
	case "empty":
		return nil, nil
	case "fail":
		return nil, protocol.Fail("the panel broke", false)
	case "leak":
		return nil, errors.New("secret path /etc/x")
	case "die":
		os.Exit(1)
	}
	return nil, protocol.MethodNotFound(p.Method)
}
