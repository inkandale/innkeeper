package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/davidgibbons/innkeeper/protocol"
)

// aiServices are the services the config lists under services.
func aiServices(config map[string]any) []string {
	list, _ := config["services"].([]any)
	var out []string
	for _, s := range list {
		if s, ok := s.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// aiChat streams the last message's words as deltas and replies "echo: "
// and the message. A last message of "hang" waits to be cancelled, then
// calls cancelled.
func aiChat(ctx context.Context, conn *protocol.Conn, params json.RawMessage, cancelled func()) (any, error) {
	var p protocol.AIChatParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Messages) == 0 {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "ai.chat needs messages", false)
	}
	text := p.Messages[len(p.Messages)-1].Content
	if text == "hang" {
		<-ctx.Done()
		cancelled()
		return nil, ctx.Err()
	}
	for _, w := range strings.Fields(text) {
		_ = conn.Notify(protocol.NotifyAIDelta, protocol.AIDelta{Run: p.Run, Text: w + " "})
	}
	return protocol.AIChatResult{Text: "echo: " + text, Model: "fake"}, nil
}

// deltaLog keeps the ai.delta texts this process received, by run.
type deltaLog struct {
	mu   sync.Mutex
	runs map[string][]string
}

func (l *deltaLog) add(params json.RawMessage) {
	var d protocol.AIDelta
	if json.Unmarshal(params, &d) != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.runs == nil {
		l.runs = map[string][]string{}
	}
	l.runs[d.Run] = append(l.runs[d.Run], d.Text)
}

func (l *deltaLog) take(run string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := l.runs[run]
	delete(l.runs, run)
	return out
}

// relay sends params' service call to the core as a consumer would, and
// returns the reply with the deltas that arrived for it.
func relay(ctx context.Context, conn *protocol.Conn, params json.RawMessage, deltas *deltaLog) (any, error) {
	var r struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	var head protocol.ServiceCall
	if err := json.Unmarshal(params, &r); err != nil || json.Unmarshal(r.Params, &head) != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "relay needs method and params", false)
	}
	var res json.RawMessage
	if err := conn.Call(ctx, r.Method, r.Params, &res); err != nil {
		return nil, err
	}
	return map[string]any{"result": res, "deltas": deltas.take(head.Run)}, nil
}
