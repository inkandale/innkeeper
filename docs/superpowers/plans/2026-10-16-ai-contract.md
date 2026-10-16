# Service Contract Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `innkeeper plugin check` runs a plugin that declares `uses` against a real provider plugin through the real `services.Broker`, checking replies, streamed deltas, tool calls, and cancellation (INN-62).

**Architecture:** The Broker takes a small `services.Registry` interface, so the contract kit can give it an in-memory registry of two instances. The kit runs a fake OpenAI-compatible model server, starts the consumer and the provider under one `pluginhost.Manager` whose core handler is the Broker, drives the consumer through `action.run` and `enrich.run`, and reads the outcome from the model server and from wrappers around the Broker's handler.

**Tech Stack:** Go standard library (`net/http/httptest`, SSE by hand), existing `pluginhost`, `services`, `protocol` packages.

**Spec:** `docs/superpowers/specs/2026-10-16-ai-contract-design.md`

**Test environment:** DB-backed tests skip without
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.
Set it for every `go test` run below. The contract tests don't need it.

---

## File map

| File | Change |
|---|---|
| `internal/services/services.go` | Add the `Registry` interface; `Broker.Registry` takes it. |
| `internal/fakeplugin/ai.go` | Provider: stream `ai.chat` from an OpenAI-compatible server when the config has `base_url`. Consumer: `chatWithTool`. |
| `internal/fakeplugin/actions.go` | `actionRun` calls `chatWithTool` when the config has `connection`. |
| `internal/fakeplugin/main.go` | Pass config and mode to `actionRun`; route `ai.chat` by `base_url`; add `drop-tool-call` to the mode list. |
| `internal/testutil/fakeplugin.go` | `FakeConsumer`: an action fake whose manifest declares `uses` and a `connection` instance field. |
| `internal/contract/model.go` | Create: the fake model server. |
| `internal/contract/uses.go` | Create: the uses suite, its harness, and the in-memory registry. |
| `internal/contract/action.go`, `enrich.go` | Extract `actionRunParams` and `enrichRunParams` so the uses suite sends the same sample calls. |
| `internal/contract/contract.go` | Extract `checkSecrets`; run the uses suite for a manifest with `uses`. |
| `internal/contract/contract_test.go` | Tests for the uses suite. |
| `cmd/innkeeper/main.go` | Read `INNKEEPER_PROVIDER_PLUGIN` and `INNKEEPER_PROVIDER_CONFIG`. |
| `docs/protocol.md` | Document the new checks and variables. |
| innkeeper-plugins: `ai/check-provider.json`, `Makefile`, `go.mod`, `go.sum` | Run forge-ai's check with `ai` as provider. |

---

### Task 1: Broker takes a Registry interface

**Files:**
- Modify: `internal/services/services.go:19-26`

- [ ] **Step 1: Add the interface and change the field**

Replace the `Broker` struct with:

```go
// Registry is what the Broker needs to know about instances and to call
// them. *plugins.Registry is the production one.
type Registry interface {
	Instance(ctx context.Context, id int64) (pluginhost.Instance, error)
	InstanceWith(ctx context.Context, id int64, capability string) (pluginhost.Instance, error)
	Services(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance) ([]string, error)
	Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error
}

// Broker answers service calls for the plugins on one Manager.
type Broker struct {
	Registry Registry
	// Plugins runs the providers; its core handler must be Handle's.
	Plugins *pluginhost.Manager
	// Runs carries the providers' notifications back to the call.
	Runs *pluginhost.Runs
}
```

The `plugins` import stays: `call` still checks `plugins.ErrIncapable` and the other sentinels.

- [ ] **Step 2: Build and run the broker tests**

Run: `go build ./... && go test ./internal/services/`
Expected: builds; `ok` (`cmd/innkeeper/worker.go:67` passes `svc.registry`, a `*plugins.Registry`, which satisfies the interface).

- [ ] **Step 3: Commit**

```bash
git add internal/services/services.go
git commit -m "refactor(services): take the registry the broker needs as an interface"
```

---

### Task 2: The fakeplugin plays provider and consumer

**Files:**
- Modify: `internal/fakeplugin/ai.go`, `internal/fakeplugin/actions.go`, `internal/fakeplugin/main.go`
- Modify: `internal/testutil/fakeplugin.go`

These are test fixtures; Task 4's tests exercise them.

- [ ] **Step 1: Stream `ai.chat` from an OpenAI-compatible server**

Append to `internal/fakeplugin/ai.go` (add `bufio`, `bytes`, `net/http` to its imports):

```go
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
		return nil, protocol.NewError(-32000, "model server: "+resp.Status, false)
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
```

- [ ] **Step 2: Add the consumer's chat**

Append to `internal/fakeplugin/ai.go`:

```go
// chatWithTool sends ai.chat through the core with one tool, answers each
// tool call, and asks again, as a real consumer does. The drop-tool-call
// mode leaves the call out of the assistant message it sends back.
func chatWithTool(ctx context.Context, conn *protocol.Conn, run string, instance int64, mode string) error {
	params := protocol.AIChatParams{ServiceCall: protocol.ServiceCall{Instance: instance, Run: run + "/chat"},
		Messages: []protocol.ChatMessage{{Role: "user", Content: "Say OK."}},
		Tools:    []protocol.ChatTool{{Name: "lookup", Parameters: json.RawMessage(`{"type": "object"}`)}}}
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
```

- [ ] **Step 3: Call it from `actionRun`**

In `internal/fakeplugin/actions.go`, change the signature and add the chat after the `in.Fail` check:

```go
func actionRun(ctx context.Context, conn *protocol.Conn, params json.RawMessage, config map[string]any, mode string) (any, error) {
```

```go
	if in.Fail {
		return nil, protocol.NewError(-32000, "the model refused", true)
	}
	if id, ok := config["connection"].(float64); ok {
		if err := chatWithTool(ctx, conn, p.Run, int64(id), mode); err != nil {
			return nil, err
		}
	}
```

- [ ] **Step 4: Wire `main.go`**

In `internal/fakeplugin/main.go`:

1. Add `| drop-tool-call` to the end of the `-mode` flag's help string.
2. Change `return actionRun(ctx, conn, params)` to `return actionRun(ctx, conn, params, config, *mode)`.
3. Replace the `case protocol.MethodAIChat:` body with:

```go
		case protocol.MethodAIChat:
			if base, ok := config["base_url"].(string); ok {
				return openAIChat(ctx, conn, base, params)
			}
			return aiChat(ctx, conn, params, func() {
				callsMu.Lock()
				calls["ai.chat.cancelled"]++
				callsMu.Unlock()
			})
```

- [ ] **Step 5: Add `FakeConsumer`**

Append to `internal/testutil/fakeplugin.go`:

```go
// FakeConsumer is an action fake that declares uses = ["ai.chat"]. Its
// actions call ai.chat on the instance its connection config names.
func FakeConsumer(t *testing.T, args ...string) string {
	t.Helper()
	dir := FakePluginWith(t, []string{"action"}, args...)
	f, err := os.OpenFile(filepath.Join(dir, "plugin.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	_, err = f.WriteString(`uses = ["ai.chat"]

[config]
type = "object"

[config.properties.connection]
type = "integer"
format = "instance"
x-offers = "ai.chat"
`)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
```

`uses` must come before `[config]`: TOML puts keys after a table header into that table.

- [ ] **Step 6: Build and run the existing tests**

Run: `go vet ./internal/fakeplugin/ ./internal/testutil/ && go test ./internal/contract/ ./internal/services/ ./internal/actions/`
Expected: `ok` for each. No behavior changes without `base_url` or `connection` in the config.

- [ ] **Step 7: Commit**

```bash
git add internal/fakeplugin internal/testutil/fakeplugin.go
git commit -m "test(fakeplugin): stream ai.chat from a model server and consume it with a tool"
```

---

### Task 3: The fake model server

**Files:**
- Create: `internal/contract/model.go`

- [ ] **Step 1: Write the server**

```go
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

// modelServer is a fake OpenAI-compatible chat API. It streams each reply in
// two chunks, calls the first tool of the first request that offers tools,
// and checks the first request that answers that call. After holdNext, the
// next request gets one chunk, then waits for its client to go away.
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
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		s.chat(w, r)
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
	callTool := s.toolName == "" && len(req.Tools) > 0
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
```

- [ ] **Step 2: Build**

Run: `go vet ./internal/contract/`
Expected: no output. Go allows unused functions, so the server builds before Task 4 uses it.

- [ ] **Step 3: Commit**

```bash
git add internal/contract/model.go
git commit -m "feat(contract): add a fake OpenAI-compatible model server"
```

---

### Task 4: The uses suite

**Files:**
- Create: `internal/contract/uses.go`
- Modify: `internal/contract/action.go`, `internal/contract/enrich.go`, `internal/contract/contract.go`
- Test: `internal/contract/contract_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/contract/contract_test.go`:

```go
// provide makes the fake an ai.chat provider that streams from the check's
// model server.
func provide(t *testing.T) {
	Provider = testutil.FakePluginWith(t, []string{"ai"})
	ProviderConfig = `{"services": ["ai.chat"], "base_url": "${INNKEEPER_FAKE_AI_URL}"}`
	t.Cleanup(func() { Provider, ProviderConfig = "", "" })
}

func TestConsumerCallsAProviderThroughTheBroker(t *testing.T) {
	provide(t)
	results, err := Check(context.Background(), testutil.FakeConsumer(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}
	for _, name := range []string{checkProviderName, checkReplyName, checkDeltasName, checkToolsName, checkCancelName} {
		if !slices.ContainsFunc(results, func(r Result) bool { return r.Name == name }) {
			t.Errorf("check %q didn't run", name)
		}
	}
}

func TestConsumerThatDropsTheToolCallFails(t *testing.T) {
	provide(t)
	results, err := Check(context.Background(), testutil.FakeConsumer(t, "-mode=drop-tool-call"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	err = failed(results)[checkToolsName]
	if err == nil || !strings.Contains(err.Error(), "without the assistant message") {
		t.Fatalf("got %v, want the missing call named", err)
	}
}

func TestUsesWithoutAProviderFails(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakeConsumer(t), quiet)
	if err != nil {
		t.Fatal(err)
	}
	err = failed(results)[checkProviderName]
	if err == nil || !strings.Contains(err.Error(), "INNKEEPER_PROVIDER_PLUGIN") {
		t.Fatalf("got %v, want INNKEEPER_PROVIDER_PLUGIN named", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/contract/ -run 'Consumer|Uses'`
Expected: build failure: `undefined: Provider`, `undefined: checkProviderName`, and so on.

- [ ] **Step 3: Extract the sample call params**

In `internal/contract/action.go`, replace the param-building part of `checkActionRuns` with a helper:

```go
// actionRunParams runs a on a sample entity with its first field and no inputs.
func actionRunParams(a protocol.Action) protocol.ActionRunParams {
	var card struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(sampleCard, &card)
	params := protocol.ActionRunParams{Run: "contract-check", Action: a.ID, Inputs: json.RawMessage(`{}`),
		Entity:  protocol.ActionEntity{Kind: protocol.ApplyCard, Data: card.Data},
		Context: protocol.ActionContext{Cards: []protocol.ContextItem{}, Lorebooks: []protocol.ContextItem{}, Skills: []string{}}}
	switch a.Applies {
	case protocol.ApplyNew:
		params.Entity.Data = nil
	case protocol.ApplyLorebook:
		params.Entity = protocol.ActionEntity{Kind: protocol.ApplyLorebook, Data: sampleLorebook}
	}
	if len(a.Fields) > 0 {
		params.Field = a.Fields[0]
	}
	return params
}
```

`checkActionRuns` becomes:

```go
func checkActionRuns(ctx context.Context, p *pluginhost.Process, list []protocol.Action) error {
	for _, a := range list {
		var res protocol.ActionRunResult
		err := call(ctx, p, protocol.MethodActionRun, actionRunParams(a), &res)
		var rpcErr *protocol.Error
		switch {
		case errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound:
			continue
		case err != nil:
			return fmt.Errorf("%s: %w", a.ID, err)
		case res.Report != nil:
			if err := res.Report.Validate(); err != nil {
				return fmt.Errorf("%s: %w", a.ID, err)
			}
		}
		var proposal map[string]json.RawMessage
		if len(res.Proposal) > 0 && (json.Unmarshal(res.Proposal, &proposal) != nil || proposal == nil) {
			return fmt.Errorf("%s: proposal is not a JSON object", a.ID)
		}
	}
	return nil
}
```

In `internal/contract/enrich.go`, extract the params the same way:

```go
// sampleRevision is the revision id enrichRunParams sends.
const sampleRevision = 1

// enrichRunParams asks for the sample card's enrichments.
func enrichRunParams() protocol.EnrichRunParams {
	var card struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(sampleCard, &card)
	return protocol.EnrichRunParams{Run: "contract-check",
		Revisions: []protocol.EnrichRevision{{CardID: 1, RevisionID: sampleRevision, Data: card.Data}}}
}
```

In `checkEnrich`, delete the `card` decoding, `const revision = 1`, and `params := ...`; call `call(ctx, p, protocol.MethodEnrichRun, enrichRunParams(), &res)`; and replace `revision` with `sampleRevision` in the two places that read it.

Run: `go test ./internal/contract/ -run 'Action|Enrich'`
Expected: `ok`.

- [ ] **Step 4: Extract `checkSecrets` in `contract.go`**

Replace the `secrets` loop in `Check` with `secrets := checkSecrets(m)` and add:

```go
// checkSecrets gives each secret the manifest declares a placeholder value.
func checkSecrets(m *protocol.Manifest) map[string]string {
	secrets := map[string]string{}
	for _, s := range m.Secrets {
		secrets[s] = "contract-check"
	}
	return secrets
}
```

- [ ] **Step 5: Write `uses.go`**

```go
package contract

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/davidgibbons/innkeeper/internal/pluginhost"
	"github.com/davidgibbons/innkeeper/internal/plugins"
	"github.com/davidgibbons/innkeeper/internal/services"
	"github.com/davidgibbons/innkeeper/protocol"
)

// Provider is the folder of the plugin that answers the services a checked
// plugin uses. A plugin with uses fails the check without one.
var Provider string

// ProviderConfig is the provider's instance config as JSON. The check
// replaces ${INNKEEPER_FAKE_AI_URL} in it with its model server's URL.
var ProviderConfig string

const fakeAIURL = "${INNKEEPER_FAKE_AI_URL}"

const (
	checkProviderName = "a provider plugin starts behind the service broker"
	checkReplyName    = "a service call reaches the provider and its reply comes back"
	checkDeltasName   = "ai.delta from the provider streams through to the consumer"
	checkToolsName    = "a tool call round-trips through the provider"
	checkCancelName   = "cancelling the consumer's call cancels the provider's request"
)

const consumerID, providerID = 1, 2

// checkUses runs the plugin and the Provider under the real service broker,
// drives the plugin's actions and enrich.run, and checks what reached the
// model server and what came back.
func checkUses(ctx context.Context, m *protocol.Manifest, params protocol.InitializeParams, log *slog.Logger) []Result {
	model := newModelServer()
	defer model.Close()
	h, err := startHarness(ctx, m, params, model.URL+"/v1", log)
	results := []Result{{checkProviderName, err}}
	if err != nil {
		return results
	}
	defer h.mgr.Close()

	var first func(context.Context) error
	for _, drive := range h.drivers(ctx) {
		before := model.requestCount()
		// Only what reached the model server matters, not the consumer's own reply.
		_ = drive(ctx)
		if first == nil && model.requestCount() > before {
			first = drive
		}
		if h.calls.err() == nil && h.deltas.Load() >= 2 && model.toolRoundTrip() == nil {
			break
		}
	}

	replyErr := h.calls.err()
	if replyErr == nil && model.requestCount() == 0 {
		replyErr = errors.New("the provider answered without a request to the model server")
	}
	var deltaErr error
	if n := h.deltas.Load(); n < 2 {
		deltaErr = fmt.Errorf("%d ai.delta reached the consumer; the model streamed 2", n)
	}
	cancelErr := errors.New("no consumer call reached the model server")
	if first != nil {
		cancelErr = checkCancel(ctx, model, first)
	}
	return append(results,
		Result{checkReplyName, replyErr},
		Result{checkDeltasName, deltaErr},
		Result{checkToolsName, model.toolRoundTrip()},
		Result{checkCancelName, cancelErr})
}

// checkCancel holds the model's next reply open, cancels the consumer's call
// once the request arrives, and wants the provider to drop the request.
func checkCancel(ctx context.Context, model *modelServer, drive func(context.Context) error) error {
	model.holdNext()
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = drive(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-model.holding:
	case <-time.After(callTimeout):
		return fmt.Errorf("the call didn't reach the model server within %s", callTimeout)
	}
	cancel()
	select {
	case <-model.ended:
		return nil
	case <-time.After(callTimeout):
		return fmt.Errorf("the model request was still open %s after the consumer's call was cancelled", callTimeout)
	}
}

// harness runs a consumer and its provider under one Manager whose core is
// the service broker.
type harness struct {
	mgr      *pluginhost.Manager
	consumer pluginhost.Instance
	calls    serviceLog
	deltas   atomic.Int64
}

func startHarness(ctx context.Context, m *protocol.Manifest, params protocol.InitializeParams, modelURL string, log *slog.Logger) (*harness, error) {
	if Provider == "" {
		return nil, fmt.Errorf("plugin %s uses %s; set INNKEEPER_PROVIDER_PLUGIN to the folder of a plugin that offers them",
			m.Name, strings.Join(m.Uses, ", "))
	}
	pm, err := protocol.LoadManifest(Provider)
	if err != nil {
		return nil, fmt.Errorf("provider: %w", err)
	}
	var providerConfig map[string]any
	raw := strings.ReplaceAll(cmp.Or(ProviderConfig, "{}"), fakeAIURL, modelURL)
	if err := json.Unmarshal([]byte(raw), &providerConfig); err != nil {
		return nil, fmt.Errorf("INNKEEPER_PROVIDER_CONFIG: %w", err)
	}
	providerParams := params
	providerParams.Config, providerParams.Secrets = providerConfig, checkSecrets(pm)
	consumerParams := params
	consumerParams.Config = withProvider(m, params.Config)

	h := &harness{consumer: pluginhost.Instance{ID: fmt.Sprint(consumerID), Version: "1", Manifest: m, Params: consumerParams}}
	reg := instances{
		consumerID: h.consumer,
		providerID: {ID: fmt.Sprint(providerID), Version: "1", Manifest: pm, Params: providerParams},
	}
	runs := pluginhost.NewRuns(log)
	broker := &services.Broker{Registry: reg, Runs: runs}
	core := broker.Handle(func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		if method != protocol.NotifyAIDelta {
			return noCore(ctx, method, raw)
		}
		res, err := runs.Handle(ctx, method, raw)
		if err == nil {
			h.deltas.Add(1)
		}
		return res, err
	})
	h.mgr = pluginhost.NewManager(h.calls.record(core), time.Minute, log)
	broker.Plugins = h.mgr
	if err := h.mgr.Start(ctx, reg[providerID]); err != nil {
		h.mgr.Close()
		return nil, fmt.Errorf("provider: %w", err)
	}
	return h, nil
}

// withProvider is config with every instance field that offers a method in
// m's uses set to the provider.
func withProvider(m *protocol.Manifest, config map[string]any) map[string]any {
	out := maps.Clone(config)
	if out == nil {
		out = map[string]any{}
	}
	props, _ := m.Config["properties"].(map[string]any)
	for key, v := range props {
		prop, _ := v.(map[string]any)
		offers, _ := prop["x-offers"].(string)
		if prop["format"] == "instance" && slices.Contains(m.Uses, offers) {
			out[key] = providerID
		}
	}
	return out
}

// drivers are the consumer calls that may use a service: each action on
// the sample entity, then enrich.run on the sample card.
func (h *harness) drivers(ctx context.Context) []func(context.Context) error {
	var out []func(context.Context) error
	if h.consumer.Manifest.Has("action") {
		var list protocol.ActionListResult
		if err := h.call(ctx, protocol.MethodActionList, nil, &list); err == nil {
			for _, a := range list.Actions {
				params := actionRunParams(a)
				out = append(out, func(ctx context.Context) error {
					return h.call(ctx, protocol.MethodActionRun, params, nil)
				})
			}
		}
	}
	if h.consumer.Manifest.Has("enrich") {
		out = append(out, func(ctx context.Context) error {
			return h.call(ctx, protocol.MethodEnrichRun, enrichRunParams(), nil)
		})
	}
	return out
}

func (h *harness) call(ctx context.Context, method string, params, result any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	return h.mgr.Call(ctx, h.consumer, method, params, result)
}

// serviceLog records how the broker answered the consumer's service calls.
type serviceLog struct {
	mu      sync.Mutex
	replied bool
	last    error
}

func (l *serviceLog) record(next protocol.Handler) protocol.Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		res, err := next(ctx, method, params)
		if _, ok := protocol.Services[method]; ok {
			l.mu.Lock()
			if err == nil {
				l.replied = true
			} else {
				l.last = err
			}
			l.mu.Unlock()
		}
		return res, err
	}
}

// err is nil once a service call got a reply.
func (l *serviceLog) err() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case l.replied:
		return nil
	case l.last != nil:
		return fmt.Errorf("the broker answered the consumer with an error: %w", l.last)
	}
	return errors.New("the consumer made no service call from its actions or enrich.run")
}

// instances is the broker's registry for a check: the consumer and its
// provider.
type instances map[int64]pluginhost.Instance

func (r instances) Instance(_ context.Context, id int64) (pluginhost.Instance, error) {
	inst, ok := r[id]
	if !ok {
		return pluginhost.Instance{}, plugins.ErrNotFound
	}
	return inst, nil
}

func (r instances) InstanceWith(ctx context.Context, id int64, capability string) (pluginhost.Instance, error) {
	inst, err := r.Instance(ctx, id)
	if err == nil && !inst.Manifest.Has(capability) {
		err = fmt.Errorf("%w: plugin %s can't %s", plugins.ErrIncapable, inst.Manifest.Name, capability)
	}
	return inst, err
}

func (r instances) Services(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance) ([]string, error) {
	info, err := mgr.Info(ctx, inst)
	return info.Services, err
}

func (r instances) Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
	return mgr.Call(ctx, inst, method, params, result)
}
```

- [ ] **Step 6: Run the suite from `Check`**

In `internal/contract/contract.go`, after the `for _, suite := range suites` loop and before the shutdown call:

```go
	if len(m.Uses) > 0 {
		results = append(results, checkUses(ctx, m, params, log)...)
	}
```

- [ ] **Step 7: Run the new tests**

Run: `go test -race ./internal/contract/ -run 'Consumer|Uses' -v`
Expected: all three PASS.

If `TestConsumerCallsAProviderThroughTheBroker` fails on the cancel check, check that the fake consumer passes its handler ctx to `conn.Call` (it does through `chatWithTool`) and that the core's `Call` sends `cancel` on ctx end (`protocol/jsonrpc.go:199`).

- [ ] **Step 8: Run the whole package**

Run: `go test -race ./internal/contract/ ./internal/services/`
Expected: `ok` for both. Existing check counts don't change: no fixture but `FakeConsumer` declares `uses`.

- [ ] **Step 9: Commit**

```bash
git add internal/contract
git commit -m "feat(contract): check a consumer and its provider through the real broker"
```

---

### Task 5: CLI and docs

**Files:**
- Modify: `cmd/innkeeper/main.go` (in `pluginCheck`)
- Modify: `docs/protocol.md:1055-1079`

- [ ] **Step 1: Read the variables**

In `pluginCheck`, after `contract.DatabaseURL = ...`:

```go
	contract.Provider = os.Getenv("INNKEEPER_PROVIDER_PLUGIN")
	contract.ProviderConfig = os.Getenv("INNKEEPER_PROVIDER_CONFIG")
```

- [ ] **Step 2: Document the checks**

Invoke the `documentation-writing` skill first. In `docs/protocol.md`, add a bullet after the `ai` plugin bullet (line 1055):

```markdown
- a plugin that declares `uses` runs with the provider plugin in
  `INNKEEPER_PROVIDER_PLUGIN` behind the core's service broker, and the
  provider talks to a fake OpenAI-compatible server. Running its actions and
  `enrich.run`, a service call gets the provider's reply, the reply's two
  streamed pieces reach the plugin as `ai.delta`, a tool call the model makes
  comes back with the same id, name, and arguments and a `tool` message
  answering it, and cancelling the plugin's call ends the provider's request
  within 5 seconds. The plugin's config fields with `format = "instance"`
  point at the provider. A tool call's `extra` isn't checked: the OpenAI
  format has no field for it
```

After the `INNKEEPER_PLUGIN_CONFIG` paragraph (line 1079), add:

```markdown
For a plugin that declares `uses`, set `INNKEEPER_PROVIDER_PLUGIN` to the
provider's plugin folder and `INNKEEPER_PROVIDER_CONFIG` to its instance
config. The check replaces `${INNKEEPER_FAKE_AI_URL}` in that config with its
fake server's URL, such as
`{"provider": "openai-compatible", "base_url": "${INNKEEPER_FAKE_AI_URL}", "model": "contract"}`.
```

- [ ] **Step 3: Run what CI runs**

```bash
go vet ./...
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass, and the DB tests run rather than skip.

- [ ] **Step 4: Commit**

```bash
git add cmd/innkeeper/main.go docs/protocol.md
git commit -m "feat(contract): take the provider plugin for uses from the environment"
```

---

### Task 6: Check forge-ai with the ai plugin (innkeeper-plugins)

Do this in a worktree of `~/git/innkeeper-plugins` off `origin/main`, after the core branch is merged and pushed.

**Files:**
- Create: `ai/check-provider.json`
- Modify: `Makefile` (`check` target), `go.mod`, `go.sum`

- [ ] **Step 1: Pin the core with the check**

Run: `go get github.com/davidgibbons/innkeeper@main && go mod tidy`
Expected: `go.mod` names the new core pseudo-version.

- [ ] **Step 2: Add the provider config**

`ai/check-provider.json`:

```json
{"provider": "openai-compatible", "base_url": "${INNKEEPER_FAKE_AI_URL}", "model": "contract", "kinds": ["chat"]}
```

- [ ] **Step 3: Set the provider in `make check`**

Change the `check` target's loop line to export the two variables:

```make
check: build
	for p in $(PLUGINS); do cfg=; [ -z "$$INNKEEPER_PLUGIN_DATABASE_URL" ] || cfg="$$(cat $$p/check-config.json 2>/dev/null)"; INNKEEPER_PLUGIN_CONFIG="$$cfg" INNKEEPER_PROVIDER_PLUGIN=ai INNKEEPER_PROVIDER_CONFIG="$$(cat ai/check-provider.json)" go tool innkeeper plugin check $$p || exit 1; done
```

And add a line to the comment above it: `A plugin that declares uses is checked against the ai plugin, which talks to the kit's fake model server.`

- [ ] **Step 4: Run it**

Run: `INNKEEPER_PLUGIN_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres make check`
Expected: forge-ai's output includes `PASS` for all five uses checks:
`a provider plugin starts behind the service broker`,
`a service call reaches the provider and its reply comes back`,
`ai.delta from the provider streams through to the consumer`,
`a tool call round-trips through the provider`,
`cancelling the consumer's call cancels the provider's request`.

forge-ai offers tools in its library actions, so the tool check passes once one of those runs. If forge-ai has no `check-config.json`, the `connection` field still gets filled from the manifest.

- [ ] **Step 5: Commit**

```bash
git add ai/check-provider.json Makefile go.mod go.sum
git commit -m "test(forge-ai): check it against the ai plugin through the core's broker"
```
