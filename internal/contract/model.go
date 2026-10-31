package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

const (
	// toolCallID and toolArguments are the tool call the fake model makes.
	toolCallID    = "contract-call"
	toolArguments = `{"contract":true}`
)

// The OpenAI chat API's wire types, as far as the fake model uses them.
type oaiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

type oaiToolCall struct {
	Index    int         `json:"index"`
	ID       string      `json:"id,omitempty"`
	Type     string      `json:"type,omitempty"`
	Function oaiFunction `json:"function"`
}

type oaiMessage struct {
	Role       string        `json:"role"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiDelta struct {
	Content   string        `json:"content,omitempty"`
	ToolCalls []oaiToolCall `json:"tool_calls,omitempty"`
}

type oaiChoice struct {
	Index        int      `json:"index"`
	Delta        oaiDelta `json:"delta"`
	FinishReason *string  `json:"finish_reason"`
}

type oaiChunk struct {
	ID      string      `json:"id"`
	Object  string      `json:"object"`
	Model   string      `json:"model"`
	Choices []oaiChoice `json:"choices"`
}

// modelServer is a fake OpenAI-compatible chat and embeddings API. It streams
// each chat reply in two chunks, calls the first tool of the first request that
// offers tools, and checks the first request that answers that call. After
// holdNext, the next chat request gets one chunk, then waits for its client to
// go away. Embeddings are [length, 1] per input.
type modelServer struct {
	*httptest.Server
	// holding is closed when the held request has had its chunk; ended
	// when that request's client went away.
	holding, ended chan struct{}
	stop           chan struct{}

	mu       sync.Mutex
	requests int
	hold     bool
	toolName string // the tool the model called, or ""
	answered bool
	toolErr  error
}

func newModelServer() *modelServer {
	s := &modelServer{holding: make(chan struct{}), ended: make(chan struct{}), stop: make(chan struct{})}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			s.chat(w, r)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/embeddings"):
			s.embed(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	return s
}

// Close ends a request still held, then stops the server.
func (s *modelServer) Close() {
	close(s.stop)
	s.Server.Close()
}

func (s *modelServer) holdNext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hold = true
}

func (s *modelServer) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

// toolRoundTrip reports why the model's tool call didn't come back with its
// result, or nil once it did.
func (s *modelServer) toolRoundTrip() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.toolName == "":
		return errors.New("no request to the model offered tools")
	case !s.answered:
		return fmt.Errorf("the model called %s, but no later request answered the call", s.toolName)
	}
	return s.toolErr
}

func (s *modelServer) chat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []oaiMessage `json:"messages"`
		Tools    []struct {
			Function oaiFunction `json:"function"`
		} `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.requests++
	hold := s.hold
	s.hold = false
	callTool := !hold && s.toolName == "" && len(req.Tools) > 0
	if callTool {
		s.toolName = req.Tools[0].Function.Name
	}
	if s.toolName != "" && !s.answered && answers(req.Messages) {
		s.answered, s.toolErr = true, answerErr(req.Messages, s.toolName)
	}
	name := s.toolName
	s.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	switch {
	case hold:
		writeChunk(w, oaiDelta{Content: "Contract "}, nil)
		close(s.holding)
		select {
		case <-r.Context().Done():
			close(s.ended)
		case <-s.stop:
		}
	case callTool:
		writeChunk(w, oaiDelta{ToolCalls: []oaiToolCall{{ID: toolCallID, Type: "function",
			Function: oaiFunction{Name: name, Arguments: toolArguments}}}}, nil)
		finish(w, "tool_calls")
	default:
		writeChunk(w, oaiDelta{Content: "Contract "}, nil)
		writeChunk(w, oaiDelta{Content: "check."}, nil)
		finish(w, "stop")
	}
}

// answers reports whether msgs hold a tool result for the model's call.
func answers(msgs []oaiMessage) bool {
	for _, m := range msgs {
		if m.Role == "tool" && m.ToolCallID == toolCallID {
			return true
		}
	}
	return false
}

// answerErr reports why msgs don't carry the model's call back unchanged.
func answerErr(msgs []oaiMessage, name string) error {
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if m.Role != "assistant" || c.ID != toolCallID {
				continue
			}
			if c.Function.Name != name || c.Function.Arguments != toolArguments {
				return fmt.Errorf("the call came back as %s(%s), want %s(%s)", c.Function.Name, c.Function.Arguments, name, toolArguments)
			}
			return nil
		}
	}
	return fmt.Errorf("the result for %s came back without the assistant message that made the call", toolCallID)
}

func writeChunk(w http.ResponseWriter, d oaiDelta, finishReason *string) {
	b, _ := json.Marshal(oaiChunk{ID: "contract", Object: "chat.completion.chunk", Model: "contract",
		Choices: []oaiChoice{{Delta: d, FinishReason: finishReason}}})
	fmt.Fprintf(w, "data: %s\n\n", b)
	w.(http.Flusher).Flush()
}

func finish(w http.ResponseWriter, reason string) {
	writeChunk(w, oaiDelta{}, &reason)
	fmt.Fprint(w, "data: [DONE]\n\n")
}

// embed answers an OpenAI embeddings request with [length, 1] per input.
func (s *modelServer) embed(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string          `json:"model"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// OpenAI accepts a single string or a list.
	var inputs []string
	var one string
	if json.Unmarshal(req.Input, &one) == nil {
		inputs = []string{one}
	} else if err := json.Unmarshal(req.Input, &inputs); err != nil {
		http.Error(w, "input must be a string or a list of strings", http.StatusBadRequest)
		return
	}
	type datum struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float32 `json:"embedding"`
	}
	data := make([]datum, len(inputs))
	for i, in := range inputs {
		data[i] = datum{Object: "embedding", Index: i, Embedding: []float32{float32(len(in)), 1}}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data, "model": req.Model})
}
