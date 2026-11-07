# Browse sections design

Date: 2026-11-06

## Purpose

The filter sidebar and sort menu list every key of every enrich instance,
grouped by plugin. With card-lint, crucible, forge-ai and chub-archive
installed that is about 30 keys, most of which repeat each other: crucible's
sub-scores correlate with its `quality` at 0.55 to 0.80, and forge-ai declares
the same seven score keys as crucible.

This spec lets a plugin say which keys are worth browsing and where they
belong, and merges keys that mean the same thing across plugins. The menus
shrink to about eight entries, grouped by what the owner is asking, not by
which plugin answered.

[Rules](2026-11-06-rules-design.md) builds on it: its saved-search editor and
preview reuse the sectioned filter UI.

## Decisions

| Topic | Decision |
|---|---|
| Sections | Core defines five: Score, Popularity, Kind, Content warnings, Problems. A key names one in the manifest; a browsable key without one goes under Other. |
| Browsable keys | A key with `browse = true` shows in the filter sidebar and sort menu. The rest show only on the card page, and stay usable through the API, MCP and search syntax. |
| Old manifests | A plugin that sets `browse` on no key has every key browsable, as today, so third-party plugins don't vanish from the menus. |
| Grid key | Always browsable. |
| Merging | Browsable keys with the same name and type, across instances, show as one entry. Its value for a card comes from the first instance in priority order that has one. The server merges, since the UI never sees `priority`. |
| Merged label | The label of the highest-priority instance that declares the key. |
| Card page | Unchanged values, but each instance's non-browsable scores show as one compact bar list under its browsable ones. |
| Out of scope | Changing which keys a plugin writes. Hiding keys per library. |

## Manifest

Two optional fields on each `[enrichments]` key:

| Field | Meaning |
|---|---|
| `section` | `score`, `popularity`, `kind`, `warnings` or `problems`. Any other value fails validation. |
| `browse` | `true` to list the key in the filter sidebar and sort menu. |

`section` without `browse` fails validation, since only browsable keys are
grouped. A grid key may set `section` alone, since it is always browsable.
A plugin that uses either field sets `protocol_version = "1.2"`, so an older
core reports it as needing a newer Innkeeper instead of failing on unknown
keys. The protocol docs list the five sections and what belongs in each:

| Section | Holds |
|---|---|
| `score` | How good the card is, such as a quality score. |
| `popularity` | How the card does with readers, such as a site's rating. |
| `kind` | What the card is: type, format, point of view, language, topics. |
| `warnings` | Content ratings and content flags. |
| `problems` | Defects to fix, such as lint problems. |

`GET /enrichments/keys` returns `section` and `browse` on each key, and a
`browse` list of the merged keys, so the UI needs no other source. Both fields bump `protocol.Minor` and add a row to
the version table in `docs/protocol.md`.

## Merged keys

Today a filter or sort that names no instance must name a grid key, and it
reads the grid score: each card's value from the highest-priority grid key of
any name. So `{"enrichment": "lint"}` can match on forge-ai's `quality`. This
spec makes an instance-less term read only the key it names, across every
instance where that key is browsable. The grid score itself, and the tile
that shows it, are unchanged.

- **Which instances:** every enabled enrich instance whose plugin declares
  the key as browsable, with the same type.
- **Order:** by the key's `priority`, highest first, then by instance ID,
  as `Scorers` orders them now. Only grid keys can set `priority`, so a
  merged key that isn't a grid key orders by instance ID.
- **Filter:** `{"enrichment": "quality", "gte": 70}` matches a card when the
  first instance in that order that has a value for it has one of 70 or more.
- **Sort:** `enrichment:quality` sorts by that same value.
- **Facet:** `enrichment:<key>` counts a text or list key's values the same
  way, alongside the existing `enrichment:<instance>:<key>`.

A key whose type differs between plugins doesn't merge. crucible's `rating`
(text) and chub-archive's `rating` (score) stay two entries, each naming its
instance, and a term on `rating` with no instance fails with 400.

The API resolves the instance list once per request, as `withScores` resolves
`Scorers` now. The search code takes the list per key instead of one list for
the grid.

## UI

- **Filter sidebar:** enrichment facets sit under section headings, in the
  order of the table above, then Other. A merged key is one facet. Each
  section collapses, as facet sections do now.
- **Sort menu:** browsable score keys only, merged, labeled without the
  instance name. A link that sorts by an instance's key that isn't browsable
  still works and shows that key in the menu.
- **Card page:** the Enrichments panel shows each instance's browsable keys
  as now, then its other score keys as one bar list, and its other text and
  list keys below.

## Testing

- Manifest validation rejects an unknown `section`, and `section` without
  `browse`.
- A plugin with no `browse` key lists every key; one with a `browse` key lists
  only those.
- A merged filter, sort and facet read the highest-priority instance's value,
  and fall back to the next instance for a card the first hasn't scored.
- Keys with the same name and different types don't merge.
- Web tests: facets group by section, the sort menu lists merged browsable
  keys only, and the card page shows the bar list.

## Plugin changes

In `innkeeper-plugins`, released with the core change:

| Plugin | Browsable keys and sections | Other changes |
|---|---|---|
| card-lint | `lint` score, `language` kind, `problems` problems | Leave `no_char_macro` out of the `lint` score: it fires on 60,000 cards, many of them good. |
| crucible | `quality` score, `card_type` kind, `format` kind, `pov` kind, `rating` warnings, `flags` warnings | Relabel `rating` as "Content rating" and `quality` as "Quality", so it merges cleanly with forge-ai's. |
| forge-ai | `quality` score | None. |
| chub-archive | `rating` popularity, `topics` kind | None. |
