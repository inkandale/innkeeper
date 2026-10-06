# Lumiverse Get Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lumiverse's `target.get` returns the avatar and the card's world book links, as SillyTavern's does, so imports and reconcile see the whole character.

**Architecture:** All changes are in `innkeeper-plugins/lumiverse`. The plugin keeps `blob_tmp` from `initialize`, reads the avatar from `GET /characters/{id}/avatar` when asked, and reads links from `extensions.world_book_ids`.

**Tech Stack:** Go. No new dependencies.

**Spec:** `2026-10-05-reconcile-design.md`, step 1.

---

## Design notes

- **Avatar.** `GET /api/v1/characters/{id}/avatar` with no `size` returns the full image: the crop if the owner set one, else the original (Lumiverse `characters.routes.ts`, `app.get("/:id/avatar")`). A `404` means no avatar: reply without one, not with an error. Write the bytes with `pluginio.WriteTmp(p.blobTmp, "avatar-*"+avatarExt(img), img)`.
- **Raw replies.** `decode` only unmarshals JSON. When `out` is `*[]byte`, it stores the body as is.
- **Links.** `extensions.world_book_ids` (a list of IDs), or the older single `world_book_id` when the list is absent. Return `[]string{}`, not nil, when there are none, so the core records "links none" rather than "unknown". Both keys stay out of the returned card, as now.
- **Another app's keys.** Characters imported into Lumiverse from SillyTavern keep SillyTavern's `extensions.fav` and `extensions.world` (67 and 38 of 96 on the first server). The SillyTavern plugin drops both on read, so Lumiverse drops them too; otherwise the same card hashes differently per app.

## Tasks

### Task 1: Raw replies and `blob_tmp`

**Files:** `lumiverse/client.go`, `lumiverse/main.go`, `lumiverse/client_test.go`, `lumiverse/live_test.go`

- [ ] Test `TestDecodeRaw`: a fake route returning `image/png` bytes, read with `var b []byte; c.call(ctx, "GET", path, nil, &b)`, equals the bytes.
- [ ] Run `go test ./lumiverse -run TestDecodeRaw`; it fails with `bad reply`.
- [ ] In `decode`, before `json.Unmarshal`:
  ```go
  if raw, ok := out.(*[]byte); ok {
  	*raw = body
  	return nil
  }
  ```
- [ ] `type plugin struct { c *client; blobTmp string }`; `initialize` stores `p.BlobTmp`. Update `newPlugin` in `client_test.go` and the literal in `live_test.go` to set `blobTmp: t.TempDir()`.
- [ ] `go test ./lumiverse` passes. Commit: `Read raw replies, and keep blob_tmp in the Lumiverse plugin.`

### Task 2: Avatar

**Files:** `lumiverse/cards.go`, `lumiverse/fake_test.go`, `lumiverse/cards_test.go`

- [ ] Fake: `images map[string][]byte` by character ID. `f.avatar` (the upload) stores the uploaded bytes there. New route `"GET /characters/{id}/avatar": f.getAvatar` writes them with `Content-Type: image/png`, or `404 {"error":"Not found"}` when there are none.
- [ ] Test `TestGetAvatar`: put a card with `Avatar: avatarFile(t, "abc")`, get with `Avatar: true`; the returned path is under `p.blobTmp` and its bytes equal `f.images[id]`.
- [ ] Test `TestGetNoAvatar`: a character with no image, get with `Avatar: true`; no error and `Avatar == ""`.
- [ ] Test: get without `Avatar` makes no avatar request (count requests in the fake).
- [ ] Run the three; they fail.
- [ ] In `get`, after building the card:
  ```go
  res := protocol.TargetGetResult{Card: raw, LorebookRemoteIDs: links}
  if in.Avatar {
  	var img []byte
  	err := p.c.call(ctx, "GET", "/characters/"+url.PathEscape(in.RemoteID)+"/avatar", nil, &img)
  	switch {
  	case notFound(err):
  	case err != nil:
  		return nil, err
  	default:
  		if res.Avatar, err = pluginio.WriteTmp(p.blobTmp, "avatar-*"+avatarExt(img), img); err != nil {
  			return nil, protocol.NewError(-32000, "write the avatar: "+err.Error(), false)
  		}
  	}
  }
  return res, nil
  ```
- [ ] Tests pass. Commit: `Return the avatar from Lumiverse's target.get.`

### Task 3: Links and SillyTavern's keys

**Files:** `lumiverse/cards.go`, `lumiverse/cards_test.go`

- [ ] Test `TestGetLorebookLinks`: put with `LorebookRemoteIDs: []string{"wb1", "wb2"}`; get returns `[]string{"wb1", "wb2"}`. A character with only `world_book_id: "wb3"` returns `[]string{"wb3"}`. One with neither returns a non-nil empty slice.
- [ ] Test `TestGetDropsSillyTavernKeys`: set `extensions.fav = true` and `extensions.world = "Dwarves"` on the stored character; neither is in the returned card's extensions.
- [ ] Run them; they fail.
- [ ] The export route leaves `world_book_ids` out (see the fake's `exportCharacter`), so read the links from `GET /characters/{id}` before the export:
  ```go
  var c character
  if err := p.c.call(ctx, "GET", "/characters/"+url.PathEscape(in.RemoteID), nil, &c); err != nil {
  	return nil, err
  }
  src := c.Extensions
  links := []string{}
  if ids, ok := src["world_book_ids"].([]any); ok {
  	for _, id := range ids {
  		if s, ok := id.(string); ok && s != "" {
  			links = append(links, s)
  		}
  	}
  } else if id, _ := src["world_book_id"].(string); id != "" {
  	links = append(links, id)
  }
  ```
- [ ] Add `"fav", "world"` to the keys skipped alongside `innkeeperKey`, with a comment: `// SillyTavern's own keys, carried in by a SillyTavern import.`
- [ ] Tests pass. `go test ./...` passes. Commit: `Return Lumiverse's world book links, and drop SillyTavern's keys on read.`

### Task 4: Live check and README

**Files:** `lumiverse/live_test.go`, `lumiverse/README.md`

- [ ] In the live test, get the created character with `Avatar: true` and check that a file comes back and the links match what was put.
- [ ] README: under what `get` returns, add the avatar and links, and that `fav` and `world` are dropped.
- [ ] Run the live test against a throwaway Lumiverse if one is available; otherwise say so in the PR.
- [ ] Commit, push the branch, open a PR. After merge, the next core image build bundles it (`ci: bundle innkeeper-plugins' latest main commit`).
