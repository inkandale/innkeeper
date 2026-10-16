# chub-archive Image Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** chub-archive copies each image it ingests into a folder it owns, named by hash, and reads avatars from there, so the takeout only needs mounting during an ingest.

**Architecture:** New helpers in `chub-archive/images.go` name, find, and write images in the store. The parallel image loader in `ingest.go` writes each decoded image to the store, and also fills the store for versions already ingested. `get` in `main.go` reads only from the store. Spec: `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, `chub-archive` section.

**Tech Stack:** Go 1.27, stdlib (`net/http.DetectContentType`, `os.CreateTemp`, `os.Rename`), Postgres via `catalogkit`.

---

## Setup

- Work in a worktree of `~/git/innkeeper-plugins` on branch `inn-37f-image-store`, from `origin/main`:
  `git -C ~/git/innkeeper-plugins worktree add -b inn-37f-image-store ../innkeeper-plugins-inn-37f origin/main`
- Every test command needs the test database, or the database tests skip and pass without running. `make check` needs its own variable, or the contract check skips ingest:
  `export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres INNKEEPER_PLUGIN_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`
- The fixture image is `chub-archive/testdata/images/a/b/c/0000000000000000000000000000d`, a PNG with hash `abc0000000000000000000000000000d`. In the store it lives at `ab/c0/abc0000000000000000000000000000d.png`.

## Files

| File | Change |
|---|---|
| `chub-archive/images.go` | `validHash`, `imageExt`, `storePath`, `findStored`, `saveImage`, `checkWritable` |
| `chub-archive/images_test.go` | `fixtureHash`, `TestSaveImage`, `TestCheckWritable` |
| `chub-archive/ingest.go` | Precheck the store; `loadImage` writes to it and backfills |
| `chub-archive/ingest_test.go` | `testEnv` sets a store; store assertions and new tests |
| `chub-archive/main.go` | `env.imageStore`, config read, `get` from the store, version 0.2.0 |
| `chub-archive/main_test.go` | `TestGetAvatar` without the takeout |
| `chub-archive/plugin.toml`, `index.toml` | `image_store` config, version 0.2.0 |
| `chub-archive/check-config.json`, `chub-archive/testdata/store/.gitkeep`, `.gitignore` | Contract check gets a store |
| `chub-archive/real_test.go`, `chub-archive/README.md` | `CHUB_IMAGE_STORE`, setup docs |

---

### Task 1: Store helpers

**Files:**
- Modify: `chub-archive/images.go`
- Test: `chub-archive/images_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `chub-archive/images_test.go` (its imports, `os`, `path/filepath`, and `testing`, already cover this):

```go
// fixtureHash names the fixture image, a PNG in testdata/images.
const fixtureHash = "abc0000000000000000000000000000d"

func TestSaveImage(t *testing.T) {
	store := t.TempDir()
	b, err := os.ReadFile("testdata/images/a/b/c/0000000000000000000000000000d")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := saveImage(store, fixtureHash, b); err != nil {
			t.Fatal(err)
		}
	}
	want := filepath.Join(store, "ab", "c0", fixtureHash+".png")
	if got := findStored(store, fixtureHash); got != want {
		t.Fatalf("findStored = %q, want %q", got, want)
	}
	if entries, _ := os.ReadDir(filepath.Dir(want)); len(entries) != 1 {
		t.Fatalf("store folder holds %d files, want only the image", len(entries))
	}

	// Bytes that aren't an image, and hashes that could name another path, store nothing.
	other := "fff0000000000000000000000000000f"
	if err := saveImage(store, other, []byte("not an image")); err != nil || findStored(store, other) != "" {
		t.Fatalf("stored a non-image, err %v", err)
	}
	if err := saveImage(store, "../x", b); err != nil || findStored(store, "../x") != "" {
		t.Fatalf("stored under a bad hash, err %v", err)
	}
	if findStored("", fixtureHash) != "" {
		t.Fatal("found an image with no store set")
	}
}

func TestCheckWritable(t *testing.T) {
	if err := checkWritable(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"", "testdata/missing", "testdata/dump.sql"} {
		if err := checkWritable(dir); err == nil {
			t.Errorf("checkWritable(%q) passed, want an error", dir)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./chub-archive/ -run 'TestSaveImage|TestCheckWritable' -count=1`
Expected: build failure, `undefined: saveImage`.

- [ ] **Step 3: Implement**

Replace `chub-archive/images.go` with:

```go
package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/inkandale/innkeeper-plugins/internal/pluginio"
)

// imageExts maps the image types the store keeps to their extensions.
var imageExts = map[string]string{"image/png": "png", "image/webp": "webp", "image/jpeg": "jpg", "image/gif": "gif"}

// validHash reports whether hash is lowercase hex long enough to name a
// file, so it can't name a path outside a folder.
func validHash(hash string) bool {
	return len(hash) >= 4 && strings.Trim(hash, "0123456789abcdef") == ""
}

// readImage reads the image the archive stores under hash, from the first of
// dirs that has it. It returns nil and no error when none has it. The
// archive keeps each at <a>/<b>/<c>/<the rest of the hash>.
func readImage(dirs []string, hash string) ([]byte, error) {
	if !validHash(hash) {
		return nil, nil
	}
	for _, dir := range dirs {
		b, err := pluginio.ReadFile(filepath.Join(dir, hash[:1], hash[1:2], hash[2:3], hash[3:]))
		if !errors.Is(err, fs.ErrNotExist) {
			return b, err
		}
	}
	return nil, nil
}

// storePath is where the image store keeps the image with hash.
func storePath(store, hash, ext string) string {
	return filepath.Join(store, hash[:2], hash[2:4], hash+"."+ext)
}

// findStored returns the path of the image with hash in the store, or "" if
// the store doesn't have it.
func findStored(store, hash string) string {
	if store == "" || !validHash(hash) {
		return ""
	}
	for _, ext := range imageExts {
		if p := storePath(store, hash, ext); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// saveImage writes b, the image with hash, into the store unless it is there
// already. Bytes that aren't a stored image type are skipped. It writes a
// temporary file and renames it, so a crash never leaves a partial image.
func saveImage(store, hash string, b []byte) error {
	ext := imageExts[http.DetectContentType(b)]
	if ext == "" || !validHash(hash) {
		return nil
	}
	path := storePath(store, hash, ext)
	if fileExists(path) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		os.Remove(f.Name())
	}
	return err
}

// checkWritable fails unless dir is a folder ingest can write to. It
// doesn't create dir: a missing store usually means a missing mount.
func checkWritable(dir string) error {
	if dir == "" {
		return errors.New("is not set")
	}
	f, err := os.CreateTemp(dir, ".check-*")
	if err != nil {
		return err
	}
	f.Close()
	return os.Remove(f.Name())
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./chub-archive/ -run 'TestSaveImage|TestCheckWritable|TestReadImage' -count=1 -v`
Expected: all three PASS.

- [ ] **Step 5: Commit**

```bash
git add chub-archive/images.go chub-archive/images_test.go
git commit -m "feat(chub-archive): find and write images in an image store by hash"
```

---

### Task 2: The `image_store` setting and its precheck

**Files:**
- Modify: `chub-archive/main.go` (the `env` struct and `Open`)
- Modify: `chub-archive/ingest.go` (start of `ingest`)
- Modify: `chub-archive/plugin.toml`, `chub-archive/check-config.json`, `.gitignore`
- Create: `chub-archive/testdata/store/.gitkeep` (empty)
- Test: `chub-archive/ingest_test.go`

- [ ] **Step 1: Give the test env a store and write the failing test**

In `testEnv` in `chub-archive/ingest_test.go`, change the return to:

```go
	return &env{store: store, dump: "testdata/dump.sql", images: []string{"testdata/images"},
		imageStore: t.TempDir(), blobTmp: t.TempDir()}
```

Add:

```go
// Without a writable store, every version would be ingested without its
// image being kept, and avatars would be gone once the takeout is unmounted.
func TestIngestNeedsAWritableImageStore(t *testing.T) {
	for _, dir := range []string{"", "testdata/missing", "testdata/dump.sql"} {
		e := testEnv(t)
		e.imageStore = dir
		params, _ := json.Marshal(protocol.CatalogIngestParams{Run: "r"})
		_, err := ingest(context.Background(), e, func(string, any) error { return nil }, params)
		var rpc *protocol.Error
		if !errors.As(err, &rpc) || rpc.Code != protocol.CodeInvalidParams || !strings.Contains(err.Error(), "image_store") {
			t.Fatalf("%q: err = %v, want invalid params naming image_store", dir, err)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./chub-archive/ -run TestIngestNeedsAWritableImageStore -count=1`
Expected: build failure, `unknown field imageStore`.

- [ ] **Step 3: Implement**

In `chub-archive/main.go`, add the field to `env`:

```go
type env struct {
	store      *catalogkit.Store // nil without a database_url
	dump       string            // config.dump
	images     []string          // config.images
	imageStore string            // config.image_store
	blobTmp    string
}
```

and in `Open`, after `e.dump, _ = p.Config["dump"].(string)`:

```go
			e.imageStore, _ = p.Config["image_store"].(string)
```

In `chub-archive/ingest.go`, right after the loop that checks `e.images` folders:

```go
	if err := checkWritable(e.imageStore); err != nil {
		return res, invalid(fmt.Errorf("config image_store: %w; is the image store mounted?", err))
	}
```

In `chub-archive/plugin.toml`, after the `images` property:

```toml
[config.properties.image_store]
type = "string"
title = "Image store"
description = "Absolute path of a writable folder that ingest copies the archive's images into, about 100 GB for the 01-05-2025 dump. Avatars are read from here, so the takeout needs mounting only while ingesting."
```

Set `chub-archive/check-config.json` to:

```json
{"dump": "testdata/dump.sql", "images": ["testdata/images"], "image_store": "testdata/store"}
```

Create the empty file `chub-archive/testdata/store/.gitkeep`, and append to `.gitignore`:

```
# The contract check's image store.
/chub-archive/testdata/store/*
!/chub-archive/testdata/store/.gitkeep
```

- [ ] **Step 4: Run the package's tests**

Run: `go test ./chub-archive/ -count=1`
Expected: PASS, including the new test.

- [ ] **Step 5: Commit**

```bash
git add chub-archive .gitignore
git commit -m "feat(chub-archive): add the image_store setting, and refuse to ingest without it"
```

---

### Task 3: Ingest fills the store

**Files:**
- Modify: `chub-archive/ingest.go` (`loadImage`)
- Test: `chub-archive/ingest_test.go`

- [ ] **Step 1: Write the failing tests**

In `TestIngest`, right after the checkpoint assertions, add:

```go
	if findStored(e.imageStore, fixtureHash) == "" {
		t.Fatal("ingest didn't copy Aria's image into the image store")
	}
```

Add:

```go
// Versions already ingested still get their image copied, so a new or
// incomplete store fills on the next ingest.
func TestIngestBackfillsTheImageStore(t *testing.T) {
	e := testEnv(t)
	runIngest(t, e, "", nil)
	e.imageStore = t.TempDir()
	if res, _, _ := runIngest(t, e, "", nil); res.Added != 0 || res.Updated != 0 {
		t.Fatalf("re-ingest = %+v, want nothing new", res)
	}
	if findStored(e.imageStore, fixtureHash) == "" {
		t.Fatal("re-ingest didn't copy the image into the new store")
	}
}

// A store write that fails is retried from the checkpoint, like a failed read.
func TestIngestImageStoreWriteErrorIsRetryable(t *testing.T) {
	e := testEnv(t)
	// A file where the image's folder belongs fails the write after the precheck passes.
	if err := os.WriteFile(filepath.Join(e.imageStore, "ab"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	params, _ := json.Marshal(protocol.CatalogIngestParams{Run: "r"})
	_, err := ingest(context.Background(), e, func(string, any) error { return nil }, params)
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || !rpc.Retryable() {
		t.Fatalf("err = %v, want a retryable error", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./chub-archive/ -run 'TestIngest$|TestIngestBackfillsTheImageStore|TestIngestImageStoreWriteErrorIsRetryable' -count=1`
Expected: all three FAIL: the store stays empty, and the write test gets no error.

- [ ] **Step 3: Implement**

In `chub-archive/ingest.go`, replace the body of `loadImage` from the `stored` check to the end with:

```go
	seen, err := stored(ctx, e.store, id, version)
	if err != nil {
		return nil, err
	}
	hash := row["image_hash"]
	if seen && findStored(e.imageStore, hash) != "" {
		return nil, nil
	}
	b, err := readImage(e.images, hash)
	if err != nil && !errors.Is(err, fs.ErrPermission) && !errors.Is(err, syscall.EISDIR) && !errors.Is(err, pluginio.ErrTooLarge) {
		return nil, err
	}
	var img image.Image
	if err == nil && b != nil {
		if img, err = cardhash.DecodeImageBytes(b); err == nil {
			if err := saveImage(e.imageStore, hash, b); err != nil {
				return nil, err
			}
		}
	}
	if err != nil {
		log.Printf("%s version %s: image %s: %v", id, version, hash, err)
	}
	if seen {
		// Already ingested; this only filled the store.
		return nil, nil
	}
	return img, nil
```

and update its doc comment to:

```go
// loadImage reads and decodes a version row's image and copies it into the
// image store. It returns nil for a version already stored, after filling
// the store if it lacks the image. An image that can never be read is logged
// and left out; any other read or write error is returned, to fail the run
// for the core to retry.
```

The consumer in `ingest` already turns a returned error into a retryable one, so nothing else changes.

- [ ] **Step 4: Run the package's tests**

Run: `go test -race ./chub-archive/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add chub-archive/ingest.go chub-archive/ingest_test.go
git commit -m "feat(chub-archive): copy each ingested image into the image store"
```

---

### Task 4: `catalog.get` reads avatars from the store

**Files:**
- Modify: `chub-archive/main.go` (`get`)
- Test: `chub-archive/main_test.go` (`TestGetAvatar`)

- [ ] **Step 1: Change the test**

In `TestGetAvatar`, right after `runIngest(t, e, "", nil)`, add:

```go
	e.images = nil // The takeout is unmounted after ingest.
```

Replace the block that starts `// An image that can't be read leaves the item without an avatar` with:

```go
	// An image missing from the store leaves the item without an avatar, not an error.
	if err := os.Remove(findStored(e.imageStore, fixtureHash)); err != nil {
		t.Fatal(err)
	}
	if res, err := get(ctx, e, getParams("chub_character_def:100", "")); err != nil || res.Avatar != "" {
		t.Fatalf("missing image: avatar = %q, err %v; want none", res.Avatar, err)
	}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./chub-archive/ -run TestGetAvatar -count=1`
Expected: FAIL, `avatar = "", want a file in ...`, because `get` still reads the takeout.

- [ ] **Step 3: Implement**

In `chub-archive/main.go`, in `get`, replace everything from `b, err := readImage(e.images, hash)` to the end of the function with:

```go
	path := findStored(e.imageStore, hash)
	if path == "" {
		return res, nil
	}
	b, err := pluginio.ReadFile(path)
	if err != nil {
		// The item is still worth showing without its avatar.
		log.Printf("get %s version %s: image %s: %v", p.ID, res.Version, hash, err)
		return res, nil
	}
	res.Avatar, err = pluginio.WriteTmp(e.blobTmp, "avatar-*"+filepath.Ext(path), b)
	return res, err
```

Add `"path/filepath"` to its imports, and change the doc comment to:

```go
// get answers catalog.get, with the version's image from the image store as
// its avatar.
```

- [ ] **Step 4: Run the package's tests**

Run: `go test -race ./chub-archive/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add chub-archive/main.go chub-archive/main_test.go
git commit -m "feat(chub-archive): read avatars from the image store"
```

---

### Task 5: Version, real-dump test, and README

**Files:**
- Modify: `chub-archive/main.go` (`const version`), `chub-archive/plugin.toml`, `index.toml` (the chub-archive entry)
- Modify: `chub-archive/real_test.go`, `chub-archive/README.md`

- [ ] **Step 1: Bump chub-archive to 0.2.0**

Set `version = "0.2.0"` in `chub-archive/plugin.toml` and in the chub-archive entry of `index.toml`, and `const version = "0.2.0"` in `chub-archive/main.go`. The publish workflow releases a plugin when its version changes.

- [ ] **Step 2: Give the real-dump test a store**

In `chub-archive/real_test.go`, set the field in the `env` literal:

```go
	e := &env{store: store, dump: dump, images: strings.Split(os.Getenv("CHUB_IMAGES"), ":"),
		imageStore: os.Getenv("CHUB_IMAGE_STORE"), blobTmp: t.TempDir()}
```

and add `CHUB_IMAGE_STORE` to its doc comment: "CHUB_IMAGE_STORE the image store".

- [ ] **Step 3: Update the README**

Edit `chub-archive/README.md` with the Edit tool, not a shell heredoc:

- Setup step 2: replace "Keep it mounted after the ingest: `catalog.get` reads each avatar from it." with "It only needs to be mounted while an ingest runs."
- Add a step after it: "Mount a writable folder of about 100 GB for the image store, read-write into both the API and worker containers. Ingest copies the archive's images into it, and `catalog.get` reads avatars from it."
- Setup step 3: add "and **Image store** to that folder."
- In Behavior, change the image bullet's first sentence to "A version whose image is missing or unreadable (permission denied, a folder, over 32 MiB, or not an image) is stored without one, and nothing is copied to the image store."
- Add a Behavior bullet: "Re-ingesting copies any image the image store lacks, so pointing an instance at a new store fills it."
- In the test command, add the line `CHUB_IMAGE_STORE=/path/to/image-store \`.

- [ ] **Step 4: Run every check**

Run: `go vet ./chub-archive/ && go test -race -count=1 ./catalogkit ./chub-archive ./folder && make check`
Expected: everything passes, and `make check` lists `PASS  catalog.ingest completes` for chub-archive. Without `INNKEEPER_PLUGIN_DATABASE_URL` that line is missing and the store goes untested. The contract check ingests the fixture into `chub-archive/testdata/store` and calls `catalog.get` with an avatar. Then run `git status --short` and confirm nothing under `testdata/store` other than `.gitkeep` shows up.

- [ ] **Step 5: Commit**

```bash
git add chub-archive index.toml
git commit -m "feat(chub-archive): release 0.2.0 with the image store"
```

---

## After merge

- The local `chub_real` schema already holds every version. An ingest on it with an empty store and the takeout mounted fills the store, about 235k images, without adding versions. Production gets its store the same way after the data copy.
- Update the INN-37 ticket per the Kaneo rules.
