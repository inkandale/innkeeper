# URL import design

Date: 2026-10-13

## Purpose

The owner pastes the page URL of a card on a public site, and that card lands
in the library with its source linked. The first sites are chub.ai,
RisuRealm, and Character Tavern.

## Decisions

| Topic | Decision |
|---|---|
| Capability | One `url-import` plugin with the existing `import` capability. Params are `{"url": "..."}`. No protocol or importer change. |
| Sites | One resolver file per site. Adding a site is one resolver plus one core detector. |
| Item | The canonical page URL. Importing the same URL again adds nothing unless the card changed. |
| Decoding | The plugin decodes with the shared `card` package (`card.Decode`, `card.DecodeLorebook`), as `card-import` does. |
| Source links | The plugin appends the canonical page URL to the card's CCv3 `data.source` list. Core detection reads `data.source`. |
| JanitorAI | Deferred. Its API returns no definition without a logged-in token, so a Janitor URL fails with a clear reason. |
| Hidden definitions | A card whose definition the site hides fails. No stub card is imported. |

## Plugin

`innkeeper-plugins/url-import`, in Go:

- `main.go` answers `import.run`. It reads `params.url`, picks the resolver by
  host, and sends one `emit.card` (or `emit.lorebook`) and one `progress`. No
  checkpoint: a run is one fetch.
- Each resolver fetches the card bytes and avatar, and returns them with the
  canonical page URL.
- Every request sends `User-Agent: innkeeper-url-import/<version>`. chub.ai
  blocks `curl/` agents, and JanitorAI blocks the Go default.
- Every request has a 30-second timeout and a body cap of 32 MiB. RisuRealm's
  PNGs with embedded assets run past 10 MiB.
- The plugin declares no config and no secrets.

### chub.ai

| | |
|---|---|
| URLs | Hosts `chub.ai`, `www.chub.ai`, `characterhub.org`, `www.characterhub.org`, `venus.chub.ai`. Paths `/characters/{user}/{slug}`, `/{user}/{slug}` (a character), `/lorebooks/{user}/{slug}` |
| Card | `GET https://api.chub.ai/api/characters/{user}/{slug}?full=true` gives `node.id`. Then `GET https://api.chub.ai/api/v4/projects/{id}/repository/files/card.json/raw` gives a V2 card, which `card.Decode` converts to CCv3 |
| Lorebook | `GET https://api.chub.ai/api/lorebooks/{user}/{slug}?full=true`. `node.definition.embedded_lorebook` is already a CCv3 `character_book`, sent as `emit.lorebook` |
| Avatar | `node.max_res_url` |
| Item | `https://chub.ai/characters/{user}/{slug}` or `https://chub.ai/lorebooks/{user}/{slug}` |

The card already carries `extensions.chub.full_path` and `id`, which the
existing `chub` detector reads. Private cards that need an API key are out
of scope.

### RisuRealm

| | |
|---|---|
| URLs | `realm.risuai.net/character/{uuid}`, with a dashed UUID |
| Type | `GET https://realm.risuai.net/character/{uuid}/__data.json` gives `card.type` and `img`, in SvelteKit's devalue format |
| Card | `type` `normal`: `GET https://realm.risuai.net/api/v1/download/json-v3/{uuid}`, a CCv3 JSON card. `type` `charx`: `GET .../download/charx-v3/{uuid}`, a JPEG with a CharX ZIP appended. Each format is refused for the other type |
| Avatar | `normal`: `https://sv.risuai.xyz/resource/{img}`. `charx`: the CharX icon, which `card.Decode` returns |
| Item | `https://realm.risuai.net/character/{uuid}` |

The card's `data.source` already holds `risurealm:{uuid}`.

### Character Tavern

| | |
|---|---|
| URLs | `character-tavern.com/character/{author}/{slug}` |
| Card | `GET https://ct-cards.storage.character-tavern.com/{author}/{slug}.png?action=download`, a PNG with a CCv3 `ccv3` chunk. Without `?action=download` the chunks come back as zTXt, which `card.Decode` doesn't read |
| Avatar | The same PNG |
| Item | `https://character-tavern.com/character/{author}/{slug}` |

### JanitorAI

`janitorai.com/characters/...` fails, non-retryable: "JanitorAI needs a
login; not supported yet". Anonymous requests to
`janitorai.com/hampter/characters/{uuid}` return the definition fields as
null even for public definitions.

## Core and codec

1. **`card.Decode`** reads a CharX ZIP that has data in front of it, such as
   RisuRealm's JPEG prefix. Only the `isZip` check changes, because Go's
   `archive/zip` already reads past prepended data.
2. **Detection** also passes each string in the card's `data.source` to every
   detector's `fromURL`. A lorebook's `character_book` has no `source` field,
   so lorebook links still come from `extensions.chub`.
3. **New detectors** in `internal/library/detect.go`:
   - `risu`: ref is the UUID. Reads `https://realm.risuai.net/character/{uuid}`
     and `risurealm:{uuid}`.
   - `character-tavern`: ref is `{author}/{slug}`.
   - `chub` also accepts `characterhub.org`, `venus.chub.ai`, and the
     two-part `/{user}/{slug}` path.
4. **`ink`** gains `ink cards import-url <instance-id> <url>`, which starts an
   import job with params `{"url": ...}`, as `ink cards import` does with an
   upload.
5. **Web**: the import panel in `BringInCards.tsx` gets a URL field next to
   upload, shown when a `url-import` instance exists.
6. **MCP**: `start_import` gains a `url` param beside `path`, sent as
   `params.url`.

## Errors

| Case | Result |
|---|---|
| Host has no resolver | Invalid params: "no importer for {host}" |
| URL path doesn't match the site's shapes | Invalid params, naming the expected shape |
| HTTP 404 | Not retryable: "not found on {site}" |
| HTTP 429 or 5xx, or a timeout | Retryable, so the core re-queues the job |
| Body over the cap | Not retryable |
| Bytes aren't a card | Not retryable, wrapping `card.ErrNotCard` |
| JanitorAI | Not retryable, as above |

## Testing

- Each resolver runs against an `httptest` server that serves trimmed
  fixtures from the 2026-10-13 probes, with the site's base URL injected.
  The tests cover URL parsing, both RisuRealm types, a chub lorebook, and
  each error row.
- `TestReal`, skipped unless `URL_IMPORT_REAL=1`, imports one known SFW card
  from each live site.
- `card`: a prefixed-CharX test.
- `detect.go`: table tests for the new detectors and for `data.source`.

## Out of scope

- JanitorAI, which needs a login (its own ticket).
- Several URLs in one import.
- Checking imported cards for newer versions on their site.
- Private chub.ai cards that need an API key.
- Browsing a live site from Innkeeper (INN-38 for chub.ai).
