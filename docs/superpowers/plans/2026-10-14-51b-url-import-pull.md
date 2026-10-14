# url-import Pulls Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `url-import` answers `target.get` and `target.get_lorebook` for a source's `kind:ref`, so the core can check each card's site page for the author's changes.

**Architecture:** INN-51, spec `docs/superpowers/specs/2026-10-14-source-updates-design.md`. This plan implements [the contract for 51b](2026-10-14-51a-source-updates-core.md#contract-for-51b-and-51c) and nothing else.
- `url-import` gains the `pull` capability, never `push`, and declares `source_kinds = ["chub", "risu", "character-tavern"]`.
- `target.get` turns `kind:ref` into the page URL with the existing `sourceKinds` table, then fetches it with the existing site resolver. Import and pull share one decode step.
- A gone page answers `-32004`. A request waits so that requests to one host are at least a second apart.

**Tech Stack:** Go (`net/http`, `httptest`).

**Order:** Tasks 1–4 build on the core that `go.mod` pins today. Task 5 needs the core's `source_kinds` manifest key, from 51a Task 1, because the core refuses a manifest with unknown keys. Merge all of 51a before this plan's branch, so a core never meets a `url-import` it can't load.

**Repo:** every path is relative to the plugins repo, `github.com/davidgibbons/innkeeper-plugins`. Don't work in `~/git/innkeeper-plugins`, the main checkout; other sessions use it. Make a worktree:

```bash
git -C ~/git/innkeeper-plugins fetch
git -C ~/git/innkeeper-plugins worktree add ../innkeeper-plugins-inn-51b -b inn-51b-url-import-pull origin/main
cd ~/git/innkeeper-plugins-inn-51b
```

No commit may be marked as a breaking change.

**Stable output.** The core flags an update whenever two fetches of an unchanged page differ, so this plan checked what the sites send. Fetched twice on 2026-10-14, chub's `card.json` (it's a file in the card's git repo), chub's avatar PNG, and two RisuRealm `json-v3` downloads were each byte-identical. chub's `extensions.chub` holds `id`, `full_path`, `expressions`, `alt_expressions`, and `related_lorebooks`; none of them are view counts or dates. The core's content hash already drops `creation_date` and `modification_date`, and `CardData` drops the app-local `extensions.fav` and `extensions.world`. So nothing in `data` is stripped. Two things would still cause false updates, and this plan fixes both:
- **`data.source`.** The import adds the page URL to it, but a card imported from a file has none. `target.describe` leaves `source` out of `fields`, so it's never compared. It lists pages Innkeeper linked, not anything the author wrote.
- **chub's avatar is the card PNG** (`chara_card_v2.png`), with the card text in a `chara` chunk. When the author edits the text, the avatar's bytes change too, so every text update would also look like a new avatar, and autosync would always skip it. Avatars lose their card chunks before they're returned (Task 3).

Character Tavern wasn't fetched live. Task 6's live test covers it.

---

## File map

| File | Change |
|---|---|
| `url-import/fetch.go` | `gone` error (-32004) for 404 and 410; per-host spacing in `fetcher.get` |
| `url-import/fetch_test.go` | New: the spacing test |
| `url-import/chub.go` | A hidden lorebook or a missing card ID is `gone` |
| `url-import/import.go` | `decodeCard` and `siteAvatar`, shared by `emit` and `target.get` |
| `url-import/target.go` | New: `targetInfo`, `follow`, `getCard`, `getLorebook` |
| `url-import/target_test.go` | New |
| `url-import/main.go` | Route the target methods; `pull` in `initialize`; version 0.3.0 |
| `url-import/plugin.toml`, `index.toml` | `pull`, `source_kinds`, version 0.3.0 |
| `url-import/README.md` | Document following pages |
| `url-import/real_test.go` | `TestRealFollow`: two live fetches must match |
| `url-import/chub_test.go` | Stricter error codes; the avatar carries no card |
| `go.mod`, `go.sum` | Pin a core with `source_kinds` |

---

### Task 1: Gone pages answer -32004

The core shows `-32004` as "No longer on chub". Today a 404 and a hidden lorebook are a plain permanent error. A 401 or 403 stays a plain permanent error: Cloudflare answers 403 when it blocks a client, and a block shouldn't mark every card as gone.

**Files:**
- Modify: `url-import/fetch.go` (the status `switch` in `get`; helpers at the bottom)
- Modify: `url-import/chub.go` (the two `permanent(...)` calls for a missing lorebook and a missing card ID)
- Test: `url-import/chub_test.go`

- [ ] **Step 1: Write the failing test**

In `url-import/chub_test.go`, in `TestChubLorebook`, replace:

```go
	if _, err := importURL(t, chubSite(t), "https://chub.ai/lorebooks/u/hidden"); rpcErr(t, err).Retryable() {
		t.Errorf("hidden lorebook: err = %v", err)
	}
```

with:

```go
	if _, err := importURL(t, chubSite(t), "https://chub.ai/lorebooks/u/hidden"); rpcErr(t, err).Code != protocol.CodeRemoteNotFound || rpcErr(t, err).Retryable() {
		t.Errorf("hidden lorebook: err = %v, want -32004", err)
	}
```

In `TestChubBadPaths`, replace:

```go
	if _, err := importURL(t, f, "https://chub.ai/characters/u/gone"); rpcErr(t, err).Retryable() {
		t.Errorf("missing card: err = %v, want not retryable", err)
	}
```

with:

```go
	if _, err := importURL(t, f, "https://chub.ai/characters/u/gone"); rpcErr(t, err).Code != protocol.CodeRemoteNotFound || rpcErr(t, err).Retryable() {
		t.Errorf("missing card: err = %v, want -32004", err)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./url-import -run 'TestChubLorebook|TestChubBadPaths'`
Expected: FAIL, with `want -32004` for both.

- [ ] **Step 3: Implement**

In `url-import/fetch.go`, replace this case in `get`:

```go
	case resp.StatusCode == http.StatusNotFound:
		return nil, permanent(fmt.Errorf("not found on %s", site))
```

with:

```go
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return nil, gone(fmt.Errorf("not found on %s", site))
```

At the end of `url-import/fetch.go`, add:

```go
// gone is a page that was deleted, made private, or hides its definition.
func gone(err error) error {
	return protocol.NewError(protocol.CodeRemoteNotFound, err.Error(), false)
}
```

In `url-import/chub.go`, replace:

```go
			return found{}, permanent(errors.New("chub.ai sent no lorebook; its definition may be hidden"))
```

with:

```go
			return found{}, gone(errors.New("chub.ai sent no lorebook; its definition may be hidden"))
```

and replace:

```go
		return found{}, permanent(errors.New("chub.ai sent no card ID"))
```

with:

```go
		return found{}, gone(errors.New("chub.ai sent no card ID; its definition may be hidden"))
```

`getAvatar` keeps a card whose picture is gone, since it lets through any error that isn't retryable.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./url-import`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add url-import/fetch.go url-import/chub.go url-import/chub_test.go
git commit -m "feat(url-import): answer -32004 for a page that is gone"
```

---

### Task 2: Space requests to one host

The core checks sources one at a time, but each check is two or three requests, and a library of 93 chub cards would send them as fast as chub answers. A shared `fetcher` makes each request to a host wait until a second has passed since the last one. Imports go through it too.

**Files:**
- Modify: `url-import/fetch.go` (`fetcher`, `newFetcher`, `get`)
- Create: `url-import/fetch_test.go`

- [ ] **Step 1: Write the failing test**

Create `url-import/fetch_test.go`:

```go
package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestFetcherSpacesRequestsToOneHost(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.chub.ai/x", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET avatars.charhub.io/x", func(http.ResponseWriter, *http.Request) {})
	f := sites(t, mux)
	f.gap = 200 * time.Millisecond
	start := time.Now()
	// The second host doesn't wait; the third request waits for the first's gap.
	for _, u := range []string{"https://api.chub.ai/x", "https://avatars.charhub.io/x", "https://api.chub.ai/x"} {
		if _, err := f.get(context.Background(), "test", u); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < f.gap || d >= 2*f.gap {
		t.Errorf("three requests took %v, want one gap of %v", d, f.gap)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./url-import -run TestFetcherSpacesRequestsToOneHost`
Expected: FAIL to compile: `f.gap undefined`.

- [ ] **Step 3: Implement**

In `url-import/fetch.go`, add `"sync"` to the imports and replace:

```go
// fetcher makes the plugin's requests to sites.
type fetcher struct{ http *http.Client }

func newFetcher() *fetcher {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: refusePrivate}).DialContext
	return &fetcher{http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: httpsOnly, Transport: tr}}
}
```

with:

```go
// fetcher makes the plugin's requests to sites.
type fetcher struct {
	http *http.Client
	gap  time.Duration // the least time between two requests to one host

	mu   sync.Mutex
	next map[string]time.Time // when each host may next be asked
}

func newFetcher() *fetcher {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Control: refusePrivate}).DialContext
	return &fetcher{http: &http.Client{Timeout: 30 * time.Second, CheckRedirect: httpsOnly, Transport: tr}, gap: time.Second}
}

// wait holds a request to host until gap has passed since the last one.
// ponytail: one gap for every site and none for redirects; per-site gaps if a site asks for more.
func (f *fetcher) wait(ctx context.Context, host string) error {
	if f.gap == 0 {
		return nil
	}
	f.mu.Lock()
	at := time.Now()
	if n := f.next[host]; n.After(at) {
		at = n
	}
	if f.next == nil {
		f.next = map[string]time.Time{}
	}
	f.next[host] = at.Add(f.gap)
	f.mu.Unlock()
	select {
	case <-time.After(time.Until(at)):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

In `get`, after the `https` check and before `req.Header.Set("User-Agent", ...)`, add:

```go
	if err := f.wait(ctx, req.URL.Host); err != nil {
		return nil, err
	}
```

`sites` in `import_test.go` builds a `fetcher` with no gap, so the resolver tests don't slow down.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./url-import`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add url-import/fetch.go url-import/fetch_test.go
git commit -m "feat(url-import): space out requests to one site"
```

---

### Task 3: One decode for import and pull, with avatars that carry no card

`emit` decodes the card, adds the page to `data.source`, and picks the avatar. `target.get` needs the same three steps, so they move into `decodeCard`. The avatar also loses its card chunks, so chub's `chara_card_v2.png` changes only when its picture does. `card.Decode` already returns a PNG without its card chunks.

**Files:**
- Modify: `url-import/import.go` (`emit`)
- Test: `url-import/chub_test.go` (`TestChubCard`)

- [ ] **Step 1: Write the failing test**

In `url-import/chub_test.go`, add `"errors"`, `"os"`, and `"github.com/davidgibbons/innkeeper-plugins/card"` to the imports. In `TestChubCard`, after the `if ev.Item != item ...` block, inside the loop, add:

```go
		b, err := os.ReadFile(ev.Avatar)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := card.Decode(b); !errors.Is(err, card.ErrNotCard) {
			t.Errorf("%s: the avatar still carries the card: err = %v", page, err)
		}
```

The fixture avatar, `card/testdata/v2-seraphina.png`, has a `chara` chunk, as chub's do.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./url-import -run TestChubCard`
Expected: FAIL with `the avatar still carries the card: err = <nil>`.

- [ ] **Step 3: Implement**

In `url-import/import.go`, replace the card half of `emit`:

```go
	c, img, err := card.Decode(got.card)
	if err != nil {
		return permanent(fmt.Errorf("%s: %w", got.item, err))
	}
	if c, err = withSource(c, got.item); err != nil {
		return permanent(fmt.Errorf("%s: %w", got.item, err))
	}
	if got.avatar != nil {
		if png, err := card.AsPNG(got.avatar); err == nil {
			img = png
		} else {
			// The card matters more than its picture; keep the one Decode returned.
			log.Printf("%s: no avatar: %v", got.item, err)
		}
	}
	ev := protocol.EmitCard{Run: run, Item: got.item, Card: c}
```

with:

```go
	c, img, err := decodeCard(got)
	if err != nil {
		return err
	}
	ev := protocol.EmitCard{Run: run, Item: got.item, Card: c}
```

and add after `emit`:

```go
// decodeCard reads the card got holds as CCv3, with its page in data.source,
// and its avatar as a PNG, or nil.
func decodeCard(got found) (json.RawMessage, []byte, error) {
	c, img, err := card.Decode(got.card)
	if err != nil {
		return nil, nil, permanent(fmt.Errorf("%s: %w", got.item, err))
	}
	if c, err = withSource(c, got.item); err != nil {
		return nil, nil, permanent(fmt.Errorf("%s: %w", got.item, err))
	}
	if got.avatar != nil {
		if png, err := siteAvatar(got.avatar); err == nil {
			img = png
		} else {
			// The card matters more than its picture; keep the one Decode returned.
			log.Printf("%s: no avatar: %v", got.item, err)
		}
	}
	return c, img, nil
}

// siteAvatar returns a site's picture as a PNG without card chunks, so its
// bytes change only when the picture does. chub serves the card PNG itself.
func siteAvatar(b []byte) ([]byte, error) {
	if _, img, err := card.Decode(b); err == nil && img != nil {
		b = img
	}
	return card.AsPNG(b)
}
```

A JPEG or a PNG without a card fails `card.Decode` and goes to `AsPNG` unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./url-import`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add url-import/import.go url-import/chub_test.go
git commit -m "feat(url-import): import avatars without their card chunks"
```

---

### Task 4: Answer target.describe, target.get, and target.get_lorebook

**Files:**
- Create: `url-import/target.go`, `url-import/target_test.go`
- Modify: `url-import/main.go` (the method `switch`)

- [ ] **Step 1: Write the failing tests**

Create `url-import/target_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/davidgibbons/innkeeper/protocol"
)

// get runs target.get for remoteID.
func get(t *testing.T, f *fetcher, remoteID string, avatar bool) (protocol.TargetGetResult, error) {
	t.Helper()
	raw, _ := json.Marshal(protocol.TargetGetParams{RemoteID: remoteID, Avatar: avatar})
	res, err := getCard(context.Background(), f, t.TempDir(), raw)
	if err != nil {
		return protocol.TargetGetResult{}, err
	}
	return res.(protocol.TargetGetResult), nil
}

// getBook runs target.get_lorebook for remoteID.
func getBook(t *testing.T, f *fetcher, remoteID string) (protocol.TargetGetLorebookResult, error) {
	t.Helper()
	raw, _ := json.Marshal(protocol.TargetGetParams{RemoteID: remoteID})
	res, err := getLorebook(context.Background(), f, raw)
	if err != nil {
		return protocol.TargetGetLorebookResult{}, err
	}
	return res.(protocol.TargetGetLorebookResult), nil
}

func TestTargetDescribeLeavesOutBooksAndSources(t *testing.T) {
	if err := targetInfo.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"character_book", "source"} {
		if slices.Contains(targetInfo.Fields, field) {
			t.Errorf("fields include %s", field)
		}
	}
	if !targetInfo.Pull || targetInfo.Update {
		t.Errorf("pull %v, update %v; want a target that only pulls", targetInfo.Pull, targetInfo.Update)
	}
}

func TestTargetListsAreEmpty(t *testing.T) {
	b, _ := json.Marshal(noItems)
	if string(b) != `{"items":[]}` {
		t.Errorf("lists answer %s", b)
	}
}

func TestTargetGetChubCard(t *testing.T) {
	f := chubSite(t)
	res, err := get(t, f, "chub:u/seraphina", true)
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Spec string `json:"spec"`
		Data struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Card, &c); err != nil || c.Spec != "chara_card_v3" || c.Data.Name == "" {
		t.Fatalf("card %s, err %v", res.Card, err)
	}
	if res.Avatar == "" {
		t.Error("no avatar, though asked for one")
	}
	again, err := get(t, f, "chub:u/seraphina", false)
	if err != nil {
		t.Fatal(err)
	}
	if again.Avatar != "" {
		t.Error("an avatar, though not asked for one")
	}
	if !bytes.Equal(again.Card, res.Card) {
		t.Error("two fetches of an unchanged page differ")
	}
}

func TestTargetGetGone(t *testing.T) {
	f := chubSite(t)
	for _, id := range []string{"chub:u/gone", "chub:onlyone", "janitor:a/b", "nokind", "risu:not-a-uuid"} {
		_, err := get(t, f, id, false)
		if e := rpcErr(t, err); e.Code != protocol.CodeRemoteNotFound || e.Retryable() {
			t.Errorf("%s: err = %v, want -32004", id, err)
		}
	}
}

func TestTargetGetOutageIsRetryable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET api.chub.ai/api/characters/u/down", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	if _, err := get(t, sites(t, mux), "chub:u/down", false); !rpcErr(t, err).Retryable() {
		t.Errorf("err = %v, want retryable", err)
	}
}

func TestTargetGetChubLorebook(t *testing.T) {
	f := chubSite(t)
	got, err := getBook(t, f, "chub:u/eldoria")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lorebook) == 0 {
		t.Fatal("no lorebook")
	}
	// RisuRealm and Character Tavern have no lorebook pages.
	for _, id := range []string{"chub:u/hidden", "risu:09db4bf3-d6c7-467c-a35c-71b38b0299ba", "character-tavern:a/b"} {
		_, err := getBook(t, f, id)
		if e := rpcErr(t, err); e.Code != protocol.CodeRemoteNotFound || e.Retryable() {
			t.Errorf("%s: err = %v, want -32004", id, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./url-import -run TestTarget`
Expected: FAIL to compile: `undefined: getCard`, `undefined: targetInfo`, `undefined: noItems`.

- [ ] **Step 3: Implement**

Create `url-import/target.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/davidgibbons/innkeeper-plugins/card"
	"github.com/davidgibbons/innkeeper-plugins/internal/pluginio"
	"github.com/davidgibbons/innkeeper/protocol"
)

// targetInfo answers target.describe. url-import follows a source's page
// and never pushes. fields leave out character_book, which the core doesn't
// compare, and source, which lists the pages Innkeeper linked rather than
// anything the author wrote.
var targetInfo = protocol.TargetInfo{
	Fields: []string{"name", "nickname", "description", "personality", "scenario", "first_mes", "mes_example",
		"alternate_greetings", "group_only_greetings", "system_prompt", "post_history_instructions",
		"creator_notes", "creator_notes_multilingual", "tags", "creator", "character_version", "extensions", "assets"},
	Pull:           true,
	Lorebooks:      protocol.LorebooksStandalone,
	LorebookFields: []string{"name", "description", "scan_depth", "token_budget", "recursive_scanning", "extensions", "entries"},
}

// noItems answers target.list and target.list_lorebooks: there is nothing to
// bind except the sources the core already knows.
var noItems = protocol.TargetListResult{Items: []protocol.TargetItem{}}

// follow fetches the page that remoteID, kind:ref, names. A remote_id that
// url-import can't read is gone, so the core stops expecting it.
func follow(ctx context.Context, f *fetcher, remoteID string, lorebook bool) (found, error) {
	kind, ref, _ := strings.Cut(remoteID, ":")
	toSource, ok := sourceKinds[kind]
	var s protocol.Source
	if ok {
		s, ok = toSource(ref, lorebook)
	}
	if !ok {
		return found{}, gone(fmt.Errorf("url-import can't follow %q", remoteID))
	}
	u, err := url.Parse(s.URL)
	if err != nil {
		return found{}, permanent(err)
	}
	return hosts[u.Hostname()].resolve(ctx, f, u)
}

// getCard answers target.get.
func getCard(ctx context.Context, f *fetcher, blobTmp string, raw json.RawMessage) (any, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, invalid(err)
	}
	got, err := follow(ctx, f, p.RemoteID, false)
	if err != nil {
		return nil, err
	}
	c, img, err := decodeCard(got)
	if err != nil {
		return nil, err
	}
	res := protocol.TargetGetResult{Card: c}
	if p.Avatar && img != nil {
		if res.Avatar, err = pluginio.WriteTmp(blobTmp, "avatar-*.png", img); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// getLorebook answers target.get_lorebook.
func getLorebook(ctx context.Context, f *fetcher, raw json.RawMessage) (any, error) {
	var p protocol.TargetGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, invalid(err)
	}
	got, err := follow(ctx, f, p.RemoteID, true)
	if err != nil {
		return nil, err
	}
	book, err := card.DecodeLorebook(got.lorebook, got.name)
	if err != nil {
		return nil, permanent(fmt.Errorf("%s: %w", got.item, err))
	}
	return protocol.TargetGetLorebookResult{Lorebook: book}, nil
}
```

Each source's URL host is a key of `hosts` (`chub.ai`, `realm.risuai.net`, `character-tavern.com`), so the lookup in `follow` always finds a resolver. Only `chubSource` gives a lorebook page, and its URL is under `/lorebooks/`, where `chub` returns a lorebook.

In `url-import/main.go`, add these cases to the method `switch`, after `MethodSitesParse`:

```go
		case protocol.MethodTargetDescribe:
			return targetInfo, nil
		case protocol.MethodTargetList, protocol.MethodTargetListLorebooks:
			return noItems, nil
		case protocol.MethodTargetGet:
			tmp := blobTmp.Load()
			if tmp == nil {
				return nil, errors.New("target.get before initialize")
			}
			return getCard(ctx, f, *tmp, params)
		case protocol.MethodTargetGetLorebook:
			return getLorebook(ctx, f, params)
```

`target.put` and `target.put_lorebook` fall through to `MethodNotFound`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./url-import && go test -race ./url-import`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add url-import/target.go url-import/target_test.go url-import/main.go
git commit -m "feat(url-import): answer target.get for a source's page"
```

---

### Task 5: Declare pull and source_kinds, and release 0.3.0

This needs a core with 51a merged. Until then, test against the 51a worktree with a `replace` that you never commit.

**Files:**
- Modify: `go.mod`, `go.sum`
- Modify: `url-import/plugin.toml`, `index.toml` (the `url-import` entry), `url-import/main.go` (`version`, `initialize`), `url-import/README.md`

- [ ] **Step 1: Pin the core**

After 51a merges, pin core `main`:

```bash
go get github.com/davidgibbons/innkeeper@main
go mod tidy
git diff go.mod
```

Expected: only the `github.com/davidgibbons/innkeeper` version line changes, to a pseudo-version dated after 51a merged.

To test before 51a merges, point at the worktree where 51a is built instead. Drop the replace before every commit:

```bash
go mod edit -replace github.com/davidgibbons/innkeeper=/Users/dgibbons/git/<51a worktree>
# ...test...
go mod edit -dropreplace github.com/davidgibbons/innkeeper
```

- [ ] **Step 2: Commit the pin**

```bash
git add go.mod go.sum
git commit -m "chore: pin the core with source_kinds"
```

- [ ] **Step 3: Declare the capability**

Replace `url-import/plugin.toml` with:

```toml
name = "url-import"
version = "0.3.0"
protocol = 1
command = ["./url-import"]
capabilities = ["import", "sites", "pull"]
inputs = ["url"]
source_kinds = ["chub", "risu", "character-tavern"]
```

In `index.toml`, replace the `url-import` entry's lines from `version` through `description` with:

```toml
version = "0.3.0"
protocol = 1
capabilities = ["import", "sites", "pull"]
description = "Import a card or lorebook from its page URL on chub.ai, RisuRealm, or Character Tavern, link library items to their pages there, and check those pages for the author's changes. Needs a core with source_kinds."
```

In `url-import/main.go`, change `const version = "0.2.0"` to `const version = "0.3.0"`, and the `initialize` reply's capabilities to:

```go
				Capabilities: []string{"import", "sites", "pull"}}, nil
```

Also change the package comment's last sentence to: `...and is a sites plugin for the same sites, which follows their pages for the author's changes.`

- [ ] **Step 4: Document following**

In `url-import/README.md`, after the paragraph that starts "It is also a `sites` plugin", add:

```markdown
It also pulls: the core checks each source of kind `chub`, `risu`, or
`character-tavern` by calling `target.get` with `remote_id` `kind:ref`, such
as `chub:alice/vyr`, and `target.get_lorebook` for a chub lorebook. A page
that is gone, private, or hides its definition answers `-32004`. A timeout, a
5xx, or a 429 is retryable. It never pushes. Requests to one site are at least
a second apart. Avatars come back without card chunks, so chub's card PNG
changes only when its picture does.
```

And replace the "To try the live sites" block with:

````markdown
To try the live sites:

```bash
URL_IMPORT_REAL="https://chub.ai/characters/<user>/<slug> https://realm.risuai.net/character/<uuid>" go test ./url-import -run TestReal -v
URL_IMPORT_FOLLOW="chub:<user>/<slug> risu:<uuid> character-tavern:<author>/<slug>" go test ./url-import -run TestRealFollow -v
```
````

- [ ] **Step 5: Run the checks**

Run: `go vet ./... && go test -race ./... && make check`
Expected: PASS. `make check` loads `plugin.toml` through the pinned core, which accepts `source_kinds` only with `pull` and without `push`.

- [ ] **Step 6: Commit**

```bash
git add url-import/plugin.toml index.toml url-import/main.go url-import/README.md
git commit -m "feat(url-import): release 0.3.0, which follows source pages"
```

---

### Task 6: Live check that a page fetches the same twice

**Files:**
- Modify: `url-import/real_test.go`

- [ ] **Step 1: Add the test**

Replace `url-import/real_test.go` with:

```go
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestReal imports each page URL_IMPORT_REAL lists, separated by spaces, from
// the live sites. Pick SFW cards that are public.
func TestReal(t *testing.T) {
	pages := strings.Fields(os.Getenv("URL_IMPORT_REAL"))
	if len(pages) == 0 {
		t.Skip("URL_IMPORT_REAL is not set")
	}
	f := newFetcher()
	for _, page := range pages {
		sent, err := importURL(t, f, page)
		if err != nil {
			t.Errorf("%s: %v", page, err)
			continue
		}
		t.Logf("%s: sent %d notifications", page, len(sent))
	}
}

// TestRealFollow fetches each remote_id URL_IMPORT_FOLLOW lists, such as
// chub:{user}/{slug}, twice from the live sites. Two copies that differ would
// flag an update on every check.
func TestRealFollow(t *testing.T) {
	ids := strings.Fields(os.Getenv("URL_IMPORT_FOLLOW"))
	if len(ids) == 0 {
		t.Skip("URL_IMPORT_FOLLOW is not set")
	}
	f := newFetcher()
	for _, id := range ids {
		a, err := get(t, f, id, true)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		b, err := get(t, f, id, true)
		if err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		if !bytes.Equal(a.Card, b.Card) || fileSum(t, a.Avatar) != fileSum(t, b.Avatar) {
			t.Errorf("%s: two fetches differ", id)
		}
	}
}

// fileSum is the SHA-256 of the file at path, or "" for no file.
func fileSum(t *testing.T, path string) string {
	t.Helper()
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}
```

- [ ] **Step 2: Run it against the live sites**

These two pages were public and SFW on 2026-10-14. Add a Character Tavern card you know is public.

Run: `URL_IMPORT_FOLLOW="chub:horcocks/yes-my-liege risu:09db4bf3-d6c7-467c-a35c-71b38b0299ba character-tavern:<author>/<slug>" go test ./url-import -run TestRealFollow -v`
Expected: PASS. If a site fails it, find the value that changed between the two copies and leave it out in that site's resolver before going on.

- [ ] **Step 3: Commit**

```bash
git add url-import/real_test.go
git commit -m "test(url-import): check that two live fetches of a page match"
```

---

### Task 7: Verify

- [ ] **Step 1: Run what CI runs**

Run: `go vet ./... && go test -race ./... && make check`
Expected: PASS.

- [ ] **Step 2: Check the contract**

Compare against [the contract for 51b](2026-10-14-51a-source-updates-core.md#contract-for-51b-and-51c):

| Item | Where |
|---|---|
| Manifest: `pull`, no `push`, `source_kinds` | `url-import/plugin.toml` |
| `target.describe`: no `character_book`, `pull` true, `update` false, `standalone` with `lorebook_fields`, no site contact | `targetInfo` |
| `target.get`: `{card}`, plus `avatar` when asked, no `lorebook_remote_ids` | `getCard` |
| `target.get_lorebook`: `{lorebook}` | `getLorebook` |
| Gone: `-32004`, not retryable | `gone`, `follow` |
| Transient: retryable | `fetcher.get` (unchanged) |
| Lists: `{"items": []}` | `noItems` |
| Put: method not found | `main.go`'s default |
| Stable output | Task 6 |
| Pacing | `fetcher.wait` |

- [ ] **Step 3: Read the diff**

Run: `git diff origin/main --stat && git diff origin/main`
Check for a committed `replace` in `go.mod`, dead code, and comments that narrate the change.
