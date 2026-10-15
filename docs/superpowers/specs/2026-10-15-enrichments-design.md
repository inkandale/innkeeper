# Enrichments design

Date: 2026-10-15

## Purpose

Phase 7 of the [Forge](2026-10-14-forge-design.md). An **enrichment** is a
value a plugin computes about a card revision, such as a quality score. The
[core design](2026-10-03-innkeeper-core-design.md#search-and-enrichment)
names the `enrich` capability, the `enrichment` table, and the manifest's
`[enrichments]` declarations. This spec makes them concrete, and forge-ai
becomes the first enricher, storing its evaluate scores.

## Decisions

| Topic | Decision |
|---|---|
| Runs | Only when the owner asks: a bulk action on a selection, or Evaluate on the card page. Nothing runs on save. |
| Scores instance | A library setting names one `enrich` instance. The grid, filters, and sort use its values. The card page shows every instance's values. |
| Stale values | A value on a revision that is no longer the head is stale. Filters and sort still use it; the UI marks it stale. |
| History | Every result is kept, so results from different models and revisions can be compared. |
| forge-ai keys | `quality` and the six dimension scores, each 0 to 100. |
| Entities | Cards only. |
| MCP and CLI | Search filters and sort reach MCP through the shared search code. No new tools. |

## Protocol

### Manifest

`[enrichments]` already exists: each key has a `label`, a `type` (`text`,
`score` with `range`, or `list`). This spec adds one optional field:

| Field | Meaning |
|---|---|
| `grid` | `true` to show the key on the library grid. At most one key per plugin |

### `enrich.run`

Params:

| Param | Meaning |
|---|---|
| `run` | Run token, as in `action.run` |
| `revisions` | `[{"card_id", "revision_id", "data"}]`, at most 20 per call |

The plugin sends `progress` with `run`, `message`, `done`, and `total` while
it works.

Reply:

```json
{"results": [
  {"revision_id": 812, "values": {"quality": 78, "voice": 88}, "meta": {"model": "…", "prompt": "evaluate-2"}},
  {"revision_id": 813, "error": "the model returned no JSON"}
]}
```

- `values` holds only keys the manifest declares, each matching its type, and
  a score within its range. Core drops anything else with a warning in the
  job's progress.
- A revision with `error` stores nothing. The rest of the batch still counts.
- A revision missing from `results` counts as an error.

## Core

### Table

`enrichment`: `id`, `card_id`, `revision_id`, `instance_id`, `key`, `value`
(JSON), `meta` (JSON), `created_at`. Indexed on `(card_id, key, instance_id,
created_at desc)`. Deleting an instance keeps its rows; deleting a card
deletes them.

The **current value** of a key for a card and instance is its newest row. It
is stale when its `revision_id` isn't the card's head.

### Job

A job kind, `enrich`, with the instance as `InstanceID` and the card IDs in
its payload. It sends the head revisions in batches of 20 and stores each
result. Progress counts cards done and failed. A failed batch fails only its
cards; the job fails only when every batch does. Stop is job cancel.

### Settings

`PATCH /settings` gains `score_instance`: an `enrich` instance ID, or `null`.
When unset and exactly one `enrich` instance exists, the core uses that one.

### API

| Method and path | Tier | Does |
|---|---|---|
| `POST /enrichments/runs` | write | Body: `instance_id`, `card_ids` (at most 10,000). Returns `202` with the job |
| `GET /cards/{id}/enrichments` | read | Every instance's current values, each with `label`, `type`, `range`, `stale`, `meta`, and `created_at`; `?history=true` adds older rows |
| `GET /enrichments/keys` | read | Each key the score instance's plugin declares, with its label, type, range, and `grid` |

### Search

- **Filter:** `{"enrichment": "quality", "gte": 70, "lte": 100}` matches cards
  whose current value from the score instance is in range. `{"enrichment":
  "quality", "missing": true}` matches cards with no value.
- **Sort:** `enrichment:quality`, highest first. Cards without a value go last.
- **Include:** `enrichments` adds the score instance's current values to each
  hit, with `stale`.

All three use the score instance only. Without one, an enrichment filter or
sort is a `400`.

## Web

- **Grid:** each tile shows the `grid` key as a small badge, dimmed with a
  "stale" title when stale.
- **Filters sidebar:** a range filter per `score` key, and "Not evaluated".
- **Sort menu:** one entry per `score` key.
- **Bulk action:** **Evaluate** runs the score instance, or asks which
  instance when there are several. It shows the job's progress like other
  bulk actions.
- **Card page:** an Enrichments section in the left panel lists each
  instance's values, marking stale ones, with **Evaluate** to rerun on the
  head revision.
- **Settings:** a **Scores from** picker on the settings page.

## forge-ai

- Capability `enrich`, with these keys:

  ```toml
  [enrichments]
  quality = { type = "score", range = [0, 100], label = "Quality", grid = true }
  voice = { type = "score", range = [0, 100], label = "Voice" }
  # …one per evaluate dimension
  ```

- `enrich.run` runs the evaluate prompt on each revision, one model call per
  revision, and maps the report's `score` to `quality` and each dimension to
  its key. `meta` holds the model and the prompt's version.
- The six dimension keys come from the evaluate prompt; the plan lists them.

## Tests

- Go: `enrich.run` replies with undeclared keys, wrong types, and
  out-of-range scores are dropped with warnings; a revision missing from the
  reply counts as failed; the current value and staleness follow new
  revisions; filters, sort, and include use only the score instance; a
  filter without a score instance is a `400`. Against the fake plugin and a
  real PostgreSQL.
- Web: the grid badge and its stale state; the range filter builds the right
  query; Evaluate starts a run on the selection.
- Plugin: an evaluate report maps to the declared keys; a revision whose model
  call fails returns `error` without failing the batch.
