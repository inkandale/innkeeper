# AI Services Part 1: The `ai` Capability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** One plugin instance can call another's model services (`ai.chat`, `ai.embed`) through the core, which checks that the caller declared the call and the provider offers it, forwards the call and its stream, and passes cancellation through.

**Architecture:** Section 1 of `docs/superpowers/specs/2026-10-15-ai-services-design.md`.

The pieces:
- **Protocol types** (`protocol/ai.go`): the methods, the request and reply types, the `Services` map (service method → capability), and `CodeAIRejected`.
- **Manifest:** the `ai` capability and `uses`.
- **Initialize:** `InitializeResult.Services` lists what an instance offers, and `checkInfo` refuses a service outside the plugin's capabilities.
- **`protocol.Peer(ctx)`:** gives a core handler the connection a call came in on, so it can notify the caller.
- **Registry and pluginhost:** `Registry.Services` and `Registry.InstancesOffering` answer "which instances offer `ai.chat`" by starting each `ai` instance and reading its initialize reply. Part 2's instance picker uses `InstancesOffering`.
- **Broker** (`internal/services`): a core handler that wraps `Runs.Handle` on the worker's Manager.
  - It reads only `instance` and `run` from a service call and never parses prompts or models.
  - It forwards the rest unchanged to the provider and relays the provider's `ai.delta` notifications to the caller under the caller's own token.
- **Fake plugin:** an `ai` provider and a `relay` method that acts as a consumer.
- **Contract check:** an `ai` plugin answers each service it offers.

**Tech Stack:** Go, pgx, PostgreSQL (tests), JSON-RPC over stdio.

**Tests:** DB tests skip without the database URL. Run Go tests with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`. Below, `$DB` means that assignment.

**Deviations from the spec:**
- **Services are named by their method everywhere,** so the same names appear in initialize's `services`, in `uses`, and in part 2's `x-offers`. An instance offers `["ai.chat", "ai.embed"]`, not `["chat", "embed"]`.
- **`ai.image` gets no constant.** It's reserved by leaving it out of `Services` until a provider supports it.
- **Only the worker's Manager gets the broker.** Consumers call services while they answer `action.run` or `enrich.run`, which both run on the worker. The API's Manager stays `api.NoCore`, so a plugin there gets `-32601` for `ai.*`.
- **The provider instance id is an integer** (`ServiceCall.Instance`, `int64`), so part 2's picker stores an integer, not a string.
- **Tool-call assembly stays in the provider.** `ai.chat` returns whole tool calls, and `ai.delta` carries text only. The consumer runs the tool loop.

---

## File map

| File | Change |
|---|---|
| `docs/protocol.md` | `ai` section, `uses` row, initialize `services`, contract bullet |
| `protocol/ai.go`, `protocol/ai_test.go` | New: methods, types, `Services`, `CodeAIRejected` |
| `protocol/manifest.go`, `protocol/manifest_test.go` | `ai` capability, `Uses` and its validation |
| `protocol/messages.go` | `InitializeResult.Services` |
| `protocol/jsonrpc.go`, `protocol/jsonrpc_test.go` | `Peer(ctx)` |
| `internal/pluginhost/process.go`, `process_test.go` | `checkInfo` checks services |
| `internal/pluginhost/runs.go`, `runs_test.go` | `callerInstance` → exported `CallerInstance` |
| `internal/pluginhost/manager.go`, `manager_test.go` | `Manager.Info` |
| `internal/fakeplugin/ai.go`, `internal/fakeplugin/main.go` | Fake provider, `relay`, delta log |
| `internal/plugins/plugins.go`, `internal/plugins/services_test.go` | `Services`, `InstancesOffering`, shared start-failure marking |
| `internal/services/services.go`, `services_test.go` | New: `Broker` |
| `cmd/innkeeper/worker.go` | Wire the broker |
| `internal/contract/ai.go`, `contract.go`, `contract_test.go` | `ai` suite |

---

### Task 1: Protocol documentation

The protocol doc is the contract parts 3 and 4 build against, so it lands first.

**Files:**
- Modify: `docs/protocol.md`

- [ ] **Step 1: Add `ai` to the capabilities row and a `uses` row**

In the Manifest table, change the `capabilities` row's list to include `ai`:

```markdown
| `capabilities` | yes | Any of `import`, `search`, `push`, `pull`, `action`, `records`, `enrich`, `codec`, `auth`, `mcp`, `catalog`, `sites`, `ai`, or `theme`. A theme lists no other capability |
```

Add this row after `database`:

```markdown
| `uses` | no | Service methods the plugin calls on other instances, each listed once, such as `["ai.chat"]`; see [`ai`](#ai) |
```

- [ ] **Step 2: Document `services` in initialize**

In Lifecycle step 1, replace "Reply with `protocol`, `name`, `version`, and `capabilities`. These must match the manifest, or the core stops the plugin." with:

```markdown
   Reply with `protocol`, `name`, `version`, and `capabilities`. These must
   match the manifest, or the core stops the plugin. An `ai` plugin also
   replies with `services`: the service methods this instance offers, such
   as `["ai.chat"]`. The core stops a plugin that lists a service outside its
   capabilities.
```

- [ ] **Step 3: Add the `ai` section before `## Checking a plugin`**

````markdown
### `ai`

An `ai` plugin offers model services to other plugins. One instance is one
connection to a model. The owner picks which instance a consumer uses, and
the core forwards the consumer's calls to it.

| Method | Does |
|---|---|
| `ai.chat` | One model turn: messages and optional tools in, text and tool calls out |
| `ai.embed` | One vector per text |

**Offering.** List the methods the instance offers in initialize's
`services`, from its config. Don't contact the model during `initialize`.

**Calling.** Declare the methods in `uses`, then send them to the core with
`instance`, the provider instance's ID from your config. The core replies
`-32003` when your manifest doesn't declare the method, or when the instance
doesn't offer it, and forwards everything else to the provider without
reading it. The provider gets the same params without `instance`.

**`ai.chat`**

| Param | Meaning |
|---|---|
| `instance` | The provider instance; the core removes it before forwarding |
| `run` | Optional token. When set, the provider streams `ai.delta` notifications and the core relays each to the caller with this token |
| `messages` | `[{"role", "content", "parts", "tool_calls", "tool_call_id"}]`. `role` is `system`, `user`, `assistant`, or `tool`. `parts` replaces `content` for a message with images: `[{"type": "text", "text"}, {"type": "image_url", "image_url"}]`, where `image_url` may be a `data:` URL |
| `tools` | `[{"name", "description", "parameters"}]`, `parameters` a JSON Schema. Optional |
| `temperature`, `max_tokens` | Optional; the provider's defaults otherwise |

Reply with `text` and, when the model called tools, `tool_calls`:
`[{"id", "name", "arguments"}]`, `arguments` a JSON string. Answer each tool
call with a `tool` message carrying its `tool_call_id`, then call `ai.chat`
again.

While it streams, the provider sends `ai.delta` with `run` and `text`, the
next piece of the reply, in order. The caller gets every delta before the
reply.

A provider that refuses the request as invalid, such as tools for a model
without tool support, replies `-32010`. Rate limits and timeouts are
retryable errors.

**`ai.embed`** takes `instance` and `texts`, and replies with `vectors`, one
array of numbers per text, in order.

When the caller cancels a call, the core cancels the provider's.

```json
{"jsonrpc": "2.0", "id": "c4", "method": "ai.chat", "params": {"instance": 9, "run": "mine-1", "messages": [{"role": "user", "content": "Say hello."}]}}
{"jsonrpc": "2.0", "method": "ai.delta", "params": {"run": "mine-1", "text": "Hello"}}
{"jsonrpc": "2.0", "id": "c4", "result": {"text": "Hello"}}
```
````

- [ ] **Step 4: Add the contract bullet**

In "Checking a plugin", after the `enrich` bullet, add:

```markdown
- an `ai` plugin's `ai.chat` and `ai.embed`, for each it lists in `services`,
  return a reply or an error other than `-32601` within 5 seconds. Checked
  without a connection, a provider can only fail, which counts as an answer
```

- [ ] **Step 5: Commit**

```bash
git add docs/protocol.md
git commit -m "docs(protocol): describe the ai capability and uses"
```

---

### Task 2: Protocol types and the manifest

**Files:**
- Create: `protocol/ai.go`, `protocol/ai_test.go`
- Modify: `protocol/manifest.go`, `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing tests**

`protocol/ai_test.go`:

```go
package protocol

import (
	"encoding/json"
	"testing"
)

func TestServiceCallHeaderReadsFromAChatRequest(t *testing.T) {
	raw, err := json.Marshal(AIChatParams{ServiceCall: ServiceCall{Instance: 9, Run: "mine-1"},
		Messages: []ChatMessage{{Role: "user", Content: "Hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var head ServiceCall
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatal(err)
	}
	if head != (ServiceCall{Instance: 9, Run: "mine-1"}) {
		t.Fatalf("header = %+v from %s", head, raw)
	}
}
```

Append to the `tests` table in `TestManifestErrors` (`protocol/manifest_test.go`):

```go
		{"unknown use", `secrets = ["api_key"]`, `secrets = ["api_key"]
uses = ["ai.draw"]`, `uses "ai.draw"`},
		{"duplicate use", `secrets = ["api_key"]`, `secrets = ["api_key"]
uses = ["ai.chat", "ai.chat"]`, `uses "ai.chat" listed twice`},
```

And add:

```go
func TestManifestUses(t *testing.T) {
	body := strings.Replace(validManifest, `secrets = ["api_key"]`, `secrets = ["api_key"]
uses = ["ai.chat", "ai.embed"]`, 1)
	m, err := LoadManifest(writeManifest(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Uses, ",") != "ai.chat,ai.embed" {
		t.Fatalf("Uses = %v", m.Uses)
	}
	ai := &Manifest{Name: "a", Version: "1", Protocol: Version, Command: []string{"a"}, Capabilities: []string{"ai"}}
	if err := ai.Validate(); err != nil {
		t.Fatalf("ai capability: %v", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./protocol/ -run 'TestServiceCall|TestManifestErrors|TestManifestUses'`
Expected: FAIL to compile, with `undefined: AIChatParams` and `m.Uses undefined`.

- [ ] **Step 3: Write `protocol/ai.go`**

```go
package protocol

import "encoding/json"

// The ai capability offers model services to other plugins. A consumer
// declares the methods it calls in its manifest's uses and sends them to the
// core, which forwards each to the provider instance it names.
const (
	MethodAIChat  = "ai.chat"
	MethodAIEmbed = "ai.embed"
	// NotifyAIDelta is the next piece of an ai.chat reply's text.
	NotifyAIDelta = "ai.delta"
)

// Services maps each service method to the capability that offers it.
var Services = map[string]string{MethodAIChat: "ai", MethodAIEmbed: "ai"}

// CodeAIRejected is a provider refusing a request as invalid, such as tools
// for a model without tool support.
const CodeAIRejected = -32010

// ServiceCall is the part of every service call the core reads: the
// provider instance, and the caller's token for the call's notifications.
type ServiceCall struct {
	Instance int64  `json:"instance,omitempty"`
	Run      string `json:"run,omitempty"`
}

// ChatMessage is one message of an ai.chat conversation. Parts replace
// Content for a message with images.
type ChatMessage struct {
	Role       string        `json:"role" enum:"system,user,assistant,tool"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

// ContentPart is text or an image; ImageURL may be a data: URL.
type ContentPart struct {
	Type     string `json:"type" enum:"text,image_url"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// ChatTool is a function the model may call. Parameters is a JSON Schema.
type ChatTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolCall is one call the model made. Arguments is a JSON string.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// AIChatParams is one model turn.
type AIChatParams struct {
	ServiceCall
	Messages    []ChatMessage `json:"messages"`
	Tools       []ChatTool    `json:"tools,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
}

// AIChatResult is the model's reply.
type AIChatResult struct {
	Text      string     `json:"text"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	// Model is the model that answered, for the consumer's records.
	Model string `json:"model,omitempty"`
}

// AIDelta streams ai.chat's text under the call's Run token.
type AIDelta struct {
	Run  string `json:"run"`
	Text string `json:"text"`
}

// AIEmbedParams asks for one vector per text.
type AIEmbedParams struct {
	ServiceCall
	Texts []string `json:"texts"`
}

// AIEmbedResult holds the vectors in the order of the texts.
type AIEmbedResult struct {
	Vectors [][]float32 `json:"vectors"`
}
```

- [ ] **Step 4: Add `ai` and `Uses` to the manifest**

In `protocol/manifest.go`, append `"ai"` to `Capabilities`:

```go
var Capabilities = []string{"import", "search", "push", "pull", "action", "records", "enrich", "codec", "auth", "mcp", "theme", "catalog", "sites", "ai"}
```

Add to `Manifest`, after `Database`:

```go
	// Uses are the service methods the plugin calls on other instances.
	Uses []string `toml:"uses"`
```

In `Validate`, before `return errors.Join(errs...)`:

```go
	seenUses := map[string]bool{}
	for _, u := range m.Uses {
		if _, ok := Services[u]; !ok {
			errs = append(errs, fmt.Errorf("uses %q must be one of %v", u, slices.Sorted(maps.Keys(Services))))
		}
		if seenUses[u] {
			errs = append(errs, fmt.Errorf("uses %q listed twice", u))
		}
		seenUses[u] = true
	}
```

- [ ] **Step 5: Run the tests**

Run: `go test ./protocol/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add protocol/ai.go protocol/ai_test.go protocol/manifest.go protocol/manifest_test.go
git commit -m "feat(protocol): add the ai capability and uses"
```

---

### Task 3: Initialize reports services

**Files:**
- Modify: `protocol/messages.go`, `internal/pluginhost/process.go`, `internal/pluginhost/process_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/pluginhost/process_test.go`:

```go
func TestCheckInfoRefusesServicesOutsideCapabilities(t *testing.T) {
	info := func(caps []string, services ...string) protocol.InitializeResult {
		return protocol.InitializeResult{Protocol: protocol.Version, Name: "p", Version: "1", Capabilities: caps, Services: services}
	}
	manifest := func(caps ...string) *protocol.Manifest {
		return &protocol.Manifest{Name: "p", Version: "1", Capabilities: caps}
	}
	if err := checkInfo(manifest("ai"), info([]string{"ai"}, protocol.MethodAIChat)); err != nil {
		t.Fatalf("ai plugin offering ai.chat: %v", err)
	}
	if err := checkInfo(manifest("ai"), info([]string{"ai"}, "ai.draw")); err == nil {
		t.Error("an unknown service passed")
	}
	if err := checkInfo(manifest("action"), info([]string{"action"}, protocol.MethodAIChat)); err == nil {
		t.Error("ai.chat from a plugin without ai passed")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/pluginhost/ -run TestCheckInfoRefusesServices`
Expected: FAIL to compile, with `unknown field Services`.

- [ ] **Step 3: Add the field and the check**

In `protocol/messages.go`, add to `InitializeResult`:

```go
	// Services are the service methods this instance offers, for ai.
	Services []string `json:"services,omitempty"`
```

In `internal/pluginhost/process.go`, at the end of `checkInfo` before `return nil`:

```go
	for _, s := range info.Services {
		if c, ok := protocol.Services[s]; !ok || !m.Has(c) {
			return fmt.Errorf("plugin %s offers %q, which isn't a service of its capabilities", m.Name, s)
		}
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/pluginhost/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add protocol/messages.go internal/pluginhost/process.go internal/pluginhost/process_test.go
git commit -m "feat(pluginhost): check the services a plugin offers"
```

---

### Task 4: `protocol.Peer`

**Files:**
- Modify: `protocol/jsonrpc.go`, `protocol/jsonrpc_test.go`

- [ ] **Step 1: Write the failing test**

Append to `protocol/jsonrpc_test.go`:

```go
func TestHandlerCanNotifyItsPeer(t *testing.T) {
	got := make(chan string, 1)
	ha := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		var s string
		_ = json.Unmarshal(params, &s)
		got <- method + ":" + s
		return nil, nil
	}
	hb := func(ctx context.Context, method string, _ json.RawMessage) (any, error) {
		return nil, Peer(ctx).Notify("note", "hi")
	}
	a, _, _ := pair(t, ha, hb)
	if err := a.Call(context.Background(), "hello", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s != "note:hi" {
			t.Fatalf("peer got %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the notification never arrived")
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./protocol/ -run TestHandlerCanNotifyItsPeer`
Expected: FAIL to compile, with `undefined: Peer`.

- [ ] **Step 3: Put the Conn in the handler context**

In `protocol/jsonrpc.go`, in `Run`, right after `ctx, cancel := context.WithCancel(ctx)`:

```go
	ctx = context.WithValue(ctx, peerKey{}, c)
```

Add after `Run`:

```go
type peerKey struct{}

// Peer is the connection the handler's call came in on, so a handler can
// notify the caller; nil outside a handler.
func Peer(ctx context.Context) *Conn {
	c, _ := ctx.Value(peerKey{}).(*Conn)
	return c
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./protocol/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add protocol/jsonrpc.go protocol/jsonrpc_test.go
git commit -m "feat(protocol): let a handler reach the connection it serves"
```

---

### Task 5: `Manager.Info` and `CallerInstance`

**Files:**
- Modify: `internal/pluginhost/manager.go`, `manager_test.go`, `runs.go`, `runs_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/pluginhost/manager_test.go`:

```go
func TestManagerInfoStartsThePlugin(t *testing.T) {
	mgr, inst := newManager(t, time.Minute)
	info, err := mgr.Info(context.Background(), inst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "fake" || mgr.proc(inst.key()) == nil {
		t.Fatalf("Info = %+v, running %v", info, mgr.proc(inst.key()) != nil)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/pluginhost/ -run TestManagerInfo`
Expected: FAIL to compile, with `mgr.Info undefined`.

- [ ] **Step 3: Add `Info` and export `CallerInstance`**

In `internal/pluginhost/manager.go`, after `Start`:

```go
// Info is the instance's initialize reply, starting its plugin if needed.
func (m *Manager) Info(ctx context.Context, inst Instance) (protocol.InitializeResult, error) {
	e, err := m.acquire(ctx, inst)
	if err != nil {
		return protocol.InitializeResult{}, err
	}
	defer m.release(e)
	return e.proc.Info, nil
}
```

In `internal/pluginhost/runs.go`, rename `callerInstance` to `CallerInstance` in its definition and both uses in `Handle`. Its doc comment becomes:

```go
// CallerInstance is the instance whose plugin made the call, or "".
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/pluginhost/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/pluginhost/
git commit -m "feat(pluginhost): read an instance's initialize reply"
```

---

### Task 6: The fake provider and consumer

**Files:**
- Create: `internal/fakeplugin/ai.go`
- Modify: `internal/fakeplugin/main.go`

The fake gets tested through Tasks 7 and 8.

- [ ] **Step 1: Write `internal/fakeplugin/ai.go`**

```go
package main

import (
	"context"
	"encoding/json"
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
	return protocol.AIChatResult{Text: "echo: " + text}, nil
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
```

- [ ] **Step 2: Wire it into the handler**

In `internal/fakeplugin/main.go`, next to `calls`:

```go
	var deltas deltaLog
```

In the `protocol.MethodInitialize` case, after `res := protocol.InitializeResult{...}`:

```go
			res.Services = aiServices(init.Config)
```

Add these cases to the method `switch`, before `case "freeze":`:

```go
		case protocol.MethodAIChat:
			return aiChat(ctx, conn, params, func() {
				callsMu.Lock()
				calls["ai.chat.cancelled"]++
				callsMu.Unlock()
			})
		case protocol.NotifyAIDelta:
			deltas.add(params)
			return nil, nil
		case "relay":
			return relay(ctx, conn, params, &deltas)
```

- [ ] **Step 3: Build**

Run: `go build ./internal/fakeplugin/ && go vet ./internal/fakeplugin/`
Expected: no output.

- [ ] **Step 4: Commit**

```bash
git add internal/fakeplugin/
git commit -m "test(fakeplugin): offer ai.chat and relay service calls"
```

---

### Task 7: Which instances offer a service

**Files:**
- Modify: `internal/plugins/plugins.go`
- Create: `internal/plugins/services_test.go`

- [ ] **Step 1: Write the failing test**

`internal/plugins/services_test.go`:

```go
package plugins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

func TestInstancesOfferingListsOnlyInstancesThatOfferTheService(t *testing.T) {
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := New(testdb.New(t), map[string]*protocol.Manifest{"fake": m},
		&blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	mgr := pluginhost.NewManager(nil, time.Minute, quiet)
	t.Cleanup(mgr.Close)
	create := func(name string, services ...string) int64 {
		id, err := reg.CreateInstance(ctx, NewInstance{Plugin: "fake", Name: name,
			Config: map[string]any{"services": services}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	chat := create("Chat", protocol.MethodAIChat)
	create("Embed", protocol.MethodAIEmbed)

	got, err := reg.InstancesOffering(ctx, mgr, protocol.MethodAIChat)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != chat {
		t.Fatalf("instances offering ai.chat = %+v, want only %d", got, chat)
	}
	if _, err := reg.InstancesOffering(ctx, mgr, "ai.draw"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("an unknown service: %v, want ErrInvalid", err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `$DB go test ./internal/plugins/ -run TestInstancesOffering`
Expected: FAIL to compile, with `reg.InstancesOffering undefined`.

- [ ] **Step 3: Share start-failure marking, then add the lookups**

In `internal/plugins/plugins.go`, replace `Call` with:

```go
// Call sends method to inst's plugin through mgr. A plugin that fails to
// start marks the instance broken.
func (r *Registry) Call(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance, method string, params, result any) error {
	return r.markStartFailure(ctx, inst, mgr.Call(ctx, inst, method, params, result))
}

// markStartFailure marks inst broken when err is a failed start, and
// returns err.
func (r *Registry) markStartFailure(ctx context.Context, inst pluginhost.Instance, err error) error {
	var startErr *pluginhost.StartError
	if errors.As(err, &startErr) && ctx.Err() == nil {
		id, perr := strconv.ParseInt(inst.ID, 10, 64)
		if perr == nil {
			perr = r.MarkBroken(context.WithoutCancel(ctx), id, inst.Version, err)
		}
		return errors.Join(err, perr)
	}
	return err
}

// Services lists the service methods inst offers, starting its plugin if
// needed.
func (r *Registry) Services(ctx context.Context, mgr *pluginhost.Manager, inst pluginhost.Instance) ([]string, error) {
	info, err := mgr.Info(ctx, inst)
	return info.Services, r.markStartFailure(ctx, inst, err)
}

// InstancesOffering lists the working instances that offer the service
// method, oldest first, starting their plugins to ask. One that fails to
// start is marked broken and left out.
func (r *Registry) InstancesOffering(ctx context.Context, mgr *pluginhost.Manager, method string) ([]InstanceInfo, error) {
	capability, ok := protocol.Services[method]
	if !ok {
		return nil, fmt.Errorf("%w: %q is not a service", ErrInvalid, method)
	}
	list, err := r.InstancesWith(ctx, capability)
	if err != nil {
		return nil, err
	}
	out := []InstanceInfo{}
	for _, info := range list {
		if info.BrokenError != nil {
			continue
		}
		inst, err := r.Instance(ctx, info.ID)
		if errors.Is(err, ErrBroken) {
			continue
		}
		if err != nil {
			return nil, err
		}
		services, err := r.Services(ctx, mgr, inst)
		var startErr *pluginhost.StartError
		if errors.As(err, &startErr) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if slices.Contains(services, method) {
			out = append(out, info)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run the package tests**

Run: `$DB go test ./internal/plugins/`
Expected: PASS, including `TestCallMarksBrokenOnStartFailure`.

- [ ] **Step 5: Commit**

```bash
git add internal/plugins/plugins.go internal/plugins/services_test.go
git commit -m "feat(plugins): list the instances that offer a service"
```

---

### Task 8: The broker

**Files:**
- Create: `internal/services/services.go`, `internal/services/services_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/services/services_test.go`:

```go
package services

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/internal/secrets"
	"github.com/inkandale/innkeeper/internal/testdb"
	"github.com/inkandale/innkeeper/internal/testutil"
	"github.com/inkandale/innkeeper/protocol"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fixture struct {
	mgr                *pluginhost.Manager
	chat, embed        pluginhost.Instance
	consumer, stranger pluginhost.Instance
	chatID, embedID    int64
}

// as is the fake's manifest under another name, with one capability and uses.
func as(m *protocol.Manifest, name, capability string, uses ...string) *protocol.Manifest {
	c := *m
	c.Name, c.Capabilities, c.Uses = name, []string{capability}, uses
	c.Command = []string{"./fakeplugin", "-caps=" + capability, "-name=" + name}
	return &c
}

func setup(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	m, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"ai"}))
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	reg := plugins.New(testdb.New(t), map[string]*protocol.Manifest{
		"fake":     m,
		"consumer": as(m, "consumer", "action", protocol.MethodAIChat),
		"stranger": as(m, "stranger", "action"),
	}, &blobstore.Store{Name: "library", Root: t.TempDir()}, box)
	runs := pluginhost.NewRuns(quiet)
	b := &Broker{Registry: reg, Runs: runs}
	mgr := pluginhost.NewManager(b.Handle(runs.Handle), time.Minute, quiet)
	b.Plugins = mgr
	t.Cleanup(mgr.Close)
	create := func(plugin, name string, config map[string]any) (int64, pluginhost.Instance) {
		id, err := reg.CreateInstance(ctx, plugins.NewInstance{Plugin: plugin, Name: name, Config: config})
		if err != nil {
			t.Fatal(err)
		}
		inst, err := reg.Instance(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return id, inst
	}
	f := fixture{mgr: mgr}
	f.chatID, f.chat = create("fake", "Chat", map[string]any{"services": []string{protocol.MethodAIChat}})
	f.embedID, f.embed = create("fake", "Embed", map[string]any{"services": []string{protocol.MethodAIEmbed}})
	_, f.consumer = create("consumer", "Consumer", nil)
	_, f.stranger = create("stranger", "Stranger", nil)
	return f
}

type relayed struct {
	Result protocol.AIChatResult `json:"result"`
	Deltas []string              `json:"deltas"`
}

// relayChat has from's plugin send ai.chat for text to the instance to.
func (f fixture) relayChat(ctx context.Context, from pluginhost.Instance, to int64, text string) (relayed, error) {
	params := protocol.AIChatParams{ServiceCall: protocol.ServiceCall{Instance: to, Run: "consumer-run"},
		Messages: []protocol.ChatMessage{{Role: "user", Content: text}}}
	var out relayed
	err := f.mgr.Call(ctx, from, "relay", map[string]any{"method": protocol.MethodAIChat, "params": params}, &out)
	return out, err
}

func wantForbidden(t *testing.T, err error) {
	t.Helper()
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeForbidden {
		t.Fatalf("got %v, want -32003", err)
	}
}

func TestChatReachesTheProviderAndStreamsBack(t *testing.T) {
	f := setup(t)
	got, err := f.relayChat(context.Background(), f.consumer, f.chatID, "hello there")
	if err != nil {
		t.Fatal(err)
	}
	if got.Result.Text != "echo: hello there" || !slices.Equal(got.Deltas, []string{"hello ", "there "}) {
		t.Fatalf("relayed %+v", got)
	}
}

func TestCallerWithoutUsesIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.relayChat(context.Background(), f.stranger, f.chatID, "hello")
	wantForbidden(t, err)
}

func TestProviderThatDoesntOfferTheServiceIsRefused(t *testing.T) {
	f := setup(t)
	_, err := f.relayChat(context.Background(), f.consumer, f.embedID, "hello")
	wantForbidden(t, err)
}

func TestCancellingTheCallerCancelsTheProvider(t *testing.T) {
	f := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.relayChat(ctx, f.consumer, f.chatID, "hang")
		done <- err
	}()
	count := func(method string) int {
		var n int
		if err := f.mgr.Call(context.Background(), f.chat, "calls", method, &n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	eventually(t, func() bool { return count(protocol.MethodAIChat) == 1 })
	cancel()
	<-done
	eventually(t, func() bool { return count("ai.chat.cancelled") == 1 })
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition never held")
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `$DB go test ./internal/services/`
Expected: FAIL to compile, with `undefined: Broker`.

- [ ] **Step 3: Write `internal/services/services.go`**

```go
// Package services routes the service calls one plugin instance makes to
// another, such as ai.chat. It checks that the caller declared the method
// and the provider offers it, forwards the call, and relays its stream.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/internal/plugins"
	"github.com/inkandale/innkeeper/protocol"
)

// Broker answers service calls for the plugins on one Manager.
type Broker struct {
	Registry *plugins.Registry
	// Plugins runs the providers; its core handler must be Handle's.
	Plugins *pluginhost.Manager
	// Runs carries the providers' notifications back to the call.
	Runs *pluginhost.Runs
}

// Handle answers service calls and sends every other call to next.
func (b *Broker) Handle(next protocol.Handler) protocol.Handler {
	return func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		if _, ok := protocol.Services[method]; !ok {
			return next(ctx, method, params)
		}
		return b.call(ctx, method, params)
	}
}

func (b *Broker) call(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var head protocol.ServiceCall
	var fwd map[string]json.RawMessage
	if err := json.Unmarshal(params, &head); err != nil || json.Unmarshal(params, &fwd) != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "a service call's params must be an object", false)
	}
	callerID, err := strconv.ParseInt(pluginhost.CallerInstance(ctx), 10, 64)
	if err != nil {
		return nil, forbidden("only a plugin instance can call a service")
	}
	caller, err := b.Registry.Instance(ctx, callerID)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(caller.Manifest.Uses, method) {
		return nil, forbidden(fmt.Sprintf("plugin %s doesn't declare %s in uses", caller.Manifest.Name, method))
	}
	provider, err := b.Registry.InstanceWith(ctx, head.Instance, protocol.Services[method])
	if errors.Is(err, plugins.ErrIncapable) || errors.Is(err, plugins.ErrNotFound) {
		return nil, forbidden(fmt.Sprintf("instance %d doesn't offer %s", head.Instance, method))
	}
	if err != nil {
		return nil, err
	}
	offered, err := b.Registry.Services(ctx, b.Plugins, provider)
	if err != nil {
		return nil, err
	}
	if !slices.Contains(offered, method) {
		return nil, forbidden(fmt.Sprintf("instance %d doesn't offer %s", head.Instance, method))
	}
	delete(fwd, "instance")
	if head.Run != "" {
		token, remove := b.Runs.Add(provider.ID, relay(protocol.Peer(ctx), head.Run))
		defer remove()
		fwd["run"], _ = json.Marshal(token)
	}
	var res json.RawMessage
	if err := b.Registry.Call(ctx, b.Plugins, provider, method, fwd, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// relay sends the provider's ai.delta notifications to the caller under the
// caller's own token.
func relay(caller *protocol.Conn, run string) protocol.Handler {
	return func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != protocol.NotifyAIDelta {
			return nil, protocol.MethodNotFound(method)
		}
		var d protocol.AIDelta
		if err := json.Unmarshal(params, &d); err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
		}
		d.Run = run
		return nil, caller.Notify(method, d)
	}
}

func forbidden(msg string) *protocol.Error {
	return &protocol.Error{Code: protocol.CodeForbidden, Message: msg}
}
```

- [ ] **Step 4: Run the tests**

Run: `$DB go test -race ./internal/services/`
Expected: PASS, all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/services/
git commit -m "feat(services): route ai calls between plugin instances"
```

---

### Task 9: Wire the broker into the worker

**Files:**
- Modify: `cmd/innkeeper/worker.go:64-67`

- [ ] **Step 1: Wrap the run router**

Replace:

```go
	runs := pluginhost.NewRuns(log)
	im := importer.New(svc.lib, svc.registry, q, runs, log)
	mgr := pluginhost.NewManager(runs.Handle, 5*time.Minute, log)
	defer mgr.Close()
	im.Plugins = mgr
```

with:

```go
	runs := pluginhost.NewRuns(log)
	im := importer.New(svc.lib, svc.registry, q, runs, log)
	broker := &services.Broker{Registry: svc.registry, Runs: runs}
	mgr := pluginhost.NewManager(broker.Handle(runs.Handle), 5*time.Minute, log)
	defer mgr.Close()
	im.Plugins, broker.Plugins = mgr, mgr
```

Add the import `"github.com/inkandale/innkeeper/internal/services"`.

- [ ] **Step 2: Build and vet**

Run: `go build ./... && go vet ./cmd/...`
Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add cmd/innkeeper/worker.go
git commit -m "feat(worker): serve ai calls between plugins"
```

---

### Task 10: The contract check

**Files:**
- Create: `internal/contract/ai.go`
- Modify: `internal/contract/contract.go`, `internal/contract/contract_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/contract/contract_test.go`:

```go
func TestAIPluginAnswersItsServices(t *testing.T) {
	t.Cleanup(func() { Config = nil })
	for services, wantPass := range map[string]bool{"ai.chat": true, "ai.embed": false} {
		Config = map[string]any{"services": []any{services}}
		results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"ai"}), quiet)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(results, func(r Result) bool { return r.Name == checkServicesName })
		if i < 0 {
			t.Fatalf("%s: the ai check didn't run: %v", services, results)
		}
		if (results[i].Err == nil) != wantPass {
			t.Errorf("%s: got %v, want pass %v", services, results[i].Err, wantPass)
		}
	}
}
```

The fake answers `ai.chat` but not `ai.embed`, so offering `ai.embed` must fail the check.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./internal/contract/ -run TestAIPluginAnswersItsServices`
Expected: FAIL to compile, with `undefined: checkServicesName`.

- [ ] **Step 3: Write `internal/contract/ai.go`**

```go
package contract

import (
	"context"
	"errors"
	"fmt"

	"github.com/inkandale/innkeeper/internal/pluginhost"
	"github.com/inkandale/innkeeper/protocol"
)

const checkServicesName = "an ai plugin answers each service it offers with a reply or a plugin error"

// checkServices sends a small request to each service the plugin offers. A
// provider checked without its connection can only fail, so an error other
// than -32601 counts as an answer.
func checkServices(ctx context.Context, p *pluginhost.Process) error {
	requests := map[string]any{
		protocol.MethodAIChat:  protocol.AIChatParams{Messages: []protocol.ChatMessage{{Role: "user", Content: "Say OK."}}},
		protocol.MethodAIEmbed: protocol.AIEmbedParams{Texts: []string{"OK"}},
	}
	var errs []error
	for _, s := range p.Info.Services {
		err := call(ctx, p, s, requests[s], nil)
		var rpcErr *protocol.Error
		if err == nil || errors.As(err, &rpcErr) && rpcErr.Code != protocol.CodeMethodNotFound {
			continue
		}
		errs = append(errs, fmt.Errorf("%s: %w", s, err))
	}
	return errors.Join(errs...)
}
```

- [ ] **Step 4: Add the suite**

In `internal/contract/contract.go`, add to `suites` after the `enrich` entry:

```go
		{[]string{"ai"}, func() []Result { return []Result{{checkServicesName, checkServices(ctx, p)}} }},
```

- [ ] **Step 5: Run the contract tests**

Run: `go test ./internal/contract/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/contract/
git commit -m "feat(contract): check an ai plugin answers its services"
```

---

### Task 11: Run what CI runs

- [ ] **Step 1: Go**

Run:

```bash
go vet ./...
$DB go test -race ./...
```

Expected: PASS. No web changes in this part, so `npm run gen` isn't needed: `InitializeResult` and `Manifest` don't appear in the API schema. Confirm with `git diff --stat origin/main -- web/` showing nothing.

- [ ] **Step 2: Reread the diff**

Run: `git diff origin/main --stat` and read `git diff origin/main` once more for dead code, stale names (`callerInstance`), and anything this plan didn't ask for.

---

## Contracts for later plans

Exported names parts 2 to 4 use. Each is defined in the task named.

**Protocol (`protocol/ai.go`, Task 2)**
- `protocol.MethodAIChat = "ai.chat"`, `protocol.MethodAIEmbed = "ai.embed"`, `protocol.NotifyAIDelta = "ai.delta"`
- `protocol.Services map[string]string`: service method → capability (`"ai"`). The single list of service names; `x-offers` values must be keys of it.
- `protocol.CodeAIRejected = -32010`: provider refused the request as invalid (e.g. tools on a model without tool support).
- `protocol.ServiceCall{Instance int64 "instance"; Run string "run"}`: embedded in every service request.
- `protocol.ChatMessage{Role, Content string; Parts []ContentPart; ToolCalls []ToolCall; ToolCallID string}`, roles `system|user|assistant|tool`
- `protocol.ContentPart{Type "text"|"image_url"; Text; ImageURL string}`
- `protocol.ChatTool{Name, Description string; Parameters json.RawMessage}`
- `protocol.ToolCall{ID, Name, Arguments string}` (`Arguments` is a JSON string)
- `protocol.AIChatParams{ServiceCall; Messages []ChatMessage; Tools []ChatTool; Temperature *float64; MaxTokens *int}`
- `protocol.AIChatResult{Text string; ToolCalls []ToolCall; Model string}`
- `protocol.AIDelta{Run, Text string}`: one piece of streamed text, in order, all before the reply
- `protocol.AIEmbedParams{ServiceCall; Texts []string}`, `protocol.AIEmbedResult{Vectors [][]float32}`

**Manifest and initialize (Tasks 2–3)**
- Capability `"ai"` in `protocol.Capabilities`.
- `protocol.Manifest.Uses []string` (TOML `uses`), each a key of `protocol.Services`.
- `protocol.InitializeResult.Services []string` (JSON `services`): the service methods this instance offers. The core stops a plugin listing one outside its capabilities.

**Connection (Task 4)**
- `protocol.Peer(ctx) *protocol.Conn`: the connection a handler's call came in on.

**Plugin host and registry (Tasks 5, 7)**
- `pluginhost.(*Manager).Info(ctx, inst) (protocol.InitializeResult, error)`: starts the plugin if needed.
- `pluginhost.CallerInstance(ctx) string`: the calling instance's ID, or `""`.
- `plugins.(*Registry).Services(ctx, mgr, inst) ([]string, error)`: what one instance offers; a failed start marks it broken.
- `plugins.(*Registry).InstancesOffering(ctx, mgr *pluginhost.Manager, method string) ([]plugins.InstanceInfo, error)`: working instances offering `method`, oldest first. Returns `plugins.ErrInvalid` for a method not in `protocol.Services`. **Part 2's instance picker calls this with the API server's Manager (`api.Server.Plugins`).**

**Broker (Tasks 8–9)**
- `services.Broker{Registry, Plugins, Runs}` with `Handle(next protocol.Handler) protocol.Handler`, wired only on the worker's Manager. A service call from a plugin on the API's Manager gets `-32601`.
- The picker's stored value is the provider's instance ID as a JSON integer, which a consumer copies into `ServiceCall.Instance`.
