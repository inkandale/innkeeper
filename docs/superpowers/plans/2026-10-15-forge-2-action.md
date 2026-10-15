# Forge Phase 2: The Action Capability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Plugins can offer actions on cards and lorebooks. The core lists them, runs one as a job, keeps only the changes in the action's scope, and lets the plugin read the library through `library.call` when the owner allows it.

**Architecture:** INN-55, phase 2 of `docs/superpowers/specs/2026-10-14-forge-design.md`. The protocol section in `docs/protocol.md` is the contract phase 3 builds against; it is committed on its own first.

Pieces:
- **Protocol types** (`protocol/action.go`): declarations, run params, the reply, the report, and `library.call`, with their validation.
- **Run router** (`pluginhost.Runs`): the worker's plugins send `progress` and `library.call` with a run token. One router, shared by the importer and actions, sends each call to the run that owns the token. The importer's own token map moves into it.
- **Actions package** (`internal/actions`): the `action` job kind, its payload and progress, the start checks shared by the API and the job, scope enforcement, and `library.call`.
- **In-process tools** (`mcp.Call`): runs one built-in MCP tool through the API handler with a tier carried in the context (`auth.InProcess`), so library reads get the API's checks exactly as `/mcp` does.
- **API:** `GET /actions`, `POST /actions/runs`, `card_id` on `GET /jobs`, `library_access` on instances. The cache for `mcp.tools` becomes a generic per-instance list cache that `action.list` shares.
- **Library access:** a `library_access` column on `plugin_instance`, default false, shown on the instance settings page for plugins with `action`.
- **Fake plugin and checks:** six actions in `internal/fakeplugin/actions.go`; `innkeeper plugin check` lists and runs each action.

**Tech Stack:** Go, pgx, PostgreSQL migrations, Huma, MCP Go SDK, React and TypeScript for one settings toggle.

**Tests:** DB tests skip without the database URL. Run Go tests with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`. Below, `$DB` means that assignment.

**Deviations from the spec:**
- The job payload names the entity as `card_id` or `lorebook_id`, as a push job's does. `GET /jobs?card_id=` matches any job whose payload has that `card_id`, through a generic payload containment filter in `jobs.ListOptions`, so the jobs package learns nothing about actions.
- `library.call` serves only the four tools the spec names, not every read-tier tool: the others read apps and instance settings, not the library.
- Refusals use a new error code, `-32003`, so a plugin can tell "not allowed" from a tool failure. A token whose run has ended gets `-32602` from the shared router.
- A run with images for an action that doesn't declare `images` is a `400`, so the core never hands files to a plugin that didn't ask for them.
- The job re-reads `action.list` when it starts, so scope follows the declaration of the plugin that runs it. The start checks run again there.
- A run is never retried: the job fails on any error, on a worker stop, and on a second attempt after a crashed worker.

---

## File map

| File | Change |
|---|---|
| `protocol/action.go`, `protocol/action_test.go` | New: methods, types, `Action.Validate`, `Report.Validate`, `CodeForbidden` |
| `protocol/mcp.go` | `objectSchema` shared by `Tool.Validate` and `Action.Validate` |
| `internal/pluginhost/runs.go`, `runs_test.go` | New: `Runs` router |
| `internal/jobs/worker.go` | `Run.Token` |
| `internal/jobs/queue.go` | `ListOptions.Payload` containment filter |
| `internal/importer/importer.go`, test | Use `Runs`; drop `Handle` and the token map |
| `internal/db/migrations/0023_library_access.sql` | `plugin_instance.library_access` |
| `internal/plugins/plugins.go` | `LibraryAccess` in `InstanceInfo`, `NewInstance`, `InstanceEdit` |
| `internal/auth/oauth.go` | `InProcess`, `InProcessTier` |
| `internal/api/auth.go` | `authorized` honors an in-process tier |
| `internal/mcp/mcp.go`, `tools.go` | `tool.call`, `Call` |
| `internal/actions/actions.go`, `scope.go`, `run.go`, tests | New package |
| `internal/api/server.go` | `APIHandler`, `actions` cache, fail mapping |
| `internal/api/tools.go`, `internal/api/listcache.go` | Generic list cache and fan-out; `mcp.tools` uses it |
| `internal/api/actions.go`, `actions_test.go` | New routes and tests |
| `internal/api/jobs.go` | `card_id` filter, summary cuts `result` |
| `internal/api/instances.go` | `library_access` on create and update |
| `internal/fakeplugin/actions.go`, `main.go` | Fake actions |
| `internal/contract/contract.go`, test | Action checks |
| `cmd/innkeeper/worker.go` | Shared router, API handler for tools, action handler |
| `web/src/settings/Instance.tsx`, test | Library access checkbox |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated with `npm run gen` |

---

### Task 1: Protocol types

**Files:** Create `protocol/action.go`, `protocol/action_test.go`. Modify `protocol/mcp.go`.

- [ ] **Step 1: Failing tests.** `TestActionValidate` table: a valid field action; bad ID (`Tighten`, 41 chars); empty label; applies `scene`; fields with `new`; a repeated or empty field; inputs `{"type": "string"}`. `TestReportValidate`: empty summary, severity `urgent`, a finding without text, a dimension without key, a move without `to`, and a full valid report.
- [ ] **Step 2:** `go test ./protocol/` fails to compile.
- [ ] **Step 3: Implement.**

```go
const (
	MethodActionList  = "action.list"
	MethodActionRun   = "action.run"
	MethodLibraryCall = "library.call"
	// CodeForbidden is the core refusing a plugin's call, such as library.call without Library access.
	CodeForbidden = -32003
)

type Action struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Description string          `json:"description,omitempty"`
	Applies     string          `json:"applies" enum:"card,lorebook,new"`
	Fields      []string        `json:"fields,omitempty"`
	Instruction bool            `json:"instruction,omitempty"`
	Inputs      json.RawMessage `json:"inputs,omitempty"`
	ReportOnly  bool            `json:"report_only,omitempty"`
	Library     bool            `json:"library,omitempty"`
	Images      bool            `json:"images,omitempty"`
}

type ActionListResult struct{ Actions []Action `json:"actions"` }
type ActionEntity struct { Kind string `json:"kind"`; Data json.RawMessage `json:"data,omitempty"` }
type ContextItem struct { ID int64 `json:"id"`; Name string `json:"name"`; Data json.RawMessage `json:"data"` }
type ActionContext struct { Cards, Lorebooks []ContextItem; Skills []string } // json: cards, lorebooks, skills; never null
type ActionImages struct { Reference string `json:"reference,omitempty"`; Avatar string `json:"avatar,omitempty"` }
type ActionRunParams struct { Run, Action, Field string; Entity ActionEntity; Instruction string; Inputs json.RawMessage; Context ActionContext; Images ActionImages }
type ActionRunResult struct { Proposal json.RawMessage `json:"proposal,omitempty"`; Report *Report `json:"report,omitempty"`; Raw string `json:"raw,omitempty"` }
type Report struct { Summary string; Score *float64; Dimensions []ReportDimension; Findings []Finding; Suggestions []string }
type ReportDimension struct { Key, Label string; Score float64; Comment string }
type Finding struct { Severity, Text string; Fields []string; Move *FindingMove }
type FindingMove struct { Excerpt, To string }
type LibraryCallParams struct { Run, Name string; Arguments json.RawMessage }
```

`objectSchema(raw) bool` moves out of `Tool.Validate` into `mcp.go` and both validators call it.
- [ ] **Step 4:** `go test ./protocol/` passes.
- [ ] **Step 5:** Commit `feat(protocol): action declarations, runs, and reports`.

### Task 2: One run router for the worker's plugins

**Files:** Create `internal/pluginhost/runs.go`, `runs_test.go`. Modify `internal/jobs/worker.go`, `internal/importer/importer.go`, `importer_test.go`, `cmd/innkeeper/worker.go`.

- [ ] **Step 1: Failing test** `TestRunsRoutesByToken`: two handlers added under `1.1` and `2.1` each see only their own calls; after `remove()`, a call for `1.1` gets a `-32602` `*protocol.Error`; params without `run` get the same.
- [ ] **Step 2:** fails to compile.
- [ ] **Step 3: Implement.**

```go
// Runs sends the calls a plugin makes during a long call, such as import.run's
// notifications, to the run whose token they carry.
type Runs struct { log *slog.Logger; mu sync.Mutex; runs map[string]protocol.Handler }
func NewRuns(log *slog.Logger) *Runs
func (r *Runs) Add(token string, h protocol.Handler) (remove func())
func (r *Runs) Handle(ctx context.Context, method string, params json.RawMessage) (any, error)
```

`jobs.Run.Token()` returns `"<id>.<attempts>"`. The importer takes `*pluginhost.Runs` in `New`, registers `session.serve` per run, and drops `Importer.Handle` and its map. The worker builds one `Runs` and passes `runs.Handle` to its manager.
- [ ] **Step 4:** `$DB go test ./internal/pluginhost/ ./internal/importer/` passes.
- [ ] **Step 5:** Commit `refactor(pluginhost): route run-token calls through one shared router`.

### Task 3: Library access setting

**Files:** Create `internal/db/migrations/0023_library_access.sql`. Modify `internal/plugins/plugins.go`, `internal/api/instances.go`, `internal/api/instances_test.go`.

- [ ] **Step 1: Failing test** `TestInstanceLibraryAccess`: a new instance reads `library_access: false`; `PATCH {"library_access": true}` makes it true and leaves config untouched; creating with `library_access: true` stores it.
- [ ] **Step 2:** fails.
- [ ] **Step 3:** `ALTER TABLE plugin_instance ADD COLUMN library_access boolean NOT NULL DEFAULT false;`. `InstanceInfo.LibraryAccess` (`json:"library_access"`), `NewInstance.LibraryAccess bool`, `InstanceEdit.LibraryAccess *bool` with `coalesce($7, library_access)` in the update. API bodies get `library_access` with a doc string.
- [ ] **Step 4:** `$DB go test ./internal/plugins/ ./internal/api/ -run Instance` passes.
- [ ] **Step 5:** Commit `feat(api): a Library access setting on plugin instances`.

### Task 4: In-process MCP tools

**Files:** Modify `internal/auth/oauth.go`, `internal/api/auth.go`, `internal/mcp/mcp.go`, `internal/mcp/tools.go`. Test in `internal/api/mcp_test.go`.

- [ ] **Step 1: Failing test** `TestMCPCallInProcess`: with a card created, `mcp.Call(ctx, e.s.APIHandler(), tier.Read, "get_card", {"id": N})` returns text holding the card's name; `create_card` returns an error naming its tier; an unknown tool errors; `get_card` for a missing ID returns `IsError`.
- [ ] **Step 2:** fails.
- [ ] **Step 3:** `auth.InProcess(ctx, t)` and `auth.InProcessTier(ctx)`; `authorized` returns that tier first. `def` also builds `call func(ctx, caller, json.RawMessage)`. `mcp.Call` finds the tool, checks `t.Allows(tool.tier)`, runs it with an empty `caller.auth` under `auth.InProcess`, and converts the SDK result to `protocol.MCPCallResult`; a tool's own error becomes `IsError`. `Server.APIHandler()` is the API mux that `Handler` mounts at `/api`.
- [ ] **Step 4:** `$DB go test ./internal/api/ -run MCP` passes.
- [ ] **Step 5:** Commit `feat(mcp): run a built-in tool in process with a given tier`.

### Task 5: The actions package: payload checks and scope

**Files:** Create `internal/actions/actions.go`, `internal/actions/scope.go`, `internal/actions/actions_test.go`.

- [ ] **Step 1: Failing tests.**
  - `TestScope`: field action changing `description` and `name` keeps `description`, restores `name`, warns once; a field action removing `scenario` keeps it; a card action adding `character_book` drops it; a card action changing three keys keeps all; `report_only` returns no proposal and warns per changed key; a lorebook action keeps `entries`; `new` with base `{}` keeps every key but `character_book`; no proposal gives none.
  - `TestPayloadCheck`: applies `card` with kind `lorebook` is invalid; `new` with an ID is invalid; a field outside `fields` and a field on a whole-card action are invalid; a field action without a field is invalid; images on an action without `images` are invalid; non-object data is invalid. All wrap `ErrInvalid`.
- [ ] **Step 2:** fails.
- [ ] **Step 3: Implement.**

```go
const Kind = "action"
var ErrInvalid = errors.New("invalid action run")
type Payload struct {
	Action, Field string; Entity protocol.ActionEntity; CardID, LorebookID int64 // json card_id, lorebook_id
	Instruction string; Inputs json.RawMessage; Context ContextRefs; Images ImageRefs
}
type ContextRefs struct { Cards, Lorebooks []int64; Skills []string }
type ImageRefs struct { Reference, Avatar string } // blob SHA-256s
func (p Payload) Check(a protocol.Action) error
func Scope(a protocol.Action, field string, kind string, base, proposal json.RawMessage) (json.RawMessage, []string, error)
type Progress struct { Message string; Done, Total int64; Result *Result }
type Result struct { Proposal json.RawMessage; Report *protocol.Report; Raw string; Warnings []string }
func ListProgress(p json.RawMessage) json.RawMessage // progress without result
```

Values compare after decoding to `any`, so key order and spacing don't count as changes.
- [ ] **Step 4:** `go test ./internal/actions/` passes.
- [ ] **Step 5:** Commit `feat(actions): run checks and scope enforcement`.

### Task 6: Fake plugin actions

**Files:** Create `internal/fakeplugin/actions.go`. Modify `internal/fakeplugin/main.go`.

Actions: `tighten` (field action on `description` and `personality`), `reforge` (whole card, `instruction`; inputs `sleep_ms`, `fail`, `call`), `evaluate` (report-only, also sends a proposal), `generate` (`new`, `images`; `raw` lists the image paths it got), `sneaky` (field action on `description` that also renames the card and adds `character_book`), `lookup` (`library: true`; makes the `library.call` in inputs `call` and puts the reply or error in `raw`). Each sends one `progress`. Mode `bad-actions` lists an action with a bad ID.

- [ ] **Step 1:** Write it; `go build ./internal/fakeplugin/`.
- [ ] **Step 2:** Commit `test(fakeplugin): offer actions`.

### Task 7: The action job

**Files:** Create `internal/actions/run.go`. Modify `internal/api/jobs.go` (summary). Tests come in Task 8, through the API.

```go
type Runner struct {
	Lib *library.Library; Registry *plugins.Registry
	Plugins *pluginhost.Manager // its core handler is Runs.Handle
	Runs *pluginhost.Runs
	API http.Handler // serves library.call's tools in process
	Log *slog.Logger
}
func (rn *Runner) Run(ctx context.Context, r *jobs.Run) error
```

Run: fail a second attempt; load the instance with `action`; call `action.list` and find the first valid declaration with the ID; `Payload.Check`; resolve context to head revisions and images to blob paths; register the run's handler on `Runs` for `progress` and `library.call`; call `action.run`; validate the reply; scope it; set progress with `result`. Every error wraps `jobs.ErrFinal`.

`library.call`: refuse with `CodeForbidden` unless the declaration has `library`, the instance's `library_access` is on, and the name is one of the four tools; then `mcp.Call(runCtx, rn.API, tier.Read, …)`.

- [ ] **Step 1:** Implement; `go vet ./...`.
- [ ] **Step 2:** Commit `feat(actions): run an action as a job`.

### Task 8: API routes

**Files:** Create `internal/api/listcache.go`, `internal/api/actions.go`, `internal/api/actions_test.go`. Modify `internal/api/tools.go`, `server.go`, `jobs.go`, `queue.go`.

- [ ] **Step 1: Failing tests** in `actions_test.go`, with a `forge` plugin (the fake with `action`) and a worker running `actions.Runner`:
  - `TestListActions`: `GET /actions?applies=card` lists `tighten`, `reforge`, `evaluate`, `sneaky`, `lookup` with `instance_id` and `instance_name`, and not `generate`.
  - `TestStartActionRunRejects`: unknown action, `tighten` on a lorebook, field `scenario` on `tighten`: each `400`.
  - `TestActionRunFieldProposal`: `tighten` on a card succeeds; progress has the message and a `result.proposal` changing only `description`; `GET /jobs?kind=action&card_id=N` finds it without `result`; `GET /jobs/{id}` has it.
  - `TestActionRunDropsOutOfScope`: `sneaky` keeps `description`, warns about `name` and `character_book`.
  - `TestActionRunReportOnly`: `evaluate` has a report and no proposal.
  - `TestLibraryCallRefusals`: `reforge` with `call` gets `-32003` (no declaration); `lookup` gets `-32003` with access off, a card's name with it on, and `-32003` for `create_card`.
  - `TestCancelledActionRunLeavesNoResult`: `reforge` with `sleep_ms` 1500, cancelled while running; after 2.5 s the job is failed, `cancelled`, with no `result`.
  - `TestActionRunFailsWithPluginError`: `reforge` with `fail` fails with the plugin's message and isn't retried.
- [ ] **Step 2:** fails.
- [ ] **Step 3:** The list cache: `instanceList[R, T]` keyed by instance version, one minute (30 seconds after a failure), shared by `mcp.tools` and `action.list`; `eachWorking(insts, f)` runs `f` in parallel for 3 seconds at most. Routes: `GET /actions` (read), `POST /actions/runs` (write, `202`). `jobs.ListOptions.Payload` filters with `payload @> $7`; `GET /jobs` takes `card_id`. `summary` cuts `result` for `actions.Kind`. `failAs` maps `actions.ErrInvalid` to `400`.
- [ ] **Step 4:** `$DB go test ./internal/api/` passes.
- [ ] **Step 5:** Commit `feat(api): list actions and start action runs`.

### Task 9: Plugin checks

**Files:** Modify `internal/contract/contract.go`, `contract_test.go`.

- [ ] **Step 1: Failing tests:** the fake with `action` passes every check; `-mode=bad-actions` fails "action.list returns valid actions".
- [ ] **Step 2:** fails.
- [ ] **Step 3:** Suite for `action`: `checkActions` validates and checks unique IDs; `checkActionRuns` runs each on a sample card or lorebook (none for `new`) with its first field, `{}` inputs, and validates the reply, accepting a JSON-RPC error other than `-32601`.
- [ ] **Step 4:** `go test ./internal/contract/` passes.
- [ ] **Step 5:** Commit `feat(contract): check action plugins`.

### Task 10: Worker wiring

**Files:** Modify `cmd/innkeeper/worker.go`.

- [ ] Build the shared `Runs`, an `api.Server` for in-process tools, and `actions.Runner`; register `actions.Kind`. `go build ./cmd/innkeeper` and commit `feat(worker): run action jobs`.

### Task 11: Library access in the web UI

**Files:** Modify `web/src/settings/Instance.tsx`, `Instance.test.tsx`; regenerate the schema.

- [ ] **Step 1:** `cd web && npm run gen`.
- [ ] **Step 2: Failing test:** a plugin with `action` shows a "Library access" checkbox, unchecked; ticking it and saving sends `library_access: true`. A plugin without `action` shows none.
- [ ] **Step 3:** Checkbox with a one-line hint; sent on create, and on update only when changed.
- [ ] **Step 4:** `npm run typecheck && npm test`.
- [ ] **Step 5:** Commit `feat(web): turn on Library access for an action instance`.

### Task 12: Verify

- [ ] `$DB go vet ./... && $DB go test -race ./...`, then `cd web && npm run typecheck && npm test`.
- [ ] Read `git diff origin/main...HEAD` for dead code, copies of existing capability code, and unrequested changes.
