# Forge Phase 7: Enrichments Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An `enrich` plugin computes values about card revisions, such as quality scores. The core stores every result, and the library grid, filters, sort, and card page use the values of one chosen instance.

**Architecture:** INN-60, the core half of `docs/superpowers/specs/2026-10-15-enrichments-design.md`. The protocol section in `docs/protocol.md` is committed first; forge-ai builds `enrich.run` against it in `innkeeper-plugins`.

- **Protocol** (`protocol/enrich.go`): `enrich.run` params and reply, and `Enrichment.Check(value)`, the type and range rule core applies to each value. The manifest gains `grid` and checks that an `enrich` plugin declares keys.
- **Storage** (`internal/library/enrichment.go`): the `enrichment` table, adding results, and reading a card's current values and history. Search compiles the enrichment filter, sort, and include, given the score instance in `Query.ScoreInstance` (`json:"-"`).
- **Job** (`internal/enrich`): the `enrich` job kind. It sends head revisions in batches of 20 through the shared `pluginhost.Runs` router, checks each reply against the manifest, and stores what passes.
- **Score instance:** the `score_instance` setting, resolved in the API (`Server.scoreInstance`): the setting when it names a usable `enrich` instance, else the only `enrich` instance, else none. Every search, bulk selection, and MCP `search_cards` call goes through `Server.search` or `SearchIDs`, which set it.
- **API:** `POST /enrichments/runs`, `GET /cards/{id}/enrichments`, `GET /enrichments/keys`, `score_instance` on `/settings`.
- **Web:** a grid badge, range filters and Not evaluated in the sidebar, sort entries, bulk Evaluate, a card-page Enrichments section, and a Scores from picker.

**Tech Stack:** Go, pgx, PostgreSQL, Huma, React and TypeScript.

**Tests:** DB tests skip without the database URL. `$DB` means `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`.

**Deviations from the spec:**
- `GET /enrichments/keys` returns `{"instance_id", "keys"}`, with `instance_id` null and no keys when there's no score instance, so the UI learns both from one call.
- `GET /cards/{id}/enrichments` returns a flat list, newest first per instance and key, each value with `current`; history adds rows with `current: false`.
- An `enrich` plugin must declare at least one key, as a `records` plugin must declare a type.
- A `list` value is an array of strings.
- An interrupted run is not retried, as an action run isn't: each call costs the owner money.
- `POST /enrichments/runs` takes `query` as an alternative to `card_ids`, as `POST /bulk` does, so Evaluate works on "all matching" without the UI fetching IDs. The 10,000 limit applies to the cards either one selects.
- `sort` loses its enum for a pattern, since `enrichment:<key>` names any key.

**Known limit:** `enrichment.instance_id` has no foreign key, so deleted instances keep their results. A config restore that recreates instances can give a new instance an old ID, and that instance then shows the old instance's results as its own.

## File map

| File | Change |
|---|---|
| `docs/protocol.md` | `enrich` section, `grid`, checks |
| `protocol/manifest.go`, `manifest_test.go` | `grid`, one per plugin; `enrich` needs keys |
| `protocol/enrich.go`, `enrich_test.go` | Method, params, reply, `Enrichment.Check` |
| `internal/db/migrations/0025_enrichment.sql` | Table and index |
| `internal/backup/backup.go`, tests | `enrichment` in Assets |
| `internal/library/enrichment.go`, `enrichment_test.go` | Add and read values |
| `internal/library/search.go`, `search_test.go` | Filter, sort, include |
| `internal/enrich/enrich.go`, `enrich_test.go` | Job, payload, progress, reply checks |
| `internal/fakeplugin/enrich.go`, `main.go`, `internal/testutil/fakeplugin.go` | Fake enricher |
| `internal/api/enrichments.go`, `enrichments_test.go` | Routes, score instance |
| `internal/api/settings.go`, `search.go`, `jobs.go`, `targets.go` | `score_instance`; queries get the score instance |
| `internal/contract/enrich.go`, `contract.go`, `contract_test.go` | Enrich checks |
| `cmd/innkeeper/worker.go` | Register the job kind |
| `web/src/...` | See Task 9 |

---

### Task 1: Protocol doc

- [ ] Write the `enrich` section in `docs/protocol.md`: `grid` in the manifest table, `enrich.run` params, progress, reply, what core drops, a JSON example, and the checks. Commit `docs(protocol): the enrich capability`.

### Task 2: Protocol types and manifest

- [ ] Failing tests: `TestManifestEnrichGrid` (two `grid` keys fail; `enrich` without keys fails). `TestEnrichmentCheck`: text takes a string, not a number; score takes 0 and 100 in `[0, 100]`, not 101, `"78"`, or `null`; list takes `["a"]`, not `["a", 1]` or `"a"`.
- [ ] Implement:

```go
const MethodEnrichRun = "enrich.run"
const MaxEnrichBatch = 20
type EnrichRevision struct { CardID int64 `json:"card_id"`; RevisionID int64 `json:"revision_id"`; Data json.RawMessage `json:"data"` }
type EnrichRunParams struct { Run string `json:"run"`; Revisions []EnrichRevision `json:"revisions"` }
type EnrichResult struct { RevisionID int64 `json:"revision_id"`; Values map[string]json.RawMessage `json:"values,omitempty"`; Meta json.RawMessage `json:"meta,omitempty"`; Error string `json:"error,omitempty"` }
type EnrichRunResult struct { Results []EnrichResult `json:"results"` }
func (e Enrichment) Check(v json.RawMessage) error
```

`Enrichment` gains `Grid bool \`toml:"grid"\``.
- [ ] `go test ./protocol/`; commit `feat(protocol): enrich.run and checks for enrichment values`.

### Task 3: Table and storage

- [ ] Migration `0025_enrichment.sql`:

```sql
CREATE TABLE enrichment (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    card_id bigint NOT NULL REFERENCES card ON DELETE CASCADE DEFERRABLE INITIALLY IMMEDIATE,
    revision_id bigint NOT NULL,
    instance_id bigint NOT NULL, -- no foreign key: deleting an instance keeps its results
    key text NOT NULL,
    value jsonb NOT NULL,
    meta jsonb NOT NULL DEFAULT '{}',
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (revision_id, card_id) REFERENCES card_revision (id, card_id) DEFERRABLE INITIALLY IMMEDIATE
);
CREATE INDEX enrichment_current ON enrichment (card_id, key, instance_id, created_at DESC);
```

- [ ] Failing test `TestEnrichmentCurrentAndStale`: add a value on the head, read it current and not stale; save a new revision, it reads stale; add another on the new head, the newest wins and history lists both; another instance's value is listed separately.
- [ ] `library.EnrichmentValue{InstanceID, RevisionID, Key, Value, Meta, CreatedAt, Current, Stale}`; `AddEnrichments(ctx, instanceID, []NewEnrichment)`; `Enrichments(ctx, cardID, history bool)`.
- [ ] Add `enrichment` to the Assets backup tables.
- [ ] Commit `feat(library): store enrichment values`.

### Task 4: Search

- [ ] Failing tests in `search_test.go`: range filter, `missing`, sort highest first with missing last, include with `stale`, all only from `ScoreInstance`; each one without `ScoreInstance` is `ErrInvalid`; on lorebooks it's `ErrInvalid`; `gte` without `enrichment` is `ErrInvalid`.
- [ ] `Filter` gains `Enrichment *string`, `Gte`, `Lte *float64`, `Missing *bool`. `Hit.Enrichments map[string]HitEnrichment` (`value`, `stale`). The current value is a correlated subquery on the index.
- [ ] Commit `feat(library): filter, sort, and include by enrichment`.

### Task 5: Fake enricher

- [ ] `enrich` in the fake: keys `quality` (score 0 to 100, grid), `voice` (score), `summary` (text). `quality` is the description's length, at most 100. A card named with `fail` returns `error`; with `skip` is left out; with `bad` returns an undeclared key, a string `voice`, and `quality` 150. `-mode=bad-enrich` returns undeclared keys for every revision, for the check. Each call sends one `progress`.
- [ ] Commit `test(fakeplugin): enrich revisions`.

### Task 6: Job

- [ ] `internal/enrich`: `Kind = "enrich"`, `Payload{CardIDs}`, `Progress{Message, Done, Failed, Total, Warnings}`, `Clean(decl, sent, reply) (stored, failed, warnings)`, `Runner{Lib, Registry, Plugins, Runs}.Run`.
- [ ] Failing unit test `TestClean`: undeclared key, wrong type, and out-of-range values dropped with one warning each; a revision missing from the reply and one with `error` fail; a result for a revision not sent is ignored with a warning.
- [ ] Commit `feat(enrich): run enrich jobs in batches`.

### Task 7: API and worker

- [ ] Failing tests in `enrichments_test.go` (fake with `enrich` and a worker): a run over cards named good, `bad`, `fail`, `skip` succeeds with done 2, failed 2, and warnings; `GET /cards/{id}/enrichments` shows values with labels, stale after an edit; `?history=true` adds older rows; search filter, sort, include use the score instance; with two instances and no setting, a filter is a `400`; `PATCH /settings` takes and clears `score_instance`, refuses a non-`enrich` instance; `GET /enrichments/keys`; more than 10,000 cards is a `400`; read tier can't start a run; search_cards over MCP filters by enrichment.
- [ ] Register `enrich.Kind` in `cmd/innkeeper/worker.go`.
- [ ] `npm run gen`; commit `feat(api): enrichment runs, values, keys, and the score instance`.

### Task 8: Plugin checks

- [ ] Failing tests: the fake with `enrich` passes; `-mode=bad-enrich` fails.
- [ ] `enrich.run` on the sample card returns, within 5 seconds, a result for it whose values all pass `Check`, or an error other than `-32601`.
- [ ] Commit `feat(contract): check enrich plugins`.

### Task 9: Web

- [ ] `query.ts`: an `enrichment` term with `gte`, `lte`, or `missing`; `sort` takes `enrichment:<key>`; the grid query includes `enrichments` when there's a score instance. Test: a range term builds `{"enrichment": "quality", "gte": 70}`.
- [ ] `Tile.tsx`: the `grid` key's value as a badge, dimmed with title "stale". Test.
- [ ] `Facets.tsx`: per `score` key, min and max inputs, and Not evaluated. `Cards.tsx`: a sort entry per `score` key.
- [ ] `SelectionBar.tsx`: Evaluate, which starts `POST /enrichments/runs` with the selection's IDs, asking which instance when there are several. Test.
- [ ] `card/Enrichments.tsx` in the left panel: each instance's values, stale marked, Evaluate.
- [ ] `settings/Appearance.tsx` or a new section: Scores from.
- [ ] `npm run typecheck && npm test`; commit per piece.

### Task 10: Verify

- [ ] Browser check with the fake plugin; screenshots.
- [ ] `$DB go vet ./... && $DB go test -race -timeout 30m ./...`, then `cd web && npm ci && npm run typecheck && npm test`.
- [ ] Read `git diff origin/main...HEAD` for dead code and copies.
