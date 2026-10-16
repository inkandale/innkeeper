# Avatar Pixel Comparison Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Two avatars with the same pixels count as the same avatar, so a card bound to a second app isn't marked `ahead` because that app's PNG carries different metadata.

**Architecture:** Step 1 of `docs/superpowers/specs/2026-10-11-single-sync-design.md`. Each image blob gets a `pixel_hash` (SHA-256 of its decoded pixels), stored at ingest and computed on first use for older blobs. `Library.SameImage` compares two blobs by `sha256`, then by `pixel_hash`. The three places in `internal/targets` that compare a library avatar with an app avatar call it.

**Tech Stack:** Go, pgx, PostgreSQL, `image/*` decoders already in `cardhash`.

**Tests:** DB tests skip without `INNKEEPER_TEST_DATABASE_URL`. Run them with
`INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`. A run that prints `ok` for a DB package with that unset proves nothing.

---

## Files

- Modify: `cardhash/phash.go` — add `PixelHash(img image.Image) string`.
- Modify: `cardhash/phash_test.go` — its tests.
- Create: `internal/db/migrations/0018_blob_pixel_hash.sql`.
- Modify: `internal/library/blobs.go` — `Blob.PixelHash`, store it at ingest, add `SameImage`.
- Create: `internal/library/blobs_pixel_test.go`.
- Modify: `internal/targets/resolve.go` — avatar choice check, `newAvatar`, `appBehind`.
- Modify: `internal/targets/reconcile_compare.go` — `AvatarDiff` only for different pixels.
- Modify: `internal/targets/reconcile_test.go` — bind test with a re-encoded avatar.

---

### Task 1: `cardhash.PixelHash`

**Files:**
- Modify: `cardhash/phash.go`
- Test: `cardhash/phash_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `cardhash/phash_test.go` (add `"image/color"` to its imports if missing):

```go
func TestPixelHashIgnoresEncoding(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 4, 4))
	rgba := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			v := uint8(x*40 + y*10)
			gray.SetGray(x, y, color.Gray{Y: v})
			rgba.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}
	if PixelHash(gray) != PixelHash(rgba) {
		t.Error("same pixels in two color models hash differently")
	}
	rgba.SetNRGBA(0, 0, color.NRGBA{R: 1, G: 0, B: 0, A: 255})
	if PixelHash(gray) == PixelHash(rgba) {
		t.Error("one changed pixel hashes the same")
	}
}

func TestPixelHashIncludesSize(t *testing.T) {
	a := image.NewGray(image.Rect(0, 0, 2, 8))
	b := image.NewGray(image.Rect(0, 0, 8, 2))
	if PixelHash(a) == PixelHash(b) {
		t.Error("2×8 and 8×2 of the same pixels hash the same")
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./cardhash -run PixelHash`
Expected: FAIL, `undefined: PixelHash`.

- [ ] **Step 3: Implement**

In `cardhash/phash.go`, add `"crypto/sha256"`, `"encoding/binary"`, and `"encoding/hex"` to the imports, and append:

```go
// PixelHash returns the hex SHA-256 of img's size and its pixels as 8-bit
// NRGBA, so the same picture hashes the same whatever its file format,
// color model, or metadata.
func PixelHash(img image.Image) string {
	b := img.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	h := sha256.New()
	_ = binary.Write(h, binary.BigEndian, [2]uint32{uint32(b.Dx()), uint32(b.Dy())})
	h.Write(n.Pix)
	return hex.EncodeToString(h.Sum(nil))
}
```

`draw` is `golang.org/x/image/draw`, already imported; it re-exports `draw.Draw` and `draw.Src`.

- [ ] **Step 4: Run them to see them pass**

Run: `go test ./cardhash`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add cardhash/phash.go cardhash/phash_test.go
git commit -m "feat(cardhash): hash an image's pixels apart from its encoding"
```

---

### Task 2: store `pixel_hash` and compare blobs

**Files:**
- Create: `internal/db/migrations/0018_blob_pixel_hash.sql`
- Modify: `internal/library/blobs.go`
- Test: `internal/library/blobs_pixel_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/library/blobs_pixel_test.go`. It uses `newLibrary(t)` from `internal/library/library_test.go`.

```go
package library

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/png"
	"testing"
)

// grayPNG encodes a 16×16 gray image whose pixel (0,0) is first.
func grayPNG(t *testing.T, first uint8) []byte {
	t.Helper()
	img := image.NewGray(image.Rect(0, 0, 16, 16))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	img.Pix[0] = first
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// withText inserts a tEXt chunk before IEND, as SillyTavern embeds a card.
func withText(b []byte, text string) []byte {
	data := append([]byte("chara\x00"), text...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	body := append([]byte("tEXt"), data...)
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(body))
	iend := len(b) - 12
	return append(append(append([]byte{}, b[:iend]...), chunk...), b[iend:]...)
}

func TestSameImage(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	put := func(b []byte) string {
		t.Helper()
		blob, err := lib.PutBlob(ctx, bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return blob.SHA256
	}
	plain := put(grayPNG(t, 0))
	tagged := put(withText(grayPNG(t, 0), "card json"))
	changed := put(grayPNG(t, 1))
	if plain == tagged {
		t.Fatal("the tEXt chunk didn't change the bytes")
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{plain, plain, true},
		{plain, tagged, true},
		{plain, changed, false},
		{plain, "", false},
		{"", "", true},
	} {
		got, err := lib.SameImage(ctx, c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("SameImage(%.8s, %.8s) = %v, %v; want %v", c.a, c.b, got, err, c.want)
		}
	}
}

func TestSameImageFillsOldBlobs(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, err := lib.PutBlob(ctx, bytes.NewReader(grayPNG(t, 0)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.PutBlob(ctx, bytes.NewReader(withText(grayPNG(t, 0), "x")))
	if err != nil {
		t.Fatal(err)
	}
	// A blob stored before the column existed has no pixel_hash.
	if _, err := lib.pool.Exec(ctx, `UPDATE blob SET pixel_hash = NULL`); err != nil {
		t.Fatal(err)
	}
	if same, err := lib.SameImage(ctx, a.SHA256, b.SHA256); err != nil || !same {
		t.Fatalf("SameImage = %v, %v", same, err)
	}
	var n int
	if err := lib.pool.QueryRow(ctx, `SELECT count(*) FROM blob WHERE pixel_hash IS NOT NULL`).Scan(&n); err != nil || n != 2 {
		t.Errorf("%d blobs got a pixel_hash, %v", n, err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library -run SameImage`
Expected: FAIL, `lib.SameImage undefined`.

- [ ] **Step 3: Add the migration**

Create `internal/db/migrations/0018_blob_pixel_hash.sql`:

```sql
-- The SHA-256 of an image's decoded pixels, so the same picture with
-- different metadata compares equal. NULL until computed.
ALTER TABLE blob ADD COLUMN pixel_hash text;
```

- [ ] **Step 4: Store it at ingest and add `SameImage`**

In `internal/library/blobs.go`:

1. Add the field to `Blob`, after `AvatarPhash`:

```go
	// PixelHash is the SHA-256 of an image's decoded pixels; nil for other blobs.
	PixelHash *string `json:"-"`
```

2. Change `blobCols` to `sha256, size, mime, store, avatar_phash, pixel_hash, created_at` and `scanBlob` to scan `&b.PixelHash` after `&b.AvatarPhash`.

3. In `record`, replace the phash block and the INSERT with:

```go
	var phash *int64
	var pixels *string
	if strings.HasPrefix(mime, "image/") {
		if img, err := cardhash.DecodeImage(path); err == nil {
			h, p := cardhash.Phash(img), cardhash.PixelHash(img)
			phash, pixels = &h, &p
		}
	}
	if _, err := l.pool.Exec(ctx, `INSERT INTO blob (sha256, size, mime, store, avatar_phash, pixel_hash)
		VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (sha256) DO UPDATE SET created_at = now()`,
		sha, size, mime, l.blobs.Name, phash, pixels); err != nil {
		return Blob{}, err
	}
```

4. Append:

```go
// SameImage reports whether blobs a and b are the same picture: the same
// bytes, or images with the same pixels. Either may be "", meaning no
// avatar. A blob stored before pixel_hash existed gets one now.
func (l *Library) SameImage(ctx context.Context, a, b string) (bool, error) {
	if a == b {
		return true, nil
	}
	if a == "" || b == "" {
		return false, nil
	}
	pa, err := l.pixelHash(ctx, a)
	if err != nil || pa == "" {
		return false, err
	}
	pb, err := l.pixelHash(ctx, b)
	return pb != "" && pa == pb, err
}

// pixelHash returns the blob's pixel_hash, computing and storing it if
// missing; "" when the blob isn't an image that decodes.
func (l *Library) pixelHash(ctx context.Context, sha string) (string, error) {
	blob, err := l.Blob(ctx, sha)
	if err != nil {
		return "", err
	}
	if blob.PixelHash != nil {
		return *blob.PixelHash, nil
	}
	if !strings.HasPrefix(blob.MIME, "image/") {
		return "", nil
	}
	path, err := l.blobs.Path(sha)
	if err != nil {
		return "", err
	}
	img, err := cardhash.DecodeImage(path)
	if err != nil {
		return "", nil
	}
	p := cardhash.PixelHash(img)
	_, err = l.pool.Exec(ctx, `UPDATE blob SET pixel_hash = $2 WHERE sha256 = $1`, sha, p)
	return p, err
}
```

- [ ] **Step 5: Run the tests to see them pass**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/library ./internal/db`
Expected: `ok` for both. `internal/db` runs the migrations test.

- [ ] **Step 6: Commit**

```bash
git add internal/db/migrations/0018_blob_pixel_hash.sql internal/library/blobs.go internal/library/blobs_pixel_test.go
git commit -m "feat(library): compare avatars by pixels"
```

---

### Task 3: targets compare avatars by pixels

**Files:**
- Modify: `internal/targets/resolve.go` (around lines 490-500 and 570-595)
- Modify: `internal/targets/reconcile_compare.go:71`
- Test: `internal/targets/reconcile_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/targets/reconcile_test.go`. It reuses `patternPNG` and `setAvatar` from that file; add `"encoding/binary"` and `"hash/crc32"` to its imports.

```go
// tagPNG copies the PNG at path with a tEXt chunk before IEND, as
// SillyTavern embeds the card in its avatars.
func tagPNG(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("chara\x00card json")
	body := append([]byte("tEXt"), data...)
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	chunk = append(chunk, body...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(body))
	iend := len(b) - 12
	out := append(append(append([]byte{}, b[:iend]...), chunk...), b[iend:]...)
	tagged := filepath.Join(t.TempDir(), "tagged.png")
	if err := os.WriteFile(tagged, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return tagged
}

// An app avatar with the library's pixels but other metadata binds in sync;
// one changed pixel leaves the card ahead.
func TestReconcileBindIgnoresAvatarMetadata(t *testing.T) {
	for _, change := range []bool{false, true} {
		e := setup(t, nil)
		img := patternPNG(t, func(x, y int) uint8 { return uint8(x * y) })
		lia := e.libCard(map[string]any{"name": "Lia", "description": liaText})
		e.setAvatar(lia, img)
		appImg := tagPNG(t, img)
		if change {
			appImg = patternPNG(t, func(x, y int) uint8 { return uint8(x*y) + 1 })
		}
		e.appCard("r1", map[string]any{"name": "Lia", "description": liaText}, map[string]any{"avatar": appImg})
		snap, err := e.s.Reconcile(e.ctx, ReconcileParams{InstanceID: e.inst, Apply: ApplyObvious}, nil)
		if err != nil || !snap.Items[0].AutoBound {
			t.Fatalf("change=%v: items %+v, %v", change, snap.Items, err)
		}
		want := "in_sync"
		if change {
			want = "ahead"
		}
		if got := e.status(lia); got != want {
			t.Errorf("change=%v: status %s, want %s", change, got, want)
		}
	}
}
```

Check `internal/fakeplugin/target.go:196-225`: `get` copies the stored `avatar` path's file, so the `avatar` key in `appCard`'s extra is what the app serves.

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets -run TestReconcileBindIgnoresAvatarMetadata`
Expected: FAIL, `change=false: status ahead, want in_sync`.

- [ ] **Step 3: Use `SameImage` in resolve**

In `internal/targets/resolve.go`, in the function containing `avatar := ""` (around line 489):

Replace

```go
	if _, ok := r.Choices[avatarChoice]; ok && avatar == it.avatar {
		return res, fmt.Errorf("%w: %s", merge.ErrUnknownHunk, avatarChoice)
	}
```

with

```go
	sameAvatar, err := s.lib.SameImage(ctx, avatar, it.avatar)
	if err != nil {
		return res, err
	}
	if _, ok := r.Choices[avatarChoice]; ok && sameAvatar {
		return res, fmt.Errorf("%w: %s", merge.ErrUnknownHunk, avatarChoice)
	}
```

If `s` isn't in scope there, use `rs.s.lib`. If `err` is already declared in that scope, use `=` for it.

Then replace

```go
	newAvatar := avatar
	if takeAppAvatar {
		newAvatar = it.avatar
	}
```

with

```go
	newAvatar := avatar
	// Taking the app's copy of the same picture would only add a revision.
	if takeAppAvatar && !sameAvatar {
		newAvatar = it.avatar
	}
```

and

```go
	appBehind := wantHash != it.hash || newAvatar != it.avatar
```

with

```go
	appBehind := wantHash != it.hash || (newAvatar != it.avatar && !sameAvatar)
```

When `newAvatar` is still the library's `avatar`, `sameAvatar` is exactly whether the app already has that picture. When it's `it.avatar`, the first comparison is false.

- [ ] **Step 4: Use it in the comparison Review shows**

In `internal/targets/reconcile_compare.go`, replace

```go
	if lib.avatar != it.avatar {
```

with

```go
	same, err := s.lib.SameImage(ctx, lib.avatar, it.avatar)
	if err != nil {
		return c, err
	}
	if !same {
```

- [ ] **Step 5: Run the targets tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/targets`
Expected: `ok`. It takes about two and a half minutes.

- [ ] **Step 6: Commit**

```bash
git add internal/targets/resolve.go internal/targets/reconcile_compare.go internal/targets/reconcile_test.go
git commit -m "fix(targets): an app avatar with the library's pixels isn't a change"
```

---

### Task 4: full check

- [ ] **Step 1: Run everything**

Run: `go vet ./... && INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./...`
Expected: every package `ok` or `no test files`.

- [ ] **Step 2: Note for deploy**

Bindings already marked `app_behind` keep it until their next resolve or push. On prod they clear when the library is wiped and synced again after step 2 of the spec.
