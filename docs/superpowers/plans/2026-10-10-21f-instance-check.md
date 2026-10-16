# Instance check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `POST /instances/{id}/check` tests an instance now, and `ink instances check` and the web's instance settings use it.

**Architecture:** `Registry.Check` starts the instance's plugin even when it's marked broken, through a new `Manager.Start`. A pull target then answers `target.list`, which reaches the app with the instance's address and login. The route replies `200` with `{"ok": true}` or `{"ok": false, "error": "..."}`, so the CLI and the web show why a check failed instead of a generic `502`.

**Tech Stack:** Go 1.27, huma, cobra, React.

Ticket: INN-21, slice 21f. The slices are listed in `docs/superpowers/plans/2026-10-07-21a-ink-client.md`.

Decisions recorded during planning:

- **No protocol change.** The protocol has no health method, and `initialize` must not contact the app. Starting the plugin tests what `broken` means: the plugin starts with these settings. `target.list` is the only existing call that every pull target answers from the app, so it tests the address and login. `ink`'s and the web's linking views already call it. It reads the app's whole list, so a check of a large library takes a moment. Push-only, import, codec, catalog, MCP, and auth plugins get the start test only.
- **A failed start marks the instance broken**, as any call that starts it does. **A good start clears `broken_error` and `broken_at`**, so the owner can retry a plugin that failed for a passing reason without editing its settings. Both updates apply only if the instance's version hasn't changed since the check read it.
- **An app error doesn't mark the instance broken.** Broken stops every job until the owner edits the instance, and a down app or a rejected login is the app's state, not the plugin's. The check reports it and leaves the stored state alone.
- **The route needs `tier.Read`**, like `check-binding` and the app item lists: it changes no settings, and a read token can already start the plugin and list the app.
- The route reports the start error text, which is what `GET /instances` already returns as `broken_error`. Database errors still become `500 internal error`.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/pluginhost/manager.go` | `Manager.Start`: start an instance's plugin without a call |
| `internal/plugins/plugins.go` | `Registry.Check`, and a way to load a broken instance |
| `internal/api/instances.go` | `POST /instances/{id}/check` |
| `internal/api/tiers_test.go` | `check-instance` is a read |
| `cmd/ink/instances.go` | `ink instances check` calls the route |
| `web/src/settings/Instance.tsx` | A Check button beside the broken notice |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `README.md` | The new `instances check` behavior |

---

### Task 1: `Registry.Check`

**Files:**
- Modify: `internal/pluginhost/manager.go`, `internal/plugins/plugins.go`
- Test: `internal/api/instances_test.go`

- [ ] **Step 1: Write the failing test**

`TestInstanceCheck` checks, through the route:

- A working target instance replies `{"ok": true}`.
- A broken instance whose plugin starts replies `ok` and has no `broken_error` afterwards.
- A target whose `target.list` fails (`fail_list` in its config) replies `ok: false` with the app's error, and isn't marked broken.
- An instance whose plugin can't start (the `vault` manifest has no binary) replies `ok: false` and is marked broken.
- An unknown ID is `404`.

- [ ] **Step 2: Implement**

`Manager.Start` acquires and releases the instance's process. `Registry.Check(ctx, mgr, id) (problem, err error)` loads the instance ignoring `broken_error`, starts it, marks or clears broken, then calls `target.list` for a `pull` plugin.

- [ ] **Step 3: Run the test, then commit**

Commit: `feat(api): POST /instances/{id}/check starts an instance's plugin and tests its app`

### Task 2: `ink instances check`

- [ ] Change `TestInstances` to stub `POST /instances/{id}/check`, then point the command at it. Commit: `feat(ink): instances check tests the instance now`

### Task 3: Web

- [ ] Regenerate the schema with `npm run gen`. Add a Check button to `InstanceForm` for an existing instance; it shows "Works." or the error and refetches the instance. Run `npm run typecheck` and `npm test`. Commit: `feat(web): check an instance from its settings`

### Task 4: README and verify

- [ ] Update the `instances check` text. Run `gofmt -l .`, `go vet ./...`, and the tests with `INNKEEPER_TEST_DATABASE_URL` set. Commit: `docs: instances check tests the instance now`
