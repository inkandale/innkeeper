# Discovery design

Date: 2026-10-23

## Purpose

A card page shows the card's variants and its copies in other libraries,
but nothing that is merely like it. This design adds three ways to find
cards:

- **Similar**: cards whose content reads like this card's, ranked by
  embedding distance, or by shared tags when no embed instance is set.
- **Related**: cards explicitly linked to this one by creator, lorebook, or
  collection.
- **Semantic search**: free-text search ranked by meaning instead of word
  overlap.

Each works on one library or across all of them.

## Decisions

| Topic | Decision |
|---|---|
| Embed instance | One install-wide setting, `embed_instance`, in the `setting` table, naming an instance that offers `ai.embed`. Install-wide because vectors from different models can't be compared, and similar works across libraries. |
| Core calls `ai.embed` | Core calls the embed instance directly, not through the services broker, since semantic search embeds the query when it runs. |
| What is embedded | Head revisions of cards only, in every library. Lorebooks are out of scope. |
| Embedded text | `name`, `description`, `personality`, `scenario`, and `creator_notes`, each cut to 100,000 characters as the `search` column cuts them. `first_mes` and example dialogue are left out, so the vector describes the character rather than its prose style. |
| When | Automatically. A scheduled `embed` job embeds every head revision without a vector from the current instance, so a new card or edit is embedded within a minute. Changing `embed_instance` queues the job at once. |
| Index | None. An exact cosine scan over 50,000 vectors takes about 100 ms. Add an HNSW index when an archive outgrows that. |
| Similar fallback | With no embed instance, similar ranks by shared tags. |
| Similar excludes | The card itself, its variant family, cards with the same `content_hash`, and archived cards. The Variants and OtherLibraries panels already show the first three. |
| Naming | `GET /cards/{id}/similar` (likely duplicates) becomes `GET /cards/{id}/duplicates`. Nothing in the web UI or MCP calls it. "Similar" then means this design's meaning only. |
| Paging | Every list pages with `library.Page` and returns `next_cursor`, as search does. |

## Model

Migration `0045_card_embedding.sql`:

```sql
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE card_embedding (
    revision_id bigint NOT NULL REFERENCES card_revision (id),
    instance_id bigint NOT NULL REFERENCES plugin_instance (id),
    model text NOT NULL,
    embedding vector NOT NULL,
    PRIMARY KEY (revision_id, instance_id)
);
```

The `vector` column has no fixed dimension, so a model change needs no
migration. Queries compare only rows of the current `embed_instance`, so
vectors of different dimensions never meet. Rows from an old instance stay
until the instance is deleted.

In `internal/library`, `Embeddings` stores and reads vectors. It is built
with the pool and an embedder, an interface defined here by its consumer:

```go
type embedder interface {
	Embed(ctx context.Context, texts []string) (instanceID int64, vectors [][]float32, err error)
}
```

The production embedder looks up `embed_instance` and calls `ai.embed`
through `plugins.Registry.Call`. It returns `ErrNoEmbedInstance` when the
setting is empty or names an instance that is disabled or gone.

## Jobs

An `embed` handler joins `jobs.Worker.Handlers`, scheduled every minute
with `Queue.EnsureScheduled`. A `PATCH /settings` that changes
`embed_instance` also queues it with `Queue.EnqueueOnce`.

- It selects head revisions with no `card_embedding` row for the current
  instance, in batches of 32, and stops when none are left. Import, edit,
  restore, and reconcile all move `head_revision_id`, so this one query
  covers every path without hooks in any of them, and a restarted job
  resumes where it stopped.
- It checks each `ai.embed` reply with `AIEmbedResult.Validate`.
- With no embed instance, it does nothing.
- A failed call fails the run, and the next scheduled run tries again. A
  card without a vector is missing from similar and semantic results until
  it has one.

## API

### Similar

`GET /cards/{id}/similar?scope=library|all&cursor=…&limit=20`

```json
{"basis": "embedding",
 "items": [{"card_id": 41, "library_id": 1, "score": 0.91}],
 "next_cursor": "…"}
```

- `basis: embedding`: `score` is `1 - cosine distance` between head
  revisions' vectors from the current embed instance.
- `basis: tags`: used when there is no embed instance, or the card has no
  vector yet. Candidates share at least two tags, found through the
  `card_tag_keys` GIN index with `&&`. `score` is the Jaccard index of the
  two tag sets.
- The cursor is `(score, card_id)`.
- `scope` defaults to `library`.

### Related

`GET /cards/{id}/related?group=…&cursor=…&limit=10`

With no `group`, the reply holds the first page of every group that has
results:

```json
{"groups": [
  {"group": "creator", "items": [{"card_id": 7, "library_id": 2}], "next_cursor": "…"},
  {"group": "lorebook", "items": [], "next_cursor": null}
]}
```

| Group | Cards | Scope |
|---|---|---|
| `creator` | Same non-empty `data.creator`, compared case-insensitively | All libraries |
| `lorebook` | Linked to any lorebook this card links to | All libraries |
| `collection` | Share a collection with this card | This library; collections are per-library |

Each group excludes the card itself, its variant family, and archived
cards, and orders by `card_id`. With `group`, the reply holds that group
only, paged by `cursor`.

### Semantic search

The card search input gains `semantic` (boolean). With `semantic` and `q`:

- Core embeds `q` once and adds a `similarity` sort key to search's
  existing `sortKey` and cursor code. Filters, family grouping, and paging
  work unchanged.
- `ts_rank` and the `search` match are not used. Every card with a vector
  is a candidate, ordered by distance to `q`.
- With no embed instance, the request fails with `ErrNoEmbedInstance`,
  which `Server.failAs` maps to 409, with a message naming the setting.

`search_cards` in MCP reaches this through the shared search code.

### Settings

`embed_instance` joins the install settings (`GET`/`PATCH /settings`). A
`PATCH` that changes it queues the `embed` job.

## MCP

| Tool | Arguments |
|---|---|
| `find_similar_cards` | `card_id`, `scope`, `cursor` |
| `find_related_cards` | `card_id`, `group`, `cursor` |
| `search_cards` | gains `semantic` |

## Web

- **Card page, Similar panel**: a This library / All libraries toggle and
  a tile list that loads the next page on scroll. When `basis` is `tags`, a
  note says the list is ranked by shared tags and links to the embed
  instance setting. A card from another library shows its library and an
  Import button, using the action `OtherLibraries` already has.
- **Card page, Related panel**: one collapsible section per group with
  results, each with Show more.
- **Library list**: a Semantic toggle beside the search box, shown only
  when an embed instance is set.
- **Settings**: an Embed instance picker listing instances that offer
  `ai.embed` (`GET /instances?offers=ai.embed`).

## Testing

Database tests run against PostgreSQL with pgvector, as the README
requires. `fakeplugin` already answers `ai.embed`.

- `TestEmbedJobEmbedsEditedHead`
- `TestChangingEmbedInstanceQueuesEmbedJob`
- `TestSimilarRanksByEmbeddingDistance`
- `TestSimilarFallsBackToTagsWithoutEmbedInstance`
- `TestSimilarExcludesVariantFamilyAndDuplicates`
- `TestSimilarAllScopeFindsOtherLibraries`
- `TestRelatedPagesOneGroup`
- `TestRelatedCollectionStaysInLibrary`
- `TestSemanticSearchPagesByCursor`
- `TestSemanticSearchWithoutEmbedInstanceIs409`

## Delivery

Three slices, each mergeable on its own:

1. Embeddings: migration, `embed_instance` setting, embedder, `embed` job,
   settings picker, and the `/duplicates` rename.
2. Similar and Related: API, MCP tools, and card page panels.
3. Semantic search: search input, MCP, and the library list toggle.
