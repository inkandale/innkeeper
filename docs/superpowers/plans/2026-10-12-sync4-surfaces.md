# Sync Surfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Owners use one Refresh and one Sync. Adopt is gone, and autosync can be switched on from the card panel, `ink`, and MCP.

**Architecture:** Step 4 of `docs/superpowers/specs/2026-10-11-single-sync-design.md` (INN-46).
- **Refresh:** `POST /scans` starts the existing scan job, or joins one that's running. The scan also counts each app's items that have no binding (`new_in_app`).
- **Sync:** the Sync page gets one Refresh button and one Sync button, and loses the per-app Sync and Push ahead buttons.
- **CLI and MCP:** a sync of every app goes through `POST /syncs`.
- **Adopt:** removed everywhere. Review covers it.
- **Commits:** none is marked as a breaking change: no `feat!`, no `BREAKING CHANGE` footer.

**Tech Stack:** Go, Huma, cobra (`cmd/ink`), MCP Go SDK, React + TypeScript with TanStack Query and Vitest.

**Tests:** DB tests skip without the database URL, so a run without it proves nothing. Always run with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`, and use `-timeout 15m`. After any API change, run `cd web && npm run gen`; CI checks that the generated client matches the API.

**Docs:** invoke the `documentation-writing` skill before editing README prose. Edit `README.md` with the Edit tool.

---

### Task 1: remove Adopt

Review takes over Adopt's three modes. `base` is "take the app's side", `override` is "keep in app", and `ignore` is "keep the library's side".

**Files:**
- Delete: `internal/targets/adopt.go`, `internal/targets/adopt_test.go`
- Modify: `internal/targets/data.go`, `internal/targets/data_test.go`, `internal/targets/targets.go`, `internal/targets/bind_test.go`, `internal/targets/lorebook_test.go`, `internal/api/targets.go`, `internal/api/server.go`, `internal/api/targets_test.go`, `internal/api/mcp_test.go`, `internal/mcp/tools.go`, `cmd/ink/sync.go`, `cmd/ink/bindings.go`, `cmd/ink/bindings_test.go`, `README.md`, `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Remove the core**
  - Delete `internal/targets/adopt.go` and `internal/targets/adopt_test.go`.
  - In `internal/targets/data.go`, delete `adoptable`, `applyChanges`, `overrideChanges` and `clearOverride`, but only if nothing outside adopt uses them. Grep each name first, and keep `diff`.
  - Delete their tests in `data_test.go` (`TestAdoptChanges`, `TestOverrideChangesRemovesNestedKeys`, `TestClearOverride`).
  - Delete `ErrNoChanges` from `targets.go` if nothing else uses it. Keep `ErrMoved` if anything else uses it; otherwise it went with `adopt.go`.
  - In `bind_test.go`, delete `TestBindDifferingItemHasNothingToAdopt`, or keep its bind assertion and drop the Adopt call. Choose whichever leaves a meaningful test.
  - In `lorebook_test.go`:
    - Rework `TestCheckAndAdoptLorebook` into `TestCheckLorebook`, keeping the check assertions and dropping the adopt part.
    - In `TestLorebookOverride`, drop the Adopt call. If the rest of that test depends on it, resolve the item through `e.s.ResolveLorebook` with `Default: merge.ChooseKeepInApp` instead.
  - Update comments that mention adopt so they stay true (`check.go`, `jobs.go`, `bulk_preview.go`, `targets.go`, `internal/wire/sync.go`). For example, "since the last push or adopt" becomes "since the last push or resolve". Keep the `adopt` value in `internal/library/entities.go`'s source list, because old revisions carry it.

- [ ] **Step 2: Remove the API route**
  - In `internal/api/targets.go`, delete `adoptIn` and the `adopt` route. Change the description strings that mention adopt (around lines 110 and 229) to name review instead.
  - In `internal/api/server.go`, drop `targets.ErrNoChanges` and `targets.ErrMoved` from the 409 case if they no longer exist.
  - In `internal/api/targets_test.go`, delete the adopt requests and assertions (around lines 78-87 and 154-156) and keep the rest of each test.

- [ ] **Step 3: Remove the MCP tool**
  - In `internal/mcp/tools.go`, delete `adoptIn` and the `adopt` tool.
  - In `internal/api/mcp_test.go`, delete the adopt call in `TestMCPOtherTools` and change the built-in count at line 87 from 33 to 32.

- [ ] **Step 4: Remove the CLI command**
  - In `cmd/ink/sync.go`, remove `adoptCmd()` from `AddCommand`.
  - In `cmd/ink/bindings.go`, delete `adoptCmd`, and change the heading at line 46 to `changed in the app since the last push or resolve:`.
  - Delete `TestSyncAdopt` from `cmd/ink/bindings_test.go`.

- [ ] **Step 5: README**
  - Delete the `ink sync adopt` block (around lines 499-509). In its place, add one sentence: "To take the app's changes, keep them only in that app, or keep the library's copy, review the item: `ink sync compare` and `ink sync resolve`, or Review in the web app."
  - Remove adopt from the lines around 228, 332, 537 and 639, keeping each sentence true.

- [ ] **Step 6: Build, test, regenerate**

Run:
- `go vet ./...`
- `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -timeout 15m ./internal/targets ./internal/api ./internal/mcp ./cmd/ink`
- `cd web && npm run gen && npm test`

Expected: every package `ok` and all web tests passing. `grep -rn "adopt" internal cmd --include=*.go | grep -iv catalog` should show only the `adopt` revision source and unrelated catalog adopt.

- [ ] **Step 7: Commit**

```bash
git add -A internal cmd README.md web/openapi.json web/src/api/schema.d.ts
git commit -m "feat: review replaces adopt"
```

Check `git status` first, so no stray files go in.

---

### Task 2: Refresh counts new app items and can be started

**Files:**
- Modify: `internal/targets/check.go`, `internal/targets/check_test.go`, `internal/api/targets.go`, `internal/api/targets_test.go`, `internal/api/tiers_test.go`

- [ ] **Step 1: Write the failing scan test**

Append to `internal/targets/check_test.go`:

```go
// A scan counts each app's items that nothing in the library is bound to.
func TestScanCountsNewInApp(t *testing.T) {
	e := setup(t, nil)
	e.push(e.card)
	e.appCard("r7", map[string]any{"name": "Lia", "description": liaText}, nil)
	e.appCard("r8", map[string]any{"name": "Mira", "description": miraText}, nil)
	p, err := e.s.Scan(e.ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.NewInApp[e.inst] != 2 {
		t.Errorf("new_in_app = %v, want 2 for instance %d", p.NewInApp, e.inst)
	}
}
```

If `Scan`'s `report` argument can't be nil today, pass `func(ScanProgress) {}`.

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=... go test ./internal/targets -run TestScanCountsNewInApp`
Expected: a build failure, `p.NewInApp undefined`.

- [ ] **Step 3: Count them**

In `internal/targets/check.go`, add to `ScanProgress`:

```go
	// NewInApp counts, by instance ID, the app items no binding uses.
	NewInApp map[int64]int `json:"new_in_app,omitempty"`
```

At the end of `Scan`, before it returns `p`, add:

```go
	ids, err := s.syncApps(ctx, 0)
	if err != nil {
		return p, err
	}
	for _, id := range ids {
		n, err := s.newInApp(ctx, id)
		if err != nil {
			p.Failed++
			if len(p.Errors) < maxErrors {
				p.Errors = append(p.Errors, fmt.Sprintf("instance %d: %v", id, err))
			}
			continue
		}
		if p.NewInApp == nil {
			p.NewInApp = map[int64]int{}
		}
		p.NewInApp[id] = n
		if report != nil {
			report(p)
		}
	}
```

and append:

```go
// newInApp counts the app's characters, and world books on an app that keeps
// them on their own, that no binding on the instance uses.
func (s *Service) newInApp(ctx context.Context, instanceID int64) (int, error) {
	inst, info, err := s.describeReconcile(ctx, cardKind, instanceID)
	if err != nil {
		return 0, err
	}
	kinds := []kind{cardKind}
	if info.Lorebooks == protocol.LorebooksStandalone {
		kinds = append(kinds, lorebookKind)
	}
	n := 0
	for _, k := range kinds {
		var res protocol.TargetListResult
		if err := s.reg.Call(ctx, s.plugins, inst, k.list, nil, &res); err != nil {
			return 0, err
		}
		ids := make([]string, len(res.Items))
		for i, it := range res.Items {
			ids[i] = it.RemoteID
		}
		var bound int
		if err := s.pool.QueryRow(ctx, `SELECT count(DISTINCT remote_id) FROM binding
			WHERE instance_id = $1 AND `+k.col+` IS NOT NULL AND remote_state <> 'missing' AND remote_id = ANY($2)`,
			instanceID, ids).Scan(&bound); err != nil {
			return 0, err
		}
		n += len(ids) - bound
	}
	return n, nil
}
```

Match the existing code: `Reconcile` calls `k.list` through `s.reg.Call` the same way (`reconcile.go`, the `list` closure). Add the `protocol` and `fmt` imports if missing.

- [ ] **Step 4: Run the targets package**

Run: `INNKEEPER_TEST_DATABASE_URL=... go test -timeout 15m ./internal/targets`
Expected: `ok`. The existing scan tests may assert the whole `ScanProgress`. Update them only to allow the new `NewInApp` field, and report it.

- [ ] **Step 5: Write the failing route test**

Append to `internal/api/targets_test.go`:

```go
func TestScanRoute(t *testing.T) {
	e := newEnv(t)
	var a, b jobOut
	e.call("POST", "/scans", nil, 202, &a)
	e.call("POST", "/scans", nil, 202, &b)
	if a.Kind != targets.ScanKind || b.ID != a.ID {
		t.Errorf("scans = %+v, %+v", a, b)
	}
}
```

If `e.call` needs a body for POST, pass `map[string]any{}`.

- [ ] **Step 6: Add the route**

In `internal/api/targets.go`, register it next to `check-binding`:

```go
	huma.Register(api, huma.Operation{Metadata: needs(tier.Read), OperationID: "start-scan", Method: http.MethodPost,
		Path: "/scans", Summary: "Refresh: check every app for changes", Tags: tags, DefaultStatus: http.StatusAccepted,
		Description: "Reads every bound item back from its app and counts the app items nothing is bound to. " +
			"Changes nothing in the library or the apps. Returns the scan already queued or running, if there is one."},
		func(ctx context.Context, _ *struct{}) (*jobResp, error) {
			j, err := s.Queue.EnqueueOnce(ctx, jobs.Spec{Kind: targets.ScanKind})
			if err != nil {
				return nil, s.fail(err)
			}
			return &jobResp{Body: toJob(j)}, nil
		})
```

Add `"start-scan": tier.Read,` to `tierExceptions` in `internal/api/tiers_test.go`, next to `check-binding`, with the comment `// Reads the apps; changes nothing.`

- [ ] **Step 7: Run and regenerate**

Run `INNKEEPER_TEST_DATABASE_URL=... go test -timeout 15m ./internal/api`, then `cd web && npm run gen`.

- [ ] **Step 8: Commit**

```bash
git add internal/targets/check.go internal/targets/check_test.go internal/api/targets.go internal/api/targets_test.go internal/api/tiers_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): refresh checks every app and counts new app items"
```

---

### Task 3: CLI and MCP

**Files:**
- Modify: `cmd/ink/reconcile.go`, `cmd/ink/reconcile_test.go`, `cmd/ink/sync.go`, `cmd/ink/bindings.go`, `cmd/ink/bindings_test.go`, `internal/mcp/tools.go`, `internal/api/mcp_test.go`, `README.md`

- [ ] **Step 1: Write the failing CLI tests**

Append to `cmd/ink/reconcile_test.go`. It uses `stubAPI` and `runInk` from `main_test.go`. An unmatched POST returns 202 `{"id": 9}`.

```go
func TestSyncRunEveryApp(t *testing.T) {
	got := stubAPI(t, map[string]string{
		"/jobs/9": `{"id": 9, "kind": "target.sync", "status": "succeeded",
			"progress": {"apps": [{"instance_id": 2, "job_id": 11, "totals": {"auto_imported": 3, "likely": 1}}]}}`,
	})
	out, err := runInk(t, "sync", "run")
	if err != nil {
		t.Fatal(err)
	}
	if (*got)[0] != `POST /syncs {}` || !strings.Contains(out, "11") {
		t.Errorf("requests %q, output %q", *got, out)
	}
}

func TestSyncRefreshAndAutosync(t *testing.T) {
	got := stubAPI(t, map[string]string{"PATCH /bindings/5": bindingJSON})
	if _, err := runInk(t, "sync", "refresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := runInk(t, "sync", "autosync", "5", "on"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /scans ", `PATCH /bindings/5 {"autosync":true}`}
	if !slices.Equal(*got, want) {
		t.Errorf("requests = %q, want %q", *got, want)
	}
}
```

Read `watchJob` in `cmd/ink/commands.go` first. If it streams `/jobs/9/watch` or polls something other than `GET /jobs/9`, stub that URI instead, and make the request assertion check only the first request. `bindingJSON` is in `bindings_test.go`.

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cmd/ink -run 'TestSyncRunEveryApp|TestSyncRefreshAndAutosync'`
Expected: FAIL. `sync run` needs an instance, and `refresh` and `autosync` are unknown commands.

- [ ] **Step 3: Implement**
  - **`ink sync run`.** In `cmd/ink/reconcile.go`, change `reconcileCmd`'s `Use` to `"run [instance-id] [params-json]"` and `Args` to `cobra.RangeArgs(0, 2)`.
    - With an instance ID, keep the current behavior (`POST /reconciles`, watch, print the snapshot).
    - With none, POST `/syncs` with `{}`, or `{"apply":"none"}` under `--dry-run`. Watch the job with the same `watchJob`, then read `GET /jobs/{id}` and print one line per app: `instance <id>: <totals, sorted keys, as k=v> (review: ink sync apply <job_id>)`, or `instance <id>: error: <error>`. Decode the progress into `wire.SyncResult`.
    - Set `Short` to "Sync every app with the library, or one app".
  - **`ink sync refresh`.** Add it in `cmd/ink/sync.go`:

    ```go
    func refreshCmd() *cobra.Command {
    	return &cobra.Command{Use: "refresh", Short: "Check every app for changes; changes nothing", Args: cobra.NoArgs,
    		RunE: func(cmd *cobra.Command, _ []string) error { return startJob(cmd, "/scans", nil) }}
    }
    ```

    Check `startJob`'s signature. If a nil body isn't allowed, pass `map[string]any{}` and update the test's expected request to match.
  - **`ink sync autosync <binding-id> on|off`.** Add it in `cmd/ink/bindings.go`:

    ```go
    func autosyncCmd() *cobra.Command {
    	return &cobra.Command{Use: "autosync <binding-id> on|off",
    		Short: "Let each sync push the library's copy to this app, or stop it",
    		Args:  cobra.ExactArgs(2),
    		RunE: func(cmd *cobra.Command, args []string) error {
    			id, err := parseID(args[0])
    			if err != nil {
    				return err
    			}
    			if args[1] != "on" && args[1] != "off" {
    				return fmt.Errorf("say on or off, not %q", args[1])
    			}
    			body, _ := json.Marshal(map[string]bool{"autosync": args[1] == "on"})
    			raw, err := apiCall(cmd, "PATCH", "/bindings/"+id, body)
    			if err != nil {
    				return err
    			}
    			return show(cmd, raw, printBinding)
    		}}
    }
    ```

    Register `refreshCmd()` and `autosyncCmd()` in the `sync` `AddCommand` list. Add an `autosync` line to `printBinding`'s output, next to its status.

- [ ] **Step 4: MCP**

In `internal/mcp/tools.go`:

1. Add the input type:

```go
type syncIn struct {
	InstanceID int64  `json:"instance_id,omitempty" jsonschema:"one app to sync; omit to sync every app"`
	Apply      string `json:"apply,omitempty" jsonschema:"obvious (default) or none to preview"`
}

type autosyncIn struct {
	BindingID int64 `json:"binding_id"`
	Autosync  bool  `json:"autosync"`
}
```

Match the `jsonschema` tag style the file already uses.

2. Replace `start_sync`:

```go
	def("start_sync", "Sync every target app with the library, one at a time, or one app: binds exact matches, imports new items, "+
		"and takes edits made only in an app. Pass apply \"none\" to preview. Starts a job; its finished get_job progress names "+
		"each app's reconcile job, whose progress is that app's snapshot.", tier.Write,
		func(ctx context.Context, c caller, in syncIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/syncs", in)
		}),
	def("refresh", "Check every app for changes and count the app items nothing is bound to. Changes nothing.", tier.Read,
		func(ctx context.Context, c caller, _ struct{}) (*sdk.CallToolResult, error) {
			return c.call(ctx, "POST", "/scans", nil)
		}),
	def("set_autosync", "Turn autosync on or off for a binding. With it on, each sync pushes the library's copy to that app "+
		"when the card is ahead and the app's copy hasn't changed.", tier.Destructive,
		func(ctx context.Context, c caller, in autosyncIn) (*sdk.CallToolResult, error) {
			return c.call(ctx, "PATCH", fmt.Sprintf("/bindings/%d", in.BindingID), map[string]bool{"autosync": in.Autosync})
		}),
```

Delete `reconcileIn` if nothing else uses it.

3. In `internal/api/mcp_test.go`:
   - Change the built-in count from 32 to 34.
   - Change `TestMCPReconcile` so it starts `start_sync` with `instance_id` and reads the result through the job's `apps[0].job_id`, or keep it on `/reconciles` through `compare_item`. Use whichever keeps the test's intent. Its worker needs `targets.SyncKind` registered; see `work()` in `reconcile_test.go`.
   - Add a call to `refresh` and to `set_autosync` in `TestMCPOtherTools`, with a destructive-tier client for `set_autosync`. Check `TestMCPToolsByTier` for a tier list to update.

- [ ] **Step 5: README**

Update the CLI list (around line 332) for `ink sync run [instance-id]`, `ink sync refresh` and `ink sync autosync <binding-id> on|off`. Describe autosync in one or two sentences where the README explains syncing. Say that it needs a destructive-tier token and never overwrites an edit made in the app.

- [ ] **Step 6: Run and commit**

Run `go vet ./... && INNKEEPER_TEST_DATABASE_URL=... go test -timeout 15m ./cmd/ink ./internal/mcp ./internal/api`. Expected: all `ok`.

```bash
git add cmd/ink internal/mcp/tools.go internal/api/mcp_test.go README.md
git commit -m "feat(ink): sync every app, refresh, and switch autosync from the CLI and MCP"
```

---

### Task 4: web — Sync page and the autosync toggle

**Files:**
- Modify: `web/src/sync/SyncPage.tsx`, `web/src/sync/SyncPage.test.tsx`, `web/src/card/Panel.tsx`, `web/src/card/Card.test.tsx`, `web/src/sync/Sync.css` (only if needed)

Read `SyncPage.tsx`, `SyncPage.test.tsx`, the `Sync` section of `Panel.tsx` and the binding tests in `Card.test.tsx` first. Match their patterns: `api` and `must` from `../api/client`, `useQuery` and `useMutation`, `toast`, and tests that stub `fetch` and route on pathname.

- [ ] **Step 1: Write the failing Sync page tests**

In `SyncPage.test.tsx`:
- Replace the "Sync starts a run and opens its queue" test with "Sync starts one run for every app". It clicks the single page-level `Sync` button and expects one POST to `/api/syncs` with `{}`.
- Add "Refresh starts a scan": click `Refresh`, and expect a POST to `/api/scans`.
- Add "the table shows new app items from the last scan": stub `GET /api/jobs?kind=target.scan` returning `[{ id: 3, kind: "target.scan", status: "succeeded", progress: { new_in_app: { "2": 4 } } }]`, and expect a `New in app` column cell with `4` for instance 2.
- Delete the "Push all ahead" tests, and assert that no per-row Sync button and no "Push" button remain.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/sync/SyncPage.test.tsx`
Expected: the new tests fail.

- [ ] **Step 3: Rework `SyncPage.tsx`**
  - **Remove** the `reconcile` and `pushAhead` mutations, the `arming` and `pushed` state, and the per-row action cell and its header.
  - **Add a query** `scans` with key `["jobs", "target.scan"]`: `api.GET("/jobs", { params: { query: { kind: "target.scan", limit: 1 } } })`. Read `newInApp = (scans.data?.[0]?.progress as { new_in_app?: Record<string, number> })?.new_in_app ?? {}`.
  - **Add two buttons** above the table: **Refresh**, which POSTs `/scans`, and **Sync**, which POSTs `/syncs` with `{}`. Each shows a toast on success:
    - "Refreshing every app…" for Refresh.
    - "Syncing every app…" for Sync, linking to `/settings/jobs/${j.id}`, the way the old push toast did.
  - **Refetch until done.** While the started job is queued or running, poll `GET /jobs/{id}` every 2s with `refetchInterval`. When it finishes, invalidate `["search", "card", "sync-counts"]`, `["jobs", "target.reconcile"]` and `["jobs", "target.scan"]`. Disable each button while its own job runs.
  - **Add a `New in app` column** after `Drifted`, showing `newInApp[String(i.id)] ?? "–"`.
  - **Keep the Last sync column.** Recorded reconcile jobs appear in `target.reconcile`, so the per-app link still opens that app's queue. Show `applied: <n>` after the age, where `n` is the sum of the last run's `totals.auto_bound`, `auto_imported` and `auto_pulled`. `summary()` keeps totals in list progress.

- [ ] **Step 4: Write the failing panel test**

In `web/src/card/Card.test.tsx`:
- Add `autosync: false` to the stubbed binding.
- Add the test "the autosync box turns autosync on for that app". It checks the checkbox labelled `Autosync to Lumiverse` and expects a `PATCH` to `/api/bindings/1` with `{ autosync: true }`.

Route PATCH in the fetch stub the way the file routes other methods.

- [ ] **Step 5: Add the toggle**

In `Panel.tsx`'s `Sync` section, add to each binding row, after the status text:

```tsx
<label className="autosync">
  <input
    type="checkbox"
    checked={b.autosync}
    aria-label={`Autosync to ${name(b.instance_id)}`}
    disabled={setAutosync.isPending}
    onChange={(e) => setAutosync.mutate({ id: b.id, on: e.target.checked })}
  />{" "}
  autosync
</label>
```

with a mutation in the `Sync` component:

```tsx
const setAutosync = useMutation({
  mutationFn: async ({ id, on }: { id: number; on: boolean }) =>
    must(await api.PATCH("/bindings/{id}", { params: { path: { id } }, body: { autosync: on } })),
  onSuccess: () => qc.invalidateQueries({ queryKey: [kind, id, "bindings"] }),
  onError: (e) => toast({ text: e.message, error: true }),
});
```

Use the names the file already has for the query client, toast, and the entity's `kind` and `id`.

- [ ] **Step 6: Run the web tests and type-check**

Run: `cd web && npm test && npx tsc --noEmit -p .`
Expected: everything passes. If the repo has a lint script (`npm run lint`), run it too.

- [ ] **Step 7: Commit**

```bash
git add web/src
git commit -m "feat(web): one Refresh and one Sync, and autosync per app on the card"
```

---

### Task 5: full check

- [ ] **Step 1:** `go vet ./... && INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -count=1 -timeout 20m ./...`. Every package `ok`.
- [ ] **Step 2:** `cd web && npm run gen && git diff --exit-code -- openapi.json src/api/schema.d.ts && npm test`. No diff, and all tests pass.
- [ ] **Step 3:** `git log --format=%B <base>..HEAD | grep -E '!:|BREAKING'` prints nothing.
