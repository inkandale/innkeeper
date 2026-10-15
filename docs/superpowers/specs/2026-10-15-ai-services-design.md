# AI services design

Date: 2026-10-15

## Purpose

The [Forge](2026-10-14-forge-design.md) put the AI connection inside
forge-ai, so no other plugin can use it, and its settings page is a list of
blank "additions" fields that hide the prompts they add to. This spec:

- adds an `ai` capability, so one plugin owns AI connections and other
  plugins call it through core;
- gives the instance settings page tabs, defaults with reset, a model list,
  and a Test button, all generic;
- splits the connection out of forge-ai into a new `ai` plugin;
- cuts forge-ai's twelve actions to ten distinct ones, and replaces the
  additions with a Prompts tab that shows each action's current
  instructions.

## Decisions

| Topic | Decision |
|---|---|
| Who owns AI | A new `ai` plugin. forge-ai becomes a consumer of it. |
| Plugin ids | forge-ai keeps its id, so its skills, enrichments, and instance need no migration. The connection plugin is new, with id `ai`. |
| Connection | One `ai` instance is one connection. A consumer picks an instance. |
| Connection kinds | The owner ticks chat, embed, and image. Test proves each ticked kind works. No auto-detection: OpenAI-compatible model lists don't say what a model is for. |
| Image | In the schema, refused until a provider supports it. |
| Prompt overrides | Replace only an action's instructions. The frame (JSON contract, card data, partials) stays fixed. |
| Whole-card edits | Plan, then sections: one call writes a note per field, then the field action runs on each field with its note. |
| Build order | Core `ai` capability, core settings form, `ai` plugin, forge-ai. One plan per part. |

## 1. Core: the `ai` capability

### Provider

A plugin declares `capabilities = ["ai"]`. Its `initialize` result lists the
services the instance offers, such as `["chat", "embed"]`. Core keeps that
list with the running instance.

| Method | Does |
|---|---|
| `ai.chat` | Messages and optional tools in, a reply out. Streams tokens as notifications, as action runs do. |
| `ai.embed` | Texts in, one vector per text out. |
| `ai.image` | Reserved; no provider offers it yet. |

The request and response types live in `protocol/`, close to the OpenAI chat
shape every provider forge-ai uses already speaks. These are the only AI
shapes in core.

### Consumer

A plugin declares the services it calls, such as `uses = ["ai.chat"]`. Its
config schema names the provider instance with a picker field (section 2).
While it works, the consumer sends `ai.chat` to core with that instance id,
over the reverse channel `library.call` uses.

Core refuses with `CodeForbidden` when the consumer didn't declare the
service in `uses`, or when the instance doesn't offer it. Otherwise core
starts the provider if needed, forwards the call, and relays its stream.
When the consumer's call is cancelled, core cancels the provider call.

Core never sees prompts, models, or providers: only service names, which
instance offers which, and who may call what.

### Tests

Through `internal/fakeplugin`, a consumer calling a provider through core:

- a consumer without `uses` is refused;
- an instance that doesn't offer the service is refused;
- the stream reaches the consumer;
- cancelling the consumer's call cancels the provider's.

## 2. Core: the settings form

All generic, driven by the schema and an optional `settings` capability.

### Layout

- **Tabs.** Each top-level property of type `object` is a tab, titled with
  its `title`. Top-level fields that aren't objects, and the secrets, go on
  a first tab named **General**. Deeper objects stay fieldsets.
- **Checkbox group.** An array of string enum renders as checkboxes.
- **Instance picker.** A string with `format = "instance"` and
  `x-offers = "<service>"`, such as `ai.chat`, is a dropdown of instances
  that offer that service.

Long text already renders as a textarea through `contentMediaType`.

### Defaults and reset

- A field's default comes from the schema's `default` or from
  `settings.defaults`.
- A field with no override shows its default as its value.
- Saving stores only values that differ from their default. Each field with
  an override has a reset button that clears it.
- With each override, core stores a hash of the default it replaced. When
  the plugin's default later changes, the field is marked "default updated".

### Settings calls

The `settings` capability is stateless. Core starts the plugin and sends a
`settings.*` call in place of `initialize`, passing the unsaved draft config
and the secrets: the saved ones merged with any typed but not yet saved. The
owner can list models and test a connection before saving. Secrets never go
back to the browser.

| Method | Does |
|---|---|
| `settings.defaults` | Defaults the manifest can't hold, such as prompt text kept in template files. Keyed by JSON pointer. |
| `settings.options` | Choices for one field, given its pointer. A field marked `x-options = true` is a dropdown that also accepts free text. |
| `settings.run` | Runs a button declared in the manifest, such as `[settings.actions] test = { label = "Test connection" }`. Returns pass or fail, a message, and one row per check. |

API: `POST /plugins/{id}/settings/defaults`, `/options`, and `/run`. Each
takes the draft config and an optional instance id, whose saved secrets are
merged in.

### Tests

- Go, against a real PostgreSQL: draft secrets merged with saved ones and
  never returned; override hashes stored and compared.
- Web: tabs, checkbox group, reset, the "default updated" mark, the options
  dropdown with free text, and the instance picker.

## 3. The `ai` plugin

New, in `innkeeper-plugins/ai`. One instance is one connection.

### Settings

All on the General tab:

| Field | |
|---|---|
| `provider` | The provider enum forge-ai has today. |
| `base_url` | Required for openai-compatible. |
| `api_key` | Secret. |
| `model` | `x-options`: the provider's model list for the draft settings. |
| `kinds` | Any of `chat`, `embed`, `image`. |

`settings.run` `test` makes one minimal call per ticked kind (a one-token
chat, one embedded word) and reports each with its latency.

### Services

`initialize` reports the ticked kinds. `ai.chat` runs on any-llm-go,
streams, and passes tools through, which forge-ai's skill tools and library
reads need. `ai.embed` runs on any-llm-go's embeddings where the provider has
them. Ticking `image` is refused.

The provider code moves here from forge-ai: `provider()`
(`forge-ai/settings.go`), the any-llm-go dependency, and the stream
conversion (`forge-ai/stream.go`).

### Tests

Settings parsing, the services list from kinds, and Test's per-kind report,
against a fake provider. The cross-plugin path is covered in section 1.

## 4. forge-ai

### Actions

| Group | Action | Scope | Instruction |
|---|---|---|---|
| Create | Forge | new card | required |
| Edit | Revise | field or whole card | required |
| Edit | Tighten | field or whole card | optional |
| Edit | Expand | field or whole card | optional |
| Edit | Convert to prose | field or whole card | optional |
| Edit | Change point of view | field or whole card | point of view input |
| Add | Write examples | `mes_example` | none |
| Add | Write alternate greetings | card | none |
| Add | Write lore entries | lorebook | required |
| Judge | Evaluate | card | none |

Revise keeps the id `revise`. The `rewrite` and `tighten-card` actions and
`tighten-card.tmpl` go; whole-card runs use the plan step below.

### Field or whole card

`protocol.Action` gains `WholeCard bool`. An action with `Fields` and
`WholeCard` shows in each listed field's menu and in the card menu. The run
names the field, or no field for the whole card. Any plugin can use this.

A whole-card Edit runs in three steps:

1. **Plan.** One call gets the owner's direction, the whole-card guidance
   prompt, and the card. It returns JSON with, per field, a note or `skip`.
2. **Sections.** For each field not skipped, in turn, the field prompt runs
   with its note and the rest of the card as context. Progress is reported
   per field.
3. **Result.** Only changed fields come back, so the review UI shows a diff
   per field to accept or reject.

### Prompts

Each template splits into an editable `[[define "<id>.instructions"]]` block
and the fixed frame that includes it. The frame renders the owner's override
when there is one, otherwise the default block. `settings.defaults` returns
each default block rendered from the embedded templates, so the templates
stay the one source of the defaults.

`additions` goes. An override replaces the instructions; it no longer
appends.

### Settings

| Tab | Fields |
|---|---|
| General | `connection` (`format = "instance"`, `x-offers = "ai.chat"`), `temperature`, `max_tokens` |
| Prompts | `house_style`, whole-card guidance, one instructions textarea per action, each with its default, reset, and "default updated" mark |

forge-ai stops calling providers. Every model call is `ai.chat` through
core; skill tools and `library.call` work on top of it.

### Tests

- An override replaces only the instructions block; the JSON contract
  survives.
- Against a fake `ai.chat`: skipped fields stay unchanged, each section
  sees its note, and the result holds only changed fields.

### Deploying

After forge-ai and `ai` ship, create an `ai` instance and pick it in the
forge-ai instance's General tab. Until then forge-ai's actions fail with a
missing-connection error. Old `additions` values are dropped; copy any you
want into the Prompts tab first.
