# Tag aliases

Treat a library's many spellings of one tag as one tag, without rewriting
cards. Spellings that differ only in case, spaces, `-`, or `_` are one tag
automatically. The owner merges the rest on a Tags page, which suggests
likely duplicates. Search, counts, and similar-card matching read through
the merges. A card's tags are rewritten to their primary names only when
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
| Alias | A tag key that stands for another tag, its primary tag: `fantasyworld` → `fantasy`. |
| Primary tag | A tag no alias maps to another key. Its name is how the UI shows it. |

## Decisions

| Question | Decision |
|---|---|
| Rewrite cards on merge | No. A merge adds alias rows. |
| When a card's tags change | When the owner edits them. A bulk Tag job first replaces each tag with its primary name, then applies the edit, so removing `fantasy` also removes `fantasy world`. Saving the card editor, when its tags changed, saves the list it shows with each tag replaced by its primary name. Imports, pulls from apps, MCP, and plugin writes keep tags as they come. |
| Scope | One alias table for the whole install, as rules and searches span libraries. |
| Depth | One level. A primary tag is never an alias of another key. Merging into a tag that is an alias merges into its primary tag. Merging a tag that has aliases moves them to the new primary tag. |
| Rename | Merging into a name no card has yet. |
| Make primary | Merging two spellings of one key records which one is primary, as a row mapping the key to that spelling: `scifi` → `Sci-Fi`. This is how the owner fixes a name's case or punctuation. Without such a row, a tag shows the first of its spellings in sort order. |
| Split | Deleting an alias row. |
| What leaves Innkeeper | Raw tags. Apps, exports, and card data see what the card holds. |
| Duplicate suggestions | Plurals and near spellings, computed in Go over the tag list. No `pg_trgm`, so no extension to enable. Tags with similar meaning, such as `female` and `woman`, wait for crucible's tag head. |

## Data

Migration:

- `tag_keys(tags)` returns tag keys instead of lower-cased tags. Its GIN
  index is rebuilt once. No card changes.
- `tag_alias (key text PRIMARY KEY, primary_tag text NOT NULL, created_at)`:
  `key` is the alias's tag key; `primary_tag` is the primary tag's name. A
  row whose key is its primary tag's own key names that tag's spelling.
- `tag_distinct (a text, b text, PRIMARY KEY (a, b))`: pairs of tag keys
  the owner said are not the same, with `a < b`, so a suggestion isn't
  shown again.

`cardhash.UniqueTags` keeps deduplicating by case only. Changing it would
change card hashes.

## Search and counts

- **Tag filter:** `tag: X` resolves X's key to its primary tag, then
  matches `tag_keys(r.tags) && ARRAY[primary key, every alias key]`. The
  GIN index serves `&&`. Saved searches and rules naming an alias keep
  working.
- **Tag facet:** each tag maps to `coalesce(primary key, key)`, counted
  once per card. It shows the primary tag's name: the spelling the owner
  picked, or else the first of its spellings in sort order, as today.
  Measure it against the live tag distribution before building on it.
- **Similar cards** (`discover.go`) compare primary keys, so a card
  tagged `fantasy world` matches one tagged `fantasy`.

## Tags page

`/tags`, beside Collections.

- **List:** every primary tag with its card count across all libraries
  and its aliases, most cards first. Searchable by name, paged.
- **Actions per tag:** Rename, Merge into…, and per alias, Make primary
  and Split.
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
| `GET /tags?q=&page=` | Primary tags with counts and aliases, across all libraries. Replaces the per-library alphabetical list, which only tests call. |
| `GET /tags/suggestions` | Likely duplicate pairs with counts |
| `POST /tags/merge {from, into}` | Adds alias rows. With two spellings of one key, makes `into` the primary spelling. |
| `DELETE /tags/aliases/{key}` | Splits an alias out |
| `POST /tags/distinct {a, b}` | Remembers Not the same |

## Changes

| File | Change |
|---|---|
| `internal/db/migrations/0058_tag_aliases.sql` | `tag_keys`, its index, `tag_alias`, `tag_distinct` |
| `internal/library/tags.go` (new) | Aliases, merge, split, list, suggestions |
| `internal/library/search.go` | Tag filter and facet read aliases |
| `internal/library/discover.go` | Compares primary keys |
| `internal/library/organize.go` | `EditCardTags` replaces tags with primary names before editing |
| `internal/library/entities.go` | The editor's save replaces tags with primary names |
| `internal/api/tags.go` (new) | The endpoints |
| `web/src/tags/Tags.tsx` (new) | The page |
| `docs/using.md` | Tags |

## Testing

- Tag key: `Sci-Fi`, `sci fi`, and `scifi` are one tag in search and
  counts.
- A filter on a primary tag matches cards with an alias, and a filter on
  an alias matches cards with the primary tag.
- A card holding both `fantasy` and `fantasy world` counts once.
- Merge keeps one level: merging into an alias goes to its primary tag,
  and merging a tag with aliases moves them.
- Make primary: merging `Sci-Fi` into `sci fi` shows `sci fi` in counts,
  and the key's aliases follow it.
- Bulk Remove `fantasy` takes out `fantasy world` too; bulk Add writes the
  primary name. An import keeps raw tags.
- Suggestions: plurals and one-edit pairs appear, a Not the same pair
  doesn't, and a key never pairs with itself.
- The facet query stays fast on a library shaped like the live one.
