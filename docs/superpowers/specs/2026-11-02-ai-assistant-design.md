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

The owner also wants to reforge a card by talking it through: ask for a
change, see it, push back, and go again. The Reforge panel runs one
instruction per run and can't hold a conversation. forge-ai already has the
house style, skills, and library tool loop that card writing needs, so the
chat moves into forge-ai as the **AI Assistant**, an optional panel that
finds cards and edits the one in the Forge.

## Decisions

| Topic | Decision |
|---|---|
| Home | forge-ai gains the `ui` capability and the chat. The `card-advisor` plugin is retired before it is released. |
| Switch | Core gives every `ui` plugin instance a **Show panel** switch, off by default. forge-ai's panel is titled "AI Assistant". |
| Editing | The chat writes the changed fields itself and shows them in the conversation. **Send to Review** puts them in the Forge's Review tab, where the owner takes or keeps each field as for any run. |
| What the chat sees | The Forge's current draft, unsaved changes included, on every turn while the card is open in the Forge. Whatever the owner took or kept in Review shows in the next turn. |
| Output | A turn ends with a `reply` tool call holding typed **blocks**: `text`, `picks`, `table`, and `proposal`. The panel draws each type with its own code. Skills choose blocks; they can't add new block types. |
| Writing rules | The system prompt carries the revise action's instructions, including the owner's overrides from the Prompts tab. The chat also gets forge-ai's enabled skills through `load_skill` and `load_skill_file`. |
| Writes | Unchanged: the plugin never writes to the library. A proposal only reaches the draft through Review, and the draft only reaches the library when the owner saves. |
| Scope | Cards only. Lorebooks in the Forge come later. |

## Core

### Show panel switch

- `[ui]` gains a required `title`, such as "AI Assistant".
- The instance settings page shows **Show {title}** above the permission
  switches. It is stored as the `panel` grant, which core gives to every
  `ui` manifest; manifests don't declare it.
- `Launchers` shows a bubble only for instances with the `panel` grant.
- No migration: the only `ui` plugin so far is the unreleased card advisor.

### The draft and proposals

A new `[ui]` permission, `cards.edit`, off until granted, covers both parts
below.

**Draft in context.** While a card's Forge is open, the Forge publishes its
draft to the Shell. `host.context()` adds `draft: {card, revision, data}`,
where `revision` is the revision the draft started from. It is present only
with the `cards.edit` grant, and only while that Forge is open.

**`propose` request.**

| Call | Does | Needs |
|---|---|---|
| `host.request("propose", cardId, {revision, proposal})` | Opens the card's Forge on the Review tab with `proposal` | `cards.edit` |

- The bridge refuses a `proposal` that isn't an object of strings or is over
  256 KiB.
- The Forge treats it like a finished run held in the tab. Its start is the
  data the chat saw: the draft from context when `revision` matches it,
  otherwise that revision's data. Review compares start, draft, and proposal
  with the card subject's `review`, so a field the owner edited since then
  shows both versions.
- Keys the card subject doesn't review are dropped, with a warning above the
  table, as for out-of-scope changes from an action.
- A newer proposal supersedes an unreviewed one, as a newer run's does.
- A panel proposal lives in the tab only. If the tab closes before review,
  the chat still has it and **Send to Review** sends it again.

## Plugin: forge-ai

### What moves in

| From `card-advisor` | Into `forge-ai` |
|---|---|
| `store.go` (conversations, messages) | `assistant_store.go`, tables in forge-ai's schema |
| `turn.go`, `tools.go` (one turn, search tools) | `assistant.go`, on forge-ai's tool loop in `chat.go` |
| `ui/` | `ui/` |

`complete` in `chat.go` ends a run by offering no tools in its last round.
The assistant must end on `reply`, so `toolbox` gains an optional closing
tool: when it is set, the last round offers only that tool, and the loop
stops once the model calls it. Actions leave it unset and behave as before.

The assistant uses forge-ai's AI connection. It has no settings of its own.

Manifest additions:

```toml
capabilities = ["action", "records", "enrich", "settings", "ui"]

[ui]
title = "AI Assistant"
entry = "ui/index.html"
slot = "corner"
permissions = ["library.read", "cards.push", "cards.collect", "cards.edit"]
```

### One turn

- The panel sends the turn's text and, when `host.context()` has one, the
  draft. forge-ai rejects a draft over 256 KiB.
- The tools are `search`, `similar`, `related`, `read_card`, the skill
  tools, and `reply`. The last round offers only `reply`.
- With a draft, the system prompt adds the revise instructions and the
  draft, and says to change only what the owner asked for. Without one, the
  chat can still read and discuss any card, but can't propose changes.
- The system prompt says that picks, comparisons, and edits go in blocks,
  never in Markdown tables or pasted field text.

### Blocks

`reply` takes `blocks`, an array of at most 8:

| Block | Fields | The panel shows |
|---|---|---|
| `text` | `markdown` | Paragraphs, bullets, bold, and italic |
| `picks` | `picks: [{card_id, reason}]`, at most 6 | A box per card with its avatar, name, reason, and the Open, Push, and Add to collection buttons the instance is granted |
| `table` | `columns: [string]`, `rows: [[cell]]`; a cell is a string or `{card_id}`; at most 6 columns and 20 rows | An HTML table; a card cell shows the card's name as a link that opens it |
| `proposal` | `summary`, `fields: {key: text}` | The summary and, per field, its label and new text, collapsed past a few lines. **Send to Review** sends it with the draft's revision. Shown only with the `cards.edit` grant. |

The server checks every block before saving the reply:

- Each `card_id` must come from a tool result in this turn.
- A `proposal` needs a draft in this turn. Its keys must be keys of the
  draft's data, and at most one `proposal` block is allowed per reply.
- Every string is UTF-8 and trimmed. A reason, cell, or summary is at most
  one line.
- A check that fails is returned to the model as a tool error, as
  `recommend`'s are today.

A saved message stores its blocks as JSON, which replaces the `picks` column.
Earlier turns go back to the model as text, so it can see what it said and
proposed.

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
- **Card in the Forge:** a line above the input says "Editing {name}" while
  the chat can see a draft.

## Testing

- Core: manifest validation of `title` and `cards.edit`; the `panel` grant
  hides and shows the bubble; `host.context()` includes the draft only with
  the grant and an open Forge; the bridge refuses a bad `propose`; a good one
  opens Review with the right start, drops unreviewed keys with a warning,
  and supersedes an older unreviewed proposal.
- forge-ai: each block type's checks, including a card the turn never
  returned and a proposal without a draft or with an unknown key; the revise
  instructions and draft reach the system prompt; stored blocks round-trip;
  the skill tools are offered; an action's run still ends without tools.
- Panel: `markdown.js` pipe tables; block rendering never creates HTML from
  model text; the history filter.

## Out of scope

- Lorebooks.
- Taking a proposal straight into the draft from the chat. Review stays the
  one place where changes are accepted.
- A `compare` block. Add it when a skill needs one.
- Server-side history search and paging.
