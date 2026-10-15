# The Forge design

Date: 2026-10-14

## Purpose

The Forge is the card page, upgraded to make and reshape cards with AI. It is
sub-project 4 of the [core design](2026-10-03-innkeeper-core-design.md),
"Editor and AI". The owner can generate a card from a concept, reforge a
whole card from one instruction ("make the family robots, @cards/Billy is
the brother"), rework one field, and evaluate a card and refine it from the
findings. Every AI change arrives as a proposal the owner reviews field by
field before it joins the draft.

Without an AI plugin installed, the Forge is the plain card editor.

The layout comes from a clickable prototype on branch `prototype/forge`,
variant D.

## Decisions

| Topic | Decision |
|---|---|
| What the Forge is | The card page at `/cards/:id`, upgraded. There is one editor, not a second one. |
| AI home | A plugin offering a new `action` capability. Core never knows what an action does. |
| Actions | Declared by the plugin: label, description, what it applies to, optional `fields`, inputs. |
| Running | Every action run is a job. The plugin sends progress messages; there is no token stream. Stop cancels the job. |
| Results | A proposal (complete data for one entity), a report, or both. |
| Scope | Core drops proposal changes outside the action's scope. A field action changes only its field. |
| Review | Per field: take the proposal or keep yours. Taken fields join the draft. Saving is the usual Save, so an AI edit is an ordinary revision. |
| Locking | While a run is going, the Forge locks only the fields in its scope, in the browser. |
| Entities | A proposal changes one entity. Card actions change cards; lorebook actions change lorebooks. |
| Context | Cards, lorebooks, and images the owner picks travel in the run. A plugin can also read the library through `library.call`, if its action asks and the owner allows it on that instance. Off by default. |
| Tier | Starting a run needs `write`: it spends money and sends cards to a third party. |
| MCP and CLI | Not in this project. |
| Model settings | The AI plugin's instance settings. One instance per model setup. |
| AI plugin | Go, in `innkeeper-plugins`, using [any-llm-go](https://github.com/mozilla-ai/any-llm-go). This replaces the core design's "Python for AI actions". |
| Prompts | Built into the plugin. The owner adds a house style and per-action additions in the instance settings. |
| Images | No image generation. The owner can upload an avatar to a new card, and pass a reference image or the current avatar to an action. |
| Skills | Plugin-owned, managed in the UI through a new generic `items` capability. Phase 5. |
| Stored scores | Through the core design's `enrich` capability. Phase 7. |

## The Forge page

Variant D of the prototype. A filled hammer icon in the accent color marks
everything Forge-related.

### Layout

- The left panel stays as it is.
- The main column has tabs: **Fields**, **Review (n)**, and **Report**. A new
  card has **Start**, **Fields**, and **Review**.
- On the right of the tabs is the primary hammer button, **Reforge**, or
  **Forge** on a new card. `⌘K` or `Ctrl+K` opens it too. It is hidden when
  no installed plugin offers `action`.

### Fields tab

- Every text field is a macro-highlighted textarea (see
  [Editor additions](#editor-additions)).
- A field with field actions shows a hammer button in its header, which opens
  a menu of them, such as Tighten, Expand, Convert to prose, and Rewrite….
  An action with inputs opens the Reforge panel set to that action and field.
- After an evaluation, a field with findings shows a badge per severity in
  its header. A badge opens the Report tab at that field.

### Reforge panel

A panel for changing the whole card, titled "Reforge {name}":

1. A large instruction box. Enter runs the card's revise action with it;
   Shift+Enter adds a line. Placeholder: "Reforge this card… e.g. make the
   family robots · @cards/Billy is the brother".
2. A chip row of what the mentions attach. Removing a chip removes its
   mention.
3. "Or change the whole card": the other whole-card actions, as cards with
   their label and description, such as Evaluate and Change point of view.
   An action with inputs shows them before Run.
4. A footer with the model setup picker, a hint line, and Run.

The revise action is the one whose declaration sets `instruction: true`;
the instruction box is its text input. If no action sets it, the box is
hidden.

### Mentions

In any instruction or concept box, `@` starts a mention:

| Mention | Attaches |
|---|---|
| `@cards/Billy` | That card's head revision, as context |
| `@lorebooks/Northern Reach` | That lorebook's head revision, as context |
| `@skills/college` | That skill, forced on. From phase 5 |

- Typing `@` offers the types, then names, filtered as the owner types.
  Arrow keys and Enter or Tab insert one.
- Picking from the list records the item's ID. A mention typed by hand
  resolves by exact name; an unknown or ambiguous one is underlined in red
  and blocks Run until fixed.
- The text sent to the plugin keeps the mentions as written, so "Billy should
  be the brother" still makes sense to the model.
- Until phase 5, the list offers no `skills/`.

### Running

A pinned header under the tabs shows the action, the model setup, the
plugin's latest progress message (such as "Writing Personality (3 of 8)"), a
bar when the plugin sends a total, elapsed time, and Stop.

- Fields in the run's scope are read-only and marked "locked while {action}
  runs". Others stay editable, so the owner can run a field action on one
  field while editing another.
- A whole-card action locks every text field. A field action locks its
  field. A report-only action locks nothing.
- One run at a time per field. A second whole-card run waits for the first.
- Closing the tab doesn't stop the job. On return, the Forge finds the
  card's unfinished or unreviewed runs from the job list and shows them.
- After a run, "View raw response" opens the model's raw output.

### Review tab

Opens when a proposal arrives. One row per changed field:

| Your draft | Proposal | Choice |
|---|---|---|
| The field now | The proposal as a word diff against the run's starting data | Take proposal / Keep mine |

- The word diff reuses `wordDiff` from `web/src/sync/sync.ts`.
- A sticky footer reads "Apply {n} of {m} changes to draft" with Discard.
  Apply puts the taken fields in the draft and returns to Fields, where they
  show as unsaved.
- Core's out-of-scope warnings show above the table.

### Report tab

- An overall score ring and the summary, rendered as Markdown with
  `react-markdown`, which renders no raw HTML.
- A table of dimension scores, each with a bar and a comment.
- Findings grouped by field, each with severity, text, and a checkbox. A
  finding on several fields appears under the first, with "Also: …". A
  "move" finding shows the excerpt and its destination field.
- Suggestions with checkboxes.
- **Refine with {n} selected** runs the revise action with the ticked
  findings and suggestions as its instruction, and opens Review when done.

### New card

A **New card** button on the Cards page opens `/cards/new`.

- The draft lives in the browser, saved to `localStorage` under one key, read
  and written inside `try`/`catch`. The page works without it.
- The Start tab is one form: a concept box with mentions, a point-of-view
  choice, a reference-image drop zone, an avatar upload with the existing
  crop dialog, and **Forge**, which runs the plugin's `new` action.
- The owner can skip Start and type the fields by hand.
- The first Save uploads the avatar with `POST /blobs`, calls `POST /cards`
  with the data and avatar, clears the stored draft, and moves to
  `/cards/:id`.

## Editor additions

Phase 1, before any AI:

- `nickname` and `character_version` in the left panel.
- `group_only_greetings`, with the same component as alternate greetings.
- `creator_notes_multilingual`: a list of language code and notes pairs.
- Macro highlighting in every text field: any `{{…}}` token, and `<START>` in
  example dialogue, get a tinted background. A mirrored layer behind a
  transparent textarea draws it; there is no editor library. Nothing is
  marked as an error.
- New card, as above, without the Forge button until phase 4.

Still out of scope: rich text and code editors, real tokenizers, editing
`extensions` and regex scripts, and V3 `assets`.

## The `action` capability

### Declarations

**`action.list`** takes no params. Reply with `actions`, each with:

| Field | Meaning |
|---|---|
| `id` | 1 to 40 lowercase letters, digits, and hyphens, unique within the plugin |
| `label` | Short name for buttons and menus |
| `description` | One line for the Reforge panel |
| `applies` | `card`, `lorebook`, or `new` (a new card) |
| `fields` | For a field action: the data keys it can run on. Absent for a whole-entity action |
| `instruction` | `true` if the action's main input is free text, which the Reforge panel's box fills |
| `inputs` | A JSON Schema with `"type": "object"` for the other inputs, rendered with `SchemaForm` |
| `report_only` | `true` if it never proposes changes |
| `library` | `true` if it may call `library.call` |
| `images` | `true` if it accepts a reference image |

The core caches the list for a minute per instance, as it does `mcp.tools`.

### Running

**`action.run`** params:

| Param | Meaning |
|---|---|
| `run` | Token for this call, as in `import.run` |
| `action` | The action's `id` |
| `field` | For a field action: the field it runs on |
| `entity` | `{"kind": "card" \| "lorebook", "data": {...}}`: the draft the run started from. Absent data for `new` |
| `instruction` | The instruction text, mentions as typed |
| `inputs` | Values for the declared `inputs` |
| `context` | `{"cards": [...], "lorebooks": [...], "skills": [...]}`. Cards and lorebooks are `{"id", "name", "data"}` at their head revisions. Skills are names |
| `images` | Absolute paths in the blob store: `reference`, `avatar`. Read them; don't move or change them |

While it runs, the plugin sends `progress` notifications with `run`,
`message`, and optional `done` and `total`, as in `import`.

Reply with:

| Field | Meaning |
|---|---|
| `proposal` | Complete `data` for the entity, or absent. For a card, without `character_book` |
| `report` | A report, or absent |
| `raw` | The model's raw output, for "View raw response" |

A report:

```json
{
  "summary": "Markdown text",
  "score": 78,
  "dimensions": [{"key": "voice", "label": "Voice", "score": 88, "comment": "…"}],
  "findings": [
    {"severity": "high", "text": "…", "fields": ["description", "scenario"]},
    {"severity": "medium", "text": "…", "fields": ["scenario"], "move": {"excerpt": "…", "to": "description"}}
  ],
  "suggestions": ["…"]
}
```

`severity` is `high`, `medium`, or `low`. Every field except `summary` is
optional.

### Scope

After the reply, core compares the proposal with `entity.data`:

- A field action may change only its `field`.
- A `card` or `lorebook` action may change any data key.
- A `new` action may set any data key.
- A `report_only` action's proposal is dropped.

Core drops changes outside the scope, keeps the rest, and adds a warning per
dropped key to the result.

### Library access

A plugin may send **`library.call`** while it answers an `action.run`:

| Param | Meaning |
|---|---|
| `run` | The run's token |
| `name` | An MCP tool name |
| `arguments` | The tool's arguments |

- Core answers only when the action declares `library: true` and the owner
  has turned on **Library access** for that instance. Otherwise it replies
  with an error.
- Only `read` tier tools run: `search_cards`, `get_card`, `get_lorebook`,
  `get_history`. Core serves them in process through the API, as the MCP
  server does, with a read-tier caller. A write tool returns an error.
- The reply is the tool's `content`, as `mcp.call` returns it.
- Access ends when the run does.

**Library access** is a core-owned setting on each instance of a plugin
with `action`, shown on its settings page. It defaults to off.

### Plugin checks

`innkeeper check` gains `action` checks: `action.list` answers with valid
declarations, and a run of each listed action, given a fixture card,
returns a valid reply or a plugin error. The fake plugin in
`internal/fakeplugin` offers a field action, a whole-card action, a
report-only action, a `new` action, one action that proposes out-of-scope
changes, and one that calls `library.call`.

## Core

### Job

A new job kind, `action`, with the instance as its `InstanceID`. Its payload
holds the `action.run` params, except context and images, which are stored as
IDs and blob SHAs and resolved when the job starts.

- The job's progress holds the latest message, `done`, and `total`. When the
  run ends, it also holds `result`: the scoped `proposal`, the `report`,
  `raw`, and `warnings`. The job list cuts `result`, as it does a
  reconcile's progress.
- A run is not retried: a failed or crashed run fails the job with the
  error. Running it again is the owner's choice.
- Stop uses the existing job cancel.
- Results go when the daily cleanup removes jobs over 30 days old.

### API

| Method and path | Tier | Does |
|---|---|---|
| `GET /actions?applies=card` | read | Declarations from every enabled `action` instance, each with `instance_id` and `instance_name` |
| `POST /actions/runs` | write | Starts a run. Body: `instance_id`, `action`, `field`, `entity` (`kind`, `id` or none, `data`), `instruction`, `inputs`, `context` IDs and skill names, `images` SHAs. Returns `202` with the job |
| `GET /jobs?kind=action&card_id=…` | read | Finds a card's runs, for the Forge to pick up after a reload |

Starting a run returns `400` when the action doesn't exist on the instance,
doesn't apply to the entity kind, or names a field outside its `fields`.

## The AI plugin

`innkeeper-plugins/forge-ai`, in Go, with capabilities `action` and, from
phase 7, `enrich`.

### Settings

| Setting | Meaning |
|---|---|
| `provider` | Which any-llm-go provider: `openai-compatible`, `openai`, `anthropic`, `gemini`, `mistral`, `groq`, `deepseek`, `ollama`, `llamacpp`, `llamafile` |
| `base_url` | For `openai-compatible`, `ollama`, `llamacpp`, `llamafile`, or to override the provider's default |
| `api_key` | A secret. Optional for local providers |
| `model` | Model name |
| `temperature`, `max_tokens` | Optional |
| `house_style` | Text added to every action's system prompt |
| `additions` | Optional text per action ID, added to that action's prompt |

`openai-compatible` uses `openai.NewCompatible` with `RequireAPIKey` off, for
vLLM, llama.cpp servers, LM Studio, and OpenRouter. The `openai` provider
requires a key.

### Prompts

The prompts in `character-card-generator/src/services/prompts.js` move into
the plugin as embedded template files. They were rewritten for CCv3 in that
repo's commit `0b2f57e`.

- Each prompt asks for JSON. The plugin streams the model's output, parses
  fields as they complete, and sends a progress message per field.
- `<think>` blocks from reasoning models are stripped, as the generator does.
- Library tool calls use any-llm-go's tool calling. On a model without tool
  support, the plugin skips library access and the explicit context still
  goes in the prompt.

### Actions

| ID | Applies | Kind | From |
|---|---|---|---|
| `generate` | `new` | Concept, point of view, reference image, context | Generator |
| `revise` | `card` | Instruction (`instruction: true`), context | Generator |
| `evaluate` | `card` | Report only. The generator's six dimensions and overall score | Generator |
| `tighten-card` | `card` | Whole card, no inputs | New |
| `change-pov` | `card` | Point of view | New |
| `tighten`, `expand`, `to-prose`, `rewrite` | `card`, fields: the prose fields | Field actions; `rewrite` takes an instruction | Character Architect's presets |
| `examples` | `card`, fields: `mes_example` | Number of examples | Generator |

Phase 6 adds `alternate-greetings` (whole card) and `lore-entries`
(lorebook, with the card as context).

## Plugin-owned items

Phase 5. A generic capability for data a plugin owns and the owner manages
in the UI. Skills are its first use. The capability's final name is settled
in its plan; `items` here.

- The manifest declares item types under `[items]`, each with a label and a
  JSON Schema for one item.
- Protocol methods: `items.list`, `items.get`, `items.put`, `items.delete`,
  and `items.import`, which takes a URL or an uploaded file's path in
  `blob_tmp`.
- Core stores nothing. It renders a list, a `SchemaForm` editor, and Import
  for each item type, on the instance's settings page.
- The plugin keeps its items in its own schema (`database = true`).
  Backups already dump plugin schemas.

### Skills in the AI plugin

- A skill is a `SKILL.md` in the Agent Skills format, with `name` and
  `description` frontmatter, plus optional reference files.
- Import takes a `SKILL.md`, a zip of a skill folder, or a URL to either.
- Instance settings choose which skills are enabled.
- The model sees enabled skills' names and descriptions and loads one
  through a `load_skill` tool, and a reference through `load_skill_file`.
  A `@skills/` mention forces a skill's body into the prompt. A model
  without tool calling gets only forced skills.
- `GET /actions` lists each instance's enabled skills, which feeds the
  `@skills/` autocomplete.

## Enrichments

Phase 7, with its own spec. The AI plugin offers `enrich` using the evaluate
prompt. Scores land on each revision as the core design describes, show on
the library grid, filter and sort in search, and go stale when the card
changes. A bulk action evaluates a selection.

## Phases

Each phase merges on its own and leaves the app working. Plans are written
one phase at a time, just before the phase starts.

1. **Editor additions**: new card, draft avatar, missing V3 fields, macro
   highlighting.
2. **The `action` capability**: protocol doc, declarations, job kind, scope,
   context, `library.call` and the Library access setting, API, fake plugin,
   checks.
3. **The AI plugin**: settings, prompts, and the first actions.
4. **The Forge's AI interface**: tabs, Reforge panel, mentions, field
   menus, running, Review, Report, Refine, images.
5. **Plugin-owned items and skills**.
6. **More actions**: alternate greetings, lore entries, actions on the
   lorebook page.
7. **Enrichments**.

Phases 2 and 3 can overlap once the protocol section of
[docs/protocol.md](../../protocol.md) is written.

## Later

- Macro lists declared by target plugins, for macro lookup and autocomplete
  and for warning about macros a bound app doesn't support.
- MCP and CLI access to actions.
- A "fix this" button on a finding that runs a field action.
- A guided interview for a new card, modeled on
  [The Card Architect](https://character-tavern.com/character/stranger/character_card_creator):
  the model asks about the character, offers three concepts, then builds the
  card. An action is one request and reply, so this needs a multi-turn run.

## Tests

- Go: `action` scope enforcement drops out-of-scope keys and records
  warnings; `library.call` refuses without the declaration, without Library
  access, and for write tools; a run with an unknown action or field is a
  `400`; a cancelled run leaves no result. All against the fake plugin and a
  real PostgreSQL.
- Web: mentions resolve picked, typed, unknown, and ambiguous names; field
  locks follow scope; Review applies only taken fields; Refine builds its
  instruction from the ticked items; the new-card draft survives a reload and
  is cleared by the first Save.
- Plugin: prompt rendering per action, JSON parsing with `<think>` blocks and
  partial output, house style and additions in the prompt, tool calls answered
  through a fake host.
