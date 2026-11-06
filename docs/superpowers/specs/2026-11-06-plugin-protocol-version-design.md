# Plugin protocol version

Date: 2026-11-06

## Purpose

A plugin update can need protocol features that the running core doesn't
have yet. forge-ai 0.14.0 needs the `panel` grant, `[ui] title`, and
`cards.edit`, while production still runs an older core. Today core offers
the update anyway: "check for updates" picks the newest version, and the
install fails only after the download, when the manifest doesn't validate.

Production runs develop builds, versioned `develop-<sha>`, and plugins use
protocol features before a release is cut. So a plugin can't name a minimum
core version. Instead the protocol gets its own `major.minor` version,
separate from the product version, as the Docker Engine API does.

## Decisions

| Topic | Decision |
|---|---|
| Version | The protocol is `1.N`. The major is today's `protocol.Version = 1`. A new `protocol.Minor` starts at 1. |
| Minor 1 | Covers what develop has that the last production core lacks: the `panel` grant, `[ui] title`, `cards.edit`, the draft `snapshot`, and the `context` notice. |
| Bumping | Each addition to the protocol bumps `Minor` and adds a row to the version table in `docs/protocol.md`. A change that breaks existing plugins would need a new major; this design doesn't add one. |
| Field | Manifest and index entry gain `protocol_version = "1.N"`. Leaving it out means `1.0`. `protocol = 1` stays, so the old core keeps reading the index. |
| Direction | Only a plugin needing a newer core is checked. A core update that breaks installed plugins is out of scope. |

`protocol_version` is a new key, not a string in `protocol`: the old core
decodes `protocol` as an integer, so a string there would stop it from
loading the whole index.

## Core

### Manifest and index

- `protocol.Manifest` and `plugins.Entry` gain `ProtocolVersion string`
  (`toml:"protocol_version"`).
- One function in `protocol` parses it. It returns the major and minor, or
  an error for anything that isn't `<int>.<int>`. An empty string is `1.0`.
- Manifest validation refuses a `protocol_version` that:
  - doesn't parse;
  - has a major other than `protocol`;
  - has a minor above `protocol.Minor`. The message names both versions:
    "needs protocol 1.2; this Innkeeper speaks 1.1".

### Install and update

- `install` checks the index entry's `protocol_version` before downloading,
  next to the existing `protocol` check, using the same message. It also
  checks that the tarball's manifest has the same `protocol_version` as the
  entry, as it does for `name`, `version`, and `protocol`.
- `catalog` in `internal/api/pluginstore.go` makes the newest version this
  core can run the entry it offers. Versions it can't run stay in `Versions`.
- `catalogPluginOut` gains `newer`: the newest version this core can't run
  and the protocol it needs, if it is newer than the offered one.
- The Plugins page shows "{version} needs a newer Innkeeper" under a plugin
  with `newer`, without an install or update button for it.

### Installed plugins

`LoadInstalled` logs and skips a plugin whose manifest doesn't validate. A
core rolled back below an installed plugin's minor would hide that plugin.
Instead it keeps the plugin out of the running set and lists it on the
Plugins page as needing a newer Innkeeper. Other validation errors are
skipped as before.

### Docs

`docs/protocol.md`:

- states the version as `1.1`;
- adds a table of minors and what each added;
- documents `protocol_version` in the manifest and index sections.

The Conventions section and `AGENTS.md` say to bump `Minor` with every
protocol addition.

## Plugins repo

- `indexcheck` fails when:
  - an entry's `protocol_version` differs from its plugin's manifest;
  - an entry needs a minor above the `protocol.Minor` of the core version
    the repo's `go.mod` pins.
- Each plugin's author sets `protocol_version` by hand to the newest feature
  it uses. Plugins that use nothing from minor 1 leave it out.
- forge-ai 0.14.1 declares `protocol_version = "1.1"` and ships the
  Editing-line fix that needs the `context` notice.

## Rollout

1. Core with this change ships to develop, with `Minor = 1`.
2. The plugins repo pins that core, and forge-ai 0.14.1 is published.
3. Production restarts onto the new core. From then on, it only offers
   plugin versions it can run.

The production core from before this change ignores `protocol_version` and
still offers forge-ai 0.14.x. Don't update forge-ai on production until it
restarts onto the new core.

## Testing

- Core, `protocol`:
  - parsing `protocol_version`, including empty, malformed, and wrong major;
  - manifest validation refuses a minor above `Minor`.
- Core, `internal/plugins`:
  - install refuses an entry with too high a minor before any download;
  - install refuses a tarball whose `protocol_version` differs from its
    entry.
- Core, API: `catalog` offers the newest runnable version and reports
  `newer`. A plugin with only runnable versions has no `newer`.
- Core, installed plugins: a plugin needing a higher minor isn't run and
  shows on the Plugins page.
- Web: the Plugins page shows the "needs a newer Innkeeper" line and no
  button for it.
- Plugins repo: `indexcheck` catches a mismatched entry and one above the
  pinned minor.

## Out of scope

- Checking that a core update keeps installed plugins working.
- Inferring a plugin's minor from the features its manifest uses.
- A host version range such as `innkeeper = ">=1.1.0"`, which needs
  versioned develop builds.
