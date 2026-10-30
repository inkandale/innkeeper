# Card page layout design

Date: 2026-10-30

## Purpose

The card page's left panel stacks 13 sections in a 300px column. Read-only
data crowds out the controls: enrichment values with long text squeeze into
a right-aligned list, and Related shows full-size tiles in the narrow
column. This design keeps the panel for what you edit or act on, and moves
the rest to the main column.

## Decisions

| Topic | Decision |
|---|---|
| Tab bar owner | Core. A new `PageTabs` component renders the main column's tab bar on every card, lorebook, and new-card page, with or without plugins. |
| Forge | Adds tabs and the Reforge ⌘K button to the bar instead of rendering the bar. |
| Plugin tabs | Plugins ship no UI code, so core adds a tab or button per capability: Forge for action plugins, Metadata for enrich plugins. |
| Metadata tab | A last tab, named Metadata, holding core facts and enrichment values. |
| Sidebar | Four groups: Identity, Links, Sync, Collections. Native `<details>` collapses the last three, as in `Fields.tsx`. Open state isn't remembered. |
| Related | Moves to the main column, below the tab panel, beside Similar. |
| List caps | Related's groups and Similar show at most 8 cards. Related's groups link to the gallery for the rest; Similar keeps Show more. |
| Gallery terms | Search gets `creator` and `lorebook` filters, and the gallery gets matching terms. A "similar to card N" search is out of scope. |

## Tab bar

`web/src/card/PageTabs.tsx` renders the bar alone:

```ts
type PageTab = { id: string; label: string; disabled?: boolean; panel: ReactNode };

function PageTabs(props: {
  label: string;
  tabs: PageTab[];
  selected: string;
  onSelect: (id: string) => void;
  actions?: ReactNode;
});
```

It takes over from `forge/Forge.tsx` the bar's sticky position below the
editor bar, arrow-key movement between enabled tabs, and the ARIA roles
(`tablist`, `tab`, `tabpanel`). The `forge-tabs` CSS moves to a core class
in `card/Card.css`. `actions` renders at the bar's right end.

The page holds the selected tab and passes `setTab` to the Forge, which
still switches tabs itself: to Review when a proposal arrives, to Report
when a report run started on this page finishes, and to Fields after Apply
or Discard.

### Forge

`Forge` becomes a hook, `useForgeView`, that returns the Forge's part of the
page:

| Field | Contents |
|---|---|
| `tabs` | Review and Report on a saved card or lorebook; Start and Review on a new card |
| `actions` | The Reforge ⌘K button (Forge ⌘K on a new card) |
| `fields(lock, extra)` | The field badges and hammer menus the Fields tab already takes |
| `overlays` | The Reforge panel and the repeat-run dialog |

Without an action plugin for the page's kind, `tabs` and `actions` are empty
and ⌘K does nothing. The existing check (`forge.actions.some(...)`) decides
this; it no longer hides the bar.

### Tabs per page

| Page | Tabs |
|---|---|
| Card | Fields, Review, Report, Metadata |
| Lorebook | Fields, Review, Report, Metadata |
| New card | Start, Fields, Review |

Review is disabled until a proposal is waiting, as now. A new card has no
Metadata, since nothing is saved.

## Metadata tab

Two parts, read-only except for Rescore.

### Core facts

Cards and lorebooks, all from `EntityOut`:

| Row | Source |
|---|---|
| Created | `created_at` |
| Last edited | `revision.created_at`, linking to History |
| Revision | `revision.id` and `revision.source` |
| Slug | `slug` |
| ID | `id` |
| Library | the library's name, from `library_id`; replaces the panel's "Library: …" line |
| Provenance | `provenance` |
| Content hash | `revision.content_hash`, first 12 characters, copied in full on click |

### Enrichments

Cards only. One block per enrich instance, headed by its name, with its own
Rescore button. This replaces the shared instance select and single
Rescore. `Enrichments.tsx` keeps its query and job logic; only the layout
changes.

Each value is laid out by its JavaScript type:

| Value | Layout |
|---|---|
| Number | In a grid of label and value cells, two or three columns wide |
| String of 40 characters or fewer | In the same grid |
| Longer string | Full width under its label |
| Array | Full width under its label, one bullet per item |

Stale values stay dimmed with "(stale)". A card with no values yet shows
"Not scored yet." above the Rescore buttons.

## Sidebar

Top to bottom:

1. **Identity**, always shown: portrait, Replace image and Crop, nickname,
   creator, version, tags. Unchanged apart from the library line leaving.
2. **Links**, `<details open>`:
   - **Sources**: one line per source. The link text is truncated with an
     ellipsis and the full URL in `title`. Its status, Ignore all, and
     autosync follow. Check for updates moves to a small button beside the
     Sources heading. The Source URL input stays hidden until "+ Add
     source" is clicked.
   - **Variants**: "Variant of X ×" moves here from below Identity, then the
     variant list. New variant, Fork into…, and Link existing… become one
     "Variant…" menu.
   - **In other libraries**: as now.
   - **Lorebooks** on a card, **Cards** on a lorebook: as now.
   - **History**: one link, "History (n revisions)". The five latest
     revisions are dropped; the History page lists them.
3. **Sync**, `<details open>`: as now, shown only when the card is pushed
   to an app.
4. **Collections**, `<details>` closed: as now.

The lorebook page uses the same groups through the shared components
(`Sources`, `Revisions`, `Variants`, and so on).

## Related and Similar

Both render in the main column below the tab panel, Related first.

Each of Related's groups shows at most 8 cards, then a "See all" link to
the gallery:

| Group | Gallery link |
|---|---|
| Same creator | `creator:` term with the card's creator |
| Shares a lorebook | one `lorebook:` term per linked lorebook, ORed |
| Shares a collection | one `collection:` term per collection, ORed |

The link searches the card's library, since search covers one library at a
time; matches in other libraries stay reachable only through the capped
list. Related's per-group Show more goes.

Similar shows 8 cards per page and keeps Show more, since the gallery
can't rank by likeness to one card.

## Search filters

`internal/library/search.go` gains two `Filter` fields:

| Field | Matches |
|---|---|
| `creator` | Cards whose head revision's `creator` equals the value, ignoring case |
| `lorebook` | Cards linked to the lorebook with this ID |

Both apply to cards only. Run `npm run gen` in `web/` after the Go change.

In `web/src/cards/query.ts`, `creator` and `lorebook` join `TermKind`, parse
from and write to the URL, and build their filters in `filterFor`. Neither
gets a facet section: the gallery reaches them through links. A term chip
shows `creator: <name>` and `lorebook: <name>`.

## Testing

Go, against a real PostgreSQL in `internal/library/search_test.go`:

- `TestSearchFiltersByCreatorIgnoringCase`
- `TestSearchFiltersByLorebook`, including two lorebooks ORed

Web, with Vitest:

- `PageTabs`: arrow keys skip disabled tabs; `actions` renders.
- Card page with no action plugin: Fields and Metadata show; no Reforge
  button.
- Card page with an action plugin: Review, Report, and Reforge ⌘K show; a
  proposal arriving selects Review. Move the matching `Forge.test.tsx`
  cases rather than drop them.
- Metadata: core facts render; an array value renders as bullets; a stale
  value is dimmed; each instance has its own Rescore.
- Sidebar: Links, Sync, and Collections are `<details>`; Related is absent;
  "Variant of" is inside Links.
- Related: a group of 12 shows 8 and a See all link with the right term.
- `query.test.ts`: `creator:` and `lorebook:` terms survive a URL round trip.
