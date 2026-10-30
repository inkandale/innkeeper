# Card embeddings as a plugin

Date: 2026-10-30

Supersedes the "Embed instance", "Core calls `ai.embed`", "Embedded text"
and "When" rows of [discovery-design](2026-10-23-discovery-design.md).

## Purpose

Similar cards and semantic search rank cards by embedding distance. Today
core decides what text to embed, when, and with which instance, and forge-ai
writes the premise and themes that replace a card's raw text. This design
moves all of that into one plugin, `card-embed`, so:

- an owner sets up embeddings in one place, with whatever models they run;
- the summary prompt lives in one plugin, not inside Forge;
- Innkeeper runs on PostgreSQL without pgvector, with vector features off.

Core keeps storing vectors and ranking by them.

## Decisions

| Topic | Decision |
|---|---|
| Split | Core stores vectors in `card_embedding` and ranks Similar and semantic search. `card-embed` chooses the text, calls the models, and returns the vector. |
| Vector values | A new enrichment type, `vector`. Core's enrich job writes `vector` values to `card_embedding`, not `enrichment`. |
| Vector source | The install-wide setting `embed_instance` becomes `vector_instance`: the enrich instance whose vectors Similar and search read. One instance, since vectors from different models can't be compared. |
| Query embedding | Semantic search embeds the query through the vector instance, with the new method `vector.query`, so query and cards share a model. |
| Vector space | `card-embed` reports a `space` string from its embed model, its summary model and its prompt version. Core compares only rows from the current space. |
| pgvector | Optional. Core detects it at startup. Without it, vector features are off and everything else works. |
| When | An enrich instance can update cards as they change: on import, on edit, and on unarchive. An hourly sweep catches changes it missed. Backfill is a bulk enrich the owner starts. |
| Archived cards | Never sent to any enrich plugin, whatever started the run. |
| forge-ai | Loses `premise`, `themes` and the summary call. It scores and co-writes. |

## `card-embed`

An enrich plugin. `uses = ["ai.chat", "ai.embed"]`.

| Setting | Required | Meaning |
|---|---|---|
| `embed_connection` | yes | An `ai` instance offering `ai.embed` |
| `summary_connection` | no | An `ai` instance offering `ai.chat`. A small model is enough: Gemma 4 E4B matched Qwen3.5-9B in blind tests. |
| `concurrency` | no | Cards summarized at once. Default 4. |

Enrichments:

| Key | Type | Written when |
|---|---|---|
| `premise` | text | a summary connection is set |
| `themes` | list | a summary connection is set |
| `embedding` | vector | core sent `vectors` in `initialize` |

For each card:

1. With a summary connection, ask it for the premise and themes, at
   temperature 0. The prompt and parser move from forge-ai's `summary.go`
   and `prompts/summary.tmpl`.
2. With `vectors`, embed the premise and themes, or the card's own name,
   description, personality, scenario and creator notes when there is no
   summary connection.
3. Return the values. A failed summary fails the card; it doesn't fall back
   to raw text, so one card never has vectors from two kinds of text.

`space` is the embed connection's model name, plus the summary model name
and a hash of the summary prompt when a summary connection is set. A
setting change that changes `space` leaves the old vectors unread; the
owner re-embeds with a bulk enrich. Until then those cards fall back to
tag-based Similar.

`vector.query` takes `{"text": "..."}` and returns the embed connection's
vector for it, with `space`. Queries are embedded as written: a search
like "dragon who runs a tavern, slow burn" already reads like a premise.

Manifest: `enrich_on_change = true`, and an `enrich_max_cards` large enough
that backfilling the chub archive (269,368 cards) is one run.

## Core

### pgvector detection

The numbered migrations stop creating the extension and `card_embedding`.
At startup, core:

1. runs `CREATE EXTENSION IF NOT EXISTS vector`;
2. on success, runs `CREATE TABLE IF NOT EXISTS card_embedding (...)`;
3. on failure (not installed, or not permitted), logs one line naming the
   fix and sets vectors off for this process.

Installing pgvector and restarting turns vectors on. Existing installs
already have the table.

| Piece | Vectors on | Vectors off |
|---|---|---|
| Similar | Vector distance | Shared tags |
| `semantic: true` search, API and MCP | Ranked by meaning | Error: semantic search needs pgvector |
| Settings | Vector source picker | A note that semantic features need pgvector, linking `running.md` |
| `initialize` | `features: ["vectors"]` | No `vectors`, so `card-embed` makes no `ai.embed` calls |
| `vector` values from any plugin | Stored | Dropped, with one warning on the job |

`running.md` lists pgvector as optional.

### Updating cards as they change

- `enrich_on_import` becomes `enrich_on_change`. Core queues an enrich job
  for an instance's plugin when an import or an edit creates a new head
  revision, or a card is unarchived.
- The manifest flag is a default. Each enrich instance has a setting,
  "Update cards as they change", which the owner can turn off. Core
  records when it was turned on.
- The hourly sweep queues, per instance with the setting on, the cards
  whose head revision is newer than that time and holds no value from the
  instance. At most one sweep per instance is queued or running
  (`EnqueueOnce`), and each takes at most the plugin's card cap; the next
  tick continues.
- Turning the setting on never starts a backfill: older cards get values
  only from a bulk enrich the owner starts.

`internal/embed`, its hourly schedule, `Unembedded`, and the manifest's
`embed = true` flag are removed.

### Archived cards

`heads()` in `internal/enrich` drops cards that are archived or in an
archived library, and the job's progress counts them as skipped. Every
trigger passes through it. Archiving keeps a card's values and vectors;
Similar and search already exclude archived cards.

## Migration of an existing install

1. Delete `card_embedding` rows for the old `embed_instance`, which is an
   `ai` instance, not an enrich instance.
2. Add a `card-embed` instance with only an embed connection, and set it
   as the vector instance. A bulk enrich of every library brings back
   raw-text vectors.
3. Add a summary connection, then bulk enrich the libraries worth it. The
   chub archive takes about 11 hours with Gemma 4 E4B on one RTX 5090.

forge-ai has stored no `premise` or `themes` values on this install. On
another install, they stay stored and unread once forge-ai stops declaring
them.

## Testing

Core:

- Start on PostgreSQL without pgvector: startup succeeds, and each
  vectors-off row in the table above holds.
- Install pgvector and restart: vectors turn on.
- A `vector` value is stored in `card_embedding`; a value from another
  space is not ranked.
- An edit and an unarchive queue enrich for instances with the setting on,
  and not for instances with it off.
- The sweep queues only cards changed since the setting was turned on, and
  never two runs for one instance.
- An enrich run with archived card IDs sends none of them.

`card-embed`, with fake `ai.chat` and `ai.embed`:

- Embed connection only; both connections; no `vectors` feature.
- A failed summary fails the card and makes no embed call.
- The parser tests move from forge-ai's `summary_test.go`.

Live: the five-card harness from the Gemma evaluation, pointed at
`card-embed`.
