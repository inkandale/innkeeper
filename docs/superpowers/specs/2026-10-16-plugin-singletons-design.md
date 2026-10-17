# Plugin singletons and the Plugins page design

Date: 2026-10-16. Ticket: INN-64.

## Purpose

Every plugin works through an instance, but only built-in `sites` plugins get
one without setup. A plugin like card-codec or card-import shows as installed
and does nothing until the owner clicks **Add instance**, and nothing on the
page says so. Every plugin also offers **Add instance**, though most need
exactly one.

This spec makes one instance the default, creates it on install when the
plugin needs no setup, and rebuilds the Plugins settings page around each
plugin's status.

## Decisions

| Topic | Decision |
|---|---|
| Who decides | The manifest. `multiple_instances = true` allows more than one instance; without it, a plugin has at most one. It is a field beside `database`, not a capability, because each capability names protocol methods the plugin answers. |
| Second instance of a singleton | `Registry.CreateInstance` refuses it with `plugins.ErrSingleton`; the API returns 409. The API, the MCP `create_instance` tool, and the UI all go through `CreateInstance`. |
| Instance on install | A singleton with no required config and no secrets gets its instance at startup, for every loaded plugin, and after `POST /plugins/{name}/install`. Running at startup over every plugin gives existing servers the instances they lack. Themes get none. |
| Login plugins | Never created automatically. A login instance decides who can sign in, so the owner always sets one up deliberately. |
| Deleted instance | Stays deleted. The `builtin-instance:<name>` setting that `CreateBuiltinSites` already writes records each plugin the core created an instance for. The key keeps its name so existing servers keep that record. |
| Existing extra instances | A server with two or more instances of a plugin that is now a singleton keeps them. The page stops offering to add more. |
| Description and category | New manifest fields `description` and `category`. `index.toml` already has both, and the setup wizard groups by `category`; installed plugins now carry their own. The plugins repo's `indexcheck` requires each to match the manifest. |
| Status | `PluginOut.status` is computed by the core, so the UI shows it without recomputing. |
| Groups | The page groups plugins by `category`, as the setup wizard does. Core never learns plugin names. |
| Display names | Out of scope. The page shows the manifest `name`. |

## Manifest

Three new fields in `protocol.Manifest`, documented in the manifest table in
`docs/protocol.md`:

| Field | Type | Meaning |
|---|---|---|
| `multiple_instances` | bool, default false | The owner may create more than one instance. A theme can't set it |
| `description` | string, one line | What the plugin does, shown on the Plugins page |
| `category` | string | The group the Plugins page and the setup wizard show the plugin under, such as `Apps` |

In the plugins repo, these set `multiple_instances = true`:

| Plugin | Why many |
|---|---|
| sillytavern, lumiverse | One per app server |
| ai | One per model provider |
| folder | One per indexed folder |
| chub-archive | One per takeout dump |
| password-login | Setup creates a new login when the only one is broken or has no owner; a singleton would lock the owner out |

Every plugin gets the `description` and `category` its `index.toml` entry
already has, and each changed plugin's version is bumped in both files.

## Core

### Creating instances

`CreateInstance` takes a transaction-scoped advisory lock on the plugin's
name, counts its instances, and inserts, so two concurrent requests can't
both create a singleton's instance. The automatic creation below takes the
same lock.

`CreateBuiltinSites` becomes `EnsureSingletons`, which runs over every
loaded plugin rather than built-in `sites` plugins only. A plugin qualifies
when it is a singleton, is not a theme or a login plugin, and has no secrets
and no required config. For each one, `createBuiltinOnce` runs as now: one
transaction that writes the setting and inserts the instance unless the
plugin already has one. It returns the plugins it created instances for.
The startup call site keeps its place, and the install handler calls it
after an install succeeds. Either one queues a source detect when a created
plugin has `sites`, as now.

### Plugin status

`PluginOut` gains `multiple_instances`, `description`, and `status`:

| `status` | When |
|---|---|
| `broken` | Any of the plugin's instances is broken |
| `needs_setup` | A singleton with no instance |
| `ready` | A singleton with a working instance |
| `instances` | A plugin with `multiple_instances`; the UI lists its instances |

A theme's status is `ready`. `broken` wins over the others.

## The Plugins page

`web/src/settings/Plugins.tsx`, after **Check for updates**:

1. A summary line: how many plugins are installed, have updates, and need
   setup.
2. One card per plugin, under a heading for its `category`. A plugin with
   no category goes under Other. Groups sort alphabetically, with Other
   last. Today's categories are AI, Apps, Archives, Formats, Import, and
   Login.
3. Plugins whose instances outlive an uninstall go in a **Not installed**
   group.
4. **Available** plugins show as dashed cards with **Install**, after the
   owner checks for updates.
5. **Plugin sources** fold into a `<details>` at the bottom.

The capability badges go; the group says what a plugin does.

### Singleton card

One line: name, version (or "built in"), description, status, one primary
button, and a ⋯ menu.

| Status | Shown | Primary button |
|---|---|---|
| `ready` | Ready | Settings, if the plugin has config or secrets |
| `needs_setup` | Needs setup | Set up, which opens the new-instance form |
| `broken` | Broken, with the error | Settings, if the plugin has config or secrets |

**Update to *x*** shows beside it whenever an update is available.

Settings links to the instance page. For a singleton with more than one
instance left from before this change, the card lists them as a
multi-instance card does.

### Multi-instance card

The header has the name, version, description, **Add instance**, and a ⋯
menu. Each instance is a row under it: its name, linking to the instance
page; its status, Working or Broken with the error; and a ⋯ menu with
Delete. With no instances, the card says so.

### The ⋯ menu

It holds Roll back to *x*, Uninstall, and, for a plugin that isn't
installed, Remove data. Each keeps its current confirmation. The app has no
menu component, so this adds `Menu`, built on the native `popover`
attribute, used by plugin and instance rows.

## Testing

Go, against a real PostgreSQL:

- `TestCreateInstanceRejectsSecondSingleton`: returns `ErrSingleton`; a
  `multiple_instances` plugin accepts a second.
- `TestEnsureSingletons`: creates an instance for a singleton with no setup;
  skips one with a secret, one with required config, a login plugin, and a
  `multiple_instances` plugin; the api and the worker running it at once
  create one instance; a deleted instance stays deleted.
- `TestPluginStore` (API): installing a plugin creates its instance.
- `TestPluginStatus`: one case per `status` value, plus `broken` winning.
- `protocol` manifest tests for the three new fields.
- `indexcheck` in the plugins repo: a mismatched `description` or
  `category` fails.

Web, in `Plugins.test.tsx`:

- Plugins appear under their groups, in group order.
- A `needs_setup` singleton shows **Set up** and no **Add instance**.
- A multi-instance plugin shows **Add instance** and its instance rows.
- Roll back and Uninstall are in the ⋯ menu.

## Out of scope

- The instance settings page.
- The Jobs, Access, and Appearance tabs.
- Display names for plugins.
