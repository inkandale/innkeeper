# Reconcile Surfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Drive reconcile, compare, and resolve from the HTTP API, the CLI, and MCP, so the owner can work through an app from a terminal or an agent, and the web UI has every endpoint it needs.

**Architecture:** Thin huma routes over the `targets` service (13e, 13f), CLI commands over those routes, and MCP tools over the same routes, as every other surface is built. A live, opt-in check runs reconcile against a real Lumiverse.

**Tech Stack:** Go, huma, the official MCP Go SDK. No new dependencies.

**Spec:** `2026-10-05-reconcile-design.md`, Surfaces.

**Kaneo:** INN-29, part of INN-22.

---

## Design notes

- **Routes** (`internal/api/reconcile.go`):

  | Method and path | Tier | Body or query | Reply |
  |---|---|---|---|
  | `POST /reconciles` | read | `ReconcileParams` | the job (202) |
  | `GET /instances/{id}/items/{remote_id}/compare` | read | `card_id` (optional) | `Comparison` |
  | `GET /instances/{id}/lorebook-items/{remote_id}/compare` | read | `lorebook_id` (optional) | `Comparison` |
  | `POST /instances/{id}/items/{remote_id}/resolve` | write | `Resolution` without `instance_id` and `remote_id` | `ResolveResult` |
  | `POST /instances/{id}/lorebook-items/{remote_id}/resolve` | write | same | `ResolveResult` |
  | `POST /resolutions` | write | `{"resolutions": [Resolution]}` | the job (202) |

  `POST /reconciles` is tier read although it starts a job: it changes nothing unless the instance has `auto_bind_exact`, which only a destructive-tier `PATCH /instances/{id}` can turn on. `remote_id` is path-escaped; Lumiverse IDs are UUIDs and SillyTavern's are file names such as `Brakka.png`. Errors map as 13f lists: a `*StaleError` is a 409 whose body includes the fresh `comparison`.
- **Snapshots** are read with the existing `GET /jobs/{id}`; nothing new.
- **CLI** (`cmd/innkeeper/reconcile.go`), all through the API client:
  - `innkeeper reconcile <instance> [params-json]` starts the job and watches it like `jobs watch`, then prints a table: verdict, remote ID, name, top candidate, suggested action. `--json` prints the snapshot.
  - `innkeeper compare <instance> <remote-id> [card-id]` prints each field with non-`same` hunks: the hunk ID, status, and the library and app text, with changed words marked `[-…-]` and `{+…+}`.
  - `innkeeper resolve <instance> <remote-id> <action> [card-id] [hunk=choice …] [--default=<choice>]` sends one resolution with the `app_hash` from a fresh compare. The owner reads the compare first, so this is not a blind write.
  - `innkeeper resolve --suggested <job-id> [--verdict=exact,likely]` sends every suggestion in a snapshot, optionally only for some verdicts, as one bulk job.
- **MCP** (`internal/mcp/tools.go`): `start_reconcile` (read), `compare_item` (read), `resolve_item` (write), `resolve_items` (write). `get_job` already reads snapshots. Descriptions say that `resolve_item` needs the `app_hash` from `compare_item`, and that pushing is a separate step.
- **Live check** (`internal/targets/reconcile_live_test.go`): skips unless `INNKEEPER_LIVE_LUMIVERSE_URL`, `_USERNAME`, and `_PASSWORD` are set. It reconciles against that server using a copy of the library from `INNKEEPER_LIVE_DATABASE_URL` and prints the totals and the ten closest `likely` items. It never writes to the app, and refuses to run if the instance has `auto_bind_exact`.

## Tasks

### Task 1: Routes

**Files:** `internal/api/reconcile.go`, `internal/api/reconcile_test.go`, `internal/api/server.go`, `internal/api/tiers_test.go`

- [ ] Tests through the HTTP server with the fake plugin: start a reconcile and read the snapshot from `/jobs/{id}`; compare a pair; resolve one with a stale `app_hash` and get 409 with `comparison`; resolve with an unknown hunk and get 422; bulk resolve and read per-child results; a remote ID containing `/` and `.png` round-trips.
- [ ] Tier entries for each route.
- [ ] Implement. Commit: `feat(api): reconcile, compare, and resolve app items`.

### Task 2: CLI

**Files:** `cmd/innkeeper/reconcile.go`, `cmd/innkeeper/reconcile_test.go`, `cmd/innkeeper/main.go`

- [ ] Tests against a stub API, as `commands_test.go` does: the reconcile table's columns; the compare output for one prose conflict, with the word markers; `resolve` sends the fetched `app_hash` and the parsed choices; `--suggested --verdict=exact` sends only exact items.
- [ ] Implement and add all four to `usage`. Commit: `feat(cli): reconcile, compare, and resolve from the terminal`.

### Task 3: MCP

- [ ] Tests in `internal/api/mcp_test.go`: each tool calls its route and returns the reply; `resolve_item` is hidden from a read-tier token.
- [ ] Implement. Commit: `feat(mcp): reconcile, compare, and resolve tools`.

### Task 4: Docs and live check

- [ ] README: a "Reconciling an app" section: run a reconcile, read it, compare one item, resolve it, resolve the suggestions in bulk, then push. Every command in it is run against a local stack first.
- [ ] `docs/protocol.md` needs nothing; say so in the PR.
- [ ] Live test per the design notes. Run it against the homelab Lumiverse with a restored copy of today's Innkeeper backup, and paste the totals into the PR.
- [ ] Commit: `docs: reconcile an app from the CLI and MCP`. Push, open the PR.
