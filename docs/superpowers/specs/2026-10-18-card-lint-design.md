# Card lint design

Date: 2026-10-18

## Purpose

An imported archive holds thousands of cards, most of them not worth a
model's time. forge-ai's evaluate scores a card with one LLM call, which is
too slow and too costly to run on a whole archive. This spec adds a cheap
first pass, so you filter the archive in two stages:

1. **Lint** (light): `card-lint`, a new enrich plugin, checks every card and
   detects its language. It makes no model calls and runs in milliseconds per card.
2. **Evaluate** (full): forge-ai's existing enrich run scores only the cards
   you keep after lint.

You choose what to keep in the library sidebar, which counts each problem and
each language over the current search.

Builds on the [enrichments design](2026-10-15-enrichments-design.md).

## Decisions

| Topic | Decision |
|---|---|
| Where lint lives | A new plugin, `card-lint`, in innkeeper-plugins. |
| Token counts | Estimated as a quarter of the characters, the same rule the web editor uses (`web/src/card/draft.ts`). This is close enough for filtering, and needs no tokenizer dependency. |
| Problems | A `problems` list of codes. The sidebar counts each code, and you include or exclude cards by code. |
| Language | A `language` text value, such as English or Japanese, from whatlanggo. The sidebar counts each language, so which languages to keep is a filter, not a setting. |
| Gate | One `lint` score, 0 to 100, is the grid key, so `hide_below` and the grid work on it unchanged. Each problem's weight is fixed in the plugin. |
| Settings | Only thresholds: what counts as too short, oversized, or repeated, and the placeholder list. The `default` in the config schema is the only copy of each default. |
| Duplicates | Out of scope: the hide job already finds exact and near duplicates. |
| Run size | A run past its plugin's card cap needs confirmation; a plugin whose calls cost nothing raises its cap. See [Run size](#run-size). |

## card-lint

### Manifest

```toml
name = "card-lint"
category = "Quality"
capabilities = ["enrich"]
enrich_max_cards = 1000000

[enrichments]
lint = { type = "score", range = [0, 100], label = "Lint", grid = true }
tokens = { type = "score", range = [0, 1000000], label = "Always-on tokens" }
problems = { type = "list", label = "Problems" }
language = { type = "text", label = "Language" }
```

`tokens` is the estimate for the fields sent with every message: description,
personality, scenario, system prompt, and post-history instructions. The
quarter-of-characters rule undercounts Chinese, Japanese, and Korean text,
which runs closer to a token per character.

The plugin has no database and no `uses`.

### Language

`language` is whatlanggo's name for the language of the description and first
message, after macros and markup are removed: English, Mandarin, Japanese,
Russian, and so on. A card whatlanggo can't call reliably, such as one with
only a line of text, is `Unknown`.

whatlanggo is pure Go and adds no model files. It confuses short Cyrillic text:
two sentences of Ukrainian read as Serbian, so they come out `Unknown`.
lingua-go gets these right but makes the plugin 130 MB; switch if `Unknown`
turns out common.

### Checks

`lint` starts at 100 and loses each problem's points, down to 0.

| Code | Fails when | Points |
|---|---|---|
| `no_name` | name is empty | 100 |
| `no_description` | description is empty | 100 |
| `no_first_message` | first message is empty | 100 |
| `too_short` | description, personality, and scenario together estimate under `min_tokens` | 100 |
| `oversized` | always-on tokens estimate over `max_tokens` | 20 |
| `repeated_text` | a paragraph of `repeat_words` words or more appears in two always-on fields | 15 |
| `first_message_copies_description` | the first message contains the description's first paragraph, when that paragraph has `repeat_words` words or more | 15 |
| `placeholder` | a field holds one of `placeholders`, in any case, outside `{{...}}` macros | 15 |
| `no_char_macro` | neither description nor first message uses `{{char}}` or the card's name | 10 |
| `markup` | an always-on field holds HTML tags or a `<style>` block | 10 |
| `mojibake` | a field holds text decoded with the wrong character set (`Ã©`, `â€™`) or U+FFFD | 10 |

### Settings

| Setting | Default | Sets |
|---|---|---|
| `min_tokens` | 50 | `too_short` |
| `max_tokens` | 4,000 | `oversized` |
| `repeat_words` | 20 | `repeated_text`, `first_message_copies_description` |
| `placeholders` | `<BOT>`, `<USER>`, `{char}`, `{user}`, `[name]`, `[character]` | `placeholder` |

The core saves only the values an owner changed. The plugin embeds its
`plugin.toml` and reads each `default` from it.

Changing a setting doesn't touch values already stored. To apply new settings,
run card-lint again with **Re-score cards already scored** ticked.

### `enrich.run`

The plugin runs every check on each revision in the batch. A revision whose
data won't decode gets an `Error` result, not a zero score.

### Tests

- One test per check: a card that trips only that check gets its code and
  loses its points. A card with a 100-point problem scores 0.
- A clean card scores 100 with an empty `problems` list.
- Settings: lowering `max_tokens` makes a card `oversized`; a new placeholder
  is caught; with no config, the plugin uses the schema's defaults.
- `language`: real English, Spanish, Mandarin, and Russian excerpts get their
  names; a card with one line is `Unknown`.
- `make check` runs the plugin through the real host.

## The two-stage flow

1. Import the archive.
2. Set the library's **Scores from** setting to the card-lint instance.
3. Select all and run card-lint. Its cap is 1,000,000 cards, so one run covers
   the archive with no confirmation.
4. In the sidebar, pick the languages to keep under **Language** and exclude
   problems under **Problems**. Each count updates as you filter.
5. Select all and run forge-ai's enrich on what's left. Past 10,000 cards, the
   Evaluate panel asks you to confirm.
6. Set **Scores from** back to forge-ai to see quality on the grid.

Through the API or MCP, a filter can name the instance it reads, so steps 2 and
6 aren't needed:

```json
{"and": [
  {"enrichment": "language", "instance": 12, "is": "English"},
  {"not": {"enrichment": "problems", "instance": 12, "is": "no_first_message"}},
  {"enrichment": "quality", "missing": true}
]}
```

## Core

### Value filters and facets

- **Filter:** `is` matches a text value equal to it, or a list value holding
  it: `{"enrichment": "problems", "is": "markup"}`. It takes the place of a
  range or `missing`.
- **Instance:** `instance` on an enrichment filter reads that enrich
  instance's value instead of the score instance's. A query whose every
  enrichment filter names an instance needs no score instance.
- **Facet:** `enrichment:<key>` counts the matches per text value, or per list
  item, of the score instance's current value of key, most common first, at
  most 100. Results go in `facets.enrichments[key]`.
- **Web:** the sidebar has a section per text or list key of the score
  instance, like Tags: click to include, shift-click or − to exclude, only to
  pick one. A section counts over every filter but its own key's. Terms go in
  the URL as `f=value:<key>=<value>`.

### Run size

An enrich run takes at most 10,000 cards unless its plugin's manifest sets
`enrich_max_cards`. A request past the cap is refused unless it sends
`confirm: true`; with it, the job takes every matching card. The plugin listing
shows each enrich plugin's cap, and the Evaluate panel asks for the
confirmation when the selection is larger. `card_ids` still takes at most
10,000 IDs per request; a larger run sends a query.

A confirmed run is one job. The job already sends cards 20 at a time, and a
run without **Re-score** skips cards already scored, so running it again after
an interruption carries on where it stopped.

### Tests

- Search: `is` on a text and on a list value, negated, and combined with a
  facet; the facet counts list items and respects the filter; `is` with a range,
  `is` without `enrichment`, and a facet without a score instance are refused.
- API: a query past the plugin's cap is refused without `confirm` and runs every
  card with it; the plugin listing shows the cap.
- Web: a value section counts, filters, and excludes; Evaluate past the cap
  stays disabled until confirmed and sends `confirm`.

## Feeding lint into evaluate

Out of scope here. forge-ai could add its own token estimate to the evaluate
prompt so the model judges token efficiency against a measured size. That
change is local to forge-ai and can follow once lint is in use.
