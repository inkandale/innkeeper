# Access Tiers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give every API route and every credential a tier, `read`, `write`, or `destructive`, and refuse requests above the caller's tier. Add the push `confirm` check.

**Architecture:** `internal/tier` defines the ordered tiers. Each Huma operation declares its tier in `Metadata["tier"]`, defaulting from the HTTP method. The authentication middleware resolves the caller's tier (a session is `destructive`; an API token has its own) and refuses higher operations with `403`. Push routes take an optional `confirm` that must match the current preview.

**Tech Stack:** Go 1.27, Huma v2, PostgreSQL. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-05-mcp-server-design.md`, "Tiers" and "Push confirmation". Build order step 1.

**Kaneo:** INN-7.

---

## Design notes

- **Caller tier in context.** The middleware stores the tier with `huma.WithValue`, so handlers that need it (token creation, instance updates with secrets) read it back.
- **Dynamic tier.** `PATCH /instances/{id}` and `POST /instances` are `write`, and their handlers refuse `secrets` unless the caller is `destructive`. Huma resolves the body after the middleware, so the check lives in the handler.
- **Tokens.** `api_token.tier` is `NOT NULL` with a default of `read`. The migration sets existing rows to `destructive` before adding the default, so the CLI's tokens keep working. `POST /tokens` and `innkeeper token create <name> [tier]` take a tier, default `read`. The API refuses a tier above the caller's.
- **Route table test.** A test walks the OpenAPI document and fails if an operation has no explicit tier where its method would mislead: every non-`GET` operation must declare one. That keeps a new route from silently defaulting to `write` when it should be `destructive`.
- **Confirm.** `targets.Preview` gains `Confirm`: the hex SHA-256 of the JSON of the preview without `Confirm`. Push routes take an optional `confirm` body field. When present, the handler computes the preview again and refuses with `409` and the new preview if the hashes differ. A push job, once queued, doesn't check again: the preview and the enqueue happen in one request, and the job's own expectations guard re-runs.

## Changes during the build

Review found that instance config at `write` let a `write` token raise its own access: point a target's URL at its own host and trigger a read-tier drift check to receive the plugin's secrets, change a login plugin's trust settings, or move the import folder. Creating and changing instances is `destructive`, with no per-field check. Deleting a collection is `destructive`, since collections have no history.

## Route tiers

| Tier | Operations |
|---|---|
| read | every `GET`; `POST /search`; `POST /bindings/{id}/check` |
| write | create, update, archive, restore, link and unlink, tags, collections, blobs, imports, bulk archive, adopt, cancel a job, create and update instances without secrets, login and logout routes (public anyway) |
| destructive | `POST /cards/{id}/push`, `POST /lorebooks/{id}/push`, `POST /pushes`, `DELETE /bindings/{id}`, `DELETE /instances/{id}`, `POST /tokens`, `DELETE /tokens/{id}`, instance writes with `secrets` |

`POST /tokens` is `destructive` because a token can grant access; the tier cap stops escalation. Revoking a token is `destructive` because it can lock out a client.

## Tasks

### Task 1: `internal/tier`

- [ ] Test `Parse`, `Allows`, and `String`, and that an unknown name fails.
- [ ] Implement:

```go
// Package tier orders what a caller may do: read, write, or destructive.
package tier

type Tier int

const (
	Read Tier = iota + 1
	Write
	Destructive
)

func Parse(s string) (Tier, error)
func (t Tier) String() string
// Allows reports whether a caller with t may do something needing need.
func (t Tier) Allows(need Tier) bool { return t >= need }
```

- [ ] Commit.

### Task 2: Token tiers

- [ ] Migration `0006_tiers.sql`: `ALTER TABLE api_token ADD COLUMN tier text NOT NULL DEFAULT 'destructive' CHECK (tier IN ('read', 'write', 'destructive'))`, then `ALTER COLUMN tier SET DEFAULT 'read'`.
- [ ] `auth.CreateToken(ctx, name, tier)`; `Token.Tier`; `CheckToken` returns the token's tier, or 0 when it isn't a token.
- [ ] Tests: existing rows migrate to `destructive`; a new token keeps its tier; `CheckToken` returns it.
- [ ] CLI: `innkeeper token create <name> [read|write|destructive]`, default `read`; `token list` shows the tier.
- [ ] Commit.

### Task 3: Route tiers and enforcement

- [ ] Helper `op(tier.Tier) map[string]any` for `Metadata`, merging with `public`.
- [ ] Annotate the operations in the table above.
- [ ] Middleware: resolve the caller's tier, store it in the context, refuse an operation above it with `403` "this token's tier is read; the operation needs write".
- [ ] Tests: a `read` token can `GET` and `POST /search` but gets `403` on `POST /cards`; a `write` token can create but not push; a `destructive` token can push; a session can push; the route table test.
- [ ] Commit.

### Task 4: Secrets and token creation

- [ ] Instance create and update refuse `secrets` below `destructive` with `403`.
- [ ] `POST /tokens` takes `tier` (default `read`), refuses one above the caller's with `403`; the reply includes the tier.
- [ ] Tests for both.
- [ ] Commit.

### Task 5: Push confirm

- [ ] `Preview.Confirm`, computed in `preview`.
- [ ] Push routes: optional `confirm`; mismatch is `409` with the new preview in the error detail.
- [ ] Tests: push with no confirm works; a valid one works; after an edit, the old one is refused.
- [ ] README: tiers, token creation, confirm. Commit.

## Plan series

- **Plan 7b (MCP):** tools call the API in process with the caller's `Authorization` header, so tiers apply unchanged. `tools/list` filters by the caller's tier, read from the same token check.
- **Plan 7d (OAuth):** OAuth access tokens carry a tier as their scope, and are valid only on `/mcp`.
