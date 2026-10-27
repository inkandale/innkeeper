# Innkeeper

Innkeeper keeps one library of your roleplay character cards and lorebooks,
and keeps the apps you chat in up to date with it. Edit a card once, and
SillyTavern, Lumiverse, and any other app you connect get the same version.
Edits made inside an app come back to the library.

It's self-hosted: you run it on your own machine or server.

## What it does

- **Sync with your apps.** Push cards and lorebooks to SillyTavern and
  Lumiverse, and bring back edits made in the app. When both sides changed,
  you choose what to keep.
- **Bring in cards.** Upload card files, import a folder, or paste a link
  from chub.ai, RisuRealm, or Character Tavern. Innkeeper tells you when the
  author updates a card.
- **Keep every version.** Each edit is a revision you can go back to.
  Variants, such as a rewrite of a card, group under their original.
- **Tidy a big library.** Search, filter, and sort by score. Hide duplicates
  of the same card.
- **Write with AI (optional).** Forge drafts new cards, rewrites and
  tightens fields, adds greetings and lore, and rates cards. Bring your own
  API key or a local model.
- **Work from anywhere.** Use the web UI, the `ink` command line, or let
  Claude and other MCP clients search and edit your library.

Apps, importers, and AI tools are plugins, from
[innkeeper-plugins](https://github.com/inkandale/innkeeper-plugins). The
setup wizard helps you choose the ones you need.

## Quick start

You need Docker with Compose.

```bash
curl -LO https://github.com/inkandale/innkeeper/releases/latest/download/compose.yaml
docker compose up -d
docker compose logs api | grep "setup code"
```

Open `http://localhost:8080`, enter the setup code, and choose a password.
The setup wizard then installs plugins for your apps and brings in your
cards.

If an app runs on the same machine as Innkeeper, give its address as
`host.docker.internal` rather than `localhost`. Inside the container,
`localhost` is Innkeeper itself.

## Guides

- [Using Innkeeper](docs/using.md): how the pieces fit together, importing,
  sync, variants, sources, and hiding duplicates.
- [Running Innkeeper](docs/running.md): settings, Docker Compose,
  Kubernetes, first login, plugins, and backups.
- [The CLI, MCP, and API tokens](docs/cli.md): the `ink` command line,
  connecting Claude, and access tiers.
- [Plugin protocol](docs/protocol.md): for writing plugins.
- [Contributing](CONTRIBUTING.md): working on Innkeeper itself.
