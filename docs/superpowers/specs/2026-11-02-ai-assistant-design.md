# AI Assistant

Date: 2026-11-02

Builds on [Card advisor and plugin UI](2026-11-01-card-advisor-design.md)
and [the Forge](2026-10-14-forge-design.md).

## Purpose

The card advisor works, but in use it has three problems:

- Past conversations sit in a `<select>`, which gets hard to use as the list
  grows.
- The owner's messages and the model's replies look almost the same.
- The model formats its picks as Markdown tables, which the panel shows as
  raw pipes.

The owner also wants to use the chat to reforge a card over several turns,
not only to find one. forge-ai already holds what that needs: the house
style, skills, the library tool loop, and the revise action. So the chat
moves into forge-ai as the **AI Assistant**, an optional panel the owner
switches on.

## Decisions

| Topic | Decision |
|---|---|
| Home | forge-ai gains the `ui` capability and the chat. The `card-advisor` plugin is retired before it is released. |
| Switch | Core gives every `ui` plugin instance a **Show panel** switch, off by default. forge-ai's panel is titled "AI Assistant". |
| Reforging | The chat doesn't write card text. A turn can end with a reforge instruction. Its button opens the card's Forge with the Reforge panel filled in, and the owner presses Run. The revise action does the writing, with the house style, and the result goes through the Review tab as usual. |
| Output | A turn ends with a `reply` tool call holding typed **blocks**: `text`, `picks`, `table`, and `reforge`. The panel draws each type with its own code. Skills choose blocks; they can't add new block types. |
| Skills | The assistant offers forge-ai's enabled skills through `load_skill` and `load_skill_file`, as actions do. |
| Writes | Unchanged: the plugin never writes to the library. Every button opens a core screen where the owner confirms. |

## Core

### Show panel switch

- `[ui]` gains a required `title`, such as "AI Assistant".
- The instance settings page shows **Show {title}** above the permission
  switches. It is stored as the `panel` grant, which core gives to every
  `ui` manifest; manifests don't declare it.
- `Launchers` shows a bubble only for instances with the `panel` grant.
- No migration: the only `ui` plugin so far is the unreleased card advisor.

### `reforge` request

| Call | Does | Needs |
|---|---|---|
| `host.request("reforge", cardId, instruction)` | Opens the card's Forge with the Reforge panel open and its instruction box set to `instruction` | `cards.reforge` |

- `cards.reforge` is a new `[ui]` permission, off until granted, like the
  others.
- The bridge refuses an instruction that isn't a string, is empty, or is
  over 8 KiB.
- The instruction goes in the box as text. Its `@cards/` and `@lorebooks/`
  mentions resolve the way typed ones do, so an unknown name is underlined
  and blocks Run.
- Nothing runs until the owner presses Run. An unsaved draft on that card
  stays; the Forge already reviews a proposal against the draft field by
  field.

## Plugin: forge-ai

### What moves in

| From `card-advisor` | Into `forge-ai` |
|---|---|
| `store.go` (conversations, messages) | `assistant_store.go`, tables in forge-ai's schema |
| `turn.go`, `tools.go` (one turn, search tools) | `assistant.go`, on forge-ai's tool loop in `chat.go` |

`complete` in `chat.go` ends a run by offering no tools in its last round.
The assistant must end on `reply`, so `toolbox` gains an optional closing
tool: when it is set, the last round offers only that tool, and the loop
stops once the model calls it. Actions leave it unset and behave as before.
| `ui/` | `ui/` |

The assistant uses forge-ai's AI connection. It has no settings of its own.

Manifest additions:

```toml
capabilities = ["action", "records", "enrich", "settings", "ui"]

[ui]
title = "AI Assistant"
entry = "ui/index.html"
slot = "corner"
permissions = ["library.read", "cards.push", "cards.collect", "cards.reforge"]
```

### One turn

As in the card advisor, with these changes:

- The tools are `search`, `similar`, `related`, `read_card`, the skill
  tools, and `reply`. The last round offers only `reply`.
- When `host.context()` names a card on screen, the turn's input carries its
  ID, and the system prompt tells the model which card is open.
- The system prompt says how to reforge: talk the change through with the
  owner, then end with a `reforge` block whose instruction says what to
  change and what to keep. It also says that picks and comparisons go in
  blocks, never in Markdown tables.

### Blocks

`reply` takes `blocks`, an array of at most 8:

| Block | Fields | The panel shows |
|---|---|---|
| `text` | `markdown` | Paragraphs, bullets, bold, and italic |
| `picks` | `picks: [{card_id, reason}]`, at most 6 | A box per card with its avatar, name, reason, and the Open, Push, and Add to collection buttons the instance is granted |
| `table` | `columns: [string]`, `rows: [[cell]]`; a cell is a string or `{card_id}`; at most 6 columns and 20 rows | An HTML table; a card cell shows the card's name as a link that opens it |
| `reforge` | `card_id`, `instruction`, `summary` | The summary, the instruction, and a **Reforge in Forge** button, shown only with the `cards.reforge` grant |

The server checks every block before saving the reply:

- Each `card_id` must come from a tool result in this turn. A `reforge` block
  may also name the card on screen.
- Every string is UTF-8 and trimmed. A reason, cell, or summary is at most one
  line. An instruction is at most 8 KiB.
- A check that fails is returned to the model as a tool error, as
  `recommend`'s are today.

A saved message stores its blocks as JSON, which replaces the `picks` column.
Earlier turns go back to the model as text, so it can see what it said.

The panel builds every block from text nodes, never from HTML.
`markdown.js` also turns a GFM pipe table in a `text` block into a table,
because some models write one anyway.

### Panel

- **History:** a **History** button opens a drawer over the conversation. It
  has a search box that filters titles, conversations grouped as Today,
  Yesterday, This week, and Older, and a Delete button on each row with the
  same two-step confirm. `list_conversations` already returns every
  conversation; it gets a limit when the list gets slow.
- **Messages:** the owner's messages are right-aligned bubbles tinted with
  the accent color. The assistant's are full width under a muted "AI
  Assistant" label. Turns have more space between them than messages inside
  a turn have.
- **Card on screen:** when a card is open, a line above the input says
  "Talking about {name}".

## Testing

- Core: manifest validation of `title` and `cards.reforge`; the `panel`
  grant hides and shows the bubble; the bridge refuses a bad `reforge`
  request and opens the Forge with the instruction set for a good one.
- forge-ai: each block type's checks, including a card the turn never
  returned; the card on screen allowed in `reforge`; the stored blocks
  round-trip; the skill tools are offered to the assistant.
- Panel: `markdown.js` pipe tables; block rendering never creates HTML from
  model text; the history filter.

## Out of scope

- The chat seeing the Forge's unsaved draft or the proposal in Review. The
  owner tells it what they thought of the result.
- The chat writing field text itself.
- A `compare` block. Add it when a skill needs one.
- Server-side history search and paging.
