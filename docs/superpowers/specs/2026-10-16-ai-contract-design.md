# Service contract check design

Date: 2026-10-16. Ticket: INN-62.

## Purpose

forge-ai is tested against a fake `ai.chat`, and the `ai` plugin against a
fake provider. Nothing runs a real consumer and a real provider through
`services.Broker`. This spec adds a contract check that does: for any plugin
whose manifest declares `uses`, the kit starts a provider plugin behind the
real broker and checks the calls between them.

## Decisions

| Topic | Decision |
|---|---|
| Broker's registry | `services` defines a `registry` interface of the four methods the Broker calls: `Instance`, `InstanceWith`, `Services`, `Call`. Production passes `*plugins.Registry`; the kit passes an in-memory registry of two instances. The Broker's own code runs unchanged. |
| Provider | `INNKEEPER_PROVIDER_PLUGIN` names the provider's plugin folder. `INNKEEPER_PROVIDER_CONFIG` is its instance config as JSON. |
| Fake model server | The kit runs an `httptest` server that speaks the OpenAI chat API. It replaces `${INNKEEPER_FAKE_AI_URL}` in the provider config with the server's URL, so core never learns a provider's config keys. |
| Missing provider | A plugin with `uses` and no `INNKEEPER_PROVIDER_PLUGIN` fails the check, naming the variable. |
| Tool call `Extra` | Not checked end to end. Only any-llm-go's Gemini provider sets it, and the OpenAI wire format has no field for it. The broker forwards params as raw JSON; the `ai` and forge-ai unit tests cover the code that maps `Extra`. |

## Setup

1. Start the fake model server.
2. Load the provider's manifest and build its instance from the config.
3. Build the consumer's instance from `INNKEEPER_PLUGIN_CONFIG`. Every
   property with `format = "instance"` whose `x-offers` is in `uses` is set
   to the provider's instance id.
4. Start a `pluginhost.Manager` whose core handler is the real Broker over
   the in-memory registry. The consumer is instance 1 and the provider is
   instance 2. The consumer must run under a Manager, because the Broker
   identifies the caller from the Manager's call context.
5. Wrap the Broker's handler to record each service call's outcome, and
   wrap `Runs.Handle` to count the `ai.delta` notifications relayed to the
   consumer.

The kit drives the consumer through its own capabilities: `action.run` for
each action, then `enrich.run` if declared, stopping once every check has
what it needs.

## Checks

| Check | Passes when |
|---|---|
| A service call reaches the provider and its reply comes back | The fake server got a request, and the Broker returned a result, not an error, to the consumer. |
| Deltas stream through to the consumer | The fake server streams the reply in two chunks, and at least two `ai.delta` reach the consumer. |
| Tool calls round-trip | The first request that offers tools gets a call to its first tool, id `contract-call`. A later request carries that call with the same id, name, and arguments, and a `tool` message answering it. Fails if no request offered tools. |
| Cancelling the consumer cancels the provider call | The fake server sends one chunk and holds the stream open. The kit cancels the consumer's call, and the server sees its request end within the call timeout. |

The tool-call check is strict because forge-ai, the only consumer, offers
tools. Make it conditional when a consumer without tools arrives.

## Plugins

- `ai/check-provider.json`: `openai-compatible`, `base_url` set to
  `${INNKEEPER_FAKE_AI_URL}`, model `contract`.
- `make check` exports `INNKEEPER_PROVIDER_PLUGIN=ai` and that config.
- `go.mod` pins the core version that has the check.

## Tests

- In core, the fakeplugin plays both sides. As a provider with a
  `base_url` in its config, its `ai.chat` streams from that URL over the
  OpenAI chat API. As a consumer, an action calls `ai.chat` with one tool
  and answers the tool call.
- A consumer that drops the tool call from its next request fails the
  tool-call check.
- A plugin with `uses` and no provider fails with a message naming
  `INNKEEPER_PROVIDER_PLUGIN`.
