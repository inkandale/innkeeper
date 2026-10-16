# Codec Plugin and Plugin Repo CI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Read and write character cards as V2/V3 PNG and JSON through a `codec` plugin, export cards as PNG, and give `innkeeper-plugins` CI that checks its index and runs the contract kit against every plugin.

**Architecture:** The core gains the `codec.decode` and `codec.encode` methods, a `formats` manifest key, a contract kit check, and a `format` parameter on card export, which calls the oldest working codec instance that lists the format. In `innkeeper-plugins`, a shared Go package `card` does the format work, so the `card-codec` plugin here and the importer in plan 6b use the same code. The core image builds the plugins from a pinned `innkeeper-plugins` commit.

**Tech Stack:** Go 1.27, the core's `protocol` package, `github.com/BurntSushi/toml` v1.6.0, `golang.org/x/image` v0.46.0 (WebP avatars). No other new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-03-innkeeper-core-design.md`, sections "Plugin repository", "Methods", "Testing", and "v1 scope".

**Kaneo:** INN-6. Plan 6b (INN-11) adds the login and importer plugins; plan 6c (INN-12) adds the Lumiverse target, after INN-9.

---

## Scope

In this plan:

- Protocol: `codec.decode`, `codec.encode`, the manifest's `formats`, and a contract kit check that round-trips a card through every format.
- `GET /cards/{id}/export?format=…` and `innkeeper export <card-id> [file [format]]`.
- `innkeeper-plugins`: the `card` package (CCv3, V2, and V1 cards, as PNG and JSON), the `card-codec` plugin, golden files, an `index.toml` check, a Makefile, and CI that runs the contract kit at the core version `go.mod` pins.
- The core image ships the plugins.

Left out:

| Left out | Why | Where |
|---|---|---|
| The core calling `codec.decode` | Uploads go through the importer, which uses the `card` package directly | Plan 6b |
| Standalone lorebook files (SillyTavern world info JSON) | Needs the importer's design | Plan 6b |
| iTXt and zTXt card chunks | Every card writer we know of uses tEXt | When a card turns up that needs it |
| An export menu in the UI | | Plan 7 |

## Plan series

This is plan 6a. See plan 1 for the full list; plan 6 is now 6a, 6b, and 6c.

Constraints this plan leaves for later plans:

- **Plan 6b (importer):**
  - Read files with `card.Decode` from `github.com/inkandale/innkeeper-plugins/card`. It returns CCv3 JSON and, for a PNG, the image without its card chunks. Write that image to `blob_tmp` and send it as `emit.card`'s `avatar`.
  - `card.ErrNotCard` marks a file that isn't a card. Count it as skipped, not as an import failure.
  - Add the plugin to `index.toml`. Its `command` must be `./<folder>`, because `make build` writes the binary there and `internal/indexcheck` enforces it.
- **Plan 6c (Lumiverse target):** the same `index.toml` and `command` rules. CI reaches the private core repo with the `INNKEEPER_DEPLOY_KEY` secret this plan sets up.
- **Plan 7 (UI):** offer `json` plus every format a working codec instance lists. If `GET /plugins` doesn't already return each manifest's `formats`, add it there.
- **Every later plugin plan:** bumping the core in `innkeeper-plugins` is `go get -tool github.com/inkandale/innkeeper/cmd/innkeeper@<commit>`. That one pin sets both the `protocol` package and the contract kit CI runs. To ship a new plugin commit in the image, change `PLUGINS_REF` in the core `Dockerfile`.

## Design notes

- **Formats in the manifest.** The spec says codec formats are declared in the manifest, and unknown manifest keys are errors, so `formats` is a new key. A codec must list at least one, and only a codec may list any. `json` is reserved: the core exports CCv3 JSON itself.
- **Which codec runs.** The core calls plugins only through configured instances. Export uses the oldest instance that isn't broken whose plugin lists the format. With none, export returns `400` and says to create one. A codec has no settings, so creating one is a single call.
- **The codec's output is untrusted.** The core ingests the file the codec names with `Library.IngestBlob`, which checks the path is directly inside `blob_tmp` and moves the file before reading it. The blob is unreferenced, so the daily cleanup removes it after 24 hours. Only images, minus SVG, and JSON keep their own `Content-Type`; anything else is `application/octet-stream`, and every export is an attachment.
- **Lossless round trips.** `card.Decode` returns a CCv3 card byte-for-byte unchanged, and the PNG image with only the card chunks removed. `card.Encode` writes into that image without re-encoding its pixels. V2 and V1 cards convert to CCv3 by moving their data under the V3 spec and adding `group_only_greetings: []` if it's missing, which CCv3 requires.
- **What a PNG holds.** Encode writes a `chara` chunk with a V2 card for older apps and a `ccv3` chunk with the CCv3 card, as SillyTavern does. Decode prefers `ccv3`. Some writers put a V3 card in `chara`, so decode accepts any spec in either chunk.
- **Plugins import the core's `protocol` package.** Both repos are private, so `innkeeper-plugins` CI fetches the core with a read-only deploy key on the core repo, stored as a secret. That follows the "deploy key over a PAT" preference.
- **The image builds plugins against its own core.** The `plugins` stage replaces the core module with the source being built, so the protocol package in each plugin always matches the core it ships with. It clones `innkeeper-plugins` over SSH, so `docker compose build` needs an SSH agent that can read that repo.

### Changes during the build

Reviews changed the code after these tasks were written. Where a task's code differs from the repos, the repos are right. The main changes:

- The contract kit is as strict as the core: it passes an avatar on encode, requires a plain file directly inside `blob_tmp`, and requires `-32602` for a non-card. Its `blob_tmp` is `<root>/tmp`, with `<root>` declared as a blob store.
- Exports carry `nosniff` and `Content-Security-Policy: sandbox`, take their type from the sniffed content, and are described in the OpenAPI spec as JSON or `application/octet-stream`. `GET /plugins` lists each plugin's `formats`.
- `card` caps avatars at 4096×4096 pixels and requires a V1 card to have `name` plus a content field. The golden cards are SillyTavern's Seraphina sample, files derived from it, and a V3 template; `card/testdata/README.md` says which is which.
- The deploy key secret is also set for Dependabot (`gh secret set --app dependabot`), whose runs can't read Actions secrets. Dependabot covers Actions only, since it can't read the private core.
- The `plugins` stage doesn't run `go mod tidy`. A key that isn't in the SSH agent can be passed with `docker build --ssh default=$HOME/.ssh/<key>`.

## File structure

`innkeeper`:

| File | Responsibility |
|---|---|
| `protocol/manifest.go` | `formats` key and its validation |
| `protocol/codec.go` | `codec.*` method names, params, results, `ValidExtension` |
| `internal/fakeplugin/codec.go` | Fake codec with one format, `fake` |
| `internal/testutil/fakeplugin.go` | Fake manifests list `formats` when they declare `codec` |
| `internal/contract/contract.go` | Codec checks |
| `internal/plugins/plugins.go` | `CodecInstance`, `ErrNoCodec` |
| `internal/api/export.go` | Encoding an export with a codec |
| `internal/api/entities.go` | `format` on the export route |
| `cmd/innkeeper/commands.go` | `export` takes a format |
| `docs/protocol.md`, `README.md` | Codec methods, export formats, plugins in the image |
| `Dockerfile`, `compose.yaml` | Build the plugins into the image |

`innkeeper-plugins`:

| File | Responsibility |
|---|---|
| `go.mod` | Module `github.com/inkandale/innkeeper-plugins`; pins the core as a tool |
| `card/png.go` | PNG chunk reading and writing |
| `card/card.go` | `Decode`, `Encode`, version conversion |
| `card/testdata/` | Golden cards and their expected CCv3 output |
| `card-codec/plugin.toml`, `card-codec/main.go` | The codec plugin |
| `internal/indexcheck/main.go` | Checks `index.toml` against the plugin folders |
| `index.toml` | The `card-codec` entry |
| `Makefile` | `build`, `check`, `install` |
| `.github/workflows/ci.yml`, `.github/dependabot.yml` | CI and Actions updates |
| `README.md` | Building, checking, and adding a plugin |

## Running tests

Core database tests need PostgreSQL with pgvector, as in the README:

```bash
docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

`innkeeper-plugins` tests need no database. Both repos are private, so Go needs:

```bash
go env -w GOPRIVATE=github.com/davidgibbons/*
git ls-remote https://github.com/inkandale/innkeeper HEAD   # must print a commit; if not, run: gh auth setup-git
```

Part 1 runs in `~/git/innkeeper` on a branch named `codec`. Part 2 runs in `~/git/innkeeper-plugins` on a branch named `card-codec`, after part 1 is merged and pushed. Part 3 runs in `~/git/innkeeper` after part 2 is merged and pushed.

---

# Part 1: Core codec support (`innkeeper`)

### Task 1: The `formats` manifest key

**Files:**
- Modify: `protocol/manifest.go`
- Test: `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing test**

Append to `protocol/manifest_test.go`:

```go
func TestFormats(t *testing.T) {
	manifest := func(caps []string, formats ...string) *Manifest {
		return &Manifest{Name: "c", Version: "1", Protocol: Version, Command: []string{"c"}, Capabilities: caps, Formats: formats}
	}
	if err := manifest([]string{"codec"}, "png", "json-v2").Validate(); err != nil {
		t.Fatal(err)
	}
	bad := map[string]*Manifest{
		"codec without formats": manifest([]string{"codec"}),
		"formats without codec": manifest([]string{"push"}, "png"),
		"uppercase":             manifest([]string{"codec"}, "PNG"),
		"json is the core's":    manifest([]string{"codec"}, "json"),
		"listed twice":          manifest([]string{"codec"}, "png", "png"),
	}
	for name, m := range bad {
		if m.Validate() == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./protocol -run TestFormats`
Expected: FAIL to compile with `unknown field Formats in struct literal`.

- [ ] **Step 3: Add the key**

In `protocol/manifest.go`, after `keyPattern`:

```go
// Codec formats, such as png or json-v2.
var formatPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
```

In `Manifest`, after `Enrichments`:

```go
	// Formats are what a codec plugin's codec.encode writes.
	Formats []string `toml:"formats"`
```

In `Validate`, before `return errors.Join(errs...)`:

```go
	hasCodec := slices.Contains(m.Capabilities, "codec")
	if hasCodec && len(m.Formats) == 0 {
		errs = append(errs, errors.New("a codec plugin must list its formats"))
	}
	if !hasCodec && len(m.Formats) > 0 {
		errs = append(errs, errors.New("formats needs the codec capability"))
	}
	seenFormats := map[string]bool{}
	for _, f := range m.Formats {
		if !formatPattern.MatchString(f) {
			errs = append(errs, fmt.Errorf("format %q must match %s", f, formatPattern))
		}
		if f == "json" {
			errs = append(errs, errors.New(`format "json" is the core's own CCv3 export`))
		}
		if seenFormats[f] {
			errs = append(errs, fmt.Errorf("format %q listed twice", f))
		}
		seenFormats[f] = true
	}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./protocol`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/manifest.go protocol/manifest_test.go
git commit -m "Add the formats manifest key for codec plugins"
```

### Task 2: Codec messages and a fake codec

**Files:**
- Create: `protocol/codec.go`
- Create: `internal/fakeplugin/codec.go`
- Modify: `internal/fakeplugin/main.go`
- Modify: `internal/testutil/fakeplugin.go`

Task 3's contract tests exercise this code.

- [ ] **Step 1: Add the messages**

Create `protocol/codec.go`:

```go
package protocol

import (
	"encoding/json"
	"regexp"
)

// The codec capability reads and writes card files. A codec lists the
// formats codec.encode writes in its manifest.
const (
	MethodCodecDecode = "codec.decode"
	MethodCodecEncode = "codec.encode"
)

// CodecDecodeParams names the file to read. Path is absolute; the plugin must
// not move or change the file.
type CodecDecodeParams struct {
	Path string `json:"path"`
}

// CodecDecodeResult is the card as CCv3, and its image: a file in blob_tmp,
// or empty.
type CodecDecodeResult struct {
	Card   json.RawMessage `json:"card"`
	Avatar string          `json:"avatar,omitempty"`
}

// CodecEncodeParams asks for a card file in Format, one of the manifest's
// formats. Avatar is the absolute path of the avatar file in a blob store, or
// empty.
type CodecEncodeParams struct {
	Card   json.RawMessage `json:"card"`
	Format string          `json:"format"`
	Avatar string          `json:"avatar,omitempty"`
}

// CodecEncodeResult names the file the plugin wrote directly inside
// blob_tmp, and its file name extension without the dot.
type CodecEncodeResult struct {
	Path      string `json:"path"`
	Extension string `json:"extension"`
}

var extensionPattern = regexp.MustCompile(`^[a-z0-9]{1,10}$`)

// ValidExtension reports whether ext is an extension a codec may return.
func ValidExtension(ext string) bool { return extensionPattern.MatchString(ext) }
```

- [ ] **Step 2: Add the fake codec**

Create `internal/fakeplugin/codec.go`:

```go
package main

import (
	"bytes"
	"encoding/json"
	"os"

	"github.com/inkandale/innkeeper/protocol"
)

// The fake codec's one format, "fake", is "fake:" followed by the card JSON.
// bad makes it write outside blob_tmp and accept any file as a card.
func codecEncode(blobTmp string, params json.RawMessage, bad bool) (any, error) {
	var p protocol.CodecEncodeParams
	if err := json.Unmarshal(params, &p); err != nil || p.Format != "fake" {
		return nil, protocol.NewError(protocol.CodeInvalidParams, "want format fake", false)
	}
	var c struct {
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	_ = json.Unmarshal(p.Card, &c)
	dir := blobTmp
	// A card named Escape tests that the core refuses paths outside blob_tmp.
	if bad || c.Data.Name == "Escape" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "fake-*.txt")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(append([]byte("fake:"), p.Card...)); err != nil {
		return nil, err
	}
	return protocol.CodecEncodeResult{Path: f.Name(), Extension: "txt"}, nil
}

func codecDecode(params json.RawMessage, bad bool) (any, error) {
	var p protocol.CodecDecodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	b, err := os.ReadFile(p.Path)
	if err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
	}
	card, ok := bytes.CutPrefix(b, []byte("fake:"))
	if !ok {
		if !bad {
			return nil, protocol.NewError(protocol.CodeInvalidParams, "not a fake card", false)
		}
		card = []byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Anything"}}`)
	}
	return protocol.CodecDecodeResult{Card: card}, nil
}
```

In `internal/fakeplugin/main.go`, add `bad-codec` to the `-mode` usage string:

```go
	mode := flag.String("mode", "", "bad-protocol | wrong-caps | wrong-version | slow-init | bad-errors | silent | crash-unknown | no-shutdown | bad-describe | bad-codec")
```

and add these cases after `case protocol.MethodTargetGet:`:

```go
		case protocol.MethodCodecEncode:
			return codecEncode(blobTmp, params, *mode == "bad-codec")
		case protocol.MethodCodecDecode:
			return codecDecode(params, *mode == "bad-codec")
```

- [ ] **Step 3: List formats in fake manifests**

In `internal/testutil/fakeplugin.go`, add `"slices"` to the imports, and replace the `if err := os.WriteFile(...)` block at the end of `FakePluginWith` with:

```go
	if slices.Contains(caps, "codec") {
		manifest += "formats = [\"fake\"]\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
```

- [ ] **Step 4: Build and run the existing tests**

Run: `go vet ./... && go test ./protocol ./internal/contract ./internal/pluginhost`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add protocol/codec.go internal/fakeplugin internal/testutil/fakeplugin.go
git commit -m "Add the codec.decode and codec.encode messages and a fake codec"
```

### Task 3: Contract kit codec checks

**Files:**
- Modify: `internal/contract/contract.go`
- Test: `internal/contract/contract_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/contract/contract_test.go`:

```go
func TestCodecChecks(t *testing.T) {
	results, err := Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 {
		t.Fatalf("ran %d checks, want 6", len(results))
	}
	if f := failed(results); len(f) > 0 {
		t.Fatalf("checks failed: %v", f)
	}

	results, err = Check(context.Background(), testutil.FakePluginWith(t, []string{"codec"}, "-mode=bad-codec"), quiet)
	if err != nil {
		t.Fatal(err)
	}
	f := failed(results)
	if len(f) != 2 || f[checkRoundTripName] == nil || f[checkRejectsName] == nil {
		t.Fatalf("failed = %v, want only the two codec checks", f)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/contract -run TestCodecChecks`
Expected: FAIL to compile with `undefined: checkRoundTripName`.

- [ ] **Step 3: Add the checks**

In `internal/contract/contract.go`, add `"os"` and `"path/filepath"` to the imports.

In `Check`, replace the `params := ...` line with:

```go
	// A codec writes its files here.
	tmp, err := os.MkdirTemp("", "innkeeper-check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	params := protocol.InitializeParams{InstanceID: "contract-check", Config: map[string]any{}, Secrets: secrets, BlobTmp: tmp}
```

After the `if slices.Contains(m.Capabilities, "push") { ... }` block, add:

```go
	if slices.Contains(m.Capabilities, "codec") {
		checks := []struct {
			name string
			run  func(context.Context) error
		}{
			{checkRoundTripName, func(ctx context.Context) error { return checkRoundTrip(ctx, p, m, tmp) }},
			{checkRejectsName, func(ctx context.Context) error { return checkRejects(ctx, p, tmp) }},
		}
		for _, c := range checks {
			callCtx, cancel = context.WithTimeout(ctx, callTimeout)
			err := c.run(callCtx)
			cancel()
			results = append(results, Result{c.name, callErr(err)})
			if errors.Is(err, protocol.ErrClosed) || !p.Alive() {
				return exited(results, p), nil
			}
		}
	}
```

Replace `checkDescribe`'s first error handling, from `err := p.Call(...)` to the `if err != nil { return err }` after it, with:

```go
	if err := callErr(p.Call(ctx, protocol.MethodTargetDescribe, nil, &info)); err != nil {
		return err
	}
```

Add after `checkDescribe`:

```go
// callErr explains a call that timed out or found the plugin gone.
func callErr(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("no response within %s", callTimeout)
	case errors.Is(err, protocol.ErrClosed):
		return fmt.Errorf("plugin exited: %w", err)
	}
	return err
}

const (
	checkRoundTripName = "codec round-trips a card in every format"
	checkRejectsName   = "codec.decode rejects a file that isn't a card"
)

var sampleCard = json.RawMessage(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Contract Check", "description": "A card for the codec check.", "first_mes": "Hello."}}`)

// checkRoundTrip encodes the sample card in each format and decodes it back.
func checkRoundTrip(ctx context.Context, p *pluginhost.Process, m *protocol.Manifest, tmp string) error {
	for _, format := range m.Formats {
		var enc protocol.CodecEncodeResult
		if err := p.Call(ctx, protocol.MethodCodecEncode, protocol.CodecEncodeParams{Card: sampleCard, Format: format}, &enc); err != nil {
			return fmt.Errorf("encode %s: %w", format, err)
		}
		if filepath.Dir(enc.Path) != tmp {
			return fmt.Errorf("encode %s: path %q is not directly inside blob_tmp", format, enc.Path)
		}
		if !protocol.ValidExtension(enc.Extension) {
			return fmt.Errorf("encode %s: extension %q must be 1 to 10 lowercase letters and digits", format, enc.Extension)
		}
		var dec protocol.CodecDecodeResult
		if err := p.Call(ctx, protocol.MethodCodecDecode, protocol.CodecDecodeParams{Path: enc.Path}, &dec); err != nil {
			return fmt.Errorf("decode %s: %w", format, err)
		}
		var c struct {
			Spec string `json:"spec"`
			Data struct {
				Name string `json:"name"`
			} `json:"data"`
		}
		if err := json.Unmarshal(dec.Card, &c); err != nil || c.Spec != "chara_card_v3" || c.Data.Name != "Contract Check" {
			return fmt.Errorf("decode %s: got %s, want a chara_card_v3 card named Contract Check", format, dec.Card)
		}
		if dec.Avatar != "" && filepath.Dir(dec.Avatar) != tmp {
			return fmt.Errorf("decode %s: avatar %q is not directly inside blob_tmp", format, dec.Avatar)
		}
	}
	return nil
}

// checkRejects expects an error reply, not a card, for a text file.
func checkRejects(ctx context.Context, p *pluginhost.Process, tmp string) error {
	path := filepath.Join(tmp, "not-a-card.txt")
	if err := os.WriteFile(path, []byte("This is not a character card.\n"), 0o644); err != nil {
		return err
	}
	err := p.Call(ctx, protocol.MethodCodecDecode, protocol.CodecDecodeParams{Path: path}, nil)
	var rpcErr *protocol.Error
	switch {
	case err == nil:
		return errors.New("expected an error, got a card")
	case errors.As(err, &rpcErr):
		return nil
	}
	return err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test -race ./internal/contract`
Expected: PASS, including `TestWellBehavedPluginPasses` still counting 5 checks.

- [ ] **Step 5: Commit**

```bash
git add internal/contract
git commit -m "Check codec plugins in the contract kit"
```

### Task 4: Export in a codec's format

**Files:**
- Modify: `internal/plugins/plugins.go`
- Create: `internal/api/export.go`
- Modify: `internal/api/entities.go:119-122`, `internal/api/entities.go:324-336`
- Modify: `internal/api/server.go` (`fail`)
- Modify: `internal/api/api_test.go` (`newEnv`)
- Test: `internal/api/entities_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/api/api_test.go`'s `newEnv`, give the fake plugin the codec capability:

```go
	fake, err := protocol.LoadManifest(testutil.FakePluginWith(t, []string{"auth", "import", "push", "pull", "codec"}))
```

Append to `internal/api/entities_test.go`:

```go
func TestExportFormats(t *testing.T) {
	e := newEnv(t)
	brakka := e.createCard(`{"name": "Brakka"}`)
	path := fmt.Sprintf("/cards/%d/export", brakka.ID)

	// newEnv's fake instances are codecs that write the format "fake".
	r := e.call("GET", path+"?format=fake", nil, 200, nil)
	if !strings.HasPrefix(string(r.Body), "fake:") || !strings.Contains(string(r.Body), `"Brakka"`) ||
		!strings.Contains(r.Header.Get("Content-Disposition"), "brakka.txt") ||
		r.Header.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("fake export: %s %v", r, r.Header)
	}
	r = e.call("GET", path, nil, 200, nil)
	if r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("json export Content-Type = %q", r.Header.Get("Content-Type"))
	}
	e.call("GET", path+"?format=nope", nil, 400, nil)

	// The fake writes a card named Escape outside blob_tmp.
	escape := e.createCard(`{"name": "Escape"}`)
	e.call("GET", fmt.Sprintf("/cards/%d/export?format=fake", escape.ID), nil, 502, nil)

	e.markBroken(e.loginID, "boom")
	e.markBroken(e.importID, "boom")
	e.call("GET", path+"?format=fake", nil, 400, nil)
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/api -run TestExportFormats`
Expected: FAIL: the `format=fake` export returns CCv3 JSON with `brakka.json`, because the route ignores `format`.

- [ ] **Step 3: Find a codec instance**

In `internal/plugins/plugins.go`, add to the `var ( ... )` error block:

```go
	// ErrNoCodec means no working codec instance writes a format.
	ErrNoCodec = errors.New("no codec plugin instance writes this format")
```

Add after `Instance`:

```go
// CodecInstance returns the oldest working instance of a codec plugin that
// lists format.
func (r *Registry) CodecInstance(ctx context.Context, format string) (pluginhost.Instance, error) {
	type row struct {
		ID     int64
		Plugin string
	}
	rows, err := r.pool.Query(ctx, `SELECT id, plugin FROM plugin_instance WHERE broken_error IS NULL ORDER BY id`)
	if err != nil {
		return pluginhost.Instance{}, err
	}
	all, err := pgx.CollectRows(rows, pgx.RowToStructByPos[row])
	if err != nil {
		return pluginhost.Instance{}, err
	}
	for _, i := range all {
		if m, ok := r.manifests[i.Plugin]; ok && slices.Contains(m.Capabilities, "codec") && slices.Contains(m.Formats, format) {
			return r.Instance(ctx, i.ID)
		}
	}
	return pluginhost.Instance{}, fmt.Errorf("%w: %q; create an instance of a codec plugin that lists it", ErrNoCodec, format)
}
```

- [ ] **Step 4: Encode with it**

Create `internal/api/export.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/inkandale/innkeeper/internal/blobstore"
	"github.com/inkandale/innkeeper/protocol"
)

// errBadCodec means a codec plugin's reply broke the protocol.
var errBadCodec = errors.New("codec plugin sent a bad reply")

// encode writes a card in format with the oldest working codec instance that
// lists it. revID is the revision whose avatar goes in the file.
func (s *Server) encode(ctx context.Context, revID int64, card json.RawMessage, format string) ([]byte, string, error) {
	inst, err := s.Registry.CodecInstance(ctx, format)
	if err != nil {
		return nil, "", err
	}
	rev, err := s.Lib.Cards.Revision(ctx, revID)
	if err != nil {
		return nil, "", err
	}
	params := protocol.CodecEncodeParams{Card: card, Format: format}
	if rev.AvatarBlob != nil {
		if params.Avatar, err = s.Lib.BlobPath(*rev.AvatarBlob); err != nil {
			return nil, "", err
		}
	}
	var res protocol.CodecEncodeResult
	if err := s.Registry.Call(ctx, s.Plugins, inst, protocol.MethodCodecEncode, params, &res); err != nil {
		return nil, "", err
	}
	if !protocol.ValidExtension(res.Extension) {
		return nil, "", fmt.Errorf("%w: extension %q", errBadCodec, res.Extension)
	}
	// IngestBlob refuses a path outside blob_tmp. Nothing references the
	// blob, so cleanup removes it after a day.
	b, err := s.Lib.IngestBlob(ctx, res.Path)
	if errors.Is(err, blobstore.ErrInvalid) {
		return nil, "", fmt.Errorf("%w: %w", errBadCodec, err)
	}
	if err != nil {
		return nil, "", err
	}
	if b.Size > maxBlobBytes {
		return nil, "", fmt.Errorf("%w: file is over %d MiB", errBadCodec, maxBlobBytes>>20)
	}
	_, f, err := s.Lib.OpenBlob(ctx, b.SHA256)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()
	body, err := io.ReadAll(f)
	return body, res.Extension, err
}

// exportType is an export's Content-Type. Only JSON and images other than
// SVG keep their type, so an export can't run as a page on this site.
func exportType(ext string) string {
	t := mime.TypeByExtension("." + ext)
	if ext == "json" || strings.HasPrefix(t, "image/") && ext != "svg" {
		return t
	}
	return "application/octet-stream"
}
```

In `internal/api/entities.go`, replace `exportOut` with:

```go
type exportIn struct {
	ID     int64  `path:"id"`
	Format string `query:"format" default:"json" doc:"json for CCv3 JSON, or a format a codec plugin instance writes, such as png"`
}

type exportOut struct {
	ContentDisposition string `header:"Content-Disposition"`
	ContentType        string `header:"Content-Type"`
	Body               []byte
}
```

Replace the `export-card` registration with:

```go
	huma.Register(api, huma.Operation{OperationID: "export-card", Method: http.MethodGet, Path: "/cards/{id}/export",
		Summary: "Export a card", Description: "Linked lorebooks are embedded as data.character_book. Formats other than json need a codec plugin instance that writes them.", Tags: tags},
		func(ctx context.Context, in *exportIn) (*exportOut, error) {
			c, err := s.Lib.Cards.Get(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			raw, err := s.Lib.ExportCard(ctx, in.ID)
			if err != nil {
				return nil, s.fail(err)
			}
			body, ext := []byte(raw), "json"
			if in.Format != "json" {
				if body, ext, err = s.encode(ctx, c.HeadRevisionID, raw, in.Format); err != nil {
					return nil, s.fail(err)
				}
			}
			return &exportOut{ContentDisposition: fmt.Sprintf(`attachment; filename="%s.%s"`, c.Slug, ext),
				ContentType: exportType(ext), Body: body}, nil
		})
```

In `internal/api/server.go`'s `fail`, add `errors.Is(err, plugins.ErrNoCodec)` to the `400` case, and `errors.Is(err, errBadCodec)` to the `502` case with `rpcErr`:

```go
	case errors.Is(err, library.ErrInvalid), errors.Is(err, plugins.ErrInvalid), errors.Is(err, plugins.ErrUnknownPlugin),
		errors.Is(err, targets.ErrCannot), errors.Is(err, plugins.ErrNoCodec):
		return huma.Error400BadRequest(err.Error())
```

```go
	case errors.As(err, &rpcErr), errors.Is(err, targets.ErrBadReply), errors.Is(err, errBadCodec):
		return huma.Error502BadGateway("plugin: " + err.Error())
```

- [ ] **Step 5: Run the tests**

Run: `go test -race ./internal/api ./internal/plugins`
Expected: PASS. If a test compares the fake plugin's capability list, add `"codec"` to its expected value.

- [ ] **Step 6: Commit**

```bash
git add internal/plugins/plugins.go internal/api
git commit -m "Export cards in any format a codec plugin instance writes"
```

### Task 5: `export` takes a format

**Files:**
- Modify: `cmd/innkeeper/commands.go` (`export`)
- Modify: `cmd/innkeeper/main.go` (`usage`)

- [ ] **Step 1: Pass the format**

Replace `export` in `cmd/innkeeper/commands.go` with the following, and add `"net/url"` and `"path/filepath"` to its imports:

```go
// export runs `innkeeper export <card-id> [file [format]]`. A .png file
// defaults to png, anything else to json.
func export(args []string) error {
	if len(args) < 1 || len(args) > 3 {
		return errors.New(usage)
	}
	id, err := parseID(args[0])
	if err != nil {
		return err
	}
	format := "json"
	switch {
	case len(args) == 3:
		format = args[2]
	case len(args) == 2 && strings.EqualFold(filepath.Ext(args[1]), ".png"):
		format = "png"
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	out, err := c.do(context.Background(), "GET", "/cards/"+id+"/export?format="+url.QueryEscape(format), nil)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		_, err = os.Stdout.Write(out)
		return err
	}
	return os.WriteFile(args[1], out, 0o644)
}
```

In `cmd/innkeeper/main.go`'s `usage`, change the export line to:

```text
  innkeeper export <card-id> [file [format]]
```

- [ ] **Step 2: Build and test**

Run: `go vet ./... && go test ./cmd/innkeeper`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add cmd/innkeeper
git commit -m "Let the export command choose a format"
```

### Task 6: Document codecs and export formats

**Files:**
- Modify: `docs/protocol.md`
- Modify: `README.md`

Use the `documentation-writing` skill for both files.

- [ ] **Step 1: Protocol reference**

In `docs/protocol.md`'s manifest table, add after the `enrichments` row:

```markdown
| `formats` | for `codec` | What `codec.encode` writes: lowercase words of letters and digits joined by single hyphens, such as `png`. A codec plugin must list at least one. `json` is taken: the core exports CCv3 JSON itself |
```

Add before `## Checking a plugin`:

````markdown
### `codec`

A codec reads and writes card files in the formats its manifest lists.

**`codec.decode`** reads one card file.

| Param | Meaning |
|---|---|
| `path` | Absolute path of the file. Read it; don't move or change it |

Reply with `card`, a CCv3 card, and optionally `avatar`, the card's image as a file you wrote to `blob_tmp`. Reply with error `-32602` if the file isn't a card you can read.

**`codec.encode`** writes one card file.

| Param | Meaning |
|---|---|
| `card` | The CCv3 card, with linked lorebooks embedded as `data.character_book` |
| `format` | One of your manifest's `formats` |
| `avatar` | Absolute path to the avatar image in a blob store, or absent. Read it; don't move or change it |

Reply with `path`, the file you wrote directly inside `blob_tmp`, and `extension`, its file name extension without the dot: 1 to 10 lowercase letters and digits. The core moves the file, so don't reuse the path.

```json
{"jsonrpc": "2.0", "id": 5, "method": "codec.encode", "params": {"card": {"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}, "format": "png"}}
{"jsonrpc": "2.0", "id": 5, "result": {"path": "/blobs/tmp/card-1234.png", "extension": "png"}}
```
````

In `## Checking a plugin`, add to the list after the `target.describe` line:

```markdown
- a `codec` plugin encodes a sample card in each of its `formats` and decodes it back, and returns an error when asked to decode a file that isn't a card
```

- [ ] **Step 2: README**

In `README.md`'s CLI block, replace the export line with:

```text
innkeeper export <card-id> [file [format]]      a card as CCv3 JSON, or in a codec's format; a .png file means png
```

After the `push --new-copy` paragraph, add:

````markdown
Exporting in a format other than `json` needs an instance of a codec plugin
that writes it. The first one is enough:

```bash
innkeeper call POST /instances '{"plugin": "card-codec", "name": "Codec"}'
innkeeper export 12 brakka.png
```
````

- [ ] **Step 3: Commit**

```bash
git add docs/protocol.md README.md
git commit -m "Document codec plugins and export formats"
```

### Task 7: Merge part 1

- [ ] **Step 1: Run everything**

Run: `go vet ./... && go test -race ./...`
Expected: every package `ok`.

- [ ] **Step 2: Merge and push**

Merge `codec` into `main` and push. Wait for the `ci` workflow on `main` to pass: `gh run list --branch main --limit 1`. Note the merge commit's full hash for Task 8.

---

# Part 2: Plugin repo (`innkeeper-plugins`)

### Task 8: Module and CI access to the core

**Files:**
- Create: `go.mod`, `go.sum`, `.gitignore`

- [ ] **Step 1: Give CI read access to the core**

This adds a deploy key to the core repo and a secret to the plugin repo. Ask the owner before running it.

```bash
key=$(mktemp -d)/innkeeper-plugins-ci
ssh-keygen -t ed25519 -N '' -C innkeeper-plugins-ci -f "$key"
gh repo deploy-key add "$key.pub" --repo inkandale/innkeeper --title "innkeeper-plugins CI (read-only)"
gh secret set INNKEEPER_DEPLOY_KEY --repo inkandale/innkeeper-plugins < "$key"
rm -r "$(dirname "$key")"
```

`gh repo deploy-key add` makes a read-only key unless given `--allow-write`.

- [ ] **Step 2: Start the module**

```bash
cd ~/git/innkeeper-plugins
go mod init github.com/inkandale/innkeeper-plugins
go mod edit -go=1.27
go get -tool github.com/inkandale/innkeeper/cmd/innkeeper@<Task 7 commit>
go get golang.org/x/image@v0.46.0 github.com/BurntSushi/toml@v1.6.0
```

Create `.gitignore`:

```text
# Plugin binaries, built by make build into each plugin's folder.
/card-codec/card-codec
```

- [ ] **Step 3: Check the pinned contract kit runs**

Run: `go tool innkeeper plugin check . ; echo exit=$?`
Expected: an error that `plugin.toml` is missing, and `exit=1`. That shows the pinned core builds and runs.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum .gitignore
git commit -m "Start the Go module and pin the core as a tool"
```

### Task 9: PNG chunks

**Files:**
- Create: `card/png.go`
- Test: `card/png_test.go`

- [ ] **Step 1: Write the failing test**

Create `card/png_test.go`:

```go
package card

import (
	"bytes"
	"testing"
)

func TestChunksRoundTrip(t *testing.T) {
	img, err := placeholder()
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := readChunks(img)
	if err != nil {
		t.Fatal(err)
	}
	if chunks[0].typ != "IHDR" || chunks[len(chunks)-1].typ != "IEND" {
		t.Fatalf("chunks = %v", chunks)
	}
	if !bytes.Equal(writeChunks(chunks), img) {
		t.Fatal("writing the chunks back changed the PNG")
	}
	if _, err := readChunks(img[:len(img)-5]); err == nil {
		t.Fatal("no error for a truncated PNG")
	}
}

func TestTextChunk(t *testing.T) {
	k, text, ok := textChunk("chara", []byte("abc")).text()
	if !ok || k != "chara" || string(text) != "abc" {
		t.Fatalf("got %q %q %v", k, text, ok)
	}
	if _, _, ok := (chunk{typ: "IDAT"}).text(); ok {
		t.Fatal("IDAT read as text")
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./card`
Expected: FAIL to compile with `undefined: placeholder`.

- [ ] **Step 3: Implement**

Create `card/png.go`:

```go
package card

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

// chunk is one PNG chunk, without its length and CRC.
type chunk struct {
	typ  string
	data []byte
}

func isPNG(b []byte) bool { return bytes.HasPrefix(b, pngSignature) }

// readChunks splits a PNG into its chunks, up to IEND. It doesn't check CRCs.
func readChunks(b []byte) ([]chunk, error) {
	if !isPNG(b) {
		return nil, errors.New("not a PNG")
	}
	var out []chunk
	for rest := b[len(pngSignature):]; ; {
		if len(rest) < 12 {
			return nil, errors.New("truncated PNG")
		}
		n := binary.BigEndian.Uint32(rest)
		if uint64(n) > uint64(len(rest)-12) {
			return nil, errors.New("truncated PNG")
		}
		c := chunk{typ: string(rest[4:8]), data: rest[8 : 8+n]}
		out = append(out, c)
		if c.typ == "IEND" {
			return out, nil
		}
		rest = rest[12+n:]
	}
}

// writeChunks joins chunks into a PNG.
func writeChunks(chunks []chunk) []byte {
	var buf bytes.Buffer
	buf.Write(pngSignature)
	for _, c := range chunks {
		buf.Write(binary.BigEndian.AppendUint32(nil, uint32(len(c.data))))
		buf.WriteString(c.typ)
		buf.Write(c.data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(c.typ))
		crc.Write(c.data)
		buf.Write(binary.BigEndian.AppendUint32(nil, crc.Sum32()))
	}
	return buf.Bytes()
}

// text returns a tEXt chunk's keyword and text. ok is false for other chunks.
func (c chunk) text() (keyword string, text []byte, ok bool) {
	if c.typ != "tEXt" {
		return "", nil, false
	}
	k, t, found := bytes.Cut(c.data, []byte{0})
	return string(k), t, found
}

func textChunk(keyword string, text []byte) chunk {
	return chunk{typ: "tEXt", data: append(append([]byte(keyword), 0), text...)}
}

// placeholder is the image for a PNG card without an avatar: plain gray, in
// the 2:3 shape card apps show.
func placeholder() ([]byte, error) {
	img := image.NewGray(image.Rect(0, 0, 400, 600))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./card`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add card
git commit -m "Read and write PNG chunks"
```

### Task 10: Decode and encode cards

**Files:**
- Create: `card/card.go`
- Test: `card/card_test.go`
- Create: `card/testdata/` golden files

- [ ] **Step 1: Collect golden files**

Copy the real cards available locally:

```bash
mkdir -p card/testdata
cp ~/git/character-card-generator/proxy/cards/natalie/avatar.png card/testdata/v3-natalie.png
cp ~/git/sillytavern-skills/skills/character-card-v3-generator/assets/templates/standard-card-v3.json card/testdata/v3-mirelle.json
```

`v3-natalie.png` has a V3 card in both its `ccv3` and `chara` chunks.

The owner adds a real V2 PNG card as `card/testdata/v2-<name>.png` and a real V2 JSON card as `card/testdata/v2-<name>.json`, such as chub.ai downloads in "V2" format or files from the chub archive. This session can't read the archive share. Ask the owner for the files and wait for them; Step 5's test fails until they're there.

- [ ] **Step 2: Write the failing tests**

Create `card/card_test.go`:

```go
package card

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.want.json from the current output")

// TestGolden decodes each card in testdata, compares it with
// <file>.want.json, and checks it survives PNG and V2 JSON round trips.
func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, ".want.json") {
			continue
		}
		n++
		t.Run(filepath.Base(f), func(t *testing.T) {
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			card, img, err := Decode(in)
			if err != nil {
				t.Fatal(err)
			}
			if *update {
				var buf bytes.Buffer
				if err := json.Indent(&buf, card, "", "  "); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f+".want.json", append(buf.Bytes(), '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(f + ".want.json")
			if err != nil {
				t.Fatalf("%v; run go test ./card -update and review the file", err)
			}
			if !sameJSON(t, card, want) {
				t.Fatalf("decoded card differs from %s.want.json:\n%s", f, card)
			}

			out, ext, err := Encode(card, FormatPNG, img)
			if err != nil || ext != "png" {
				t.Fatalf("encode png: %v %q", err, ext)
			}
			card2, img2, err := Decode(out)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, card2, card) {
				t.Fatalf("PNG round trip changed the card:\n%s", card2)
			}
			if img != nil && !bytes.Equal(img2, img) {
				t.Fatal("PNG round trip changed the image")
			}

			out, ext, err = Encode(card, FormatJSONV2, nil)
			if err != nil || ext != "json" {
				t.Fatalf("encode json-v2: %v %q", err, ext)
			}
			if card2, _, err = Decode(out); err != nil || !sameJSON(t, card2, card) {
				t.Fatalf("V2 JSON round trip changed the card: %v\n%s", err, card2)
			}
		})
	}
	if n < 4 {
		t.Fatalf("found %d golden cards, want at least 4: V2 and V3, each as PNG and JSON", n)
	}
}

func TestDecodeV1AndV2(t *testing.T) {
	card, img, err := Decode([]byte(`{"name": "Old", "description": "A V1 card."}`))
	if err != nil || img != nil {
		t.Fatal(err, img)
	}
	want := `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Old", "description": "A V1 card.", "group_only_greetings": []}}`
	if !sameJSON(t, card, []byte(want)) {
		t.Fatalf("V1: %s", card)
	}
	card, _, err = Decode([]byte("\xef\xbb\xbf" + `{"spec": "chara_card_v2", "spec_version": "2.0", "data": {"name": "Two", "group_only_greetings": ["Hi."]}}`))
	want = `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Two", "group_only_greetings": ["Hi."]}}`
	if err != nil || !sameJSON(t, card, []byte(want)) {
		t.Fatalf("V2: %v %s", err, card)
	}
}

func TestDecodePrefersCCv3(t *testing.T) {
	img, err := placeholder()
	if err != nil {
		t.Fatal(err)
	}
	chunks, _ := readChunks(img)
	v2 := b64([]byte(`{"spec": "chara_card_v2", "spec_version": "2.0", "data": {"name": "Old"}}`))
	v3 := b64([]byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "New"}}`))
	end := chunks[len(chunks)-1]
	chunks = append(chunks[:len(chunks)-1], textChunk("ccv3", v3), textChunk("chara", v2), end)
	card, got, err := Decode(writeChunks(chunks))
	if err != nil || !strings.Contains(string(card), `"New"`) {
		t.Fatalf("%v %s", err, card)
	}
	if !bytes.Equal(got, img) {
		t.Fatal("card chunks left in the image")
	}
}

func TestDecodeRejects(t *testing.T) {
	img, err := placeholder()
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{
		"text":         []byte("hello"),
		"unknown spec": []byte(`{"spec": "chara_card_v9", "data": {}}`),
		"no name":      []byte(`{"foo": 1}`),
		"V2 no data":   []byte(`{"spec": "chara_card_v2"}`),
		"plain PNG":    img,
	} {
		if _, _, err := Decode(in); !errors.Is(err, ErrNotCard) {
			t.Errorf("%s: err = %v, want ErrNotCard", name, err)
		}
	}
}

func TestEncodeConvertsAvatar(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 6))
	src.Set(1, 1, color.RGBA{R: 255, A: 255})
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, src, nil); err != nil {
		t.Fatal(err)
	}
	card := []byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}`)
	out, _, err := Encode(card, FormatPNG, jpg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 4 || cfg.Height != 6 {
		t.Fatalf("output %v %+v, want a 4x6 PNG", err, cfg)
	}
	if _, _, err := Encode(card, "gif", nil); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("unknown format: %v", err)
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}
```

- [ ] **Step 3: Run them to make sure they fail**

Run: `go test ./card`
Expected: FAIL to compile with `undefined: Decode`.

- [ ] **Step 4: Implement**

Create `card/card.go`:

```go
// Package card reads and writes character card files: CCv3, V2, and V1
// cards as JSON, and as PNG images that carry the card in a text chunk.
package card

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strings"
	"unicode"

	_ "golang.org/x/image/webp"
)

var (
	// ErrNotCard means the input isn't a card this package can read.
	ErrNotCard = errors.New("not a character card")
	// ErrUnknownFormat means Encode doesn't write the format.
	ErrUnknownFormat = errors.New("unknown format")
)

// Formats Encode writes.
const (
	FormatPNG    = "png"
	FormatJSONV2 = "json-v2"
)

// PNG text chunk keywords. ccv3 holds a CCv3 card. chara holds a V2 card for
// older apps, or in some writers' files a V3 one.
const (
	keywordV3 = "ccv3"
	keywordV2 = "chara"
)

// Decode reads a card file. It returns the card as CCv3 JSON and, for a PNG,
// the image without its card chunks. A CCv3 card comes back unchanged.
func Decode(b []byte) (card json.RawMessage, img []byte, err error) {
	if !isPNG(b) {
		card, err = toV3(b)
		return card, nil, err
	}
	chunks, err := readChunks(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	found := map[string][]byte{}
	var kept []chunk
	for _, c := range chunks {
		if k, text, ok := c.text(); ok && (k == keywordV3 || k == keywordV2) {
			found[k] = text
			continue
		}
		kept = append(kept, c)
	}
	text, ok := found[keywordV3]
	if !ok {
		if text, ok = found[keywordV2]; !ok {
			return nil, nil, fmt.Errorf("%w: the PNG has no ccv3 or chara chunk", ErrNotCard)
		}
	}
	raw, err := unbase64(text)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	if card, err = toV3(raw); err != nil {
		return nil, nil, err
	}
	return card, writeChunks(kept), nil
}

// Encode writes a card in format and returns the file and its extension.
// avatar is the card's image in any format image.Decode reads, or nil, which
// gives a PNG a plain placeholder.
func Encode(card json.RawMessage, format string, avatar []byte) ([]byte, string, error) {
	card, err := toV3(card)
	if err != nil {
		return nil, "", err
	}
	v2, err := toV2(card)
	if err != nil {
		return nil, "", err
	}
	switch format {
	case FormatJSONV2:
		return v2, "json", nil
	case FormatPNG:
		img, err := asPNG(avatar)
		if err != nil {
			return nil, "", err
		}
		chunks, err := readChunks(img)
		if err != nil {
			return nil, "", fmt.Errorf("avatar: %w", err)
		}
		out := make([]chunk, 0, len(chunks)+2)
		for _, c := range chunks {
			if k, _, ok := c.text(); ok && (k == keywordV3 || k == keywordV2) {
				continue
			}
			if c.typ == "IEND" {
				out = append(out, textChunk(keywordV2, b64(v2)), textChunk(keywordV3, b64(card)))
			}
			out = append(out, c)
		}
		return writeChunks(out), "png", nil
	}
	return nil, "", fmt.Errorf("%w %q", ErrUnknownFormat, format)
}

// toV3 converts a CCv3, V2, or V1 card in JSON to CCv3.
func toV3(raw []byte) (json.RawMessage, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	var spec string
	if s, ok := top["spec"]; ok {
		if err := json.Unmarshal(s, &spec); err != nil {
			return nil, fmt.Errorf("%w: spec must be a string", ErrNotCard)
		}
	}
	var data map[string]json.RawMessage
	switch spec {
	case "chara_card_v3", "chara_card_v2":
		if err := json.Unmarshal(top["data"], &data); err != nil || data == nil {
			return nil, fmt.Errorf("%w: data must be an object", ErrNotCard)
		}
		if spec == "chara_card_v3" {
			return json.RawMessage(raw), nil
		}
	case "":
		// V1 cards keep their fields at the top level.
		if _, ok := top["name"]; !ok {
			return nil, fmt.Errorf("%w: no spec and no name", ErrNotCard)
		}
		data = top
	default:
		return nil, fmt.Errorf("%w: unknown spec %q", ErrNotCard, spec)
	}
	if _, ok := data["group_only_greetings"]; !ok {
		data["group_only_greetings"] = json.RawMessage("[]")
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
}

// toV2 returns a CCv3 card's data under the V2 spec. V2 apps ignore the
// V3-only fields.
func toV2(card json.RawMessage) ([]byte, error) {
	var c struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(card, &c); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v2", "spec_version": "2.0", "data": c.Data})
}

// asPNG returns avatar as a PNG, converting other image formats, or the
// placeholder when avatar is empty.
func asPNG(avatar []byte) ([]byte, error) {
	if len(avatar) == 0 {
		return placeholder()
	}
	if isPNG(avatar) {
		return avatar, nil
	}
	img, _, err := image.Decode(bytes.NewReader(avatar))
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	return buf.Bytes(), err
}

func b64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

// unbase64 decodes a chunk's text, which some writers wrap or leave unpadded.
func unbase64(text []byte) ([]byte, error) {
	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, string(text))
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}
```

- [ ] **Step 5: Write the golden expectations and run the tests**

Run: `go test ./card -run TestGolden -update`, then read each new `card/testdata/*.want.json`. Each must be a `chara_card_v3` card whose data matches the source file's data. A V2 source must differ only in `spec`, `spec_version`, and an added `group_only_greetings: []`. Fix the code if one doesn't.

Run: `go test -race ./card`
Expected: PASS. `TestGolden` fails with `found 2 golden cards` until the owner's V2 files are in place.

- [ ] **Step 6: Commit**

```bash
git add card
git commit -m "Decode and encode V1, V2, and V3 cards as PNG and JSON"
```

### Task 11: The `card-codec` plugin

**Files:**
- Create: `card-codec/plugin.toml`, `card-codec/main.go`
- Test: `card-codec/main_test.go`
- Modify: `index.toml`
- Create: `Makefile`

- [ ] **Step 1: Write the failing test**

Create `card-codec/main_test.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

func TestEncodeDecode(t *testing.T) {
	tmp := t.TempDir()
	params, _ := json.Marshal(protocol.CodecEncodeParams{
		Card:   json.RawMessage(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}`),
		Format: "png",
	})
	res, err := encode(tmp, params)
	if err != nil {
		t.Fatal(err)
	}
	enc := res.(protocol.CodecEncodeResult)
	if filepath.Dir(enc.Path) != tmp || enc.Extension != "png" {
		t.Fatalf("encode = %+v", enc)
	}

	params, _ = json.Marshal(protocol.CodecDecodeParams{Path: enc.Path})
	res, err = decode(tmp, params)
	if err != nil {
		t.Fatal(err)
	}
	dec := res.(protocol.CodecDecodeResult)
	if !strings.Contains(string(dec.Card), `"Brakka"`) || filepath.Dir(dec.Avatar) != tmp {
		t.Fatalf("decode = %s %q", dec.Card, dec.Avatar)
	}
}

func TestDecodeNotCard(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "notes.txt")
	if err := os.WriteFile(path, []byte("not a card"), 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(protocol.CodecDecodeParams{Path: path})
	_, err := decode(tmp, params)
	var rpcErr *protocol.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != protocol.CodeInvalidParams {
		t.Fatalf("err = %v, want -32602", err)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./card-codec`
Expected: FAIL with `no Go files` or `undefined: encode`.

- [ ] **Step 3: Implement**

Create `card-codec/plugin.toml`:

```toml
name = "card-codec"
version = "0.1.0"
protocol = 1
command = ["./card-codec"]
capabilities = ["codec"]
formats = ["png", "json-v2"]
```

Create `card-codec/main.go`:

```go
// Command card-codec is a codec plugin for character cards: CCv3, V2, and V1
// as PNG and JSON.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/inkandale/innkeeper-plugins/card"
	"github.com/inkandale/innkeeper/protocol"
)

const version = "0.1.0"

// maxInput caps the files the plugin reads. Card PNGs are a few MB at most.
const maxInput = 32 << 20

func main() {
	// Set by initialize; requests run on other goroutines.
	var blobTmp atomic.Value
	handler := func(_ context.Context, method string, params json.RawMessage) (any, error) {
		tmp, _ := blobTmp.Load().(string)
		switch method {
		case protocol.MethodInitialize:
			var p protocol.InitializeParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			blobTmp.Store(p.BlobTmp)
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "card-codec", Version: version,
				Capabilities: []string{"codec"}}, nil
		case protocol.MethodShutdown:
			return nil, nil
		case protocol.MethodCodecDecode:
			return decode(tmp, params)
		case protocol.MethodCodecEncode:
			return encode(tmp, params)
		}
		return nil, protocol.MethodNotFound(method)
	}
	conn := protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func decode(tmp string, params json.RawMessage) (any, error) {
	var p protocol.CodecDecodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid(err)
	}
	b, err := readFile(p.Path)
	if err != nil {
		return nil, invalid(err)
	}
	c, img, err := card.Decode(b)
	if err != nil {
		return nil, invalid(err)
	}
	res := protocol.CodecDecodeResult{Card: c}
	if img != nil {
		if res.Avatar, err = writeTmp(tmp, "avatar-*.png", img); err != nil {
			return nil, err
		}
	}
	return res, nil
}

func encode(tmp string, params json.RawMessage) (any, error) {
	var p protocol.CodecEncodeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, invalid(err)
	}
	var avatar []byte
	if p.Avatar != "" {
		var err error
		if avatar, err = readFile(p.Avatar); err != nil {
			return nil, err
		}
	}
	out, ext, err := card.Encode(p.Card, p.Format, avatar)
	if err != nil {
		return nil, invalid(err)
	}
	path, err := writeTmp(tmp, "card-*."+ext, out)
	if err != nil {
		return nil, err
	}
	return protocol.CodecEncodeResult{Path: path, Extension: ext}, nil
}

func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxInput+1))
	if err == nil && len(b) > maxInput {
		err = fmt.Errorf("%s is over %d MiB", path, maxInput>>20)
	}
	return b, err
}

// writeTmp writes b to a new file in tmp and returns its path.
func writeTmp(tmp, pattern string, b []byte) (string, error) {
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

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}
```

Append to `index.toml`:

```toml

[[plugin]]
name = "card-codec"
path = "card-codec"
version = "0.1.0"
protocol = 1
capabilities = ["codec"]
description = "Read and write character cards: CCv3, V2, and V1, as PNG and JSON."
```

Create `Makefile`:

```make
# Plugin folders, from index.toml.
PLUGINS := $(shell sed -n 's/^path = "\(.*\)"/\1/p' index.toml)

.PHONY: build check install

# Each binary goes in its plugin's folder, where command = ["./<folder>"] finds it.
build:
	for p in $(PLUGINS); do CGO_ENABLED=0 go build -trimpath -o $$p/$$p ./$$p || exit 1; done

# Runs the contract kit from the core version go.mod pins.
check: build
	for p in $(PLUGINS); do go tool innkeeper plugin check $$p || exit 1; done

# Copies each plugin's binary and manifest into DEST, such as an image's plugin folder.
install: build
	test -n "$(DEST)"
	for p in $(PLUGINS); do mkdir -p $(DEST)/$$p && cp $$p/$$p $$p/plugin.toml $(DEST)/$$p/ || exit 1; done
```

- [ ] **Step 4: Run the tests and the contract kit**

Run: `go test -race ./... && make check`
Expected: tests PASS, then 6 `PASS` lines for `card-codec`, including both codec checks.

- [ ] **Step 5: Commit**

```bash
git add card-codec index.toml Makefile
git commit -m "Add the card-codec plugin"
```

### Task 12: Check `index.toml`

**Files:**
- Create: `internal/indexcheck/main.go`
- Test: `internal/indexcheck/main_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/indexcheck/main_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoIndex(t *testing.T) {
	if errs := check("../.."); len(errs) > 0 {
		t.Fatal(errs)
	}
}

func TestCheckFindsProblems(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := "name = %q\nversion = \"1.0.0\"\nprotocol = 1\ncommand = [%q]\ncapabilities = [\"push\"]\n"
	write("a/plugin.toml", fmt.Sprintf(manifest, "a", "./a"))
	write("b/plugin.toml", fmt.Sprintf(manifest, "b", "./b"))
	write("c/plugin.toml", fmt.Sprintf(manifest, "c", "./other"))
	write("index.toml", `
[[plugin]]
name = "a"
path = "a"
version = "2.0.0"
protocol = 1
capabilities = ["push"]
description = "Version doesn't match."

[[plugin]]
name = "c"
path = "c"
version = "1.0.0"
protocol = 1
capabilities = ["push"]
description = "Command isn't ./c."

[[plugin]]
name = "gone"
path = "gone"
version = "1.0.0"
protocol = 1
capabilities = ["push"]
description = "No folder."
`)
	// a's version, c's command, gone's folder, and b missing from the index.
	if errs := check(root); len(errs) != 4 {
		t.Fatalf("got %d problems, want 4: %v", len(errs), errs)
	}
}
```

Add `"fmt"` to the test file's imports.

- [ ] **Step 2: Run it to make sure it fails**

Run: `go test ./internal/indexcheck`
Expected: FAIL with `undefined: check`.

- [ ] **Step 3: Implement**

Create `internal/indexcheck/main.go`:

```go
// Command indexcheck checks that index.toml lists every plugin folder and
// matches each folder's plugin.toml. Run it from the repo root.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/inkandale/innkeeper/protocol"
)

type entry struct {
	Name         string   `toml:"name"`
	Path         string   `toml:"path"`
	Version      string   `toml:"version"`
	Protocol     int      `toml:"protocol"`
	Capabilities []string `toml:"capabilities"`
	Description  string   `toml:"description"`
}

func main() {
	errs := check(".")
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, err)
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}

// check returns every problem with the index in the repo at root.
func check(root string) []error {
	var index struct {
		Plugin []entry `toml:"plugin"`
	}
	md, err := toml.DecodeFile(filepath.Join(root, "index.toml"), &index)
	if err != nil {
		return []error{err}
	}
	var errs []error
	if u := md.Undecoded(); len(u) > 0 {
		errs = append(errs, fmt.Errorf("index.toml: unknown keys: %v", u))
	}
	listed := map[string]bool{}
	for _, e := range index.Plugin {
		if !filepath.IsLocal(e.Path) || strings.ContainsRune(e.Path, '/') {
			errs = append(errs, fmt.Errorf("%s: path %q must be a folder at the repo root", e.Name, e.Path))
			continue
		}
		if listed[e.Path] {
			errs = append(errs, fmt.Errorf("%s: listed twice", e.Path))
		}
		listed[e.Path] = true
		m, err := protocol.LoadManifest(filepath.Join(root, e.Path))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Path, err))
			continue
		}
		if e.Name != m.Name || e.Version != m.Version || e.Protocol != m.Protocol || !slices.Equal(e.Capabilities, m.Capabilities) {
			errs = append(errs, fmt.Errorf("%s: index entry (name %q, version %q, protocol %d, capabilities %v) doesn't match plugin.toml (%q, %q, %d, %v)",
				e.Path, e.Name, e.Version, e.Protocol, e.Capabilities, m.Name, m.Version, m.Protocol, m.Capabilities))
		}
		if !slices.Equal(m.Command, []string{"./" + e.Path}) {
			errs = append(errs, fmt.Errorf("%s: command must be [\"./%s\"], where make build writes the binary", e.Path, e.Path))
		}
		if e.Description == "" {
			errs = append(errs, fmt.Errorf("%s: description is required", e.Path))
		}
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return append(errs, err)
	}
	for _, d := range dirs {
		if !d.IsDir() || listed[d.Name()] {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, d.Name(), "plugin.toml")); err == nil {
			errs = append(errs, fmt.Errorf("%s has a plugin.toml but no index.toml entry", d.Name()))
		}
	}
	return errs
}
```

- [ ] **Step 4: Run the tests and the check**

Run: `go test ./internal/indexcheck && go run ./internal/indexcheck`
Expected: PASS, and the check prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/indexcheck
git commit -m "Check index.toml against the plugin folders"
```

### Task 13: CI and README

**Files:**
- Create: `.github/workflows/ci.yml`, `.github/dependabot.yml`
- Modify: `README.md`

- [ ] **Step 1: Add the workflow**

Create `.github/workflows/ci.yml`:

```yaml
name: ci

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

env:
  GOPRIVATE: github.com/inkandale/innkeeper

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
        with:
          persist-credentials: false
      # The core repo is private; a read-only deploy key on it lets Go fetch it.
      - name: Read access to innkeeper
        env:
          DEPLOY_KEY: ${{ secrets.INNKEEPER_DEPLOY_KEY }}
        run: |
          mkdir -p -m 700 ~/.ssh
          printf '%s\n' "$DEPLOY_KEY" > ~/.ssh/id_ed25519
          chmod 600 ~/.ssh/id_ed25519
          echo 'github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl' >> ~/.ssh/known_hosts
          git config --global url."git@github.com:inkandale/innkeeper".insteadOf https://github.com/inkandale/innkeeper
      - uses: actions/setup-go@v7
        with:
          go-version-file: go.mod
      - run: go vet ./...
      - run: go test -race ./...
      - run: go run ./internal/indexcheck
      - run: make check
```

Check the host key line against GitHub's published keys before committing: `curl -s https://api.github.com/meta | jq -r '.ssh_keys[]' | grep ed25519`.

Create `.github/dependabot.yml`:

```yaml
version: 2
updates:
  - package-ecosystem: github-actions
    directory: /
    schedule:
      interval: weekly
```

- [ ] **Step 2: README**

Use the `documentation-writing` skill. Append to `README.md`:

````markdown
## Building and checking

```bash
go env -w GOPRIVATE=github.com/davidgibbons/*
make build                    # each plugin's binary, into its folder
make check                    # the core's contract kit, against each plugin
go run ./internal/indexcheck  # index.toml matches the plugin folders
```

The core version is pinned in `go.mod` as a tool. Move it with
`go get -tool github.com/inkandale/innkeeper/cmd/innkeeper@<commit>`;
that sets both the `protocol` package and the contract kit.

## Adding a plugin

1. Make a folder whose `plugin.toml` has `command = ["./<folder>"]`.
2. Add an `index.toml` entry with the same name, version, protocol, and
   capabilities, plus a description.
3. Ignore the built binary in `.gitignore`.
````

- [ ] **Step 3: Commit, push, and merge**

```bash
git add .github README.md
git commit -m "Run tests, the index check, and the contract kit in CI"
git push -u origin card-codec
gh pr create --fill
```

Wait for CI on the PR to pass, then merge it. Note the merge commit's full hash for Task 14.

---

# Part 3: Plugins in the image (`innkeeper`)

### Task 14: Build the plugins into the image

**Files:**
- Modify: `Dockerfile`
- Modify: `compose.yaml`
- Modify: `README.md`

- [ ] **Step 1: Add a plugins stage**

In `Dockerfile`, add after the `build` stage, using Task 13's commit for `PLUGINS_REF`:

```dockerfile
# The bundled plugins, built against this core's protocol package.
FROM golang:1.27-alpine AS plugins
ARG PLUGINS_REF=<Task 13 commit>
RUN apk add --no-cache git make openssh-client \
    && mkdir -p -m 700 /root/.ssh \
    && echo 'github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl' > /root/.ssh/known_hosts
RUN --mount=type=ssh git clone git@github.com:inkandale/innkeeper-plugins.git /plugins-src \
    && git -C /plugins-src checkout --detach "$PLUGINS_REF"
COPY . /core
WORKDIR /plugins-src
RUN go mod edit -replace github.com/inkandale/innkeeper=/core \
    && go mod tidy \
    && make install DEST=/plugins
```

In the final stage, after `COPY --from=build /innkeeper /usr/local/bin/innkeeper`, add:

```dockerfile
COPY --from=plugins /plugins /plugins
```

In `compose.yaml`, replace `build: .` with:

```yaml
  build:
    context: .
    # The plugins stage clones the private innkeeper-plugins repo.
    ssh: [default]
```

- [ ] **Step 2: Build and check the bundled plugin**

Run:

```bash
ssh-add -l   # must list a key that can read innkeeper-plugins
docker build --ssh default -t innkeeper:dev .
docker run --rm innkeeper:dev plugin check /plugins/card-codec
```

Expected: 6 `PASS` lines.

- [ ] **Step 3: Export a PNG end to end**

Port 8080 is often taken on this machine, so publish elsewhere:

```bash
echo "INNKEEPER_SECRETS_KEY=$(openssl rand -base64 32)" > .env
echo "INNKEEPER_PORT=18080" >> .env
docker compose up -d --build
export INNKEEPER_URL=http://localhost:18080
export INNKEEPER_TOKEN=$(docker compose exec -T api innkeeper token create admin)
go run ./cmd/innkeeper call POST /instances '{"plugin": "card-codec", "name": "Codec"}'
go run ./cmd/innkeeper call POST /cards '{"data": {"name": "Brakka", "description": "A dwarf."}}'
go run ./cmd/innkeeper export <card id from the reply> /tmp/brakka.png
file /tmp/brakka.png
docker compose down -v
```

Expected: `PNG image data, 400 x 600`.

- [ ] **Step 4: README**

Use the `documentation-writing` skill. In `README.md`'s "Try it with Docker Compose", replace `No plugins are installed in the image.` with:

```markdown
The image includes the plugins from `innkeeper-plugins` at the commit
`PLUGINS_REF` names in the `Dockerfile`. Building it clones that private
repo, so it needs an SSH agent holding a key that can read it
(`ssh-add -l`).
```

- [ ] **Step 5: Commit, merge, and close out**

```bash
git add Dockerfile compose.yaml README.md
git commit -m "Build the bundled plugins into the image"
```

Merge into `main` and push. Move INN-6 to done, and add the plan path and both repos' merge commits to its description.
