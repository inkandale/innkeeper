# Innkeeper

Innkeeper keeps a library of character cards and lorebooks and syncs them with
the apps that use them. Plugins add the apps, sites, and tools it works with.

## Plugins

**Plugin**:
Installed code that offers one or more capabilities, described by its manifest.
_Avoid_: extension, integration, app (an app is what a target plugin talks to)

**Capability**:
One kind of work a plugin can do, such as import, push, or ai, defined by the
protocol.

**Instance**:
One configured use of a plugin: its config, secrets, and status. Everything
that runs a plugin, such as a job, binding, or enrichment, runs it through an
instance and refers to it by ID.
_Avoid_: connection, account, plugin (for a configured one)

**Instance name**:
An optional label that tells apart instances of the same plugin. An instance
without one is shown by its plugin's name. A plugin has at most one unnamed
instance, and names are unique within a plugin.
_Avoid_: using the name to identify an instance

**Singleton**:
A plugin that allows at most one instance. A disabled instance still counts
toward the limit.

**Built-in plugin**:
A plugin that ships inside Innkeeper's image. Innkeeper creates its instance
the first time it starts.
