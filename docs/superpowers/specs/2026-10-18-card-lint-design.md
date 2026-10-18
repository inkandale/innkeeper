# Card lint design

Date: 2026-10-18

## Purpose

An imported archive holds thousands of cards, most of them not worth a
model's time. forge-ai's evaluate scores a card with one LLM call, which is
too slow and too costly to run on a whole archive. This spec adds a cheap
first pass, so you filter the archive in two stages:

1. **Lint** (light): `card-lint`, a new enrich plugin, scores every card with
   deterministic checks. It makes no model calls and runs in milliseconds per card.
2. **Evaluate** (full): forge-ai's existing enrich run scores only the cards
   that pass lint.

Builds on the [enrichments design](2026-10-15-enrichments-design.md).

## Decisions

| Topic | Decision |
|---|---|
| Where lint lives | A new plugin, `card-lint`, in innkeeper-plugins. It needs no core change. |
| Token counts | Estimated as a quarter of the characters, the same rule the web editor uses (`web/src/card/draft.ts`). This is close enough for filtering, and needs no tokenizer dependency. |
| Gate | One `lint` score, 0 to 100, is the grid key, so `hide_below` and the grid work on it unchanged. |
| Problems | A `problems` list of codes, shown on the card page, says why a card lost points. |
| Duplicates | Out of scope: the hide job already finds exact and near duplicates. |
| Language | An `english` score: the share of common English words in the text, with no language-detection dependency. A card below a set level gets the `not_english` problem, which costs whatever points the settings give it. |
| Tunables | Every threshold and every problem's points are instance settings. The `default` in the config schema is the only copy of each default. |
| Core change | One, and optional: an enrichment filter can name the instance it reads (see [Core](#core)). The two-stage flow works without it. |

## card-lint

### Manifest

```toml
name = "card-lint"
category = "Quality"
capabilities = ["enrich"]
description = "Score cards with fast checks that need no model: missing fields, size, repeated text, leftover placeholders, and markup. Use it to filter an archive before running a model-based evaluation."

[enrichments]
lint = { type = "score", range = [0, 100], label = "Lint", grid = true }
tokens = { type = "score", range = [0, 1000000], label = "Always-on tokens" }
problems = { type = "list", label = "Problems" }
english = { type = "score", range = [0, 100], label = "English" }
```

`tokens` is the estimate for the fields sent with every message: description,
personality, scenario, system prompt, and post-history instructions. The
quarter-of-characters rule undercounts Chinese, Japanese, and Korean text,
which runs closer to a token per character; filter on `english` first.

### English score

`english` is the percentage of the card's words, across description and first
message, that are in a fixed list of about 100 common English words (the,
and, you, her, is, ...), after removing macros and markup. Words in a
non-Latin script, such as Cyrillic, count as words but never match the list.
Chinese and Japanese are written without spaces, so each of their characters
counts as a word.

English prose lands around 40 to 60, other Latin-script languages under 15,
and Chinese, Japanese, Korean, or Russian near 0. The score says only whether
a card is English, not which language it is in; naming the language would
need a detection library, such as whatlanggo.

The plugin has no database and no `uses`.

### Checks

`lint` starts at 100 and loses each problem's points, down to 0. A problem
worth 100 points makes the score 0 on its own; one worth 0 is still listed in
`problems` but doesn't change the score.

| Code | Fails when | Default points |
|---|---|---|
| `no_name` | name is empty | 100 |
| `no_description` | description is empty | 100 |
| `no_first_message` | first message is empty | 100 |
| `too_short` | description, personality, and scenario together estimate under `min_tokens` | 100 |
| `not_english` | `english` is under `min_english` | 100 |
| `oversized` | always-on tokens estimate over `max_tokens` | 20 |
| `repeated_text` | a paragraph of `repeat_words` words or more appears in two always-on fields | 15 |
| `first_message_copies_description` | the first message contains the description's first paragraph, when that paragraph has `repeat_words` words or more | 15 |
| `placeholder` | a field holds one of `placeholders`, in any case, outside `{{...}}` macros | 15 |
| `no_char_macro` | neither description nor first message uses `{{char}}` or the card's name | 10 |
| `markup` | an always-on field holds HTML tags or a `<style>` block | 10 |
| `mojibake` | a field holds text decoded with the wrong character set (`Ã©`, `â€™`) or U+FFFD | 10 |

### Settings

The instance's config holds the thresholds and each problem's points:

```toml
[config]
type = "object"

[config.properties]
min_tokens = { type = "integer", title = "Too short under", minimum = 0, default = 50, description = "Estimated tokens across description, personality, and scenario." }
max_tokens = { type = "integer", title = "Oversized over", minimum = 1, default = 4000, description = "Estimated always-on tokens." }
repeat_words = { type = "integer", title = "Repeated text from", minimum = 5, default = 20, description = "Words in a paragraph that, found in two always-on fields, count as repeated." }
min_english = { type = "integer", title = "Not English under", minimum = 0, maximum = 100, default = 25, description = "The English score below which a card gets not_english." }
placeholders = { type = "array", title = "Placeholders", items = { type = "string" }, default = ["<BOT>", "<USER>", "{char}", "{user}", "[name]", "[character]"] }

[config.properties.points]
type = "object"
title = "Points"
description = "What each problem costs, 0 to 100. 100 sets the score to 0; 0 only lists the problem."

[config.properties.points.properties]
no_name = { type = "integer", minimum = 0, maximum = 100, default = 100 }
# ... one entry per code, with the defaults in the table above
```

The core saves only the values an owner changed. The plugin embeds its
`plugin.toml` and reads each `default` from it, so a default lives in one
place.

Changing a setting doesn't touch values already stored. To apply new settings,
run card-lint again with **Re-score cards already scored** ticked.

### `enrich.run`

The plugin runs every check on each revision in the batch. A revision whose
data won't decode gets an `Error` result, not a zero score.

### Tests

- One test per check: a card that trips only that check gets its code and
  loses its points.
- A card with a 100-point problem scores 0 whatever else it has.
- A clean card scores 100 with an empty `problems` list.
- Settings: lowering `max_tokens` makes a card `oversized`; setting a code's
  points to 0 keeps it in `problems` and restores the score; with no config,
  the plugin uses the schema's defaults.
- `english`: an English card scores at least 25; a Spanish, a Chinese, and a
  Russian card each score under 15. Fixtures are real excerpts, not word lists.
- `make check` runs the plugin through the real host, as for the other plugins.

## The two-stage flow

With today's core:

1. Import the archive.
2. Set the library's **Scores from** setting to the card-lint instance.
3. Select all and run card-lint. A run refuses a selection of over 10,000 cards
   (`enrich.MaxCards`), so split a larger archive by collection or tag and run
   each part.
4. Filter to `lint` at least 60, select all, and run forge-ai's enrich.
   The bulk run takes the filtered query and runs the instance you pick,
   so lint filters which cards forge-ai scores.
5. Set **Scores from** back to forge-ai to see quality on the grid, and hide or
   archive from there.

Step 5 switching the grid back and forth is the cost of having one score
instance per library.

## Core

Optional, and a separate change from the plugin.

An enrichment filter gains `instance`:

```json
{"enrichment": "lint", "instance": 12, "gte": 60}
```

It reads that enrich instance's value instead of the score instance's. Without
`instance`, the filter behaves as it does now. Sort and include stay on the
score instance.

With it, the library can keep forge-ai as its score instance, and a single
query selects "passed lint, not yet evaluated":

```json
{"and": [
  {"enrichment": "lint", "instance": 12, "gte": 60},
  {"enrichment": "quality", "missing": true}
]}
```

Through the API or MCP, this drops steps 2 and 5: the whole flow runs without
touching settings.

- **Search:** `enrichmentSQL` takes the instance from the filter when set,
  from `ScoreInstance` otherwise. A filter whose every enrichment term names
  an instance needs no score instance. An instance that isn't an enrich instance
  isn't refused; its cards just have no values, as with an unscored key.
- **Tests:** a filter naming an instance matches on that instance's value
  while the score instance holds a different one; one query combines two
  instances; `instance` without `enrichment` is refused.
- **Web:** out of scope. The filter sidebar still lists only the score
  instance's keys, so in the UI the flow still switches **Scores from**.

## Feeding lint into evaluate

Out of scope here. forge-ai could add its own token estimate to the evaluate
prompt so the model judges token efficiency against a measured size. That
change is local to forge-ai and can follow once lint is in use.
