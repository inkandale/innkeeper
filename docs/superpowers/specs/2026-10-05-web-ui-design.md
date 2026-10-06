# Web UI design

Date: 2026-10-05

## Purpose

The web UI is where the owner browses, organizes, edits, and syncs their
library. It follows Stash and Overseerr: a dense image grid with overlays,
facets to filter by, bulk actions on a selection, and a detail page per item.
Collections are one optional way to organize, not the main navigation.

It is a client of the HTTP API like the CLI and MCP server. Where a screen
needs data the API doesn't return yet, this spec adds it to the API.

## Decisions

| Topic | Decision |
|---|---|
| Serving | Embedded in the Go binary and served at `/`. The API moves to `/api`. |
| Stack | Vite, React, TypeScript, React Router, TanStack Query. |
| API client | Types from `openapi-typescript`, calls through `openapi-fetch`. The schema is generated from Huma and checked in CI. |
| Styling | Plain CSS with custom properties and named grid areas. No Tailwind, no component kit. Radix primitives only for dialog, popover, menu, and combobox. |
| Themes | A plugin capability that ships one stylesheet and no process. |
| Paging | Numbered pages with a page-size picker. No infinite scroll. |
| Text editing | Auto-growing `<textarea>`s. No rich text or code editor. |
| Diffs | The backend returns hunks. The UI renders them; it has no diff library. |
| Token counts | Estimated in the client as characters ÷ 4 and labelled `~`. |
| Auth | The existing session cookie. No tokens in the browser. |

## Serving and routes

| Path | Served by |
|---|---|
| `/api/*` | The Huma API, mounted with `http.StripPrefix("/api")`. Its own paths don't change. The OpenAPI `servers` entry is `/api`. |
| `/mcp`, `/oauth/*`, `/.well-known/*`, `/auth/{id}/callback` | Unchanged, at the root. MCP clients and login providers store these URLs. |
| Everything else | The UI from `embed.FS`. A path that isn't a file returns `index.html` for React Router. |

What the move touches:

- MCP dispatches tool calls through the API handler in process. It gets the
  inner mux, so its paths don't change.
- The CLI keeps `INNKEEPER_URL` as the site root and adds `/api` in its one
  request function. `innkeeper call PATCH /instances/3` keeps working.
- OAuth resource metadata, `docs/`, and the README examples.

Code layout:

- `web/` is the Vite project. `web/embed.go` embeds `web/dist`.
- `innkeeper openapi` prints the spec. `web/src/api/schema.d.ts` is generated
  from it and committed. CI regenerates it and fails on a diff.
- The Dockerfile gains a Node stage that builds `web/dist` before the Go stage.
- `vite dev` proxies `/api`, `/auth`, and `/oauth` to a local `innkeeper api`,
  so the session cookie stays same-origin.

## Themes

A theme plugin's manifest sets `capabilities = ["theme"]` and
`stylesheet = "theme.css"`, and has no `command`. The core never starts a
process for it. It serves the file at `/api/themes/{name}.css`.

The selected theme is a core setting. The UI adds a `<link>` to it after its
own CSS. The UI is served with a Content-Security-Policy of
`style-src 'self'` and `script-src 'self'`, so a theme can restyle the UI but can't run script.

The UI is built so that one stylesheet can change a lot:

- **Tokens** on `:root`: colors, font, radius, spacing, density, and the tile
  image's aspect ratio.
- **Grid areas** on each page: `rail`, `facets`, `toolbar`, `main`, and
  `aside`. A theme can move or hide any of them with `grid-template-areas`,
  for example putting the card page's details panel on the right.

The default theme is the UI's own CSS. Themes that replace components or the
whole UI are out of scope.

## Shell

A left rail with Cards, Lorebooks, Collections, Sync, and Settings. It
collapses to icons and becomes a bottom bar on a narrow screen.

`/login` lists `GET /api/auth/providers` and runs the chosen provider's begin
and complete flow. Any `401` sends the browser there and back afterwards.

## Cards page

Three columns: the rail, a collapsible facet panel, and the grid with its
toolbar.

**Facets** are sections for sync state per target, tags, collections, and
sources, each value with a count for the current filter. Clicking a value
adds it to the filter; shift-click adds its negation. Collections are one
facet among the others.

**Toolbar**: search box, active filters as removable chips, sort (updated,
name, created), and grid or list view.

**Tile**:

- The image, at the theme's aspect ratio, carries the overlays: a select box
  top left, one sync badge per target top right, and the lorebook count
  bottom right.
- Below the image: name, two lines of description, and the first tags with a
  `+N`.
- Sync badges: green in sync, blue library ahead, orange drifted, gray can't
  check.

**Paging**: numbered pages with 24, 48, or 96 per page.

**URL state**: the whole query, page, and page size live in the URL, so back,
refresh, and bookmarks work.

**Selection**: click a tile's box, shift-click for a range, or choose "select
all matching". While anything is selected, a selection bar replaces the
toolbar with Tag, Collection, Push, Archive, and Export. Each starts a bulk
job and shows a toast that links to the job.

### Catalog sources

The core spec's `/search` takes `sources`, such as `catalog:chub-archive`
(500k+ cards). The grid, facets, paging, and card page take a `source`, so a
catalog is this same page pointed at another source. For a catalog:

- Totals and facet counts come from the plugin and may be approximate. The UI
  shows what it gets, such as "10,000+".
- The plugin may cap how deep paging goes. The UI hides page links past the
  cap.
- The card page is read-only, and Save, Push, and Sync become **Adopt into
  library**.

The archive UI itself belongs to the archive catalog's spec.

## Card page

**Left panel**, fixed while the right side scrolls:

- Portrait, name, and creator.
- Save, Push, Export, and Archive. The API has no card delete.
- Image: replace and crop.
- Tags, collections, and sources. These save on change; they aren't part of
  a revision.
- Sync per target. Phase 7 links each one to its drift review.
- Linked lorebooks.
- The last few revisions, linking to the history view.

**Right frame**, scrolling: one collapsible section per field, each a
textarea with a `~` token count. The sections are description, personality,
scenario, first message, alternate greetings (one textarea each, add and
remove), example dialogue, system prompt, post-history instructions, and
creator notes. A last section shows `extensions` as read-only JSON.

**Saving**: Save writes a revision with its `base_revision`. Sections with
unsaved edits are marked, and leaving the page asks first. A `409` shows a
"changed elsewhere" banner with two choices: reload and lose the edits, or
compare your version with the head. The comparison lists only the fields
both sides changed. Keeping your version moves your edits onto the head and
takes every other field from it.

**History**: a full-screen view with revisions on the left and a
field-by-field diff on the right, against the previous revision or the head,
with Restore.

**Image**: Replace uploads to `POST /api/blobs` and saves the new avatar as a
revision. Crop is a fixed 2:3 box with drag and zoom, and saves a new blob,
so the original stays in history.

## Lorebooks

**List**: lorebooks have no image, tags, or collections, so it's a table of
name, entry count, linked cards as small avatars, and sync badges. It has the
same search, facets (sync and sources), paging, and selection as Cards. The
selection bar offers Push and Archive.

**Lorebook page**: the same split as the card page.

- Left panel: name, description, Save and Push, sync per target, linked
  cards, history, and sources.
- Right frame: a filter box and one collapsible row per entry. Collapsed, a
  row shows its keys and the first line of content. Expanded, it has keys and
  secondary keys as chip inputs, content, and constant, selective, enabled,
  order, position, and depth (`extensions.depth`). Entries can be added,
  deleted, and reordered by dragging or with Move up and Move down.

Saving and `409` work as on the card page.

## Collections

A grid of collections, each with a 2×2 mosaic of member avatars, a name, and
a count, plus create, rename, and delete. Opening one goes to the Cards page
with that collection's facet set.

## Sync

**Sync page**: one row per target instance with its plugin, counts (in sync,
ahead, drifted, can't check), last reconcile time, and two actions:
Reconcile and Push all ahead.

**Reconcile queue**, shown when a run's job finishes:

- Verdict chips with counts: exact, likely, new, bound, error.
- One row per app item: app copy ⇄ library match, verdict, score, and a
  dropdown of every option the backend lists, with its suggestion
  preselected.
- **Apply N suggested** sends the selected rows to `POST /api/resolutions`.

**Diff editor**: clicking a row opens it nearly full screen.

- Each field shows library and app columns, with a library, app, or both
  choice per hunk. Fields that are the same are collapsed.
- Linked lorebooks are nested sections, as the reconcile spec says.
- "Other match…" searches the library to bind any card.
- Resolve calls `POST /api/instances/{id}/items/{remote_id}/resolve`. On a
  `409`, the app item changed, and the editor reloads it.

A card's drift review opens the same editor for its bound item through the
compare endpoint.

**Push preview**: before any push (one card, bulk, or Push all ahead), a
dialog shows what will change in the app as a diff for one card or as counts
for many. The push starts when the owner confirms.

The reconcile API comes from the reconcile spec. The Sync screens wait for
it.

## Settings

Four tabs:

- **Plugins**: each plugin with its capabilities and status. Instances can
  be added, edited, and deleted.
  - The settings form is rendered from the manifest's JSON Schema by our own
    renderer, which covers string, number, boolean, enum, and nested
    objects. Anything else is a raw JSON field.
  - Secrets are write-only and show "set" or "not set".
  - A broken instance shows its error.
- **Jobs**: a table of kind, state, progress, and age, filtered by state.
  The table refreshes every 2 seconds while a job runs, and a job's page
  updates live from `/api/jobs/{id}/events`. A bulk job opens to
  its children by status and outcome, with Cancel and a link to its result,
  such as an export zip.
- **Access**: API tokens (create with a tier, shown once, revoke) and OAuth
  grants (list, revoke).
- **Appearance**: the default theme and installed theme plugins, with a live
  preview.

## API additions

| Addition | For |
|---|---|
| `/api` prefix and SPA fallback | Serving |
| `innkeeper openapi` | Client generation |
| `theme` capability, `stylesheet` manifest key, `GET /api/themes/{name}.css`, theme setting | Themes |
| `POST /search`: `facets` (tags, collections, targets, sources), `sort`, `page.number`, `total`, `include: ["targets"]`, a `source` filter | Cards page |
| `GET /api/blobs/{sha256}?w=320`: a resized JPEG, 160, 320, or 640 wide, cached in the blob store's `thumbs/` folder | Thumbnails. Card PNGs are often 1–3 MB. |
| `/bulk` actions tag, untag, collect, uncollect, and export, as job kinds `card.tag`, `card.collect`, and `card.export`. Pushes stay on `/pushes`. Export is one job that writes a zip blob, deleted after a day. | Bulk actions |
| An `ids` search filter, so `/bulk` and `/pushes` take picked cards or a search query | "Select all matching" |
| `GET /api/jobs` fills `children` for bulk jobs | Settings → Jobs |
| `GET /api/cards/{id}/revisions/{a}/compare/{b}`, and the same for lorebooks, using `internal/merge` | History |
| `collections` on `GET /api/cards/{id}` | The card page's Collections section |
| `POST /api/lorebooks/search`: the `/search` query over lorebooks, with a `cards` include and an entry count per hit | Lorebook list |
| `kind: "lorebook"` on `/bulk` (archive and unarchive) and `/pushes` | Lorebook bulk actions |
| `cards` on `GET /api/lorebooks/{id}` | The lorebook page's linked cards |

The cursor stays on `/search` for the CLI and MCP.

## Errors

- Every API error shows as a toast with Huma's `detail`.
- `401` goes to login. `409` on a save shows the banner above.
- `502` and `503` from a plugin name the plugin and link to its instance.
- Lost connection shows an offline bar. TanStack Query retries reads.
- Unsaved edits are never dropped without asking.

## Testing

- **Vitest and Testing Library**: the schema form renderer, the hunk
  renderer, URL to query state and back, selection, and the `409` flow.
- **Playwright**, a few runs in CI against a real `innkeeper api` and worker
  with test PostgreSQL and the fake plugin: log in, filter and page, bulk
  tag, edit and save a card, force a `409`, and reconcile and resolve one
  item.
- **Go**: tests for each API addition in the existing style, plus `/api`
  routing and the SPA fallback.

## Build order

One plan per phase:

1. **Foundation**: `/api` move, embedding, Node build stage, schema
   generation and its CI check, shell, login, theme capability, default
   tokens.
2. **Library grid**: search additions, thumbnails, Cards page, Collections.
3. **Bulk actions**: new job kinds, query-based `/bulk`, selection bar,
   Settings → Jobs.
4. **Card page**: editor, history and revision compare, image replace and
   crop.
5. **Lorebooks**: list and page.
6. **Settings**: Plugins, schema form, Access, Appearance.
7. **Sync**: Sync page, reconcile queue, diff editor, drift review, push
   preview. Starts after the reconcile API is merged.

## Out of scope

- The archive catalog UI (sub-project 2).
- AI editing (sub-project 4).
- Saved searches.
- Editing `extensions` and regex scripts.
- Real tokenizers.
- Themes that replace components or the whole UI.
