# INN-65d: Scoping API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The owner can create, rename, and delete libraries, and the API and MCP read and write any library, with collections and variants kept inside one.

**Architecture:** The backend half of build step 4 of the [libraries design](../specs/2026-10-16-libraries-design.md). The web UI comes next, on this branch.

- **Libraries.** `internal/library/libraries.go` lists, creates, renames, and deletes `library` rows. Deleting runs as a `library.delete` job in `internal/bulk`: it deletes the library's cards and lorebooks 500 at a time, each batch in one transaction with what hangs off them (bindings and their pushes, sources, enrichments, lorebook links, collection memberships, the detection queue), then the library's collections and its row. No card delete exists to reuse, so this is the first. Revision rows refuse deletes, so migration 0033 lets a transaction that sets `innkeeper.delete` delete them, as `innkeeper.restore` already does. Garbage collection then removes blobs no revision references.
- **`library` parameter.** `Query.Library` becomes the JSON field `library`, so search, facets, bulk actions, and enrich queries take it. Create, fork, and import take it in their bodies; `/tags`, `/collections`, and `/source-checks` as a query parameter. The API checks the library exists and answers 422 when it doesn't. `Entities.Create` and `CreateFromName` take the library; `CreateFromNameTx`, used only by imports from target apps, keeps library 1.
- **Forks.** `Fork` takes a target library, 0 for the original's. Every fork gets a source row of kind `innkeeper` whose ref is the original's ID (`library.ForkSourceKind`). Only a fork into the same library becomes a variant of the original and copies its collections.
- **Scoping.** Migration 0033 gives `collection` a `library_id` (default 1) with names unique per library. `AddToCollection` and `SetVariantOfTx` refuse a card in another library with `ErrInvalid`.
- **In other libraries.** `Entities.InOtherLibraries` runs the similar lookup's SQL with the library condition flipped; `GET /{cards,lorebooks}/{id}/in-other-libraries` returns each match with its `library_id`.
- **Library-wide jobs.** A source check takes a library (payload `library`, default 1), follows only that library's sources, and reads binding IDs 500 at a time. An enrich run started from a query keeps the query (with its library and score instance) in its payload and reads card IDs a batch at a time, instead of holding up to 10,000 IDs.
- **MCP.** `list_libraries`; `search_cards`, `create_card`, `create_lorebook`, `start_import`, and `fork_card` take `library`.

**Tech Stack:** Go, PostgreSQL, huma, the MCP Go SDK.

---

## Context for the implementer

- Worktree `~/git/innkeeper-inn-65-scoping`, branch `inn-65-scoping`, stacked on `inn-65-import`. Never touch `~/git/innkeeper`.
- Run Go tests with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`. The `internal/library` and `internal/api` packages take minutes; use `-run`.
- Never hand-edit `web/openapi.json` or `web/src/api/schema.d.ts`; run `npm run gen` in `web/`.

## Decisions

| Topic | Decision |
|---|---|
| Missing library | 422 naming the library, before any job is queued. |
| Default library | Can't be deleted: 409. |
| Delete confirmation | `DELETE /libraries/{id}` with body `{"confirm": "<the library's name>"}`; anything else is 422. Destructive tier. Returns the job, 202. |
| Collections | Their own `library_id`, the simplest way to keep an empty collection scoped. |
| Fork library | Defaults to the original's library rather than 1, so forking a card in another library keeps it there. |
| Fork link | A source row on every fork, so code can find the original whether or not the fork is a variant. |
| Lorebook links | Not scoped; the design scopes only collections and variants. |
| Source check schedule | The scheduled check has no payload, so it checks library 1. A start joins a running check even for another library, as sync does. |
| Enrich cap | A query run is still capped at `enrich.MaxCards`, counted before it is queued. |

## Files

- Create: `internal/db/migrations/0033_scoping.sql` (0032 is taken on `inn-65-import`), `internal/library/libraries.go`, `internal/api/libraries.go`.
- Modify: `internal/db/db_test.go` (32 migrations on this branch, 33 once rebased onto `inn-65-import`).
- Modify: `internal/library/entities.go`, `import.go`, `variants.go`, `organize.go`, `similar.go`, `search.go`.
- Modify: `internal/bulk/bulk.go` (delete job), `cmd/innkeeper/worker.go`.
- Modify: `internal/targets/sources.go`, `jobs.go`; `internal/enrich/enrich.go`.
- Modify: `internal/api/server.go`, `entities.go`, `search.go`, `organize.go`, `jobs.go`, `targets.go`, `enrichments.go`; `internal/mcp/tools.go`.
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`.

### Task 1: Libraries in the store

- [ ] Tests in `internal/library/libraries_test.go`: `TestLibraryCRUD` (create, list with counts, rename, a taken name is `ErrNameTaken`, an empty one `ErrInvalid`, missing is `ErrNotFound`); `TestDeleteLibraryRemovesItsCardsAndBlobs` (cards and lorebooks with revisions, a binding, a source, an enrichment, links, a collection, and variants in library 2; one avatar used only there and one shared with library 1; after `DeleteLibrary` and `CollectGarbage(0)` library 2 and its rows are gone, library 1's card and the shared blob stay); `TestDeleteDefaultLibraryRefused`.
- [ ] Run, see them fail. Write migration 0033 and `libraries.go`; give `Create` and `CreateFromName` a library argument and update their callers.
- [ ] Run, commit `feat(library): create, rename, and delete libraries`.

### Task 2: Scoping in the store

- [ ] Tests: `TestVariantRefusesOtherLibrary`, `TestCollectionRefusesOtherLibrary` (and `Collections` lists one library), `TestForkIntoOtherLibrary` (no variant link, no collection copy, a source row naming the original; a same-library fork keeps today's behavior and also gets the row), `TestInOtherLibraries`.
- [ ] Run, see them fail. Implement.
- [ ] Run, commit `feat(library): keep collections and variants in one library`.

### Task 3: API

- [ ] Tests in `internal/api/libraries_test.go`: CRUD routes and their errors; delete needs the exact name, refuses library 1, and its job removes the library; `library` on search, create, fork, import, tags, collections, and source checks, with 422 for a missing one; in-other-libraries.
- [ ] Run, see them fail. Implement the routes and the job handler; register it in `cmd/innkeeper/worker.go`.
- [ ] Run, commit `feat(api): libraries and the library parameter`.

### Task 4: Library-wide jobs

- [ ] Tests: a source check for library 2 checks only library 2's bindings (`internal/targets`); an enrich query run reads its cards from the query and counts them (`internal/enrich` or `internal/api`).
- [ ] Run, see them fail. Implement paging.
- [ ] Run, commit `feat(jobs): run source checks and enrich queries on one library`.

### Task 5: MCP

- [ ] Tests in `internal/api/mcp_test.go`: `list_libraries`, and `create_card`, `search_cards`, `fork_card`, `start_import` with `library`; bump the built-in tool count.
- [ ] Run, see them fail. Implement. Commit `feat(mcp): list libraries and take a library`.

### Task 6: Schema and checks

- [ ] `cd web && npm run gen && npm run typecheck && npm test`; commit `chore(web): regenerate the API schema`.
- [ ] `go vet ./...`, `go test -race ./...` with the database variables.
