# Password Login and Card Importer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the owner log in with a password, and import cards and lorebooks from a folder or an uploaded file, through two plugins in `innkeeper-plugins` that ship in the core image.

**Architecture:** `password-login` is an `auth` plugin that checks a bcrypt hash kept in an instance secret and always reports the subject `owner`. `card-import` is an `import` plugin that reads one folder set in its instance config, or one uploaded blob named by its sha256, with the `card` package from plan 6a. That package gains `DecodeLorebook` for standalone lorebook files. The core fixes its manifest loader so a `config` schema can nest, and changes its image pin, Compose file, and README.

**Tech Stack:** Go 1.27, the core's `protocol` package, `golang.org/x/crypto/bcrypt` v0.57.0 (new), `os.Root` for the import folder. No other new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, sections "Plugin repository" and "v1 scope". Constraints from plan 4's and plan 6a's "Plan series" sections.

**Kaneo:** INN-11. Plan 6c (INN-12) adds the Lumiverse target.

---

## Scope

In this plan:

- `password-login`: `auth.begin` returns a password form; `auth.complete` checks it against the `password_hash` secret. `password-login hash` makes that hash.
- `card-import`: `import.run` over the folder in `config.folder`, or a subfolder of it, or over one upload with `params.blob`. PNG and JSON cards, CCv3 and SillyTavern world info lorebooks. Checkpoints for folder imports.
- `card.DecodeLorebook`, with SillyTavern's `Eldoria.json` as its golden file.
- Core: `LoadManifest` accepts nested tables under `config`, which every JSON Schema with `properties` needs.
- `internal/pluginio`: the file helpers `card-codec` has now, shared with `card-import`.
- The core image ships both plugins; Compose mounts `./import` read-only at `/import`; the README covers first login and importing.

Left out:

| Left out | Why | Where |
|---|---|---|
| An `innkeeper upload` command | `curl` to `POST /blobs` does it; the UI is the real upload path | Plan 7, or when the CLI needs it |
| Contract kit checks for `import` and `auth` | The plugins' own tests cover them; no third-party plugin needs them yet | When one does |
| Several passwords or users | v1 has one owner | Not planned |
| Zip and CHARX archives | No source in v1 produces them | When one does |

## Plan series

This is plan 6b. See plan 1 for the full list.

Constraints this plan leaves for later plans:

- **Plan 7 (UI):**
  - Upload: `POST /blobs`, then `POST /imports` with `{"instance_id": <card-import instance>, "params": {"blob": "<sha256>", "name": "<file name>"}}`. `name` names a lorebook that has none.
  - An upload that isn't a card or lorebook fails the job with `-32602`. A folder import skips such files and says how many in the job's progress `message`.
  - The login form `password-login` returns has one field, `password`, with `"format": "password"`.
- **Plan 6c and later plugins:** shared file helpers are in `internal/pluginio`. Read input with `pluginio.ReadFile` or `pluginio.ReadAll`, which cap it at 32 MiB, and write to `blob_tmp` with `pluginio.WriteTmp`.

## Design notes

- **Nested config schemas.** The TOML decoder reports tables nested inside a `map[string]any` as undecoded, so `LoadManifest` rejected any `config` with `properties`, written as tables or inline. `config` is free-form JSON Schema, so the loader now ignores undecoded keys under it.
- **Bcrypt, made by the plugin.** The core stores secrets encrypted, so the hash guards against a leaked key or backup. Bcrypt is the boring choice with a recognizable format. `password-login hash` reads a password on stdin and prints its hash at cost 12, so the owner needs no other tool.
- **One import folder per instance.** Import params come from anyone with an API token, and the plugin runs as the core's user. So params can only name a path inside `config.folder`, which the owner sets when creating the instance, and the plugin opens that folder with `os.Root`, which refuses `..` and symlinks that leave it. The walk also skips symlinks. With no `config.folder`, only uploads work.
- **Uploads by sha256.** The plugin checks `blob` is 64 lowercase hex digits before building `<blob_stores.library>/ab/cd/<sha256>`, so it can't name another file. The item ID is the sha256, so the same upload twice adds nothing.
- **Lorebooks before cards.** A CCv3 `character_book` with `name` and `description` passes `card.Decode` as a V1 card, so the importer tries `card.DecodeLorebook` first. A lorebook always has `entries`; no card does.
- **World info conversion follows SillyTavern.** `DecodeLorebook` maps world info fields to `character_book` fields as SillyTavern's `convertWorldInfoToCharacterBook` does, and renames the fields it keeps under `extensions` to SillyTavern's names there, so a book pushed back to SillyTavern keeps its settings. Unlike SillyTavern, it keeps every other field under `extensions` with its own name, so nothing is lost.
- **Checkpoints by position.** A folder import walks the whole folder first, giving a total for progress and a fixed order. The checkpoint is the last file finished; a resumed run starts after it in the walk order. String comparison doesn't match walk order (`a/b` walks before `a-c`), so the plan looks the file up rather than comparing. If the file is gone, the run starts over and the core skips what it already has. Checkpoints go out every 20 files, since the core writes each one to the database.
- **Skipped files.** A file that isn't a card or lorebook, or can't be read, is logged to stderr and counted in the progress message. The protocol has no skip notification, so the core's `skipped` count covers only duplicates.

## File structure

`innkeeper-plugins`:

| File | Responsibility |
|---|---|
| `internal/pluginio/pluginio.go` | `MaxInput`, `ReadFile`, `ReadAll`, `WriteTmp` |
| `card-codec/main.go` | Uses `pluginio` instead of its own helpers |
| `card/lorebook.go` | `DecodeLorebook`, `ErrNotLorebook`, world info conversion |
| `card/testdata/eldoria.lorebook.json` | SillyTavern's sample world info, and its `.want.json` |
| `password-login/main.go`, `plugin.toml`, `README.md` | The login plugin and its `hash` command |
| `card-import/main.go` | Plugin entry point and `initialize` |
| `card-import/import.go` | `import.run`: params, uploads, folder walks |
| `card-import/plugin.toml`, `README.md` | Manifest with the `folder` config schema |
| `index.toml`, `.gitignore` | The two new plugins |

`innkeeper`:

| File | Responsibility |
|---|---|
| `protocol/manifest.go` | Nested `config` tables aren't unknown keys |
| `Dockerfile` | `PLUGINS_REF` at the merged plugins commit |
| `compose.yaml`, `.gitignore` | The read-only `/import` mount |
| `README.md` | First login with `password-login`; importing |

## Running tests

`innkeeper-plugins` tests need no database. Go needs private module access:

```bash
go env -w GOPRIVATE=github.com/davidgibbons/*
git ls-remote https://github.com/inkandale/innkeeper HEAD   # must print a commit; if not, run: gh auth setup-git
```

Every part uses a branch named `login-and-importer`. Part 1 runs in `~/git/innkeeper`. Part 2 runs in `~/git/innkeeper-plugins` after part 1 is merged and pushed. Part 3 runs in `~/git/innkeeper` after part 2 is merged and pushed.

---

# Part 1: Nested config in manifests (`innkeeper`)

### Task 1: Accept nested `config` tables

**Files:**
- Modify: `protocol/manifest.go`
- Test: `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing test**

Append to `protocol/manifest_test.go`, adding `"os"` and `"path/filepath"` to its imports if they are missing:

```go
func TestLoadManifestNestedConfig(t *testing.T) {
	dir := t.TempDir()
	toml := `name = "c"
version = "1"
protocol = 1
command = ["c"]
capabilities = ["import"]

[config]
type = "object"

[config.properties.folder]
type = "string"
`
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	props, _ := m.Config["properties"].(map[string]any)
	if _, ok := props["folder"]; !ok {
		t.Fatalf("config = %v; want properties.folder", m.Config)
	}

	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(toml+"\n[extra]\nx = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(dir); err == nil {
		t.Fatal("an unknown top-level table loaded")
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./protocol -run TestLoadManifestNestedConfig`
Expected: FAIL with `unknown keys: config.properties.folder, config.properties.folder.type`.

- [ ] **Step 3: Ignore undecoded keys under `config`**

In `LoadManifest` in `protocol/manifest.go`, replace:

```go
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", abs, strings.Join(keys, ", "))
	}
```

with:

```go
	var unknown []string
	for _, k := range md.Undecoded() {
		// config is free-form JSON Schema, but the decoder lists its nested
		// tables as undecoded even though they are in the map.
		if k[0] != "config" {
			unknown = append(unknown, k.String())
		}
	}
	if len(unknown) > 0 {
		return nil, fmt.Errorf("manifest %s: unknown keys: %s", abs, strings.Join(unknown, ", "))
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./protocol`
Expected: PASS.

- [ ] **Step 5: Commit, merge, and push**

```bash
git add protocol
git commit -m "Let a manifest's config schema nest tables"
```

Review, merge `login-and-importer` into `main`, push, and note `git rev-parse HEAD`.

- [ ] **Step 6: Move `innkeeper-plugins` onto it**

In `~/git/innkeeper-plugins`, on a new branch `login-and-importer`:

```bash
go get -tool github.com/inkandale/innkeeper/cmd/innkeeper@<commit from Step 5>
go test ./... && make check
git add go.mod go.sum
git commit -m "Move to the core that accepts nested config schemas"
```

# Part 2: Plugins (`innkeeper-plugins`)

### Task 2: Shared file helpers

**Files:**
- Create: `internal/pluginio/pluginio.go`
- Test: `internal/pluginio/pluginio_test.go`
- Modify: `card-codec/main.go`

- [ ] **Step 1: Write the failing test**

Create `internal/pluginio/pluginio_test.go`:

```go
package pluginio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadFileCaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(MaxInput + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ReadFile(path); err == nil {
		t.Fatal("read a file over MaxInput")
	}
}

func TestWriteTmp(t *testing.T) {
	tmp := t.TempDir()
	path, err := WriteTmp(tmp, "avatar-*.png", []byte("x"))
	if err != nil || filepath.Dir(path) != tmp {
		t.Fatalf("WriteTmp = %q, %v", path, err)
	}
	if _, err := WriteTmp("", "avatar-*.png", nil); err == nil {
		t.Fatal("wrote without a blob_tmp")
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/pluginio`
Expected: FAIL to compile with `undefined: MaxInput`.

- [ ] **Step 3: Write the package**

Create `internal/pluginio/pluginio.go`, moving the helpers out of `card-codec/main.go`:

```go
// Package pluginio has the file helpers plugins share.
package pluginio

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxInput caps the files plugins read. Card PNGs are a few MB at most.
const MaxInput = 32 << 20

// ReadFile reads the file at path, failing if it is over MaxInput.
func ReadFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return ReadAll(f)
}

// ReadAll reads f, failing if it holds more than MaxInput bytes.
func ReadAll(f *os.File) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, MaxInput+1))
	if err == nil && len(b) > MaxInput {
		err = fmt.Errorf("%s is over %d MiB", f.Name(), MaxInput>>20)
	}
	return b, err
}

// WriteTmp writes b to a new file in tmp, the blob_tmp folder, and returns
// its path.
func WriteTmp(tmp, pattern string, b []byte) (string, error) {
	if tmp == "" {
		return "", errors.New("initialize sent no blob_tmp")
	}
	f, err := os.CreateTemp(tmp, pattern)
	if err != nil {
		return "", err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
```

- [ ] **Step 4: Use it in `card-codec`**

In `card-codec/main.go`:

- Delete `maxInput`, `readFile`, and `writeTmp`, and the `errors` and `io` imports.
- Add the import `"github.com/inkandale/innkeeper-plugins/internal/pluginio"`.
- Replace `readFile(` with `pluginio.ReadFile(` (two places) and `writeTmp(` with `pluginio.WriteTmp(` (two places).

- [ ] **Step 5: Run the tests**

Run: `go vet ./... && go test ./internal/pluginio ./card-codec`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/pluginio card-codec/main.go
git commit -m "Move the codec's file helpers into a shared package"
```

### Task 3: Decode lorebook files

**Files:**
- Create: `card/lorebook.go`
- Create: `card/testdata/eldoria.lorebook.json`, `card/testdata/eldoria.lorebook.json.want.json`
- Test: `card/lorebook_test.go`
- Modify: `card/card_test.go` (`TestGolden` skips lorebooks), `card/testdata/README.md`

- [ ] **Step 1: Add the golden file**

```bash
curl -sfL https://raw.githubusercontent.com/SillyTavern/SillyTavern/release/default/content/Eldoria.json \
  -o card/testdata/eldoria.lorebook.json
jq '.entries | length' card/testdata/eldoria.lorebook.json   # prints 4
```

Append to `card/testdata/README.md`:

```markdown
- `eldoria.lorebook.json`: SillyTavern's sample world info, `default/content/Eldoria.json` in the same repo. `TestGolden` skips `*.lorebook.json`; `TestLorebookGolden` reads them.
```

In `card/card_test.go`, `TestGolden` would try to decode it as a card. Change its skip condition from:

```go
		if strings.HasSuffix(f, ".want.json") || strings.HasSuffix(f, ".md") {
```

to:

```go
		if strings.HasSuffix(f, ".want.json") || strings.HasSuffix(f, ".md") || strings.HasSuffix(f, ".lorebook.json") {
```

- [ ] **Step 2: Write the failing tests**

Create `card/lorebook_test.go`:

```go
package card

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLorebookGolden decodes each testdata/*.lorebook.json, compares it with
// its .want.json, and checks the result decodes to itself.
func TestLorebookGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*.lorebook.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden lorebooks: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			book, err := DecodeLorebook(in, "Eldoria")
			if err != nil {
				t.Fatal(err)
			}
			if *update {
				var buf bytes.Buffer
				if err := json.Indent(&buf, book, "", "  "); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f+".want.json", append(buf.Bytes(), '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(f + ".want.json")
			if err != nil {
				t.Fatalf("%v; run go test ./card -run TestLorebookGolden -update and review the file", err)
			}
			if !sameJSON(t, book, want) {
				t.Fatalf("decoded lorebook differs from %s.want.json:\n%s", f, book)
			}
			again, err := DecodeLorebook(book, "other")
			if err != nil || !sameJSON(t, again, book) {
				t.Fatalf("decoding the result changed it: %v\n%s", err, again)
			}
		})
	}
}

func TestDecodeLorebookShapes(t *testing.T) {
	book := `{"name": "B", "entries": [{"keys": ["k"], "content": "c", "extensions": {}, "enabled": true, "insertion_order": 0, "use_regex": false}]}`
	for name, in := range map[string]string{
		"character_book": book,
		"lorebook_v3":    `{"spec": "lorebook_v3", "data": ` + book + `}`,
	} {
		got, err := DecodeLorebook([]byte(in), "file")
		if err != nil || !sameJSON(t, got, []byte(book)) {
			t.Errorf("%s: %v\n%s", name, err, got)
		}
	}
	got, err := DecodeLorebook([]byte(`{"entries": []}`), "file")
	if err != nil || !strings.Contains(string(got), `"name":"file"`) {
		t.Errorf("unnamed book: %v %s", err, got)
	}
}

func TestDecodeWorldInfo(t *testing.T) {
	in := `{"entries": {
		"10": {"uid": 10, "key": ["b"], "content": "B", "disable": true, "position": 1, "excludeRecursion": true, "addMemo": true},
		"2": {"uid": 2, "key": ["a"], "content": "A", "position": 0, "extensions": null}}}`
	got, err := DecodeLorebook([]byte(in), "world")
	if err != nil {
		t.Fatal(err)
	}
	var book struct {
		Name    string
		Entries []map[string]any
	}
	if err := json.Unmarshal(got, &book); err != nil {
		t.Fatal(err)
	}
	if book.Name != "world" || len(book.Entries) != 2 || book.Entries[0]["content"] != "A" {
		t.Fatalf("book = %s; want name world and entries in uid order", got)
	}
	a, b := book.Entries[0], book.Entries[1]
	if a["position"] != "before_char" || a["enabled"] != true {
		t.Errorf("entry 2 = %v", a)
	}
	ext := b["extensions"].(map[string]any)
	if b["id"] != 10.0 || b["enabled"] != false || b["position"] != "after_char" || b["use_regex"] != true ||
		ext["position"] != 1.0 || ext["exclude_recursion"] != true || ext["addMemo"] != true {
		t.Errorf("entry 10 = %v", b)
	}
}

func TestDecodeLorebookRejects(t *testing.T) {
	for _, in := range []string{
		`not json`,
		`[]`,
		`{"name": "x", "description": "a V1 card"}`,
		`{"entries": "x"}`,
		`{"entries": [1]}`,
		`{"spec": "chara_card_v3", "data": {"name": "x"}}`,
	} {
		if _, err := DecodeLorebook([]byte(in), "f"); !errors.Is(err, ErrNotLorebook) {
			t.Errorf("%s: err = %v, want ErrNotLorebook", in, err)
		}
	}
}
```

- [ ] **Step 3: Run them to make sure they fail**

Run: `go test ./card -run Lorebook`
Expected: FAIL to compile with `undefined: DecodeLorebook`.

- [ ] **Step 4: Write `DecodeLorebook`**

Create `card/lorebook.go`:

```go
package card

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// ErrNotLorebook means the input isn't a lorebook this package can read.
var ErrNotLorebook = errors.New("not a lorebook")

// stExtensionKeys renames world info fields kept under an entry's
// extensions to the names SillyTavern gives them there, so it reads them
// back when it imports the book. Fields not listed keep their names.
var stExtensionKeys = map[string]string{
	"excludeRecursion":          "exclude_recursion",
	"displayIndex":              "display_index",
	"outletName":                "outlet_name",
	"groupOverride":             "group_override",
	"groupWeight":               "group_weight",
	"preventRecursion":          "prevent_recursion",
	"delayUntilRecursion":       "delay_until_recursion",
	"scanDepth":                 "scan_depth",
	"matchWholeWords":           "match_whole_words",
	"useGroupScoring":           "use_group_scoring",
	"caseSensitive":             "case_sensitive",
	"automationId":              "automation_id",
	"matchPersonaDescription":   "match_persona_description",
	"matchCharacterDescription": "match_character_description",
	"matchCharacterPersonality": "match_character_personality",
	"matchCharacterDepthPrompt": "match_character_depth_prompt",
	"matchScenario":             "match_scenario",
	"matchCreatorNotes":         "match_creator_notes",
	"ignoreBudget":              "ignore_budget",
}

// DecodeLorebook reads a standalone lorebook file: a CCv3 character_book,
// one wrapped as {"spec": "lorebook_v3", "data": ...}, or SillyTavern world
// info. It returns a CCv3 character_book. name names a book that has none,
// such as the file name without its extension. Every error wraps
// ErrNotLorebook.
func DecodeLorebook(b []byte, name string) (json.RawMessage, error) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotLorebook, err)
	}
	var spec string
	_ = json.Unmarshal(top["spec"], &spec)
	if spec == "lorebook_v3" {
		var data map[string]json.RawMessage
		if err := json.Unmarshal(top["data"], &data); err != nil || data == nil {
			return nil, fmt.Errorf("%w: data must be an object", ErrNotLorebook)
		}
		top = data
	}
	entries := bytes.TrimSpace(top["entries"])
	switch {
	case len(entries) > 0 && entries[0] == '[':
		var list []map[string]json.RawMessage
		if err := json.Unmarshal(entries, &list); err != nil {
			return nil, fmt.Errorf("%w: entries: %w", ErrNotLorebook, err)
		}
	case len(entries) > 0 && entries[0] == '{':
		list, err := fromWorldInfo(entries)
		if err != nil {
			return nil, fmt.Errorf("%w: entries: %w", ErrNotLorebook, err)
		}
		if top["entries"], err = json.Marshal(list); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: no entries list", ErrNotLorebook)
	}
	var current string
	if json.Unmarshal(top["name"], &current) != nil || current == "" {
		top["name"], _ = json.Marshal(name)
	}
	return json.Marshal(top)
}

// fromWorldInfo converts world info entries, an object keyed by uid, to a
// character_book entries list, as SillyTavern does.
func fromWorldInfo(raw json.RawMessage) ([]map[string]any, error) {
	var byID map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &byID); err != nil {
		return nil, err
	}
	// Keys are decimal uids; ordering by length, then text, sorts them as numbers.
	ids := slices.SortedFunc(maps.Keys(byID), func(a, b string) int {
		return cmp.Or(cmp.Compare(len(a), len(b)), cmp.Compare(a, b))
	})
	entries := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		wi := byID[id]
		var ext map[string]json.RawMessage
		_ = json.Unmarshal(wi["extensions"], &ext)
		if ext == nil {
			ext = map[string]json.RawMessage{}
		}
		// SillyTavern keys may be /regex/, so use_regex is always true.
		e := map[string]any{"keys": []string{}, "content": "", "enabled": true, "insertion_order": 100,
			"use_regex": true, "extensions": ext}
		for k, v := range wi {
			switch k {
			case "extensions":
			case "uid":
				e["id"] = v
			case "key":
				e["keys"] = v
			case "keysecondary":
				e["secondary_keys"] = v
			case "order":
				e["insertion_order"] = v
			case "comment", "content", "constant", "selective":
				e[k] = v
			case "disable":
				e["enabled"] = string(v) != "true"
			case "position":
				e["position"] = "after_char"
				if string(v) == "0" {
					e["position"] = "before_char"
				}
				ext[k] = v
			default:
				ext[cmp.Or(stExtensionKeys[k], k)] = v
			}
		}
		entries = append(entries, e)
	}
	return entries, nil
}
```

- [ ] **Step 5: Generate and review the golden output**

Run: `go test ./card -run TestLorebookGolden -update`

Review `card/testdata/eldoria.lorebook.json.want.json`: `name` is `Eldoria`, four entries in uid order, each with `keys`, `content`, `enabled: true`, `insertion_order: 100`, `use_regex: true`, `position: "before_char"`, and `extensions` holding `display_index`, `exclude_recursion`, `addMemo`, and the rest.

- [ ] **Step 6: Run the tests**

Run: `go test ./card`
Expected: PASS, including `TestGolden`, which skips the lorebook.

- [ ] **Step 7: Commit**

```bash
git add card
git commit -m "Decode CCv3 lorebooks and SillyTavern world info"
```

### Task 4: The `password-login` plugin

**Files:**
- Create: `password-login/main.go`, `password-login/plugin.toml`, `password-login/README.md`
- Test: `password-login/main_test.go`
- Modify: `go.mod`, `go.sum`, `index.toml`, `.gitignore`

- [ ] **Step 1: Add bcrypt**

Run: `go get golang.org/x/crypto@v0.57.0`

- [ ] **Step 2: Write the failing tests**

Create `password-login/main_test.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
	"golang.org/x/crypto/bcrypt"
)

func completeParams(password string) json.RawMessage {
	b, _ := json.Marshal(protocol.AuthCompleteParams{Params: map[string]string{"password": password}})
	return b
}

func TestComplete(t *testing.T) {
	h, err := bcrypt.GenerateFromPassword([]byte("letmein"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	res, err := complete(string(h), completeParams("letmein"))
	if err != nil || res.(protocol.AuthCompleteResult).Subject != "owner" {
		t.Fatalf("complete = %v, %v", res, err)
	}
	for name, c := range map[string]struct{ hash, password string }{
		"wrong password": {string(h), "nope"},
		"no password":    {string(h), ""},
		"no hash":        {"", "letmein"},
		"not bcrypt":     {"letmein", "letmein"},
	} {
		if _, err := complete(c.hash, completeParams(c.password)); err == nil {
			t.Errorf("%s: logged in", name)
		}
	}
}

func TestHash(t *testing.T) {
	var out bytes.Buffer
	if err := printHash(strings.NewReader("letmein\n"), &out); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword(bytes.TrimSpace(out.Bytes()), []byte("letmein")); err != nil {
		t.Fatalf("hash %q doesn't match: %v", out.String(), err)
	}
	if err := printHash(strings.NewReader("\n"), io.Discard); err == nil {
		t.Fatal("hashed an empty password")
	}
}
```

- [ ] **Step 3: Run them to make sure they fail**

Run: `go test ./password-login`
Expected: FAIL to compile with `undefined: complete`.

- [ ] **Step 4: Write the plugin**

Create `password-login/main.go`:

```go
// Command password-login is a login provider that checks one password and
// reports the subject "owner". Run "password-login hash" to make the
// password_hash secret from a password on stdin.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
	"golang.org/x/crypto/bcrypt"
)

const version = "0.1.0"

// subject is who every login is. The owner sets it as the instance's owner_subject.
const subject = "owner"

// cost is the bcrypt cost for new hashes: about a quarter second each.
const cost = 12

const form = `{"type": "object", "properties": {"password": {"type": "string", "format": "password", "title": "Password"}}, "required": ["password"]}`

func main() {
	if len(os.Args) == 2 && os.Args[1] == "hash" {
		if err := printHash(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	// Set by initialize; requests run on other goroutines.
	var hash atomic.Value
	handler := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			var p protocol.InitializeParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
			}
			hash.Store(p.Secrets["password_hash"])
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "password-login", Version: version,
				Capabilities: []string{"auth"}}, nil
		case protocol.MethodShutdown:
			return nil, nil
		case protocol.MethodAuthBegin:
			return protocol.AuthBeginResult{Form: json.RawMessage(form)}, nil
		case protocol.MethodAuthComplete:
			h, _ := hash.Load().(string)
			return complete(h, params)
		}
		return nil, protocol.MethodNotFound(method)
	}
	conn := protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// complete checks the form's password against hash.
func complete(hash string, params json.RawMessage) (any, error) {
	var p protocol.AuthCompleteParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	if hash == "" {
		return nil, protocol.NewError(-32000, "the instance has no password_hash secret; make one with: password-login hash", false)
	}
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(p.Params["password"]))
	switch {
	case err == nil:
		return protocol.AuthCompleteResult{Subject: subject, Name: "Owner"}, nil
	case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
		return nil, protocol.NewError(-32000, "wrong password", false)
	}
	return nil, protocol.NewError(-32000, "password_hash is not a bcrypt hash: "+err.Error(), false)
}

// printHash reads a password line from r and writes its bcrypt hash to w.
func printHash(r io.Reader, w io.Writer) error {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	password := strings.TrimRight(line, "\r\n")
	if password == "" {
		return errors.New("no password on stdin")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(h))
	return err
}
```

Create `password-login/plugin.toml`:

```toml
name = "password-login"
version = "0.1.0"
protocol = 1
command = ["./password-login"]
capabilities = ["auth"]
secrets = ["password_hash"]
```

Create `password-login/README.md`:

````markdown
# password-login

Logs in with one password. Every login is the subject `owner`, so set the
instance's `owner_subject` to `owner`.

The `password_hash` secret is a bcrypt hash. Make one with the plugin, which
reads the password from stdin:

```bash
read -rs PW && printf '%s\n' "$PW" | ./password-login hash; unset PW
```
````

Append to `index.toml`:

```toml

[[plugin]]
name = "password-login"
path = "password-login"
version = "0.1.0"
protocol = 1
capabilities = ["auth"]
description = "Log in as the owner with one password."
```

Append to `.gitignore`:

```text
/password-login/password-login
```

- [ ] **Step 5: Run the tests and checks**

Run: `go vet ./... && go test ./password-login && go run ./internal/indexcheck && make check`
Expected: PASS; `make check` reports every check passing for `card-codec` and `password-login`.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum password-login index.toml .gitignore
git commit -m "Add the password-login plugin"
```

### Task 5: `card-import` uploads

**Files:**
- Create: `card-import/main.go`, `card-import/import.go`, `card-import/plugin.toml`
- Test: `card-import/import_test.go`
- Modify: `index.toml`, `.gitignore`

- [ ] **Step 1: Write the failing tests**

Create `card-import/import_test.go`:

```go
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// recorder keeps the notifications an import sends.
type recorder struct{ sent []any }

func (r *recorder) notify(_ string, params any) error {
	r.sent = append(r.sent, params)
	return nil
}

// items lists what was emitted, as "card <item>" or "lorebook <item>".
func (r *recorder) items() []string {
	var out []string
	for _, p := range r.sent {
		switch p := p.(type) {
		case protocol.EmitCard:
			out = append(out, "card "+p.Item)
		case protocol.EmitLorebook:
			out = append(out, "lorebook "+p.Item)
		}
	}
	return out
}

// startImport runs import.run with params and checkpoint, both JSON.
func startImport(t *testing.T, e env, params, checkpoint string) (*recorder, error) {
	t.Helper()
	raw, err := json.Marshal(protocol.ImportRunParams{Run: "1.1", Params: json.RawMessage(params),
		Checkpoint: json.RawMessage(checkpoint)})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	_, err = run(context.Background(), e, rec.notify, raw)
	return rec, err
}

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "card", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func isInvalid(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams
}

func TestImportUpload(t *testing.T) {
	lib, tmp := t.TempDir(), t.TempDir()
	put := func(b []byte) string {
		sum := sha256.Sum256(b)
		sha := hex.EncodeToString(sum[:])
		path := filepath.Join(lib, sha[:2], sha[2:4], sha)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return sha
	}
	e := env{library: lib, blobTmp: tmp}

	sha := put(testdata(t, "v3-seraphina.png"))
	rec, err := startImport(t, e, `{"blob": "`+sha+`", "name": "seraphina.png"}`, `null`)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.items(); !slices.Equal(got, []string{"card " + sha}) {
		t.Fatalf("emitted %q", got)
	}
	if avatar := rec.sent[0].(protocol.EmitCard).Avatar; filepath.Dir(avatar) != tmp {
		t.Fatalf("avatar %q is not in blob_tmp", avatar)
	}

	sha = put(testdata(t, "eldoria.lorebook.json"))
	rec, err = startImport(t, e, `{"blob": "`+sha+`", "name": "Eldoria.json"}`, `null`)
	if err != nil {
		t.Fatal(err)
	}
	if book := rec.sent[0].(protocol.EmitLorebook).Lorebook; !strings.Contains(string(book), `"name":"Eldoria"`) {
		t.Fatalf("lorebook not named from the upload: %.200s", book)
	}

	sha = put([]byte("not a card"))
	if _, err := startImport(t, e, `{"blob": "`+sha+`"}`, `null`); !isInvalid(err) {
		t.Fatalf("non-card upload: err = %v, want -32602", err)
	}
}

func TestImportRejects(t *testing.T) {
	dir := t.TempDir()
	missing := strings.Repeat("a", 64)
	for name, c := range map[string]struct {
		e      env
		params string
	}{
		"no folder configured":   {env{}, `{}`},
		"path leaves the folder": {env{folder: dir}, `{"path": "../etc"}`},
		"absolute path":          {env{folder: dir}, `{"path": "/etc"}`},
		"missing subfolder":      {env{folder: dir}, `{"path": "nope"}`},
		"path and blob":          {env{folder: dir, library: dir}, `{"path": "a", "blob": "` + missing + `"}`},
		"unknown param":          {env{folder: dir}, `{"folder": "/etc"}`},
		"blob not a sha256":      {env{library: dir}, `{"blob": "../../etc/passwd"}`},
		"missing upload":         {env{library: dir}, `{"blob": "` + missing + `"}`},
	} {
		if _, err := startImport(t, c.e, c.params, `null`); !isInvalid(err) {
			t.Errorf("%s: err = %v, want -32602", name, err)
		}
	}
}
```

The folder cases in `TestImportRejects` fail until Task 6, because the stub's error isn't `-32602`. This task's Step 4 runs only `TestImportUpload`.

- [ ] **Step 2: Run them to make sure they fail**

Run: `go test ./card-import`
Expected: FAIL to compile with `undefined: env`.

- [ ] **Step 3: Write the plugin**

Create `card-import/main.go`:

```go
// Command card-import is an import plugin. It imports character cards and
// lorebooks from a folder, or from one file uploaded to the core.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/inkandale/innkeeper/protocol"
)

const version = "0.1.0"

func main() {
	// Set by initialize; requests run on other goroutines.
	var cur atomic.Pointer[env]
	var conn *protocol.Conn
	handler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case protocol.MethodInitialize:
			var p protocol.InitializeParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			folder, _ := p.Config["folder"].(string)
			cur.Store(&env{folder: folder, blobTmp: p.BlobTmp, library: p.BlobStores["library"]})
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "card-import", Version: version,
				Capabilities: []string{"import"}}, nil
		case protocol.MethodShutdown:
			return nil, nil
		case protocol.MethodImportRun:
			e := cur.Load()
			if e == nil {
				return nil, errors.New("import.run before initialize")
			}
			return run(ctx, *e, conn.Notify, params)
		}
		return nil, protocol.MethodNotFound(method)
	}
	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}
```

Create `card-import/import.go`:

```go
package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
	"github.com/inkandale/innkeeper/protocol"
)

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// env is what initialize sends that imports need.
type env struct {
	folder  string // config.folder: the only folder imports may read
	blobTmp string
	library string // the library blob store, which holds uploads
}

// params are an import's settings: Path, a folder inside config.folder, or
// Blob, the sha256 of an uploaded file, with Name, its original file name.
type params struct {
	Path string `json:"path"`
	Blob string `json:"blob"`
	Name string `json:"name"`
}

// checkpoint is the last file a folder import finished.
type checkpoint struct {
	Last string `json:"last"`
}

// notifyFunc sends a notification to the core.
type notifyFunc func(method string, params any) error

// importer runs one import.run call.
type importer struct {
	env
	run    string
	notify notifyFunc
}

// run answers import.run.
func run(ctx context.Context, e env, notify notifyFunc, raw json.RawMessage) (any, error) {
	var p protocol.ImportRunParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, invalid(err)
	}
	var ps params
	if len(p.Params) > 0 && string(p.Params) != "null" {
		dec := json.NewDecoder(bytes.NewReader(p.Params))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ps); err != nil {
			return nil, invalid(fmt.Errorf("params: %w", err))
		}
	}
	var cp checkpoint
	if len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null" {
		if err := json.Unmarshal(p.Checkpoint, &cp); err != nil {
			return nil, invalid(fmt.Errorf("checkpoint: %w", err))
		}
	}
	im := &importer{env: e, run: p.Run, notify: notify}
	var err error
	switch {
	case ps.Blob != "" && ps.Path != "":
		err = invalid(errors.New("params: give path or blob, not both"))
	case ps.Blob != "":
		err = im.importUpload(ps.Blob, ps.Name)
	default:
		err = im.importFolder(ctx, path.Clean(ps.Path), cp.Last)
	}
	if err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// importUpload imports one uploaded file from the library blob store.
func (im *importer) importUpload(sha, name string) error {
	if !sha256Pattern.MatchString(sha) {
		return invalid(errors.New("params: blob must be a sha256 in lowercase hex"))
	}
	if im.library == "" {
		return errors.New("initialize sent no library blob store")
	}
	b, err := pluginio.ReadFile(filepath.Join(im.library, sha[:2], sha[2:4], sha))
	if errors.Is(err, fs.ErrNotExist) {
		return invalid(fmt.Errorf("no uploaded file %s; uploads are kept for 24 hours", sha))
	}
	if err != nil {
		return err
	}
	if err := im.emit(sha, name, b); errors.Is(err, card.ErrNotCard) {
		return invalid(fmt.Errorf("%s is not a card or lorebook: %w", cmp.Or(name, sha), err))
	} else if err != nil {
		return err
	}
	return im.notify(protocol.NotifyProgress, protocol.Progress{Run: im.run, Done: 1, Total: 1})
}

// importFolder is added in the next task.
func (im *importer) importFolder(ctx context.Context, dir, last string) error {
	return errors.New("folder imports aren't done yet")
}

// emit decodes one file and sends the card or lorebook it holds. A file that
// is neither gets an error wrapping card.ErrNotCard. name, the file's name,
// names a lorebook that has none.
func (im *importer) emit(item, name string, b []byte) error {
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	// Lorebooks first: one with a name and description would pass as a V1 card.
	if book, err := card.DecodeLorebook(b, stem); err == nil {
		return im.notify(protocol.NotifyEmitLorebook, protocol.EmitLorebook{Run: im.run, Item: item, Lorebook: book})
	}
	c, img, err := card.Decode(b)
	if err != nil {
		return err
	}
	ev := protocol.EmitCard{Run: im.run, Item: item, Card: c}
	if img != nil {
		if ev.Avatar, err = pluginio.WriteTmp(im.blobTmp, "avatar-*.png", img); err != nil {
			return err
		}
	}
	return im.notify(protocol.NotifyEmitCard, ev)
}
```

Create `card-import/plugin.toml`:

```toml
name = "card-import"
version = "0.1.0"
protocol = 1
command = ["./card-import"]
capabilities = ["import"]

[config]
type = "object"

[config.properties.folder]
type = "string"
title = "Folder"
description = "Absolute path of the folder imports may read, as the core sees it, such as /import. Leave empty to import only uploads."
```

Append to `index.toml`:

```toml

[[plugin]]
name = "card-import"
path = "card-import"
version = "0.1.0"
protocol = 1
capabilities = ["import"]
description = "Import character cards and lorebooks from a folder or an uploaded file."
```

Append to `.gitignore`:

```text
/card-import/card-import
```

- [ ] **Step 4: Run the upload test**

Run: `go vet ./card-import && go test ./card-import -run TestImportUpload`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add card-import index.toml .gitignore
git commit -m "Add the card-import plugin, importing one upload"
```

### Task 6: `card-import` folders

**Files:**
- Modify: `card-import/import.go`
- Test: `card-import/import_test.go`
- Create: `card-import/README.md`

- [ ] **Step 1: Write the failing test**

Append to `card-import/import_test.go`:

```go
// writeFolder makes a folder from name to content. Content starting with
// "testdata/" is that card/testdata file instead.
func writeFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		b := []byte(content)
		if rest, ok := strings.CutPrefix(content, "testdata/"); ok {
			b = testdata(t, rest)
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImportFolder(t *testing.T) {
	dir := writeFolder(t, map[string]string{
		"sub/seraphina.png": "testdata/v3-seraphina.png",
		"seraphina.json":    "testdata/v2-seraphina.json",
		"eldoria.json":      "testdata/eldoria.lorebook.json",
		"junk.json":         `{"theme": "dark"}`,
		"notes.txt":         "not a card",
	})
	outside := writeFolder(t, map[string]string{"secret.json": "testdata/v2-seraphina.json"})
	if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	e := env{folder: dir, blobTmp: tmp}

	rec, err := startImport(t, e, `{}`, `null`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lorebook eldoria.json", "card seraphina.json", "card sub/seraphina.png"}
	if got := rec.items(); !slices.Equal(got, want) {
		t.Fatalf("emitted %q, want %q", got, want)
	}
	if book := rec.sent[0].(protocol.EmitLorebook).Lorebook; !strings.Contains(string(book), `"name":"eldoria"`) {
		t.Errorf("lorebook not named from its file: %.200s", book)
	}
	var progress protocol.Progress
	var cp protocol.Checkpoint
	for _, p := range rec.sent {
		switch p := p.(type) {
		case protocol.Progress:
			progress = p
		case protocol.Checkpoint:
			cp = p
		case protocol.EmitCard:
			if strings.HasSuffix(p.Item, ".png") && filepath.Dir(p.Avatar) != tmp {
				t.Errorf("avatar %q is not in blob_tmp", p.Avatar)
			}
		}
	}
	if progress.Done != 4 || progress.Total != 4 || !strings.Contains(progress.Message, "1 file") {
		t.Errorf("last progress = %+v; want 4 of 4, one file skipped", progress)
	}
	if string(cp.Checkpoint) != `{"last":"sub/seraphina.png"}` {
		t.Errorf("last checkpoint = %s", cp.Checkpoint)
	}

	rec, err = startImport(t, e, `{}`, `{"last": "junk.json"}`)
	if want := []string{"card seraphina.json", "card sub/seraphina.png"}; err != nil || !slices.Equal(rec.items(), want) {
		t.Errorf("resumed import emitted %q, %v; want %q", rec.items(), err, want)
	}
	rec, err = startImport(t, e, `{"path": "sub"}`, `null`)
	if want := []string{"card sub/seraphina.png"}; err != nil || !slices.Equal(rec.items(), want) {
		t.Errorf("subfolder import emitted %q, %v; want %q", rec.items(), err, want)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./card-import`
Expected: FAIL: `TestImportFolder` with `folder imports aren't done yet`, and the folder cases of `TestImportRejects`.

- [ ] **Step 3: Write `importFolder`**

In `card-import/import.go`, add `"log"`, `"os"`, and `"slices"` to the imports, add after `sha256Pattern`:

```go
// batch is how many files a folder import reads between checkpoints. The
// core writes each checkpoint to the database.
const batch = 20
```

and replace the `importFolder` stub with:

```go
// importFolder imports every .png and .json file under dir, a slash path
// inside config.folder, resuming after the file last.
func (im *importer) importFolder(ctx context.Context, dir, last string) error {
	if im.folder == "" {
		return invalid(errors.New("this instance has no folder in its config; set one, or import an upload with params.blob"))
	}
	if !fs.ValidPath(dir) {
		return invalid(fmt.Errorf("params: path %q must be relative to the folder and stay inside it", dir))
	}
	root, err := os.OpenRoot(im.folder)
	if err != nil {
		return invalid(fmt.Errorf("config folder: %w", err))
	}
	defer root.Close()

	// Walk first, for a progress total and a fixed order to resume in.
	var files []string
	err = fs.WalkDir(root.FS(), dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ext := strings.ToLower(path.Ext(name))
		if d.Type().IsRegular() && (ext == ".png" || ext == ".json") {
			files = append(files, name)
		}
		return nil
	})
	if err != nil {
		return invalid(err)
	}
	// ponytail: if last is gone, the import starts over; the core skips items it has.
	start := slices.Index(files, last) + 1

	skipped := 0
	for i := start; i < len(files); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := files[i]
		b, err := readIn(root, name)
		if err == nil {
			if err = im.emit(name, name, b); err != nil && !errors.Is(err, card.ErrNotCard) {
				return err
			}
		}
		if err != nil {
			skipped++
			log.Printf("skipped %s: %v", name, err)
		}
		if n := i + 1; n%batch == 0 || n == len(files) {
			p := protocol.Progress{Run: im.run, Done: int64(n), Total: int64(len(files))}
			if skipped > 0 {
				p.Message = fmt.Sprintf("skipped %d file(s) that aren't cards or lorebooks, or can't be read", skipped)
			}
			if err := im.notify(protocol.NotifyProgress, p); err != nil {
				return err
			}
			cp, _ := json.Marshal(checkpoint{Last: name})
			if err := im.notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: im.run, Checkpoint: cp}); err != nil {
				return err
			}
		}
	}
	return nil
}

// readIn reads one file inside root.
func readIn(root *os.Root, name string) ([]byte, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return pluginio.ReadAll(f)
}
```

The test checks the message contains `1 file`, which `skipped 1 file(s)` does.

- [ ] **Step 4: Document the plugin**

Create `card-import/README.md`:

````markdown
# card-import

Imports CCv3, V2, and V1 cards as PNG or JSON, and lorebooks as CCv3
`character_book` JSON or SillyTavern world info.

Set `config.folder` to the folder imports may read. Import params are one of:

| Params | Imports |
|---|---|
| `{}` | Every `.png` and `.json` file under `config.folder` |
| `{"path": "fantasy"}` | Every such file under `config.folder/fantasy` |
| `{"blob": "<sha256>", "name": "brakka.png"}` | One file uploaded with `POST /blobs`, within 24 hours. `name` is optional and names a lorebook that has none |

A folder import skips files that aren't cards or lorebooks, and symlinks.
The job's progress says how many it skipped; the plugin log says which.
````

- [ ] **Step 5: Run the tests and checks**

Run: `go vet ./... && go test -race ./... && go run ./internal/indexcheck && make check`
Expected: PASS; `make check` passes for all three plugins.

- [ ] **Step 6: Commit**

```bash
git add card-import
git commit -m "Import cards and lorebooks from a folder"
```

### Task 7: Merge part 2

- [ ] **Step 1: Review and merge**

Run the code review the repo uses, fix what it finds, then merge `login-and-importer` into `main` and push. CI must pass on `main`.

- [ ] **Step 2: Note the commit**

Run: `git rev-parse HEAD` on `main`. Part 3 pins the image to it.

---

# Part 3: Plugins in the image (`innkeeper`)

### Task 8: Ship the plugins and document them

**Files:**
- Modify: `Dockerfile`, `compose.yaml`, `.gitignore`, `README.md`

- [ ] **Step 1: Pin the plugins**

In `Dockerfile`, set `ARG PLUGINS_REF=` to the commit from Task 7.

- [ ] **Step 2: Mount the import folder**

In `compose.yaml`, under `x-innkeeper` → `volumes`, after `- blobs:/data/blobs`:

```yaml
    # Folder imports read from here; see "Importing" in README.md.
    - ${INNKEEPER_IMPORT_DIR:-./import}:/import:ro
```

Append to `.gitignore`:

```text
/import/
```

- [ ] **Step 3: Document first login**

In `README.md`, replace the "First login" section's body with:

````markdown
Auth cannot be turned off. No one can log in until a login provider names
an owner, so start with an API token:

```bash
export INNKEEPER_TOKEN=$(innkeeper token create admin)
export INNKEEPER_URL=http://localhost:8080
```

The bundled `password-login` plugin logs in with one password, kept as a
bcrypt hash. Make the hash with the plugin, then create a login instance
with it:

```bash
read -rs PW && printf '%s\n' "$PW" | docker compose exec -T api /plugins/password-login/password-login hash; unset PW
innkeeper call POST /instances '{"plugin": "password-login", "name": "Login", "owner_subject": "owner", "secrets": {"password_hash": "<hash>"}}'
```

For another login provider, `owner_subject` is the identity it reports for
you; its README says what that is. Changing or clearing `owner_subject` logs
that identity out.
````

- [ ] **Step 4: Document importing**

In `README.md`, add before `## Sync`:

````markdown
## Importing

The bundled `card-import` plugin reads CCv3, V2, and V1 cards as PNG or
JSON, and lorebooks as CCv3 or SillyTavern world info JSON. Importing the
same file again adds nothing.

To import a folder, put it under `./import`, or set `INNKEEPER_IMPORT_DIR`
in `.env`. Compose mounts it read-only at `/import`:

```bash
innkeeper call POST /instances '{"plugin": "card-import", "name": "Import", "config": {"folder": "/import"}}'
innkeeper import <instance-id>                         # everything in /import
innkeeper import <instance-id> '{"path": "fantasy"}'   # only /import/fantasy
```

To import one file, upload it, then import the `sha256` from the reply
within 24 hours:

```bash
curl -sf -H "Authorization: Bearer $INNKEEPER_TOKEN" -H 'Content-Type: application/octet-stream' \
  --data-binary @brakka.png "$INNKEEPER_URL/blobs"
innkeeper import <instance-id> '{"blob": "<sha256>", "name": "brakka.png"}'
```

````

- [ ] **Step 5: Check it end to end**

Run the stack and follow the README exactly, as a reader would:

```bash
mkdir -p import && cp ~/git/innkeeper-plugins/card/testdata/{v3-seraphina.png,eldoria.lorebook.json} import/
docker compose build && docker compose up -d
```

Then, with `INNKEEPER_TOKEN` and `INNKEEPER_URL` set:

1. Hash a password and create the login instance as "First login" says.
2. Log in:
   ```bash
   curl -sf -c jar -X POST "$INNKEEPER_URL/auth/<login-id>/begin"
   curl -sf -b jar -c jar -X POST "$INNKEEPER_URL/auth/<login-id>/complete" \
     -H 'Content-Type: application/json' -d '{"params": {"password": "<password>"}}' -o /dev/null -w '%{http_code}\n'
   ```
   Expected: the first prints a form with a `password` field; the second prints `200`. A wrong password gets `401`.
3. Create the import instance and run `innkeeper import <id>`, then `innkeeper jobs watch <job-id>`.
   Expected: the job succeeds with `imported: 2`: the Seraphina card, whose embedded lorebook becomes a linked one, and the standalone Eldoria lorebook. Running it again gives `imported: 0, skipped: 2`.
4. Upload `card-codec`'s golden `v2-seraphina.png` and import its sha256 as "Importing" says.
   Expected: the job succeeds.

If `docker compose build` can't clone `innkeeper-plugins`, pass the key: `docker compose build --ssh default=$HOME/.ssh/id_rsa`.

- [ ] **Step 6: Commit and merge**

```bash
git add Dockerfile compose.yaml .gitignore README.md
git commit -m "Ship the login and import plugins, and document first login and importing"
```

Review, merge `login-and-importer` into `main`, and push. Move INN-11 to done.
