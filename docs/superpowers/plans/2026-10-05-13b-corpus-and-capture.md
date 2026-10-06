# Card Corpus and Capture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A committed set of real, SFW cards, plus what the codec and each app's plugin return for them, and a test that every variant of a card normalizes to the same content hash.

**Architecture:** Source cards and captured variants live in `testdata/corpus/` in the core repo. An opt-in Go test captures the variants by starting the real plugin binaries against throwaway SillyTavern and Lumiverse containers. The normal test suite only reads the files.

**Tech Stack:** Go, Docker Compose, the bundled plugins. No new Go dependencies.

**Spec:** `2026-10-05-reconcile-design.md`, step 2 and Testing.

**Kaneo:** INN-24, part of INN-22.

---

## Design notes

- **Layout.** One directory per card, named by slug:
  ```
  testdata/corpus/
    README.md                    # one row per card: creator, URL, license, what it covers
    compose.yaml                 # throwaway SillyTavern and Lumiverse
    <slug>/source.png|json|charx
    <slug>/codec.json            # codec.decode result: {"card": …, "lorebook": …}
    <slug>/sillytavern.json      # target.get after target.put: {"fields": […], "card": …, "lorebooks": […]}
    <slug>/lumiverse.json
    <slug>/codec.error           # instead of codec.json when the codec can't read it
  ```
  The tests are in `internal/corpus/` (`capture_test.go`, opt-in, and `corpus_test.go`, always run) and read `../../testdata/corpus`.
- **Sources.** chub.ai is unreachable from the build network (403 by country). Character Tavern and RisuRealm are reachable. Coverage needed: V2 PNG, V3 PNG, V2 JSON, V3 JSON, CharX, an embedded lorebook, alternate greetings, depth prompt, regex scripts, CRLF line endings, an empty optional field. Prefer cards with a stated license. The owner approves the list before anything is committed.
- **Apps.** `compose.yaml` runs `ghcr.io/sillytavern/sillytavern:1.19.0` and `ghcr.io/prolix-oc/lumiverse:1.2.0`, the homelab's versions, on host ports 18001 and 18002 (8080 is taken on the owner's machine). SillyTavern runs with `WHITELIST_MODE=false` and basic auth off. Lumiverse sets `OWNER_USERNAME=corpus` and writes the generated password to `/app/data/owner.credentials` on first start; the capture reads it with `docker compose exec`.
- **Capture.** `INNKEEPER_CORPUS_CAPTURE=1 INNKEEPER_PLUGINS_DIR=… go test ./internal/corpus -run TestCapture`. For each card: `codec.decode` the source; for each target, `initialize` the plugin with the container's URL, `target.put` the card (and its lorebook first, through `target.put_lorebook`, for a standalone target, passing the remote ID as a link), then `target.get` it back and write the file. Plugins start through `pluginhost.Start`, as `internal/contract` does.
- **What the test asserts.** For each card and each target file, project the codec's card to the target's `fields` (`targets.project`, exported as `targets.Project` for this), hash both with `cardhash.ContentHash`, and require them equal. On a mismatch, report each differing field with both values, so the fix is obvious. A card with `codec.error` is skipped with its message.
- **Fixing mismatches.** A difference that comes from an app is fixed in that app's plugin `get`, with a unit test there, then recaptured. A difference that is really noise for every app is fixed in `cardhash` normalization. Each fix leaves the corpus card that exposed it as its named case. Don't weaken the assertion to pass.

## Tasks

### Task 1: Pick the cards

- [ ] Browse Character Tavern and RisuRealm for SFW cards that cover the list in the design notes. About 15 cards.
- [ ] Write `testdata/corpus/README.md`: a table with slug, name, creator, URL, license (or "none stated"), format, and what it covers.
- [ ] **Stop and show the owner the table.** Download nothing into the repo until they approve.
- [ ] Download the approved cards to `testdata/corpus/<slug>/source.<ext>`. Commit: `test: add the card corpus sources`.

### Task 2: Compose file

- [ ] Write `testdata/corpus/compose.yaml` per the design notes, with volumes on tmpfs so every run starts empty.
- [ ] `docker compose -f testdata/corpus/compose.yaml up -d`; `curl -s localhost:18001` and `localhost:18002` answer; the Lumiverse credentials file exists. `down -v`.
- [ ] Commit: `test: run throwaway SillyTavern and Lumiverse for corpus capture`.

### Task 3: Capture

**Files:** `internal/corpus/capture_test.go`, `internal/targets/data.go` (export `Project`)

- [ ] Rename `project` to `Project` in `internal/targets`, updating its callers. `go test ./internal/targets` passes.
- [ ] `TestCapture` skips unless `INNKEEPER_CORPUS_CAPTURE=1`. It loads manifests from `INNKEEPER_PLUGINS_DIR`, and for each `testdata/corpus/*/source.*` writes `codec.json` or `codec.error`, then `sillytavern.json` and `lumiverse.json`, each with the target's `describe` fields. JSON is indented and keys sorted, so golden diffs are readable.
- [ ] Build the plugins (`make -C ../innkeeper-plugins install DEST=/tmp/innkeeper-plugins`), start compose, run the capture, stop compose.
- [ ] Check `git status`: one set of files per card. Commit: `test: capture the corpus through the codec, SillyTavern, and Lumiverse`.

### Task 4: Normalization test

**Files:** `internal/corpus/corpus_test.go`

- [ ] `TestCorpusHashes`, a subtest per card and target, as in the design notes.
- [ ] Run `go test ./internal/corpus`. Expect failures; each names a field.
- [ ] For each failure, fix it per "Fixing mismatches", recapture, and rerun. One commit per fix, naming the card and field, such as `fix(lumiverse): drop SillyTavern's fav on read (corpus: <slug>)` in the plugins repo.
- [ ] When it passes, commit: `test: require every captured variant to hash like the codec's card`.

### Task 5: Docs

- [ ] `README.md` (core), under development: how to recapture after an app upgrade, and that a golden diff is how an app's format change shows up.
- [ ] Commit, push, open the PR.
