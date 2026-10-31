package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/inkandale/innkeeper/protocol"
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

// The OpenAI chat API's wire types, as far as the fake uses them.
type oaiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type oaiToolCall struct {
	Index    int         `json:"index"`
	ID       string      `json:"id,omitempty"`
	Type     string      `json:"type,omitempty"`
	Function oaiFunction `json:"function"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    string        `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

// openAIChat sends the chat to the OpenAI-compatible server at base and
// streams its reply as ai.delta, as a real provider plugin does.
func openAIChat(ctx context.Context, conn *protocol.Conn, base string, params json.RawMessage) (any, error) {
	var p protocol.AIChatParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Messages) == 0 {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "ai.chat needs messages", false)
	}
	req := struct {
		Model    string       `json:"model"`
		Stream   bool         `json:"stream"`
		Messages []oaiMessage `json:"messages"`
		Tools    []oaiTool    `json:"tools,omitempty"`
	}{Model: "fake", Stream: true}
	for _, m := range p.Messages {
		om := oaiMessage{Role: m.Role, Content: m.Content, ToolCallID: m.ToolCallID}
		for _, c := range m.ToolCalls {
			om.ToolCalls = append(om.ToolCalls, oaiToolCall{ID: c.ID, Type: "function",
				Function: oaiFunction{Name: c.Name, Arguments: c.Arguments}})
		}
		req.Messages = append(req.Messages, om)
	}
	for _, t := range p.Tools {
		req.Tools = append(req.Tools, oaiTool{Type: "function",
			Function: oaiFunction{Name: t.Name, Description: t.Description, Parameters: t.Parameters}})
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, protocol.Fail("model server: "+resp.Status, false)
	}
	res := protocol.AIChatResult{Model: "fake"}
	var text strings.Builder
	scan := bufio.NewScanner(resp.Body)
	for scan.Scan() {
		data, ok := strings.CutPrefix(scan.Text(), "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string        `json:"content"`
					ToolCalls []oaiToolCall `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			text.WriteString(d.Content)
			_ = conn.Notify(protocol.NotifyAIDelta, protocol.AIDelta{Run: p.Run, Text: d.Content})
		}
		// The fake takes each tool call whole from one chunk; real servers may split them.
		for _, c := range d.ToolCalls {
			res.ToolCalls = append(res.ToolCalls, protocol.ToolCall{ID: c.ID, Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	res.Text = text.String()
	return res, nil
}

// chatWithTool sends ai.chat through the core with one tool, answers each
// tool call, and asks again, as a real consumer does. The drop-tool-call
// mode leaves the call out of the assistant message it sends back. The
// plain-chat mode sends one chat with no Run and no tools, as a consumer that
// neither streams nor uses tools does.
func chatWithTool(ctx context.Context, conn *protocol.Conn, run string, instance int64, mode string) error {
	params := protocol.AIChatParams{ServiceCall: protocol.ServiceCall{Instance: instance, Run: run + "/chat"},
		Messages: []protocol.ChatMessage{{Role: "user", Content: "Say OK."}},
		Tools:    []protocol.ChatTool{{Name: "lookup", Parameters: json.RawMessage(`{"type": "object"}`)}}}
	if mode == "plain-chat" {
		params.Run, params.Tools = "", nil
	}
	for {
		var res protocol.AIChatResult
		if err := conn.Call(ctx, protocol.MethodAIChat, params, &res); err != nil {
			return err
		}
		if len(res.ToolCalls) == 0 {
			return nil
		}
		reply := protocol.ChatMessage{Role: "assistant", Content: res.Text, ToolCalls: res.ToolCalls}
		if mode == "drop-tool-call" {
			reply.ToolCalls = nil
		}
		params.Messages = append(params.Messages, reply)
		for _, c := range res.ToolCalls {
			params.Messages = append(params.Messages, protocol.ChatMessage{Role: "tool", Content: "Nothing found.", ToolCallID: c.ID})
		}
		params.Tools = nil
	}
}

// aiEmbed returns one two-dimensional vector per text: its length and 1. A
// text containing POISON fails the call as a model rejecting an input does,
// and one containing DENIED as a provider refusing the instance's key does.
func aiEmbed(params json.RawMessage) (any, error) {
	var p protocol.AIEmbedParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Texts) == 0 {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "ai.embed needs texts", false)
	}
	res := protocol.AIEmbedResult{Vectors: make([][]float32, len(p.Texts))}
	for i, t := range p.Texts {
		if strings.Contains(t, "POISON") {
			return nil, protocol.NewError(protocol.CodeAIRejected, "ai.embed rejects POISON", false)
		}
		if strings.Contains(t, "DENIED") {
			return nil, protocol.NewError(protocol.CodeInternalError, "401 Unauthorized", false)
		}
		res.Vectors[i] = []float32{float32(len(t)), 1}
	}
	return res, nil
}

// aiDecide answers every question with certainty: a choice's first option by
// name, a score's top level, and a noul true.
func aiDecide(params json.RawMessage) (any, error) {
	var p protocol.AIDecideParams
	if err := json.Unmarshal(params, &p); err != nil || len(p.Questions) == 0 {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "ai.decide needs questions", false)
	}
	sure := 1.0
	answers := map[string]protocol.DecideAnswer{}
	for id, q := range p.Questions {
		a := protocol.DecideAnswer{Type: q.Type}
		switch q.Type {
		case "choice":
			var options map[string]string
			if json.Unmarshal(q.Criteria, &options) != nil || len(options) == 0 {
				return nil, protocol.NewError(protocol.CodeInvalidParams, id+": a choice needs options", false)
			}
			a.Choice = slices.Min(slices.Collect(maps.Keys(options)))
			a.Probabilities, a.Confidence = map[string]float64{a.Choice: 1}, &sure
		case "score":
			var levels []json.RawMessage
			if json.Unmarshal(q.Criteria, &levels) != nil || len(levels) < 2 {
				return nil, protocol.NewError(protocol.CodeInvalidParams, id+": a score needs at least two levels", false)
			}
			top := float64(len(levels) - 1)
			a.Score, a.Probabilities, a.Confidence = &top, map[string]float64{strconv.Itoa(len(levels) - 1): 1}, &sure
		case "noul":
			a.Noul = &sure
		default:
			return nil, protocol.NewError(protocol.CodeInvalidParams, id+": unknown type "+q.Type, false)
		}
		answers[id] = a
	}
	return protocol.AIDecideResult{Answers: answers, Model: "fake"}, nil
}
