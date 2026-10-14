# Filter sidebar

The Cards page sidebar has two problems:

- It reads poorly. Sync repeats the target name on every row ("SillyTavern: ahead", "SillyTavern: in sync"). Values in a section can only be ANDed. Exclusion is hidden behind shift-click.
- It shows no tags. The Tags facet counts library tags (`tag`, `card_tag`), which only the Organize panel and bulk tagging write. The tags a card carries from Chub sit in its `data.tags` and search never reads them. Nobody has created a library tag, so the section stays hidden.

The design ships in two PRs: tags first, because the sidebar's Tags section depends on them.

## PR 1: one kind of tag

A card's tags are its `data.tags`. Library tags are removed.

| Remove | Where |
|---|---|
| `tag` and `card_tag` tables | new migration; also the table list in `internal/backup/backup.go` |
| `SetCardTags`, `EditCardTags`, `CardTags` | `internal/library/organize.go` |
| `PUT /cards/{id}/tags` and the `tags` field on the card response | `internal/api/entities.go` |
| The Tags editor in the Organize panel | `web/src/card/Organize.tsx`, `Panel.tsx` |

The tables hold no rows in use, so the migration drops them without copying anything.

**Editing.** Tags become a field in the draft editor (`web/src/card/Fields.tsx`), edited with `ChipInput`, which moves from `web/src/lorebook/` to a shared place. A tag edit saves with the rest of the draft as one revision, so the card goes ahead on its targets and the next push sends the new tags.

**Bulk.** The `card.tag` action keeps its add and remove lists but writes a new revision for each card whose `data.tags` changes. Cards it leaves unchanged get no revision. Names match case-insensitively, as now.

**Search.** The `tag` filter, the `tags` facet, and `include: tags` all read `r.data->'tags'`, compared case-insensitively. The facet loses its 50-tag limit.

## PR 2: the sidebar

### Sections

Each section is collapsible, in this order:

| Section | Values | Filter |
|---|---|---|
| Target | Each target instance a matching card is pushed to | `target_state.instances` |
| State | `in_sync`, `ahead`, `remote_changed`, `diverged`, `missing`, `cant_check` | `target_state.states` |
| Tags | Card tags | `tag` |
| Sources | Source kinds, such as `chub` | `source` |
| Collections | Collections; hidden while none exist | `collection` |

The Lorebooks page uses the same component with Target, State, and Sources.

Values within a section combine with OR. Sections combine with AND. An excluded value is ANDed as `not`.

### Target and State

`TargetState` takes lists instead of one pair:

```json
{ "target_state": { "instances": [3, 5], "states": ["ahead"] } }
```

It matches a card with a binding on one of `instances` whose status is one of `states`. An empty list matches any value. So Target alone means "pushed there", State alone means "in that state on any target", and both together mean "in that state on one of those targets".

All Target and State terms go into one `target_state` filter, so a state applies to the selected targets rather than to any target.

### Rows

Each row shows the label and count, then two buttons that appear on hover and keyboard focus:

- **only** selects this value and clears the rest of its section.
- **−** excludes this value.

Clicking the row toggles include. Shift-click still excludes. An excluded row is struck through in the drift color.

State rows start with the status dot from `badgeClass` in `web/src/cards/Tile.tsx` and take their label from `stateLabel` in `web/src/sync/sync.ts`.

### Counts

A section's counts ignore that section's own selections and respect every other section's. A count always means "cards you would see if you picked this".

The browser computes this. Alongside the main search, each open section sends a `POST /search` with `page.size: 0`, only its own facet, and the filter built from every other section's terms. These run in parallel through React Query. `query.ts` is the one place that knows which section a term belongs to. The API gains no per-section logic.

### Tags

When open, the section lists the 10 most used tags plus any selected ones. "Show all" expands the full list in place, and a search box in the section filters it as you type.

### Collapsing

Sections start open. Each section's collapsed state is kept in localStorage, wrapped in try/catch, and not in the URL. A collapsed header shows how many of its values are selected, for example "Tags · 2". The existing toggle that hides the whole sidebar stays.

### URL

Each term is one `f` parameter, `-` prefixed when excluded:

```
?f=target:3&f=target:5&f=state:ahead&f=-tag:nsfw&f=source:chub&f=collection:2
```

Links in the old `target:<instance>:<state>` form are not read.

## Out of scope

- An archived filter.
- Section-aware counts in the API itself. MCP agents and other API callers get counts over the full match, as now.
