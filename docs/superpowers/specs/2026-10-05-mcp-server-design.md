# MCP server and access tiers design

Date: 2026-10-05

Kaneo: INN-7, MCP half. The web UI half gets its own design.

## Purpose

Let Claude, in Claude Code, claude.ai, or the Claude apps, work with the
library: search and edit cards and lorebooks, sync them to apps, run imports,
and set up plugins. Every grant of access has a tier, read, write, or
destructive, and the whole API enforces it.

## Decisions

| Topic | Decision |
|---|---|
| Transport | Streamable HTTP at `/mcp` on `innkeeper-api`. No stdio server. |
| Library | The official Go MCP SDK, `github.com/modelcontextprotocol/go-sdk` v1.8.0. |
| How tools work | Each tool sends a request through the API's own handler, in process, with the caller's credentials. Tools get the API's validation, errors, and tier checks. |
| Sign-in | OAuth 2.1 with dynamic client registration and PKCE, so any claude.ai account can connect. API tokens also work. |
| Tiers | Read, write, destructive. Every route, tool, token, and OAuth grant has one. |
| Secrets | MCP tools can set plugin secrets. Nothing returns their values. |
| Plugin tools | Plugins can add MCP tools through a new `mcp` capability. |
| Left out | `run_action` (no action plugins yet), tags, collections, backups, file uploads, deleting instances, binding items already in an app (INN-15). |

## Tiers

| Tier | Means | Examples |
|---|---|---|
| `read` | Changes nothing | Search, get, history, push previews, drift checks, job and plugin lists |
| `write` | Changes the library, reversibly through revision history | Create, update, link, adopt, restore, import, cancel a job |
| `destructive` | Can't be undone from Innkeeper, or grants more access | Push (overwrites the app's copy), bulk push, unbind, delete a collection, create, change, or delete a plugin instance, manage tokens |

Tiers are ordered: a `write` grant also allows `read`, and `destructive`
allows all three.

- **Routes.** Each API operation declares its tier in its Huma metadata. The
  default follows the method: `GET` and `HEAD` are `read`, anything else is
  `write`. Routes override it where the method misleads, such as
  `POST /search` (`read`) or `POST /cards/{id}/push` (`destructive`).
  Creating or changing a plugin instance is `destructive`: its config
  decides where the plugin sends its secrets, which folders it imports, and
  who a login plugin lets in. The authentication middleware refuses a request above
  the caller's tier with `403`.
- **Callers.** A browser session is `destructive`: it is the owner, logged
  in. An API token has a tier. Tokens that exist when this ships become
  `destructive`, so the CLI keeps working. New tokens are `read` unless the
  request asks for more. A caller can't create a token above its own tier.
- **Tools.** Each MCP tool has the tier of the route it calls. `tools/list`
  shows only the tools the caller's tier allows, and a call above it gets the
  route's `403`. Tools carry MCP annotations from their tier: `read` sets
  `readOnlyHint`, `destructive` sets `destructiveHint`, so clients ask before
  running destructive tools.

## Sign-in

Innkeeper is both the OAuth authorization server and the protected resource.

- `GET /.well-known/oauth-protected-resource` (RFC 9728) names `/mcp` as the
  resource and Innkeeper as its authorization server.
  `GET /.well-known/oauth-authorization-server` (RFC 8414) lists the
  endpoints, `S256` as the only PKCE method, and the scopes `read`, `write`,
  `destructive`.
- `POST /oauth/register` (RFC 7591) registers a client with its name and
  redirect URIs. Registration is open and grants nothing. Redirect URIs must
  be `https`, or `http` on a loopback address.
- `GET /oauth/authorize` takes the authorization code flow with PKCE `S256`
  only, and an exact registered `redirect_uri`. If the browser has no
  session, it shows a login page for the owner's login plugins first. Then a
  consent page names the client and its redirect host, and asks which tier
  to grant, with `read` selected. The tier is the token's scope.
- `POST /oauth/token` exchanges a code (single use, 60 seconds) for an access
  token (1 hour) and a refresh token (30 days, replaced at each use, and a
  reused one revokes the grant). It also takes `refresh_token` grants.
- Access tokens are valid only on `/mcp`, the resource they were issued for.
  API tokens work on `/mcp` and the rest of the API.
- Grants show in the token list, by client name and tier, and revoking one
  ends its tokens.
- A request to `/mcp` without valid credentials gets `401` with
  `WWW-Authenticate: Bearer resource_metadata="…"`.
- Browser cookies don't work on `/mcp`.
- OAuth needs `INNKEEPER_PUBLIC_URL`, the address clients reach Innkeeper
  at. Without it, `/mcp` takes only API tokens.

The login and consent pages are small server-rendered HTML pages. The login
page renders the form a login plugin's `auth.begin` returns, or follows its
redirect.

## Tools

Each tool takes and returns JSON. Errors come back as tool errors carrying
the API's status and message, such as `409` for a stale base revision.

| Tool | Tier | Calls |
|---|---|---|
| `search_cards` | read | `POST /search` |
| `get_card`, `get_lorebook` | read | `GET /cards/{id}` or `/lorebooks/{id}`, plus bindings |
| `get_history` | read | a card's or lorebook's revisions |
| `create_card`, `create_lorebook` | write | `POST /cards`, `POST /lorebooks` |
| `update_card`, `update_lorebook` | write | update with `base_revision_id` |
| `link_lorebook`, `unlink_lorebook` | write | card-lorebook links |
| `restore_revision` | write | restore an old revision as a new one |
| `preview_push` | read | push preview, card or lorebook |
| `push` | destructive | push, with a `confirm` token from the preview |
| `check_drift` | read | `POST /bindings/{id}/check` |
| `adopt` | write | `POST /bindings/{id}/adopt` |
| `list_bindings` | read | a card's or lorebook's bindings |
| `list_jobs`, `get_job` | read | jobs |
| `cancel_job` | write | `POST /jobs/{id}/cancel` |
| `start_import` | write | `POST /imports`, folder imports only |
| `list_plugins`, `list_instances` | read | plugins with config schemas; instances with secret names only |
| `create_instance` | destructive | `POST /instances` |
| `update_instance` | destructive | `PATCH /instances/{id}` |

**Login providers.** No MCP request may create or change an instance of a
login plugin, or set any instance's `owner_subject`, at any tier. A login
provider's settings decide who logs in with every tier, so an MCP client
that could change them could make itself the owner and outlive its grant.
The CLI, the API with a token, and the browser still can.

**Push confirmation.** `preview_push` returns `confirm`, a hash of the
revision it rendered, the app copy it compared against, and its changes.
`push` requires it, and recomputes the preview first. If anything differs,
`push` refuses and returns the new preview, so nothing in an app is replaced
without the diff having been shown. The confirm check lives in the API's push
routes as an optional `confirm` field, so the API offers the same safeguard.

## Plugin tools

A plugin that declares the `mcp` capability answers two methods:

- `mcp.tools` returns `tools`: each with `name`, `description`,
  `input_schema` (JSON Schema), and `tier`. A tool without a valid tier is
  `destructive`.
- `mcp.call` takes `name` and `arguments`, and returns `content`, a list of
  MCP content items (`text`, or `json`, sent as text). A plugin error becomes
  a tool error.

`/mcp` lists each working instance's tools as `<instance>_<tool>`, where
`<instance>` is the instance name in lowercase with runs of other characters
as `_`. Names that collide get the instance ID appended. When instances
change, the server sends `notifications/tools/list_changed`. A call runs
with the instance's config and secrets, with a 2-minute timeout. A plugin
that crashes or times out is a tool error saying it is unavailable.

The protocol reference gains the `mcp` capability, and the contract kit
checks that `mcp.tools` returns valid tools within 5 seconds.

## Architecture

- `internal/tier`: the three tiers, parsing, ordering.
- `internal/api`: route tiers in Huma metadata, tier checks in the
  authentication middleware, token tiers, and the `confirm` field on pushes.
- `internal/auth`: token tiers in the database, OAuth clients, codes, grants,
  access and refresh tokens.
- `internal/oauth`: the OAuth endpoints and the login and consent pages.
- `internal/mcp`: the MCP server, the tool table, dispatch through the API
  handler, plugin tools.
- `protocol`: `mcp.tools` and `mcp.call`.

A tool is a row in a table: name, description, input schema, and a function
that turns arguments into an API request and the response into a result.
Dispatch builds an `http.Request` with the caller's `Authorization` header,
serves it with the API handler into a recorder, and maps the status and
body.

## Error handling

- A tool above the caller's tier: `403` from the route, as a tool error.
- Stale base revision: `409`, so the model reads again and retries.
- Push confirmation mismatch: a tool error with the new preview.
- OAuth errors follow RFC 6749: `invalid_request`, `invalid_grant`,
  `invalid_client`, `unsupported_grant_type`, with redirects back to the
  client where the spec allows and an error page where it doesn't.
- Failed logins on the login page wait a second and look alike, as the API's
  logins do.

## Testing

- Integration tests against real PostgreSQL, as the rest of the core.
- Tiers: each tier's token against a read, a write, and a destructive route;
  a token can't create a higher one; existing tokens migrate to
  `destructive`; a route table test that every operation has a tier.
- OAuth: discovery documents; registration; authorize with PKCE through
  login and consent; code exchange; refresh with rotation; reuse of a
  refresh token revokes the grant; wrong verifier, unregistered redirect,
  expired code, and reused code are refused; an access token is refused off
  `/mcp`.
- MCP: the Go SDK's client lists tools by tier and calls them through
  `/mcp`; push without, with a stale, and with a valid `confirm`.
- Plugin tools: the fake plugin gains `mcp` tools of each tier; listing,
  naming, calls, a crash.
- By hand: Claude Code with an API token, and claude.ai on a phone through
  OAuth.

## Build order

1. Tiers across the API and tokens, and the push `confirm` field.
2. The MCP server and tools, with API tokens.
3. Plugin tools.
4. OAuth.

Each step ships on its own: after step 2, Claude Code can use Innkeeper.
