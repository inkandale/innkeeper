# The CLI, MCP, and API tokens

## Access tiers

Every API token has a tier, and every route needs one:

| Tier | Allows |
|---|---|
| `read` | Anything that changes nothing: lists, search, push previews, drift checks |
| `write` | Also changes to the library, which revision history can undo: edits, imports |
| `destructive` | Also what Innkeeper can't undo, or what grants more access: pushes, unbinding, deleting collections, creating or changing plugin instances, managing tokens |

`innkeeper token create <name>` makes a `read` token unless you name a tier.
A token can't create one with a higher tier. A browser login has every tier.

When a plugin call fails, only a `destructive` caller sees the plugin's reason,
since it can name files on the server. Other tiers see which plugin failed,
and the reason goes to the server log.

## MCP

The API serves MCP at `/mcp`, so Claude can search and edit the library,
push to apps, run imports, and set up plugins. Connect Claude Code with an
API token; its tier decides which tools Claude sees:

```bash
claude mcp add --transport http innkeeper "$INNKEEPER_URL/mcp" \
  --header "Authorization: Bearer $(innkeeper token create claude write)"
```

The `push` tool needs the `confirm` value from `preview_push`, so a push
always matches a preview Claude has shown you. Tools that need the
`destructive` tier are marked destructive, so Claude asks before running
them.

`start_job` starts any API route that queues a job, such as an import, a
sync, or an evaluation, by its operation ID and JSON body. Its description
lists each route with the body's fields. Destructive routes, such as purge,
are left out.

To connect claude.ai, or another client that signs in through OAuth, set
`INNKEEPER_PUBLIC_URL`, then add `<public URL>/mcp` as a custom connector
with no credentials. The client sends you to Innkeeper to log in, then asks
which tier to grant; read is the default. `innkeeper token list` shows
approved clients, and `innkeeper token revoke-grant <id>` cuts one off.
No MCP client or `ink` login can change login providers or who the owner
is, whatever its tier; use an API token or the web UI.

Plugins can add tools too. They show as `<instance>_<tool>`, such as
`lumiverse_list_untracked`, and each declares its tier; one that declares
none counts as `destructive`. `GET /plugin-tools` lists them.

## CLI

`ink` works with a server's API from any machine. The server binary,
`innkeeper`, runs the server and the commands that need its database.

### ink

Download `ink` for your platform from the
[latest release](https://github.com/inkandale/innkeeper/releases/latest),
or build it:

```bash
go install github.com/inkandale/innkeeper/cmd/ink@latest
```

The image includes `ink` too. Log in through your browser:

```bash
ink login https://innkeeper.example.com
```

The server needs `INNKEEPER_PUBLIC_URL`, and the URL you give must match it
exactly; otherwise `ink` refuses and names the right one. The URL must be
`http` or `https`. Innkeeper asks which tier to give `ink`, as it does for an
MCP client, and lists the login with the approved OAuth clients (Settings,
Access, or `innkeeper token list`). `ink` keeps the login in the OS keychain,
or where there is none, in a file only you can read. The login lasts while you
use `ink` at least every 30 days. `ink logout` revokes it.

Each login is a profile, named `default` unless you pass `--profile <name>`.
The first one becomes the default. `ink profiles` lists them,
`ink profiles use <name>` changes the default, and `--profile` picks one for a
single command. Logging in again to a profile replaces its login and revokes
the old one.

Scripts can set `INNKEEPER_URL` and an API token in `INNKEEPER_TOKEN`
instead; when `INNKEEPER_TOKEN` is set, they take precedence over profiles.
Make a token on the server with
`innkeeper token create <name> [read|write|destructive]`.

```text
ink login <url> [--profile name]                  log in through the browser
ink logout [--profile name]                       revoke a login and forget its profile
ink profiles [use <name>]                         list profiles, or change the default
ink call <method> <path> [json]                   any API call, such as: ink call GET /jobs
ink import <instance-id> [params-json]            start an import (importer plugins only)
ink jobs [show|cancel|watch <job-id>]             list or follow jobs
ink cards list [--archived] [--limit n] [--page n]
                                                  list cards, newest first
ink cards search <text>|<query-json> [--limit n] [--page n]
                                                  search cards by text, or with a query
ink cards show <card-id>                          a card's details; --json includes its data
ink cards history <card-id>                       a card's revisions, newest first
ink cards create <file>|- [--slug s] [-m message]
                                                  create a card from CCv3 JSON, or stdin
ink cards edit <card-id> [file] [-m message]      edit a card's data in $EDITOR, or replace it with a file's
ink cards import <instance-id> <file>             upload a card or lorebook file and import it
ink cards import-url <instance-id> <url>          import a card or lorebook from its page on a site, such as chub.ai
ink cards export <card-id> [file] [--format f]    a card as CCv3 JSON, or a codec's format (.png means png)
ink cards archive|unarchive <card-id>|<query-json>
                                                  archive or unarchive a card, or the cards a search matches
ink cards sources <card-id>                       list a card's sources
ink cards sources add <card-id> <url>
ink cards sources remove <card-id> <source-id>
ink lorebooks ...                                 the same commands for lorebooks; export writes CCv3 JSON only
ink sync push <instance-id> <card-id>|<query-json> [--new-copy] [--yes]
                                                  push one card, or the cards a search matches, to an app
ink sync preview <instance-id> <card-id>          show what pushing a card would change in an app
ink sync bindings <card-id>                       list a card's bindings
ink sync bindings show|pushes <binding-id>        show a binding, or list its pushes
ink sync status <binding-id>                      read the app's copy now and show the binding
ink sync unbind <binding-id> [--yes]              forget a binding and its push history
ink sync run [instance-id] [params-json] [--dry-run]   sync every app with the library, or one app
ink sync refresh                                  check every app for changes; changes nothing
ink sync changes [--app <instance-id>] [--state <state>]
                                                  list the cards an app needs you for
ink sync autosync <binding-id> on|off             let each sync push the library's copy to this app, or stop it
ink sync compare <instance-id> <remote-id> [card-id]
                                                  show how an app character differs from a card
ink sync resolve <instance-id> <remote-id> bind|import|skip [card-id] [hunk=choice ...] [--default choice]
                                                  bind, import, or skip one app character
ink sync apply <sync-job-id> [remote-id ...] [--verdict v,...]
                                                  resolve a sync's items as it suggested
ink instances [show|check|delete <instance-id>]   list plugin instances, or show, check, or delete one
ink instances create <plugin> <name> [config-json] [--secrets file]
ink instances configure <instance-id> [--name n] [--config json] [--secrets file]
ink plugins [catalog]                             list installed plugins, or what sources offer
ink plugins install <name> [version] [--source id]   install or update a plugin
ink plugins remove <name>                         uninstall a plugin
ink plugins purge <name> [--yes]                  delete an uninstalled plugin's instances and data
ink plugins sources [add <id> <url>|remove <id>]  list, add, or remove plugin sources
ink backups [create assets|config]                list backups, or start one
ink backups download <name> [file]                download a backup as a .tar.gz
ink backups restore <file> [--replace] [--yes]    upload a backup and restore it
```

`--json` prints JSON instead of tables, and every command takes `--help`.
`ink completion bash|zsh|fish|powershell` prints a shell completion script;
`ink completion <shell> --help` says where to put it.

`cards search` takes text, or a whole `POST /cards/search` query when the argument
starts with `{`. `list` and `search` print 50 rows unless `--limit` says
otherwise, and name the next page when there is one.

`cards edit` opens the data in `$VISUAL` or `$EDITOR` and saves a revision if
it changed. If the save fails, for example because someone else edited the
card meanwhile, the error names the file that holds your edit.

`cards archive` with a query needs a `filter` or `text`; to select every card,
filter on `archived`. It touches only cards not already in the target state.
The API's `POST /bulk` also tags, collects, and exports; see `/api/docs`.
`POST /cards/purge` deletes the cards a query matches for good, given their
count as `confirm`. It can't be undone. A pushed card is deleted with its
link to the app; the app's copy stays, so archive with unpublish first to
remove it.

`sync push` prints what it would replace in the app. For one card, that's
each field it changes; for a query, how many matching cards are new, not
pushed, in sync, or drifted there as of their last check. Use `ink sync preview` to see
one card's changes. A query push replaces only the copies it counted; a card
whose app copy changes after that fails, and the app keeps its copy. `--yes`
skips the question, not that check.

`sync push`, `sync unbind`, `plugins purge`, and `backups restore` ask before
they run, since they replace or delete data; `--yes` skips the question.
Without a terminal and without `--yes`, they fail instead of waiting.

`jobs watch` exits non-zero unless the job succeeds.

`instances check` starts the instance's plugin, even a broken one, and exits
non-zero with the reason if it doesn't work. For an app it can read cards back
from, it also lists the app's cards, which tests the address and login. A
failed start marks the instance broken, and a good one clears that. An app
error doesn't mark it broken.

`--secrets` reads a JSON object of secrets from a file, or from stdin with `-`,
so they stay out of shell history. For `instances configure`, a `null` value
removes a secret.

`sync push` replaces the app's copy, so it overwrites changes made in the app.
`--new-copy` is for apps that can't update a card: pushing there again makes
another copy, so the push fails without the flag.

Exporting in a format other than `json` needs an instance of a codec plugin
that writes it. Export uses the oldest working codec instance for the format:

```bash
ink instances create card-codec Codec
ink cards export 12 brakka.png
```

### innkeeper

```text
innkeeper api | worker | migrate                  run the server; see Running
innkeeper token create <name> [tier] | list | revoke <id> | revoke-grant <id>
innkeeper openapi                                 print the OpenAPI spec
innkeeper plugin check <plugin-dir>               run the plugin contract checks
innkeeper backup <dir> [assets|config]            back up both types, or one, into dir
innkeeper restore [--replace] <archive-or-backup-dir>...
                                                  restore backups, assets first
```
