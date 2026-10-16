# Query push confirmation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A query push never silently replaces an app copy that changed after the owner agreed to replace it.

**Architecture:** A new route, `POST /pushes/preview`, counts how the matching cards stand on the target and returns a `confirm` value. `POST /pushes` takes that `confirm`, refuses with `409` if the preview would differ now, and gives each child push job the hash of the app copy it may replace. Before it replaces a copy, the job reads the app's copy and fails if the hash differs. `ink sync push` and the web's bulk push both ask from the preview and send its `confirm`.

**Tech Stack:** Go 1.27, huma, pgx; React and TanStack Query in `web/`.

Ticket: INN-21, slice 21g.

Decisions recorded during planning:

- `confirm` covers what the owner saw: which cards match and, for each bound one, its binding version, remote state, and the app copy hash it would replace. That hash is the hash of the copy the last check saw for a drifted binding, and the accepted hash otherwise.
- The preview counts states as of each binding's last check, as the search facet does. Reading every app copy for the preview would be slow; the job reads each one anyway, just before it replaces it, so a change made after the last check is still caught.
- A child job whose app copy no longer matches fails with an error that says so. A skip would hide it in the job's progress; a failure shows in the bulk job's counts.
- `POST /pushes` without `confirm` still works, and is safe: each job may replace only a copy that matches the last push or adopt, so every drifted copy fails instead of being overwritten. Overwriting drift takes a preview, so no flag is needed to be safe.
- `--yes` skips the question only. `ink sync push` still fetches the preview and sends its `confirm`, so a script replaces only what the preview counted, and a copy that changes afterwards fails.
- A target that can't read its copies back can't be checked, at preview or at push. Its cards count as "can't check" and are pushed, as now.
- A re-run of a child job that finds a newer push skips it as already pushed, rather than failing on the copy the earlier run wrote.

Not in this slice: a single-card push queued after its preview isn't re-checked when the job runs.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/wire/sync.go` | `BulkPreview` |
| `internal/targets/bulk_preview.go` | `PreviewBulkPush`, `PreviewBulkLorebookPush`, and the per-card hashes |
| `internal/targets/jobs.go` | `EnqueueBulkPush` takes `confirm`; the child payload carries `app_hash` |
| `internal/targets/push.go` | The job reads the app's copy and fails if it changed |
| `internal/api/targets.go` | `POST /pushes/preview`; `confirm` on `POST /pushes` |
| `cmd/ink/sync.go` | `askBulkPush` uses the preview and sends `confirm` |
| `web/src/cards/SelectionBar.tsx`, `web/src/sync/sync.ts` | The bulk push panel uses the preview and sends `confirm` |
| `README.md` | Query push confirmation |

---

### Task 1: The job refuses a changed copy

**Files:** `internal/targets/push.go`, `internal/targets/jobs.go`, `internal/targets/jobs_test.go`

- [ ] **Step 1: Write the failing tests.** A bulk push queued without `confirm` fails a card whose app copy was edited, even with no check since, and leaves the edit in the app; it pushes the other card. A re-run after a successful push skips as already pushed.
- [ ] **Step 2: Implement.** `pushPayload.AppHash`; `pushExpect.appHash`. In `push`, after the lock, for an update on a target that can pull, fetch the copy and return `ErrAppChanged` on a different hash. `RunPush` fails the job on it.
- [ ] **Step 3: Commit** `feat(targets): a query push fails a card whose app copy changed`

### Task 2: Preview and `confirm`

**Files:** `internal/targets/bulk_preview.go`, `internal/targets/jobs.go`, tests

- [ ] **Step 1: Write the failing tests.** The preview counts new, ahead, in sync, can't check, and drifted. Its `confirm` changes after a check records an app edit. A push with that `confirm` replaces the drifted copy it showed; one with a stale `confirm` returns `StalePreviewError`.
- [ ] **Step 2: Implement**, computing the hashes once in `enqueueBulkPush` so the check and the payloads agree.
- [ ] **Step 3: Commit** `feat(targets): preview a query push and confirm it`

### Task 3: API

**Files:** `internal/api/targets.go`, `internal/api/targets_test.go`

- [ ] `POST /pushes/preview`; `confirm` on `POST /pushes`, `409` with the new preview on a mismatch.
- [ ] Commit `feat(api): preview a query push and refuse a stale confirm`

### Task 4: `ink sync push`

**Files:** `cmd/ink/sync.go`, `cmd/ink/sync_test.go`

- [ ] `askBulkPush` posts to `/pushes/preview`, prints its counts, asks, and returns `confirm` for the push.
- [ ] Commit `feat(ink): a query push sends the preview's confirm`

### Task 5: Web

**Files:** `web/src/cards/SelectionBar.tsx`, `web/src/sync/sync.ts`, `web/src/api/schema.d.ts`, tests

- [ ] Regenerate the schema. The push panel reads `/pushes/preview` for its summary and sends `confirm`.
- [ ] Commit `feat(web): bulk push sends the preview's confirm`

### Task 6: README and verify

- [ ] README: what a query push asks, what `confirm` guards, and what happens without it.
- [ ] Run `gofmt -l .`, `go vet ./...`, the DB test run, and the web checks.
