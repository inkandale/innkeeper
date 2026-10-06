# Merge Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A pure package that compares a library copy and an app copy of a card or lorebook, field by field and hunk by hunk, and applies the owner's per-hunk choices.

**Architecture:** `internal/merge` has no database or plugin access. A schema maps each field to a kind, the kind picks a tokenizer, and `go-diff3` diffs the tokens: `Diff3MergeIndices` with an ancestor, `DiffComm` without one. `Apply` rebuilds the data from choices and returns the base data plus the instance's override patch.

**Tech Stack:** Go, `github.com/devsisters/go-diff3` (MIT, new dependency).

**Spec:** `2026-10-05-reconcile-design.md`, Merge engine and Resolve.

**Kaneo:** INN-25, part of INN-22.

---

## Design notes

- **API.**
  ```go
  package merge

  type Kind string   // prose, list, json, scalar, entries
  type Status string // same, auto_library, auto_app, conflict, library_only, app_only
  type Choice string // library, app, keep_in_app, both

  type Hunk struct {
  	ID      string          `json:"id"`
  	Status  Status          `json:"status"`
  	Library json.RawMessage `json:"library,omitempty"` // a string for prose, an array for list, a value otherwise
  	App     json.RawMessage `json:"app,omitempty"`
  	Base    json.RawMessage `json:"base,omitempty"`
  	Words   []Span          `json:"words,omitempty"` // prose conflicts only; display only
  }
  type Span struct {
  	Side       string `json:"side"` // library or app
  	Start, End int    // byte offsets into that side's text
  }
  type Field struct {
  	Path  string `json:"path"`
  	Kind  Kind   `json:"kind"`
  	Hunks []Hunk `json:"hunks"`
  }
  type Schema map[string]Kind // top-level field → kind; unlisted fields are scalar

  var Card, Lorebook Schema

  // Compare lists every field present on either side, sorted by path.
  // ancestor is nil when there is none.
  func Compare(s Schema, library, app, ancestor json.RawMessage) ([]Field, error)

  // Apply rebuilds the data. Hunks not in choices take def. base is the new
  // library data; override is the merge patch that turns base into what this
  // app should get, or nil when they're equal.
  func Apply(s Schema, library, app, ancestor json.RawMessage, choices map[string]Choice, def Choice) (base, override json.RawMessage, err error)

  var ErrUnknownHunk, ErrBadChoice error // wrapped with the hunk IDs
  ```
- **Schemas.**
  - `Card`: prose for `description`, `personality`, `scenario`, `first_mes`, `mes_example`, `system_prompt`, `post_history_instructions`, `creator_notes`; list for `tags`, `alternate_greetings`; json for `extensions`; scalar for the rest.
  - `Lorebook`: entries for `entries`, json for `extensions`, scalar for the rest. Inside an entry: prose for `content`, json for `extensions`, scalar for the rest.
- **Statuses from go-diff3.** With an ancestor, `Diff3MergeIndices(library, ancestor, app)`: `SideCommon` → `same`, `SideA` → `auto_library`, `SideB` → `auto_app`, `SideConflict` → `conflict`, except a conflict whose two sides are equal, which is `same`. Without one, `DiffComm(library, app)`: `Common` → `same`; only `File1` → `library_only`; only `File2` → `app_only`; both → `conflict`.
- **Kinds.**
  - prose: sentence tokens (below). Hunk values are the joined text.
  - list: one token per element, as its compact JSON. Hunk values are arrays.
  - json: recurse into objects; any non-object value, including arrays, is one scalar hunk at its dotted path, such as `extensions.depth_prompt.prompt`. A key only one side has is `library_only` or `app_only`.
  - scalar: one hunk, `same` or `conflict` (or `auto_*` with an ancestor).
  - entries: entries are matched by identity: `comment` when non-empty and unique on that side, else the sorted `keys` joined by `\x1f`. A matched entry's fields are compared under the path `entries[<identity>].<field>`. An entry only one side has is one hunk at `entries[<identity>]`, `library_only` or `app_only`, whose value is the whole entry.
- **Sentence tokens.** A token ends after `.`, `!`, `?`, or `…`, plus any closing `"`, `'`, `”`, `’`, `*`, or `)`, when followed by whitespace; the token takes that whitespace. A newline also ends a token and is part of it. Joining all tokens returns the input byte for byte.
- **Words.** For a prose `conflict`, split both sides on whitespace (keeping it attached), `DiffComm` them, and record the byte ranges of the non-common runs as spans.
- **Hunk IDs.** `<path>#<i>`, `i` counting every hunk of that field from 0, including `same` ones. Deterministic for the same inputs.
- **Applying choices.** Only `conflict`, `library_only`, and `app_only` hunks take a choice; the others merge by status (`auto_app` takes the app's tokens, and so on). `library` and `app` take that side's tokens; for `*_only` that means keep or drop. `both` takes library then app, and is `ErrBadChoice` on anything but a list hunk. `keep_in_app` takes the library's tokens for `base` and the app's for the app's copy. A choice naming a hunk that doesn't exist is `ErrUnknownHunk`. `override` is the RFC 7386 merge patch from `base` to the app's copy: changed keys get the app's value, removed keys `null`, recursing into objects.

## Tasks

### Task 1: Dependency and sentence tokens

**Files:** `go.mod`, `internal/merge/tokens.go`, `internal/merge/tokens_test.go`

- [ ] `go get github.com/devsisters/go-diff3@latest`.
- [ ] Table test `TestSentences`, asserting the token list and that `strings.Join(tokens, "") == in`:
  | Input | Tokens |
  |---|---|
  | `"Hail. The forge is cold."` | `"Hail. "`, `"The forge is cold."` |
  | `"*She waves.* \"Hi!\" Then"` | `"*She waves.* "`, `"\"Hi!\" "`, `"Then"` |
  | `"Line one\nLine two"` | `"Line one\n"`, `"Line two"` |
  | `"Wait… what?"` | `"Wait… "`, `"what?"` |
  | `"v1.2 is out"` | `"v1.2 is out"` |
  | `""` | none |
- [ ] Run `go test ./internal/merge`; it fails to compile.
- [ ] Write `sentences(s string) []string` per the design notes, and `words(s string) []string` splitting after each whitespace run.
- [ ] Tests pass. Commit: `feat(merge): split prose into sentences that rejoin exactly`.

### Task 2: Hunks for one token list

**Files:** `internal/merge/hunks.go`, `internal/merge/hunks_test.go`

- [ ] `hunks(lib, app, anc []string, hasAnc bool) []span` where `span` holds a status and the three token slices.
- [ ] Tests, on sentence tokens:
  - No ancestor, one sentence changed in the middle: `same`, `conflict`, `same`.
  - No ancestor, a sentence only the app has: `same`, `app_only`.
  - Ancestor, library changed sentence 1 and app changed sentence 3: `auto_library`, `same`, `auto_app`.
  - Ancestor, both changed sentence 2 the same way: all `same`.
  - Ancestor, both changed sentence 2 differently: `conflict` with `Base` set.
- [ ] Implement with the mapping in the design notes. Tests pass. Commit: `feat(merge): turn go-diff3 results into hunks`.

### Task 3: Compare

**Files:** `internal/merge/merge.go`, `internal/merge/schema.go`, `internal/merge/merge_test.go`

- [ ] Tests:
  - Two equal cards: every hunk `same`.
  - `first_mes` differs in one sentence: one `conflict` with `ID == "first_mes#1"` and `Words` covering only the changed words.
  - `tags` `["a","b"]` vs `["a","c"]`: one `conflict` with `Library == ["b"]`, `App == ["c"]`.
  - `extensions` where the app has an extra `lumiverse_ui` key and a different `depth_prompt.prompt`: an `app_only` hunk at `extensions.lumiverse_ui`, a `conflict` at `extensions.depth_prompt.prompt`.
  - A lorebook where one entry's content changed, one entry is library-only, and the app reordered the rest: hunks only for the changed and missing entries.
  - Calling `Compare` twice gives identical IDs.
- [ ] Implement `Card`, `Lorebook`, and `Compare`. Tests pass. Commit: `feat(merge): compare card and lorebook data field by field`.

### Task 4: Apply

**Files:** `internal/merge/apply.go`, `internal/merge/apply_test.go`

- [ ] Tests:
  - `def: library`, no choices: `base` equals the library data, `override` is nil.
  - `def: app`: `base` equals the app data (as `Compare` sees it), `override` nil.
  - `first_mes#1: keep_in_app`: `base.first_mes` is the library's; `override == {"first_mes": "<app's>"}`.
  - `extensions.lumiverse_ui#0: keep_in_app`: `override == {"extensions": {"lumiverse_ui": …}}`.
  - `tags#1: both`: `["a","b","c"]`.
  - `first_mes#1: both`: `ErrBadChoice`.
  - `nope#0: app`: `ErrUnknownHunk`, and the error names `nope#0`.
  - Ancestor case: `auto_app` hunks land in `base` with no choice given.
  - For every choice combination on the corpus-sized test card, joining prose hunks round-trips: `Apply(def: library)` on equal inputs returns the input byte for byte.
- [ ] Implement `Apply` and the merge-patch diff. Tests pass. Commit: `feat(merge): apply per-hunk choices into base data and an override`.

### Task 5: Corpus

**Files:** `internal/corpus/corpus_test.go`

- [ ] Requires plan 13b. In `TestCorpusHashes`, also assert that `merge.Compare(merge.Card, codecProjected, targetCard, nil)` has only `same` hunks.
- [ ] Commit, push, open the PR.
