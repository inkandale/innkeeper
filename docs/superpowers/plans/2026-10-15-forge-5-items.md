# Forge Phase 5: Plugin-Owned Items Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A plugin can keep data the owner manages on the instance's settings page, through a generic `items` capability. An action plugin can list the skills a `@skills/` mention may name.

**Architecture:** INN-58, the core half of phase 5 of `docs/superpowers/specs/2026-10-14-forge-design.md`. The protocol section in `docs/protocol.md` (`items`, and `skills` in `action.list`) is committed first; the forge-ai plugin builds against it later in `innkeeper-plugins`.

- **Name:** the capability stays `items`. It says what the core sees, data it doesn't understand, and matches the manifest table `[items]`.
- **Manifest:** `[items.<type>]` with `label`, `schema` (a JSON Schema object), and optional `import` (`file`, `url`). Declaring `items` without a type, or a type without `items`, fails validation.
- **Protocol:** `items.list`, `items.get`, `items.put`, `items.delete`, `items.import`. `-32004` for an unknown ID, `-32602` for bad input with a message the owner reads.
- **API:** core stores nothing; each route checks the instance and type, then calls the plugin. No cache: items change through these routes.
- **Skills:** `action.list` gains optional `skills`. `GET /actions/skills` lists them per instance through the existing list cache.
- **Web:** an `Items` section on the instance settings page, one per item type, generic over the type's schema.

**Tests:** DB tests skip without the database URL. `$DB` means `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.

**Deviations from the spec:**
- Skills come from a new route, `GET /actions/skills`, not from `GET /actions`. Phase 4's UI, built in parallel, reads `GET /actions` as an array of actions; changing its shape would break it.
- `context.skills` stays unchecked by core: the plugin replies `-32602` for a skill it doesn't have, and the run fails with that message. Checking would make every run wait on `action.list`.
- An item type declares `import`, as an import plugin declares `inputs`, so the UI offers Import only where the plugin takes it.
- `SchemaForm` renders a string with `contentMediaType` as a textarea, so a skill's body is editable.

## Tiers

| Route | Tier | Why |
|---|---|---|
| `GET /instances/{id}/items/{type}` | read | Lists |
| `GET /instances/{id}/items/{type}/{item}` | read | Reads |
| `POST /instances/{id}/items/{type}` | write | Creates; deleting the item undoes it |
| `PUT /instances/{id}/items/{type}/{item}` | destructive | Replaces; the old item is gone |
| `DELETE /instances/{id}/items/{type}/{item}` | destructive | Can't be undone |
| `POST /instances/{id}/items/{type}/import` | destructive | May replace items with the same ID |
| `GET /actions/skills` | read | Lists |

Import sends the file as the `application/octet-stream` body with `?name=`, or no body and `?url=`. The core writes the body to a new file in the instance's `blob_tmp`, calls `items.import` with its path, and deletes the file after the reply.

## File map

| File | Change |
|---|---|
| `protocol/items.go`, `items_test.go` | Methods, params, replies, `ItemSummary.Validate`, `Skill.Validate` |
| `protocol/action.go` | `ActionListResult.Skills` |
| `protocol/manifest.go`, `manifest_test.go` | `items` capability, `[items]` table, validation |
| `internal/fakeplugin/items.go`, `main.go`, `actions.go` | In-memory `notes` items; skills in `action.list` |
| `internal/api/items.go`, `items_test.go` | Item routes |
| `internal/api/actions.go`, `actions_test.go` | `GET /actions/skills` |
| `internal/api/instances.go` | `items` on `GET /plugins` |
| `internal/contract/items.go`, `action.go`, `contract.go`, `contract_test.go` | Items checks; skills in the action list check |
| `web/src/settings/Items.tsx`, `Items.test.tsx`, `Instance.tsx`, `SchemaForm.tsx` | The items section |

---

### Task 1: Protocol and manifest

- [ ] Failing tests: `TestManifestItems` (valid type; `items` capability without types; types without the capability; bad type key; empty label; schema not an object; unknown import value; repeated import value; nested schema tables aren't unknown keys). `TestItemSummaryValidate`, `TestSkillValidate`.
- [ ] Implement `protocol/items.go`, the manifest table, `Skills` on `ActionListResult`.
- [ ] `go test ./protocol/`; commit `feat(protocol): item types in the manifest and the items methods`.

### Task 2: Fake plugin

- [ ] `notes` item type in an in-memory map seeded with one note; `-32004` and `-32602` as the protocol says; import reads the file or takes the URL's last segment as the note. `action.list` lists two skills.
- [ ] Commit `test(fakeplugin): keep items and list skills`.

### Task 3: API

- [ ] Failing tests in `internal/api/items_test.go`: list, get, create, replace, delete round trip; unknown type `404`; unknown item `404`; plugin's `-32602` message reaches the caller as `400`; import of an uploaded file reaches the plugin as a path inside `blob_tmp` that is gone afterwards; import by URL; read tier can list but not create; write tier can create but not delete; `GET /plugins` shows the item types. `TestListSkills` in `actions_test.go`.
- [ ] Implement; `$DB go test ./internal/api/`; commit `feat(api): proxy plugin items and list action skills`.

### Task 4: Plugin checks

- [ ] Failing tests: the fake with `items` passes; `-mode=bad-items` fails the list check; `-mode=bad-actions` with a bad skill fails the action list check.
- [ ] Implement; commit `feat(contract): check items plugins and action skills`.

### Task 5: Web

- [ ] `npm run gen`.
- [ ] Failing tests in `Items.test.tsx`: lists items per type; New opens the schema form and saves with `POST`; Edit loads the item and saves with `PUT`; Delete asks and sends `DELETE`; Import uploads a file with its name, and imports a URL; a type without `import` shows no Import.
- [ ] Implement; `npm run typecheck && npm test`; commit `feat(web): manage a plugin instance's items`.

### Task 6: Verify

- [ ] `$DB go vet ./... && $DB go test -race ./...`, then `cd web && npm run typecheck && npm test`.
- [ ] Read `git diff origin/main...HEAD` for dead code and copies.
