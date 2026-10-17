# Plugin status words and disabling instances design

Date: 2026-10-17. Ticket: INN-68. Follows [plugin singletons](2026-10-16-plugin-singletons-design.md).

## Purpose

The Plugins page still reads inconsistently:

- A singleton says Ready, but an instance row says Working.
- A multi-instance plugin with one instance gets a header and a nested row, while a singleton gets one line.
- folder's automatic instance has no folder and still says Ready.
- There's no way to turn an instance off without deleting it.

This spec gives every plugin and instance one set of status words, shows any plugin with zero or one instance as a single line, marks an instance that lacks required config as needing setup, and adds Disable.

## Decisions

| Topic | Decision |
|---|---|
| Status words | Ready, Needs setup, Broken, Disabled, for both plugins and instances. Working goes. |
| Who computes status | The core, per instance and per plugin. The page shows what the core reports. |
| Missing config | An instance whose config lacks a top-level key the plugin's config schema lists in `required` needs setup. One rule for every plugin; no plugin is named. |
| Disable | Per instance. A disabled instance keeps its settings but nothing uses it. |
| Delete | Stays per instance, labelled **Delete instance**. It removes the instance's settings and secrets; the plugin's database stays, because all of a plugin's instances share it. |
| Last login | The core refuses to disable the last usable login provider, so the owner can't lock themself out. |
| Out of scope | Disabling a whole plugin, and deleting a plugin's database without uninstalling it. |

## Status

`InstanceInfo` gains `status`, computed when the instance is read:

| `status` | When, in this order |
|---|---|
| `disabled` | The owner disabled it |
| `broken` | Its plugin failed to start |
| `needs_setup` | Its config lacks a key the config schema lists in `required` |
| `ready` | Otherwise |

`plugins.Status` changes to summarize instances instead of the manifest's instance rule:

| Plugin `status` | When, in this order |
|---|---|
| `ready` | A theme |
| `needs_setup` | No instances |
| `broken` | Any instance is broken |
| `needs_setup` | Any instance needs setup |
| `disabled` | Every instance is disabled |
| `ready` | Otherwise |

The `instances` value goes; whether a plugin allows many stays in `multiple_instances`.

## Disable

- A migration adds `disabled boolean NOT NULL DEFAULT false` to `plugin_instance`.
- `PATCH /instances/{id}` takes `disabled`; `InstanceEdit` gains `Disabled *bool`. The MCP `update_instance` tool passes it through.
- Every lookup that chooses an instance to use skips a disabled one, as it skips a broken one: `Registry.Instance` and `InstanceWith` return a new sentinel `plugins.ErrDisabled`, and `CodecInstance`, `SitesInstances`, `InstancesOffering`, `settingsInstance`, the targets' followers, and `loginProviders` leave disabled instances out. `InstancesWith` lists every instance with the capability; each caller filters with `InstanceInfo.Usable()`. The plan lists each call site found by grep.
- A job on a disabled instance fails with `ErrDisabled`; the API returns 409 for it, as for `ErrBroken`.
- Disabling a login instance ends its sessions, as changing its owner does.
- Disabling the last instance that `loginProviders` would list returns 409 with `plugins.ErrLastLogin`.
- A disabled instance still counts for the singleton rule: the owner can't add a second, and `EnsureSingletons` doesn't recreate it.

## The Plugins page

- **Zero or one instance:** one line, for singletons and multi-instance plugins alike: name, version, description, status, then **Settings** when it has an instance, **Set up** when a singleton has none, and **Add instance** when the plugin allows many. The ⋯ menu holds, as they apply: Disable or Enable, Delete instance, Roll back, Uninstall.
- **Two or more instances:** the header line with **Add instance** and the plugin's ⋯ menu (Roll back, Uninstall), then one row per instance with its status and a ⋯ menu (Disable or Enable, Delete instance).
- Status uses the four words above. Broken shows its error. Disabled is muted.
- Disable needs no confirmation; it's undone by Enable. Delete instance keeps its confirmation.

## Plugins repo

folder's config schema adds `required = ["folder"]`, and its version is bumped in `plugin.toml`, `index.toml`, and its `main.go` version constant. The core then reports the existing empty folder instance as needing setup, and never creates an empty one again.

## Testing

Go, against a real PostgreSQL:

- `TestInstanceStatus`: one case per `status`, including a missing required key.
- `TestPluginStatus`: updated for the new table.
- `TestDisabledInstanceIsSkipped`: each lookup listed above leaves out or refuses a disabled instance; Enable restores it.
- `TestDisableRefusesLastLogin`: 409 for the last usable login provider; allowed when another exists.
- `TestDisabledSingletonBlocksSecondInstance`.

Web, in `Plugins.test.tsx`:

- A multi-instance plugin with one instance renders as one line with Settings, Add instance, and its menu.
- A plugin with two instances lists both rows.
- Status words come from `status`, with no Working anywhere.
- Disable sends `PATCH {disabled: true}`; Enable sends `false`; Delete instance asks first.
