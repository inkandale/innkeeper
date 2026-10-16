# Variants

Date: 2026-10-15

Ticket: INN-52

## Purpose

Users keep several cards that share a lineage: an NSFW rewrite, another
scenario, an author's v2. Nothing links them, so the library list shows each
as an unrelated row. This design links them and folds each lineage into one
row.

A **variant** is an ordinary card or lorebook that records the entity it came
from. A **family** is a root entity and every variant below it, at any depth.
The family's **face** is the member the library list shows for the whole
family.

Everything here applies to both kinds through `library.Kind`. Where only
cards have something (collections, lorebook links), the text says so.

## Decisions

| Topic | Decision |
|---|---|
| Link | Nullable `variant_of_id` on `card` and `lorebook`, referencing the same table. No cascade: entities are never hard-deleted. |
| Content | The link belongs to the entity, not its content. Setting or clearing it makes no revision. Editing a parent never changes its variants. |
| Shape | A tree. A variant of a variant is allowed. Writing a link that would make a cycle fails with `library.ErrVariantCycle` (409). |
| Root lookup | Worked out at query time with a recursive CTE over `variant_of_id`. Nothing stores the root, so nothing can drift from the link. |
| Face | The shallowest live member, then the oldest `created_at`, then the lowest `id`. If no member is live, the root. The face changes only on archive, unarchive, or a link change, never on edit. |
| Archive | Root lookup walks through archived members, so archiving never splits a family. |
| Leaving Innkeeper | The link never does. CCv3 exports and app pushes are unchanged. Backups use `pg_dump` and include the column with no extra work. |
| Defaults | The library UI groups by family, with an ungroup toggle. The API and MCP return ungrouped results unless asked. |
| Out of scope | Pulling hunks between two family members. Reconcile suggesting a variant on its own. |

## Delivery

One plan, in three slices that each merge on their own:

1. Model, link and unlink, fork, cycle check, MCP tools, the card page, and
   `CardPicker`.
2. Grouped search and collapsed family rows in the library list.
3. Reconcile "Import as variant", the shared-sources filter, and bulk "Make
   variants of…".

## Model

Migration `0025_variants.sql`:

- `variant_of_id bigint REFERENCES card(id)` on `card`, and the same on
  `lorebook` against `lorebook(id)`.
- A partial index on each, `WHERE variant_of_id IS NOT NULL`.

In `internal/library`:

- `ErrVariantCycle` joins the sentinel errors and `Server.failAs` maps it to
  409.
- `Entities.SetVariantOf(ctx, k, id, parent)` sets the link, or clears it when
  `parent` is nil. In one transaction it walks up from `parent` with a
  recursive CTE and returns `ErrVariantCycle` if it reaches `id`. Linking an
  entity to itself is a cycle. A parent that doesn't exist in the same kind's
  table is `ErrNotFound`.
- `Entities.Fork(ctx, k, id)` creates a variant from the head revision:

  | Copied | Not copied |
  |---|---|
  | head data, including `data.tags` | sources: a shared source is reconcile's strongest match signal |
  | avatar (the blob's sha256) | bindings |
  | overrides | |
  | cards only: `collection_card` and `card_lorebook` rows | |

  The slug is `freeSlug(<slug>-variant)`, giving `-variant`, then
  `-variant-2`. The name is unchanged. The first revision's message is
  `Variant of <slug> at r<rev>`. The new entity's `variant_of_id` is the
  source entity.

## API

For each kind, under its plural path (`/cards`, `/lorebooks`):

| Route | Does |
|---|---|
| `POST /{plural}/{id}/variants` | Forks the entity. Returns the new one. |
| `PUT /{plural}/{id}/variant_of` `{id}` | Makes `{id}` this entity's parent. |
| `DELETE /{plural}/{id}/variant_of` | Clears the parent. |

The wire `Card` and `Lorebook` from GET gain:

- `variant_of`: `{id, slug, name}` of the parent, or null.
- `variants`: the direct children, in the same shape.

Search hits don't carry these; grouped search has its own `family` field.

## Search

`library.Query` gains `group` (`""` or `"family"`) and a filter,
`variant_of_root: <id>`, which matches the root and every member below it.
Both `/cards/search` and `/lorebooks/search` get them through the shared
`library.Search`.

With `group: "family"`:

1. The existing compiled filter finds the matching IDs. Archived entities
   stay hidden unless the filter mentions archived.
2. A recursive CTE maps each match to its family's root.
3. Families are ordered by their best-placed match under the query's sort.
   For text search, a family ranks where its best hit ranks.
4. Paging and `total` count families. Facets still count entities.
5. Each hit is the family's face, which may be a member that didn't match.
   It carries `family: {root_id, size, matched: [ids]}`. `size` counts live
   members, or every member when the filter mentions archived.

A family whose members are all archived appears only under the archived
filter, because step 1 finds no live match.

`shares_source: true` is a filter for entities whose source (kind and ref)
also belongs to another entity of the same kind: an `EXISTS` over the kind's
source table.

## Reconcile

The wire resolution for `action: import` gains an optional `variant_of`. The
library import sets `variant_of_id` in the same transaction as it creates the
entity. `variant_of` on any other action is `ErrInvalid` (400).

Reconcile never suggests a variant, and autosync is unchanged.

## MCP

- `fork_card` and `set_variant_of` (null unlinks). Both take `kind`, default
  `card`.
- `search_cards` passes its query through, so it accepts `group` and the new
  filters with no change.

## UI

**`CardPicker`.** `OtherMatch` moves out of `web/src/sync/Review.tsx` into a
shared component. Review, the card page, and the bulk action use it.

**Card page and lorebook page:**

- Under the title, "Variant of <name>" links to the parent, with × to unlink.
- A "Variants" section lists the direct children. "New variant" forks and
  opens the new entity. "Link existing…" opens `CardPicker` and makes the
  picked entity a variant of this one.

**Library list (`Cards.tsx`):**

- Grouped by default. "Ungroup variants" is stored in localStorage under
  `innkeeper.library.ungrouped`, wrapped in try/catch as in `Facets.tsx`.
- A family row shows "+N variants". Expanding it runs a second search with
  `variant_of_root` and lists every member, marking the ones in `matched`.
- Selecting a collapsed row selects only its face.
- A "Shares a source" toggle in the facet panel sets `shares_source`.

**Reconcile (`Review.tsx`):** "Import as variant of this" beside "Import as
new", pre-filled with the candidate being compared.

**Bulk "Make variants of…":** a `SelectionBar` action that opens
`CardPicker`. When every selected entity shares a source, the picker is
pre-filled with the oldest. The client sends one `PUT …/variant_of` per
selected entity, skipping the chosen parent, and lists each failure, such as
a 409 cycle, by name.

## Tests

Against real PostgreSQL:

- A link that would make a cycle is refused, including a self-link.
- The face is picked through an archived root.
- Grouped paging and `total` count families.
- An import resolution with `variant_of` links the new entity.
- A fork copies tags and collections but not sources or bindings.
- `shares_source` matches only entities that share a source.

Web:

- A collapsed family row expands to its members.
- Reconcile "Import as variant of this" sends `variant_of`.
- The card page × sends `DELETE …/variant_of`.
