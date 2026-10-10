# Sync Design

**Goal:** One way to bring an app's items into the library: **Sync**. It runs a
reconcile, applies the obvious decisions itself, and leaves only the ones that
need the owner in the review queue. It replaces importing from an app, which
took everything without matching anything but exact content.

**Builds on:** `2026-10-05-reconcile-design.md`. Reconcile, resolve, and the
review queue work as that spec describes; this changes what a run applies and
what the owner sees it called.

---

## Naming

The owner sees **Sync** in the web UI, the CLI, and MCP. The API path
(`/reconciles`), the job kind (`target.reconcile`), and Go identifiers stay
"reconcile", so scripts and stored jobs keep working.

| Surface | Before | After |
|---|---|---|
| Web | "Reconcile" button, "Reconcile #12", "Last reconcile" | "Sync", "Sync #12", "Last sync" |
| CLI | `ink sync reconcile <instance>` | `ink sync run <instance> [--dry-run]`; `reconcile` stays as an alias |
| MCP | `start_reconcile` | `start_sync`, with `apply: "none"` to preview |

## What a run applies

`ReconcileParams` gains `apply`: `obvious` (default) or `none`. With `none` the
run is a preview: it writes nothing, as a reconcile does today.

With `obvious`, after the snapshot is built the run resolves:

| Verdict | Action | Condition |
|---|---|---|
| `exact` | `bind` to the suggested card, keeping the library's copy | Every linked world book is `exact`, `new`, or bound with nothing differing |
| `new` | `import` | Every linked world book is `exact`, `new`, or bound with nothing differing |
| `new` world book, not linked to any card | `import` | none |

World books are resolved first, so a card's resolution names their bindings,
as `autoBind` does today. Everything else (`likely`, `bound` with changes,
`missing`) stays in the queue with its suggestion.

- Each resolution uses the hashes the run read, so an item that changed since
  is stale and stays in the queue. A failed resolution is logged and the item
  stays in the queue; the run goes on.
- An `import` needs the app's avatar. If reading it fails, the item stays in
  the queue.
- Snapshot items gain `auto_imported` next to `auto_bound`, and totals gain
  `auto_imported`. The queue shows them as "imported (auto)" and leaves them
  unchecked, as it does auto-bound items.
- `autoBind` becomes `autoApply`, handling both actions. Lifting the
  `bindOnly` restriction for this caller is the change in `resolve`.

## Removals

- **`auto_bind_exact`.** Every sync now binds exact matches. A migration drops
  `plugin_instance.auto_bind_exact`; the field leaves `PATCH /instances/{id}`,
  the instance settings page, and the MCP descriptions.
- **Importing from an app** (INN-18). Delete `internal/targets/import.go`, the
  `target.import` job kind and its worker registration, and
  `importFromTarget` in `internal/api/jobs.go`. `POST /imports` on an instance
  without the `import` capability returns `400` naming `POST /reconciles`.
  `innkeeper import` and MCP `start_import` work only with importer plugins.
  This is a breaking change: commit it as `feat!`.
- `docs/protocol.md` keeps `target.get`'s `avatar` and `lorebook_remote_ids`;
  resolve uses both.

## Tier

`POST /reconciles` moves from `read` to `write`, since a sync writes by
default. A read-only token can still read past snapshots with `GET /jobs/{id}`.
ponytail: one tier for the route; give `apply: none` its own read-tier check if
a read-only agent needs to preview.

## Web

- **Sync page.** The per-app button is "Sync". It starts a run and opens its
  queue. A run that leaves nothing to review shows its totals ("12 imported,
  3 linked, nothing to review") instead of an empty table.
- **Welcome wizard.** "Bring in cards" shows "Sync {app}" in place of
  "Import from {app}", starting the same run. Its job line links to the queue.
  File uploads to the importer are unchanged.

## Testing

- `internal/targets`: a run with `apply: obvious` on a fake app holding one
  exact, one new, one likely, and one new world book linked to the new card
  binds the exact item, imports the new card with its world book linked,
  leaves the likely item, and counts `auto_bound: 1, auto_imported: 2`.
  `apply: none` writes nothing.
- A new card whose world book is `likely` stays in the queue.
- An item changed between the read and the resolve stays in the queue.
- API: `POST /reconciles` needs `write`; `POST /imports` on an app instance is
  `400`; the instance `PATCH` no longer accepts `auto_bind_exact`.
- Web: Sync page and wizard tests updated for the new labels and the
  nothing-to-review state.
