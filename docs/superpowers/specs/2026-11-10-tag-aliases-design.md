# Tag aliases

Treat a library's many spellings of one tag as one tag, without rewriting
cards. Spellings that differ only in case, spaces, `-`, or `_` are one tag
automatically. The owner merges the rest on a Tags page, which suggests
likely duplicates. Search, counts, and similar-card matching read through
the merges. A card's tags are rewritten to their merged names only when
the owner edits that card's tags anyway.

The live instance has about 23,000 distinct tags. Ignoring spaces, `-`, and
`_` removes about 1,700 of them, and plurals about 350 more. Most of the
rest are used by one card.

This is the first of two specs. The second adds a tag head to crucible,
which suggests tags for cards from this cleaned list.

## Terms

| Term | Meaning |
|---|---|
| Tag key | A tag lower-cased, with spaces, `-`, and `_` removed: `Sci-Fi`, `sci fi`, and `scifi` all have the key `scifi`. Two spellings with one key are the same tag. |
| Alias | A tag key that stands for another tag, its canonical tag: `fantasyworld` → `fantasy`. |
| Canonical tag | A tag no alias row maps away. Its name is how the UI shows it. |

## Decisions

| Question | Decision |
|---|---|
| Rewrite cards on merge | No. A merge adds alias rows. |
| When a card's tags change | When the owner edits them. A bulk Tag job first replaces each tag with its canonical name, then applies the edit, so removing `fantasy` also removes `fantasy world`. Saving the card editor, when its tags changed, saves the list it shows with each tag replaced by its canonical name. Imports, pulls from apps, MCP, and plugin writes keep tags as they come. |
| Scope | One alias table for the whole install, as rules and searches span libraries. |
| Depth | One level. A canonical tag is never an alias. Merging into a tag that is an alias merges into its canonical tag. Merging a tag that has aliases moves them to the new canonical tag. |
| Rename | Merging into a name no card has yet. |
| Split | Deleting an alias row. |
| What leaves Innkeeper | Raw tags. Apps, exports, and card data see what the card holds. |
| Duplicate suggestions | Plurals and near spellings, computed in Go over the tag list. No `pg_trgm`, so no extension to enable. Tags with similar meaning, such as `female` and `woman`, wait for crucible's tag head. |

## Data

Migration:

- `tag_keys(tags)` returns tag keys instead of lower-cased tags. Its GIN
  index is rebuilt once. No card changes.
- `tag_alias (key text PRIMARY KEY, canonical text NOT NULL, created_at)`:
  `key` is the alias's tag key; `canonical` is the canonical tag's name.
- `tag_distinct (a text, b text, PRIMARY KEY (a, b))`: pairs of tag keys
  the owner said are not the same, with `a < b`, so a suggestion isn't
  shown again.

`cardhash.UniqueTags` keeps deduplicating by case only. Changing it would
change card hashes.

## Search and counts

- **Tag filter:** `tag: X` resolves X's key to its canonical tag, then
  matches `tag_keys(r.tags) && ARRAY[canonical key, every alias key]`. The
  GIN index serves `&&`. Saved searches and rules naming an alias keep
  working.
- **Tag facet:** each tag maps to `coalesce(canonical key, key)`, counted
  once per card. It shows the canonical tag's name, or for a tag with no
  alias, the first of its spellings in sort order, as today. Measure it against the live tag
  distribution before building on it.
- **Similar cards** (`discover.go`) compare canonical keys, so a card
  tagged `fantasy world` matches one tagged `fantasy`.

## Tags page

`/tags`, beside Collections.

- **List:** every canonical tag with its card count across all libraries
  and its aliases, most cards first. Searchable by name, paged.
- **Actions per tag:** Rename, Merge into…, and per alias, Split.
- **Suggestions,** above the list, most cards first:
  - plurals: keys equal after removing a trailing `s` or `es`
  - near spellings: keys of at least 5 characters one edit apart
  - each shows both tags with counts, and buttons Merge (into the one with
    more cards, which the owner can swap) or Not the same
- Several spellings of one key show as one tag listing them, so a key
  never appears as a suggestion against itself.

## API

| Endpoint | Does |
|---|---|
| `GET /tags?q=&page=` | Canonical tags with counts and aliases, across all libraries. Replaces the per-library alphabetical list, which only tests call. |
| `GET /tags/suggestions` | Likely duplicate pairs with counts |
| `POST /tags/merge {from, into}` | Adds alias rows. 400 if `from` and `into` have one key |
| `DELETE /tags/aliases/{key}` | Splits an alias out |
| `POST /tags/distinct {a, b}` | Remembers Not the same |

## Changes

| File | Change |
|---|---|
| `internal/db/migrations/0058_tag_aliases.sql` | `tag_keys`, its index, `tag_alias`, `tag_distinct` |
| `internal/library/tags.go` (new) | Aliases, merge, split, list, suggestions |
| `internal/library/search.go` | Tag filter and facet read aliases |
| `internal/library/discover.go` | Compares canonical keys |
| `internal/library/organize.go` | `EditCardTags` canonicalizes before editing |
| `internal/library/entities.go` | The editor's save canonicalizes tags |
| `internal/api/tags.go` (new) | The endpoints |
| `web/src/tags/Tags.tsx` (new) | The page |
| `docs/using.md` | Tags |

## Testing

- Tag key: `Sci-Fi`, `sci fi`, and `scifi` are one tag in search and
  counts.
- A filter on a canonical tag matches cards with an alias, and a filter on
  an alias matches cards with the canonical tag.
- A card holding both `fantasy` and `fantasy world` counts once.
- Merge keeps one level: merging into an alias goes to its canonical tag,
  and merging a tag with aliases moves them.
- Bulk Remove `fantasy` takes out `fantasy world` too; bulk Add writes the
  canonical name. An import keeps raw tags.
- Suggestions: plurals and one-edit pairs appear, a Not the same pair
  doesn't, and a key never pairs with itself.
- The facet query stays fast on a library shaped like the live one.
