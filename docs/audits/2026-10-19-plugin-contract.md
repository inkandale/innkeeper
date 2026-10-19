# Plugin contract audit, 2026-10-19

Findings from a read-only audit of the plugin contract against
[AGENTS.md](../../AGENTS.md): the `protocol` package, the host side in
`internal/`, the web UI, and the plugins in `innkeeper-plugins`. File
references are to `develop` at the time of the audit. Items from the
[2026-10-13 audit](2026-10-13-solid-dry.md) are not repeated. Tick an item
when its fix merges.

A new plugin for an existing capability still needs no core change: no core
code names a plugin, and `SchemaForm` renders every config, records, and
action form. The gaps are in capabilities added since the last audit, which
were each wired their own way, and in plugin-side code that each plugin
copies.

## 1. Wrong today (INN-86)

- [ ] The plugins README "Publishing" says to bump `version` in
      `plugin.toml` and `index.toml`. Each plugin also has `const version`
      in `main.go`, and `make check` fails when it differs.
- [ ] `harness.drivers` (`contract/uses.go:190`) drives only `action.run`
      and `enrich.run`. A plugin that calls `ai.chat` from `mcp.call`,
      `records.*`, `import.run`, or `settings.run` fails `plugin check`.
      Give each `suites` entry (`contract.go:88`) an optional drive func and
      have `checkUses` iterate the table.
- [ ] No contract suite for `settings` or `import`. Unchecked methods:
      `mcp.call`, `records.import`, `target.put`, `target.put_lorebook`.
      `checkServices` (`contract/ai.go:18`) accepts any reply, and its
      sample params duplicate `protocol.Services`.
- [ ] The `search` capability (`manifest.go:17`) has no methods and no
      consumer. Delete it from `Capabilities` and `docs/protocol.md`,
      including the lumiverse example at line 1150; accept and ignore it in
      manifests for a release.
- [ ] `plugins.ErrIncapable` maps to 400 in `api/jobs.go:132` and
      `plugin_settings.go:119`, and to 404 in `catalogs.go:76` and
      `auth.go:133`. `failAs` (`server.go:297`) has no case, so a path
      without a local mapping returns 500. Add it to `failAs` as 400; keep
      the 404 only in auth, where it hides that the instance exists.

## 2. Plugin SDK and author guide (INN-87)

- [ ] Eight plugins copy the initialize-and-dispatch loop: `ai`,
      `card-codec`, `card-import`, `lumiverse`, `password-login`,
      `sillytavern`, `url-import`, `forge-ai`. `catalogkit.Serve` is a
      generic ninth. Move it into core as `protocol.Serve`, reading name,
      version, and capabilities from `./plugin.toml`, so a version lives in
      `plugin.toml` and `index.toml` only.
- [ ] `-32000` is a literal 38 times across the plugins, and 10 files define
      their own `invalid()`. Add `protocol.CodeFailed`,
      `protocol.Fail(msg, retryable)`, and `protocol.InvalidParams(err)`.
- [ ] Error codes live in three files: `CodeForbidden` in `action.go:23`,
      `CodeRemoteNotFound` in `target.go:31`, `CodeAIRejected` in `ai.go`.
      Move them to `jsonrpc.go`. The protocol.md Errors section lists only
      -32601; give it a table of every code.
- [ ] `lumiverse/client.go` and `sillytavern/client.go` copy one JSON client
      and have drifted: sillytavern caps reply size and refuses redirects,
      lumiverse does neither. Extract one client package (reply cap,
      redirect refusal, status-to-error mapping with retryable 5xx, session
      retry) before a third app target copies either.
- [ ] No "write a plugin" guide. Add a section to the plugins README: which
      plugin to copy for each capability, the shared kits (`catalogkit`,
      `pluginio`, `pgtest`, `card`), the files to touch (folder,
      `plugin.toml`, `index.toml`, `.gitignore`, `check-config.json`), and
      the `fake_test.go` / `live_test.go` pattern. Write it against
      `protocol.Serve`.

## 3. Core plumbing (INN-88)

- [ ] The importer, actions, and enrich runners (`importer.go:129`,
      `actions/run.go:27`, `enrich/enrich.go:176`) repeat one run session:
      the `Runner` fields, the `jobs.ErrFinal` wrapper, the prelude
      (attempts, instance ID, payload, `InstanceWith`), and `Runs.Add` with
      a progress handler. Actions throttles progress saves and enrich saves
      every batch. Extract one helper that returns the instance, the token,
      and a throttled progress publisher.
- [ ] Capability names are bare strings in about 70 places; a typo compiles
      and `InstancesWith` returns nothing. Add typed constants in
      `protocol` and build `Capabilities` from them. Add
      `Manifest.PullOnly()` (written four times, including
      `web/src/api/client.ts:121`) and `Manifest.IsTarget()` (three times).
- [ ] `SitesInstances` (`plugins/sites.go:30`), `InstancesOffering`
      (`plugins.go:805`), and `CodecInstance` (`plugins.go:694`, its own
      SQL) each filter usable, unbroken, enabled instances by hand. Add
      `Registry.WorkingInstancesWith(ctx, cap)`.
- [ ] `catalogs()` (`api/catalogs.go:93`) calls `catalog.describe` one
      instance at a time with no cache. Use `listMethod` and `collect`, as
      `action.list` and `mcp.tools` do. `catalogCall` looks the instance up
      twice.
- [ ] `contract` takes `DatabaseURL`, `Config`, `Provider`, and
      `ProviderConfig` as package variables set from `cmd/innkeeper`. Pass a
      `contract.Options` to `Check`.
- [ ] `cmd/innkeeper/worker.go:101` builds the `ai.decide` call inline and
      skips the check that the instance offers the service. Move it to
      `Registry.Decide`.
- [ ] Queueing sites detection when an instance starts is written twice
      (`api/instances.go:255`, `cmd/innkeeper/services.go:88`).
- [ ] `web/src/login/fields.ts` renders auth forms as text or password
      inputs only. Use `SchemaForm`.
- [ ] `testutil.FakePluginWith` cannot declare `Settings.Actions`, which
      the settings suite needs. The fake plugin has no `ai.embed`.

## 4. Protocol conventions (INN-89)

Wire renames break plugins. Document the convention now; rename only when a
capability is next touched, accepting the old name for a release.

- [ ] protocol.md has no rule for declaring in the manifest versus answering
      over RPC. `settings.actions` is a static manifest list while
      `action.list` returns the same kind of list over RPC, and `pull` is
      declared in both. Rule: RPC only when the answer depends on config.
- [ ] Manifest validation (`manifest.go:247-319`) is a hand-written pair of
      checks per key. Make it a table keyed by capability, with one
      unique-and-pattern helper; `uniqueKeys` (`catalog.go:98`) already
      does half of it.
- [ ] One `Option` type: `CatalogOption` requires a label,
      `SettingsOption` doesn't.
- [ ] The card-or-lorebook field is `kind` in catalog and actions, `entity`
      in sites, and `applies` in action declarations. Point
      `ApplyCard`/`ApplyLorebook` at `KindCard`/`KindLorebook`.
- [ ] JSON Schema fields go by six names (`config`, `schema`, `inputs`,
      `input_schema`, `form`, `parameters`). `objectSchema` skips manifest
      `config` and auth `form`.
- [ ] Document what the renames can't fix yet: the instance ID is a string
      in `initialize` and an int64 in service calls; `data` is a whole CCv3
      card in `catalog.get` and the `data` object elsewhere; a `file` input
      sends `blob` to import and `path` to records; method names mix
      `put_lorebook` and `setup.complete` styles; identifier patterns
      differ by kind of ID.
- [ ] Pull-only plugins stub `target.list` because the core calls it. Skip
      the list calls for them.

## Not doing

- Per-capability call timeouts: three lines each, and each value is a
  deliberate choice.
- Pagination for `target.list` and `records.list`: nothing needs it yet. If
  a list does, reuse `CatalogPage`.
- The 9 `p.Config["x"].(string)` reads in plugins.
