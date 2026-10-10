# 37d: the `chub-archive` plugin — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `chub-archive` catalog plugin ingests the chub.ai archive's SQL dump into `catalogkit` and answers `catalog.search`, `get`, `similar`, and `ingest`, with avatars read from the archive's image folders.

**Architecture:** The plugin streams the dump's `COPY` blocks itself, one row at a time, and checkpoints by byte offset. Rows from chub's per-item tables (`chub_character`, `chub_lorebook`) go into a plugin table first, because pg_dump writes them before their `_def` tables. Each `_def` row becomes one version, which `catalogkit.Put` stores under the row's `increment`. `catalogkit` gains plugin-numbered versions, author, tag, and added-date overrides, numbers for `number_range` filters, and `enum` and `author` filters.

**Tech Stack:** Go, PostgreSQL 18 (pgx v5), the `card` codec, `cardhash`.

Spec: `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, build step 4. Plan 37c built `catalogkit` and `folder`.

**What the dump holds.** Read from `/Volumes/misc/char-archive/extracted/sql/database_01-05-2025/char_archive_database_01-05-2025.sql` (11.9 GB):

| Table | Rows | Item ID column | `increment` |
|---|---|---|---|
| `chub_character` (per-item: `data.rating`, `data.topics`, `added`, `hidden`) | 172,311 | `id` | — |
| `chub_character_def` | 248,125 | `id` | yes |
| `chub_lorebook` (per-item) | 4,951 | `id` | — |
| `chub_lorebook_def` | 12,245 | `id` | yes |
| `booru_character_def` | 2,339 | `id` | **no** |
| `generic_character_def` | 84,009 | `card_data_hash` | yes |
| `nyaime_character_def` | 744 | `id` | yes |
| `risuai_character_def` | 2,746 | `id` | yes |
| `webring_character_def` | 899 | `card_data_hash` | yes |

- `definition` is a V2 card (`"spec": "chara_card_v2"`) or, in `chub_lorebook_def`, a `character_book`. `card.Decode` and `card.DecodeLorebook` read them unchanged.
- `raw` is the original card JSON, zlib-compressed. The plugin doesn't read it.
- `increment` is one identity sequence per table, so one item's versions are numbers like 171584 and 171590.
- Images aren't in the dump. `image_hash` (32 hex digits) names a PNG at `<a>/<b>/<c>/<the other 29 digits>` under `sql/hashed-data_part1/hashed-data` or `sql/hashed-data_01-09-2025/hashed-data`.
- pg_dump writes text with `\\`, `\n`, `\r`, `\t`, `\b`, `\f`, and `\v` escaped, `\N` for NULL, and ends each block with `\.`.

**Changes from the spec.** Task 12 updates the spec to match:

- **Config** has `images`, a list of image folders, beside `dump`. Without it, items have no thumbnail, phash, or avatar.
- **Images** stay as files in the archive, not in the plugin's schema. The plugin keeps each version's `image_hash`.
- **Item ID** for `generic_character_def` and `webring_character_def` is the table and `card_data_hash`. Those tables have no source ID, so each row is its own item.
- **Versions** are the row's `increment`. `booru_character_def` has none, so each of its items has one version, `1`.
- **Progress** counts bytes of the dump, not rows, so ingest needs no counting pass over 12 GB first.
- **Re-ingest** skips a version it already has. A newer dump's rating and tag changes don't reach versions already ingested.
- **Nothing is marked removed.** The archive only grows, so `removed` is always 0.
- **The fixture dump** is written by a script with the real column lists and made-up rows, not cut from the real dump.

**Branches and worktrees.** **Do not** `git checkout` other branches or move HEAD in either main checkout.

| Part | Repo | Branch | Worktree |
|---|---|---|---|
| Tasks 1–11 | `innkeeper-plugins` | `inn-37d-chub-archive` | `../innkeeper-plugins-inn-37d` |
| Task 12 | `innkeeper` | `inn-37d-spec` | `../innkeeper-inn-37d-spec` |

No core change is needed: the protocol already has `enum` and `number_range` filters.

**Database tests** skip without `INNKEEPER_TEST_DATABASE_URL`, and a skipped run looks green. Run every test command with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres` set.

## Files

| File | Change |
|---|---|
| `catalogkit/catalogkit.go` | `enum`, `author`, and `nums.<key>` filters; migration 2 |
| `catalogkit/put.go` | `Entry.Version`, `Author`, `Tags`, `Numbers`, `Added`; exported `BadData` |
| `catalogkit/read.go` | SQL for the new filters |
| `catalogkit/*_test.go` | Tests |
| `chub-archive/dump.go`, `dump_test.go` (new) | Reads `COPY` rows and checkpoints |
| `chub-archive/rows.go`, `rows_test.go` (new) | Turns rows into entries |
| `chub-archive/ingest.go`, `ingest_test.go` (new) | `catalog.ingest` |
| `chub-archive/main.go`, `main_test.go` (new) | The process, `catalog.get` with avatars |
| `chub-archive/testdata/` (new) | `dump.sql`, one image |
| `chub-archive/plugin.toml`, `README.md`, `check-config.json` (new) | Manifest, docs, contract-check config |
| `index.toml`, `.gitignore` | The `chub-archive` entry and binary |
| **innkeeper** `docs/superpowers/specs/2026-10-09-archive-catalog-design.md` | Task 12 |

---

## Setup

- [ ] **Create the worktree**

```bash
cd ~/git/innkeeper-plugins
git worktree add ../innkeeper-plugins-inn-37d -b inn-37d-chub-archive main
cd ../innkeeper-plugins-inn-37d
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./catalogkit/ ./folder/
```

Expected: `ok` for both, not `[no test files]` or skips.

---

### Task 1: `catalogkit` serves `enum`, `author`, and number filters

**Files:**
- Modify: `catalogkit/catalogkit.go` (`Filter`, `check`, `migrations`)
- Modify: `catalogkit/read.go` (`filter`)
- Modify: `catalogkit/put.go` (`Entry.Numbers`, `Put`)
- Test: `catalogkit/catalogkit_test.go`, `catalogkit/read_test.go`

- [ ] **Step 1: Add the filters to the test config**

In `catalogkit/catalogkit_test.go`, replace `testConfig`'s `Filters` with:

```go
	Filters: []Filter{
		{CatalogFilter: protocol.CatalogFilter{Name: "tag", Label: "Tag", Type: protocol.FilterText}, Field: "tag"},
		{CatalogFilter: protocol.CatalogFilter{Name: "dir", Label: "Folder", Type: protocol.FilterText}, Field: "attrs.dir"},
		{CatalogFilter: protocol.CatalogFilter{Name: "changed", Label: "Changed", Type: protocol.FilterDateRange}, Field: "updated"},
		{CatalogFilter: protocol.CatalogFilter{Name: "author", Label: "Author", Type: protocol.FilterText}, Field: "author"},
		{CatalogFilter: protocol.CatalogFilter{Name: "site", Label: "Site", Type: protocol.FilterEnum,
			Options: []protocol.CatalogOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}}, Field: "attrs.site"},
		{CatalogFilter: protocol.CatalogFilter{Name: "rating", Label: "Rating", Type: protocol.FilterNumberRange}, Field: "nums.rating"},
		{CatalogFilter: protocol.CatalogFilter{Name: "added", Label: "Added", Type: protocol.FilterDateRange}, Field: "added"},
	},
```

`TestOpenRefusesUnsupportedFilter` keeps passing: a `number_range` on `attrs.rating` is still refused, because numbers live under `nums.`.

- [ ] **Step 2: Write the failing search test**

Append to `catalogkit/read_test.go`:

```go
func TestSearchEnumAuthorNumberFilters(t *testing.T) {
	s := openTest(t)
	put(t, s, Entry{ID: "x", Kind: protocol.KindCard, Data: testCard("Xan", "One."), Updated: t0,
		Attrs: map[string][]string{"site": {"a"}}, Numbers: map[string]float64{"rating": 4.5}})
	put(t, s, Entry{ID: "y", Kind: protocol.KindCard, Data: testCard("Yul", "Two."), Updated: t0,
		Attrs: map[string][]string{"site": {"b"}}, Numbers: map[string]float64{"rating": 2}})
	put(t, s, Entry{ID: "z", Kind: protocol.KindCard, Data: testCard("Zed", "Three."), Updated: t0})

	f := func(kv ...string) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = json.RawMessage(kv[i+1])
		}
		return m
	}
	cases := []struct {
		filters map[string]json.RawMessage
		want    []string
	}{
		{f("site", `"a"`), []string{"x"}},
		{f("author", `"ANN"`), []string{"x", "y", "z"}}, // testCard's creator is "ann"
		{f("author", `"bob"`), nil},
		{f("rating", `{"min": 3}`), []string{"x"}},
		{f("rating", `{"max": 4.5}`), []string{"x", "y"}},
		{f("rating", `{"min": 1, "max": 3}`), []string{"y"}},
	}
	for _, c := range cases {
		got := searchIDs(t, s, protocol.CatalogSearchParams{Sort: "name", Filters: c.filters})
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.filters, got, c.want)
		}
	}
}
```

- [ ] **Step 3: Run it to see it fail**

Run: `go test ./catalogkit/ -run 'TestSearchEnumAuthorNumberFilters|TestDescribe' -v`
Expected: a compile error, `unknown field Numbers in struct literal`.

- [ ] **Step 4: Accept the filters**

In `catalogkit/catalogkit.go`, replace the `Filter` doc comment and `check` (including the `ponytail:` line above it):

```go
// Filter is a declared filter and what it reads. Field is "tag", "author",
// or "attrs.<key>" for a text filter; "attrs.<key>" for an enum;
// "nums.<key>" for a number range; and "added" or "updated" for a date range.
type Filter struct {
	protocol.CatalogFilter
	Field string
}

func (f Filter) check() error {
	under := func(prefix string) bool { return strings.HasPrefix(f.Field, prefix) && len(f.Field) > len(prefix) }
	ok := false
	switch f.Type {
	case protocol.FilterText:
		ok = f.Field == "tag" || f.Field == "author" || under("attrs.")
	case protocol.FilterEnum:
		ok = under("attrs.")
	case protocol.FilterNumberRange:
		ok = under("nums.")
	case protocol.FilterDateRange:
		ok = f.Field == "added" || f.Field == "updated"
	}
	if !ok {
		return fmt.Errorf("filter %q: catalogkit can't serve a %s filter on %q", f.Name, f.Type, f.Field)
	}
	return nil
}
```

Append a second migration to `migrations` (after the closing `` `}`` of `catalogkit/1-items`, inside the slice):

```go
	{Name: "catalogkit/2-nums", SQL: `
ALTER TABLE item ADD nums jsonb NOT NULL DEFAULT '{}';
CREATE INDEX item_author ON item (instance, lower(author));
CREATE INDEX item_added ON item (instance, added);
`},
```

- [ ] **Step 5: Store `Numbers`**

In `catalogkit/put.go`, add to `Entry` after `Attrs`:

```go
	// Numbers are the values nums.<key> filters match.
	Numbers map[string]float64
```

In `Put`, after `attrs` is marshaled:

```go
	if e.Numbers == nil {
		e.Numbers = map[string]float64{}
	}
	nums, err := json.Marshal(e.Numbers)
	if err != nil {
		return 0, err
	}
```

In the item `INSERT`, add `nums` after `thumbnail` in the column list, `$19` after `$17` in `VALUES`, `nums = excluded.nums` to the `DO UPDATE SET` list, and `string(nums)` as the last argument after `s.instance`.

- [ ] **Step 6: Compile the filters to SQL**

In `catalogkit/read.go`, replace the `case protocol.FilterText:` branch of `filter` with:

```go
	case protocol.FilterText, protocol.FilterEnum:
		var v string
		_ = json.Unmarshal(raw, &v)
		switch f.Field {
		case "tag":
			b.where("tags @> ARRAY[" + b.arg(strings.ToLower(v)) + "::text]")
		case "author":
			b.where("lower(author) = lower(" + b.arg(v) + ")")
		default:
			match, _ := json.Marshal(map[string][]string{strings.TrimPrefix(f.Field, "attrs."): {v}})
			b.where("attrs @> " + b.arg(string(match)) + "::jsonb")
		}
	case protocol.FilterNumberRange:
		var r protocol.NumberRange
		_ = json.Unmarshal(raw, &r)
		num := "(nums->>" + b.arg(strings.TrimPrefix(f.Field, "nums.")) + ")::float8"
		if r.Min != nil {
			b.where(num + " >= " + b.arg(*r.Min))
		}
		if r.Max != nil {
			b.where(num + " <= " + b.arg(*r.Max))
		}
```

Update `filter`'s doc comment if it names the types; it doesn't need to.

- [ ] **Step 7: Run the package tests**

Run: `go test ./catalogkit/ ./folder/`
Expected: `ok` for both.

- [ ] **Step 8: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): serve enum, author, and number range filters"
```

---

### Task 2: `Entry` overrides for author, tags, and the added date

**Files:**
- Modify: `catalogkit/put.go` (`Entry`, `Put`, `derive`)
- Test: `catalogkit/put_test.go`

- [ ] **Step 1: Write the failing test**

Append to `catalogkit/put_test.go`:

```go
func TestPutOverrides(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	early := t0.Add(-24 * time.Hour)
	put(t, s, Entry{ID: "o", Kind: protocol.KindCard, Data: testCard("Ann", "One."), Updated: t0,
		Author: " Zed ", Tags: []string{"Sci-Fi", " "}, Added: t0})
	// A later put with an earlier Added moves the date back; a zero Added keeps it.
	put(t, s, Entry{ID: "o", Kind: protocol.KindCard, Data: testCard("Ann", "One."), Updated: t0, Added: early})
	put(t, s, Entry{ID: "o", Kind: protocol.KindCard, Data: testCard("Ann", "One."), Updated: t0})

	var author string
	var tags []string
	var added time.Time
	if err := s.pool.QueryRow(ctx, `SELECT author, tags, added FROM item WHERE id = 'o'`).Scan(&author, &tags, &added); err != nil {
		t.Fatal(err)
	}
	// The last put had no overrides, so the card's own creator and tags are back.
	if author != "ann" || !slices.Equal(tags, []string{"fantasy", "elf"}) || !added.Equal(early) {
		t.Fatalf("author %q, tags %v, added %v", author, tags, added)
	}

	put(t, s, Entry{ID: "p", Kind: protocol.KindCard, Data: testCard("Bo", "Two."), Updated: t0,
		Author: " Zed ", Tags: []string{"Sci-Fi", " "}})
	if err := s.pool.QueryRow(ctx, `SELECT author, tags FROM item WHERE id = 'p'`).Scan(&author, &tags); err != nil {
		t.Fatal(err)
	}
	if author != "Zed" || !slices.Equal(tags, []string{"sci-fi"}) {
		t.Fatalf("author %q, tags %v", author, tags)
	}
}
```

Add `"slices"` to the file's imports.

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./catalogkit/ -run TestPutOverrides`
Expected: a compile error, `unknown field Author in struct literal`.

- [ ] **Step 3: Add the fields**

In `catalogkit/put.go`, add to `Entry` after `Numbers`:

```go
	// Author and Tags, when not empty, replace the creator and tags in Data.
	Author string
	Tags   []string
	// Added is when the source first listed the item, or zero for the time
	// of the first Put. The item keeps the earliest Added it is given.
	Added time.Time
```

- [ ] **Step 4: Use them in `derive`**

In `derive`, replace the tag loop:

```go
	tags, _ := m["tags"].([]any)
	for _, t := range tags {
		if s, _ := t.(string); strings.TrimSpace(s) != "" {
			d.tags = append(d.tags, strings.ToLower(strings.TrimSpace(s)))
		}
	}
```

with:

```go
	var tags []string
	raw, _ := m["tags"].([]any)
	for _, t := range raw {
		if s, ok := t.(string); ok {
			tags = append(tags, s)
		}
	}
	if len(e.Tags) > 0 {
		tags = e.Tags
	}
	for _, t := range tags {
		if t = strings.TrimSpace(t); t != "" {
			d.tags = append(d.tags, strings.ToLower(t))
		}
	}
	if a := strings.TrimSpace(e.Author); a != "" {
		d.author = a
	}
```

- [ ] **Step 5: Store `Added`**

In `Put`, before the item `INSERT`:

```go
	var added *time.Time // NULL keeps the stored date
	if !e.Added.IsZero() {
		added = &e.Added
	}
```

In the item `INSERT`: add `added` to the column list after `nums`, `COALESCE($20::timestamptz, now())` to `VALUES` after `$19`, `added = LEAST(item.added, $20)` to the `DO UPDATE SET` list (`LEAST` ignores NULL), and `added` as the last argument.

- [ ] **Step 6: Run the package tests**

Run: `go test ./catalogkit/ ./folder/`
Expected: `ok` for both.

- [ ] **Step 7: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): let entries set the author, tags, and date added"
```

---

### Task 3: Plugin-numbered versions

**Files:**
- Modify: `catalogkit/put.go` (`Entry.Version`, `Put`)
- Test: `catalogkit/put_test.go`

- [ ] **Step 1: Write the failing test**

Append to `catalogkit/put_test.go`:

```go
// Versions the plugin numbers can arrive out of order; the highest is latest.
func TestPutPluginVersions(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	e := func(v int, name string, added time.Time) Entry {
		return Entry{ID: "v", Kind: protocol.KindCard, Data: testCard(name, "Same."), Updated: added, Added: added, Version: v}
	}
	steps := []struct {
		entry Entry
		want  Change
	}{
		{e(5, "Five", t0), Added},
		{e(9, "Nine", t0.Add(time.Hour)), Updated},
		{e(7, "Seven", t0.Add(-time.Hour)), Updated}, // older, arriving late
		{e(9, "Nine", t0.Add(time.Hour)), Unchanged},
		{e(5, "Five", t0), Unchanged},
	}
	for i, st := range steps {
		got, err := s.Put(ctx, st.entry)
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if got != st.want {
			t.Fatalf("step %d: change = %v, want %v", i, got, st.want)
		}
	}
	res, err := s.Get(ctx, "v", "")
	if err != nil {
		t.Fatal(err)
	}
	var versions []string
	for _, v := range res.Versions {
		versions = append(versions, v.Version)
	}
	if res.Item.Name != "Nine" || res.Version != "9" || !slices.Equal(versions, []string{"9", "7", "5"}) {
		t.Fatalf("item %q at %q, versions %v", res.Item.Name, res.Version, versions)
	}
	var added time.Time
	if err := s.pool.QueryRow(ctx, `SELECT added FROM item WHERE id = 'v'`).Scan(&added); err != nil || !added.Equal(t0.Add(-time.Hour)) {
		t.Fatalf("added = %v, %v; want the late version's earlier date", added, err)
	}
	if _, err := s.Put(ctx, Entry{ID: "w", Kind: protocol.KindCard, Data: testCard("W", "W."), Updated: t0, Version: -1}); !errors.Is(err, ErrBadData) {
		t.Fatalf("negative version: err = %v, want ErrBadData", err)
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./catalogkit/ -run TestPutPluginVersions`
Expected: a compile error, `unknown field Version in struct literal`.

- [ ] **Step 3: Add the field**

In `catalogkit/put.go`, add to `Entry` after `Added`:

```go
	// Version numbers this version from 1, and versions sort by it. Zero
	// lets Put number them 1, 2, 3 as the data changes.
	Version int
```

- [ ] **Step 4: Rewrite `Put`'s version logic**

At the top of `Put`, before `derive`:

```go
	if e.Version < 0 {
		return 0, fmt.Errorf("%w: %s: version %d", ErrBadData, e.ID, e.Version)
	}
```

Replace everything from `change, newVersion := Unchanged, true` through the closing brace of the `if newVersion { ... }` block with:

```go
	// seq numbers e's version. current means e is the latest version, so
	// the item's fields come from it.
	change, seq, current, newVersion := Unchanged, e.Version, true, true
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		change, seq = Added, cmp.Or(e.Version, 1)
	case err != nil:
		return 0, err
	case e.Version > 0:
		err = tx.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM item_version WHERE instance = $1 AND item_id = $2 AND seq = $3)`,
			s.instance, e.ID, e.Version).Scan(&newVersion)
		if err != nil {
			return 0, err
		}
		current = e.Version >= latest
	case hash != d.dataHash:
		seq = latest + 1
	default:
		seq, newVersion = latest, false
	}
	if change == Unchanged && (newVersion || removed && current) {
		change = Updated
	}
	version := strconv.Itoa(seq)
	var added *time.Time // NULL keeps the stored date
	if !e.Added.IsZero() {
		added = &e.Added
	}
	if current {
		_, err = tx.Exec(ctx, `
			INSERT INTO item (instance, id, kind, name, author, tags, summary, description, attrs, updated, latest,
				latest_version, content_hash, data_hash, text_simhash, image_phash, lsh, thumbnail, nums, added)
			VALUES ($18, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $19,
				COALESCE($20::timestamptz, now()))
			ON CONFLICT (instance, id) DO UPDATE SET kind = excluded.kind, name = excluded.name, author = excluded.author,
				tags = excluded.tags, summary = excluded.summary, description = excluded.description,
				attrs = excluded.attrs, updated = excluded.updated, latest = excluded.latest,
				latest_version = excluded.latest_version, content_hash = excluded.content_hash, data_hash = excluded.data_hash,
				text_simhash = excluded.text_simhash, image_phash = excluded.image_phash, lsh = excluded.lsh,
				thumbnail = excluded.thumbnail, nums = excluded.nums, added = LEAST(item.added, $20), removed = false`,
			e.ID, e.Kind, d.name, d.author, d.tags, d.summary, d.description, string(attrs), e.Updated, seq,
			version, d.contentHash, d.dataHash, d.simhash, d.phash, lsh(d.simhash, d.phash), d.thumbnail, s.instance,
			string(nums), added)
	} else {
		// An older version only moves the date added back.
		_, err = tx.Exec(ctx, `UPDATE item SET added = LEAST(added, $3) WHERE instance = $1 AND id = $2`, s.instance, e.ID, added)
	}
	if err != nil {
		return 0, BadData(e.ID, err)
	}
	if newVersion {
		_, err = tx.Exec(ctx, `INSERT INTO item_version (instance, item_id, seq, version, created, data) VALUES ($1, $2, $3, $4, $5, $6)`,
			s.instance, e.ID, seq, version, e.Updated, string(e.Data))
		if err != nil {
			return 0, BadData(e.ID, err)
		}
	}
```

Delete the `var added *time.Time` block Task 2 added before the old `INSERT`; the one above replaces it. Add `"cmp"` to the imports if `go build` asks.

- [ ] **Step 5: Export `BadData`**

The plugin's own tables meet the same data exceptions. Rename `badData` to `BadData` and change its comment to:

```go
// BadData wraps a data exception (SQLSTATE class 22), such as a NUL in text
// or invalid UTF-8 in json, in ErrBadData, so an ingest skips the entry
// instead of failing.
```

Run: `grep -rn badData catalogkit folder` — expected: no matches.

- [ ] **Step 6: Update the `Put` doc comment**

```go
// Put stores e. Data that hashes differently from the latest version adds a
// version, or with e.Version set, a version number not seen before does.
// Otherwise it only refreshes the item's other fields.
```

- [ ] **Step 7: Run the package tests**

Run: `go test ./catalogkit/ ./folder/`
Expected: `ok` for both.

- [ ] **Step 8: Commit**

```bash
git add catalogkit
git commit -m "feat(catalogkit): store versions the plugin numbers"
```

---

### Task 4: The fixture dump

**Files:**
- Create: `chub-archive/testdata/dump.sql`
- Create: `chub-archive/testdata/images/a/b/c/0000000000000000000000000000d`

- [ ] **Step 1: Write the generator to the scratchpad**

Don't commit the script. Save it outside the repo as `fixture.py`:

```python
import json, sys

def card(name, desc, tags=(), creator=""):
    return json.dumps({"data": {"name": name, "description": desc, "first_mes": "Hello.", "creator": creator,
                                "tags": list(tags)}, "spec": "chara_card_v2", "spec_version": "2.0"})

RAW = object()  # bytea, which pg_dump writes as \\x...

def esc(v):
    if v is RAW:
        return "\\\\x00"
    if v is None:
        return r"\N"
    return v.replace("\\", "\\\\").replace("\n", "\\n").replace("\r", "\\r").replace("\t", "\\t")

out = ["--", "-- PostgreSQL database dump, cut down for tests", "--", "",
       "CREATE TABLE public.aicg_chronicles (", "    id_hash text NOT NULL", ");", ""]

def block(table, cols, rows):
    out.append(f"COPY public.{table} ({cols}) FROM stdin;")
    out.extend("\t".join(esc(v) for v in r) for r in rows)
    out.extend(["\\.", ""])

block("aicg_chronicles", 'id_hash, text, url, "timestamp"', [["h1", "a log", "https://example.com", "2024-01-01 00:00:00+00"]])
block("booru_character_def", "author, name, id, definition, raw, comments, tags, image_hash, tagline, summary, metadata, created, added, source, hidden",
      [["cat", "Cora", "+pygmalion1", card("Cora", "A booru card.", ["oc"], "cat"), RAW, "[]", "{oc,female}", "0", None, None, "{}",
        "2023-06-05 01:36:32.965+00", "2024-05-08 05:10:30.593534+00", "booru", "f"]])
block("chub_character", "author, name, id, data, chats, ratings, updated, added, source, metadata, hidden",
      [["ann", "Aria", "100", json.dumps({"id": 100, "rating": 4.5, "topics": ["Fantasy", "Elf"]}), '{"chats": []}', '{"ratings": []}',
        "2025-01-02 17:46:38.876621+00", "2024-05-06 17:36:35.697631+00", "chub", "{}", "f"]])
block("chub_character_def", "author, name, id, full_path, definition, raw, image_hash, original_card_hash, added, source, metadata, hidden, increment", [
    ["ann", "Aria", "100", "ann/aria", card("Aria", "An elf ranger.", ["ranger"], "ann"), RAW, "abc0000000000000000000000000000d", "0",
     "2024-05-06 20:00:28.368094+00", "chub", "{}", "f", "5"],
    ["bob", "Brin", "101", "bob/brin", card("Brin", "A dwarf smith.", [], "bob"), RAW, "fff0000000000000000000000000000f", "0",
     "2024-06-01 00:00:00+00", "chub", "{}", "t", "7"],
    ["x", "Bad", "102", "x/bad", '{"data": "not a card"}', RAW, "0", "0", "2024-06-01 00:00:00+00", "chub", "{}", "f", "8"],
    ["ann", "Aria", "100", "ann/aria", card("Aria", 'An elf ranger who says "hi".\nShe guards\tthe woods.', ["ranger"], "ann"), RAW,
     "abc0000000000000000000000000000d", "0", "2024-10-02 09:50:16.236022+00", "chub", "{}", "f", "9"],
])
block("chub_lorebook", "author, name, id, data, ratings, updated, added, source, hidden",
      [["ann", "Eldoria", "200", json.dumps({"id": 200, "rating": 3, "topics": ["World"]}), '{"ratings": []}',
        "2025-01-02 08:33:31.690431+00", "2024-05-07 02:16:18.124364+00", "chub", "f"]])
book = json.dumps({"name": "Eldoria", "entries": [{"id": 1, "keys": ["Eldoria"], "content": "A kingdom of elves.", "enabled": True,
                                                   "insertion_order": 0, "extensions": {}}], "extensions": {}})
block("chub_lorebook_def", "author, name, id, full_path, definition, raw, image_hash, added, metadata, source, increment",
      [["ann", "Eldoria", "200", "lorebooks/ann/eldoria", book, RAW, "0", "2024-05-07 02:16:18.124005+00", "{}", "chub", "3"]])
block("chub_user", "username, id, data, image_hash, updated, added, source",
      [["ann", "1", "{}", "0", "2025-01-02 00:00:00+00", "2024-05-06 00:00:00+00", "chub"]])
block("generic_character_def", "card_data_hash, name, definition, raw, image_hash, summary, tagline, added, metadata, source, source_url, hidden, increment",
      [["3cfc7793db330003fc1ff39895edc8cc", "Vergil", card("Vergil", "A half-demon swordsman."), RAW, "0", None, None,
        "2024-05-08 15:38:35.643506+00", "{}", "janitorai", None, "f", "65394"]])
ncols = "author, name, id, definition, raw, image_hash, original_card_hash, metadata, added, source, increment"
block("nyaime_character_def", ncols, [["dee", "Ava", "43", card("Ava", "A nyaime card.", [], "dee"), RAW, "0", "0", "{}",
                                       "2024-10-05 07:27:31.990945+00", "nyaime", "4"]])
block("risuai_character_def", ncols, [["eve", "Nina", "0c95bece-1d02-416f-8834-7a87895af295", card("Nina", "A risu card.", [], "eve"), RAW,
                                       "0", "0", "{}", "2024-10-15 06:20:48.072827+00", "risuai", "2633"]])
block("stored_rentry_links", "url, added", [["https://rentry.org/x", "2024-01-01 00:00:00+00"]])
block("webring_character_def", "card_data_hash, name, author, definition, raw, image_hash, tagline, summary, added, metadata, source, source_url, hidden, increment",
      [["cdef8582d2d146742a302972a605b19b", "Esther", "crustcrunch.neocities.org", card("Esther", "A vampire knight."), RAW, "0", None, None,
        "2024-11-27 02:03:44.637213+00", "{}", "webring", None, "f", "112"]])
out += ["--", "-- PostgreSQL database dump complete", "--"]
sys.stdout.write("\n".join(out) + "\n")
```

The rows cover every table, two versions of Aria (100) with escapes in the second, a hidden card (Brin, 101) whose image is missing, a row that won't decode (102), and the tables the plugin skips.

- [ ] **Step 2: Generate the files**

```bash
mkdir -p chub-archive/testdata/images/a/b/c
python3 -I <scratchpad>/fixture.py > chub-archive/testdata/dump.sql
cp card/testdata/v2-seraphina.png chub-archive/testdata/images/a/b/c/0000000000000000000000000000d
grep -c '	' chub-archive/testdata/dump.sql
```

Expected: `15` (rows with tabs).

- [ ] **Step 3: Commit**

```bash
git add chub-archive/testdata
git commit -m "test(chub-archive): add a fixture dump and image"
```

---

### Task 5: Read `COPY` rows

**Files:**
- Create: `chub-archive/dump.go`
- Test: `chub-archive/dump_test.go`

- [ ] **Step 1: Write the failing tests**

`chub-archive/dump_test.go`:

```go
package main

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestUnescape(t *testing.T) {
	cases := map[string]string{
		`plain`:            "plain",
		`a\\b`:             `a\b`,
		`say \\"hi\\"`:     `say \"hi\"`,
		`line\nnext\tcol`:  "line\nnext\tcol",
		`\b\f\r\v`:         "\b\f\r\v",
		`\q`:               "q",
		`trailing\`:        `trailing\`,
	}
	for in, want := range cases {
		if got := unescape([]byte(in)); got != want {
			t.Errorf("unescape(%q) = %q, want %q", in, got, want)
		}
	}
}

// readAll returns table:name for every row, and the position after each.
func readAll(t *testing.T, d *dump) (rows []string, after []checkpoint) {
	t.Helper()
	for {
		table, row, err := d.next()
		if errors.Is(err, io.EOF) {
			return rows, after
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, table+":"+row["name"]+row["author"])
		after = append(after, d.at)
	}
}

func TestDumpRows(t *testing.T) {
	d, err := openDump("testdata/dump.sql", checkpoint{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	rows, after := readAll(t, d)
	// Skipped tables don't appear; parents come before their defs.
	if len(rows) != 12 || rows[0] != "booru_character_def:Coracat" || rows[1] != "chub_character:Ariaann" {
		t.Fatalf("rows = %v", rows)
	}
	if d.at.Offset != d.size || d.at.Table != "" {
		t.Fatalf("ended at %+v of %d", d.at, d.size)
	}

	// Resuming after Aria's first version gives the same remaining rows.
	i := 2 // chub_character_def's first row
	if after[i].Table != "chub_character_def" {
		t.Fatalf("row %d is in %q", i, after[i].Table)
	}
	r, err := openDump("testdata/dump.sql", after[i])
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rest, _ := readAll(t, r)
	if len(rest) != len(rows)-i-1 || rest[0] != rows[i+1] {
		t.Fatalf("resumed rows = %v, want %v", rest, rows[i+1:])
	}
}

func TestDumpRowValues(t *testing.T) {
	d, err := openDump("testdata/dump.sql", checkpoint{})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	for {
		table, row, err := d.next()
		if err != nil {
			t.Fatal(err)
		}
		if table == "chub_character_def" && row["increment"] == "9" {
			if want := `"An elf ranger who says \"hi\".\nShe guards\tthe woods."`; !strings.Contains(row["definition"], want) {
				t.Fatalf("definition = %s, want it to hold %s", row["definition"], want)
			}
			return
		}
		if table == "booru_character_def" {
			if _, ok := row["tagline"]; ok {
				t.Fatal("a NULL tagline should be absent")
			}
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./chub-archive/`
Expected: a compile error, `undefined: unescape`.

- [ ] **Step 3: Write `dump.go`**

```go
package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// errBadRow marks a dump row the plugin can't read. Ingest skips it.
var errBadRow = errors.New("bad row")

// checkpoint is where ingest is in the dump: the byte offset of the next
// line, and the COPY block it is in, if any.
type checkpoint struct {
	Table   string   `json:"table,omitempty"`
	Columns []string `json:"columns,omitempty"`
	Offset  int64    `json:"offset"`
}

// dump reads the rows of a pg_dump plain-text file's COPY blocks.
type dump struct {
	f    *os.File
	r    *bufio.Reader
	at   checkpoint
	size int64
}

var copyHeader = regexp.MustCompile(`^COPY public\.(\w+) \((.*)\) FROM stdin;$`)

// openDump opens the dump at path, positioned at at.
func openDump(path string, at checkpoint) (*dump, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		_, err = f.Seek(at.Offset, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return &dump{f: f, r: bufio.NewReaderSize(f, 1<<20), at: at, size: info.Size()}, nil
}

func (d *dump) Close() error { return d.f.Close() }

// next returns the next row of a table ingest reads, as column to value. A
// NULL column is absent. It returns io.EOF after the last row, and errBadRow
// for a row with the wrong number of fields.
func (d *dump) next() (table string, row map[string]string, err error) {
	for {
		line, err := d.r.ReadBytes('\n')
		if errors.Is(err, io.EOF) && len(line) > 0 {
			err = nil
		}
		if err != nil {
			return "", nil, err
		}
		d.at.Offset += int64(len(line))
		line = bytes.TrimSuffix(line, []byte("\n"))
		if d.at.Table == "" {
			if m := copyHeader.FindSubmatch(line); m != nil {
				d.at.Table, d.at.Columns = string(m[1]), strings.Split(string(m[2]), ", ")
			}
			continue
		}
		if string(line) == `\.` {
			d.at.Table, d.at.Columns = "", nil
			continue
		}
		if !read(d.at.Table) {
			continue
		}
		fields := bytes.Split(line, []byte("\t"))
		if len(fields) != len(d.at.Columns) {
			return d.at.Table, nil, fmt.Errorf("%w: %d fields, want %d", errBadRow, len(fields), len(d.at.Columns))
		}
		row = make(map[string]string, len(fields))
		for i, f := range fields {
			if string(f) != `\N` {
				row[d.at.Columns[i]] = unescape(f)
			}
		}
		return d.at.Table, row, nil
	}
}

// escapes are the characters pg_dump escapes besides the backslash.
var escapes = map[byte]byte{'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t', 'v': '\v'}

// unescape undoes COPY text escaping.
func unescape(b []byte) string {
	if bytes.IndexByte(b, '\\') < 0 {
		return string(b)
	}
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' || i+1 == len(b) {
			out = append(out, b[i])
			continue
		}
		i++
		if c, ok := escapes[b[i]]; ok {
			out = append(out, c)
		} else {
			out = append(out, b[i])
		}
	}
	return string(out)
}
```

`read` comes from `rows.go` in Task 6. Until then, add this temporary stub at the bottom of `dump.go` so the package compiles, and delete it in Task 6:

```go
func read(table string) bool { _, ok := sources[table]; _, p := parents[table]; return ok || p }

var sources = map[string]struct{}{"booru_character_def": {}, "chub_character_def": {}, "chub_lorebook_def": {},
	"generic_character_def": {}, "nyaime_character_def": {}, "risuai_character_def": {}, "webring_character_def": {}}
var parents = map[string]string{"chub_character": "chub_character_def", "chub_lorebook": "chub_lorebook_def"}
```

Also create `chub-archive/main.go` holding only `package main` and `func main() {}` so `go test` builds; Task 9 replaces it.

- [ ] **Step 4: Run the tests**

Run: `go test ./chub-archive/ -v`
Expected: `TestUnescape`, `TestDumpRows`, `TestDumpRowValues` pass.

- [ ] **Step 5: Commit**

```bash
git add chub-archive
git commit -m "feat(chub-archive): read rows from the dump's COPY blocks"
```

---

### Task 6: Turn rows into entries

**Files:**
- Create: `chub-archive/rows.go`
- Modify: `chub-archive/dump.go` (delete the Task 5 stub)
- Test: `chub-archive/rows_test.go`

- [ ] **Step 1: Write the failing tests**

`chub-archive/rows_test.go`:

```go
package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/davidgibbons/innkeeper-plugins/catalogkit"
	"github.com/davidgibbons/innkeeper-plugins/internal/pgtest"
	"github.com/davidgibbons/innkeeper/protocol"
)

func openStore(t *testing.T) *catalogkit.Store {
	t.Helper()
	s, err := catalogkit.Open(context.Background(), pgtest.URL(t), "test", catalogConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestPgTime(t *testing.T) {
	for in, want := range map[string]time.Time{
		"2024-05-06 20:00:28.368094+00": time.Date(2024, 5, 6, 20, 0, 28, 368094000, time.UTC),
		"2024-06-01 00:00:00+00":        time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		"2024-06-01 05:30:00+05:30":     time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
	} {
		got, err := pgTime(in)
		if err != nil || !got.Equal(want) {
			t.Errorf("pgTime(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := pgTime("May 1"); err == nil {
		t.Error("pgTime accepted May 1")
	}
}

func TestEntry(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	def := map[string]string{"author": "ann", "name": "Aria", "id": "100", "hidden": "f", "increment": "9",
		"added": "2024-10-02 09:50:16+00", "image_hash": "0",
		"definition": `{"data": {"name": "Aria", "description": "An elf.", "creator": "ann", "tags": ["ranger"]}, "spec": "chara_card_v2", "spec_version": "2.0"}`}

	e, err := entry(ctx, s, "chub_character_def", def)
	if err != nil {
		t.Fatal(err)
	}
	if e.ID != "chub_character_def:100" || e.Kind != protocol.KindCard || e.Version != 9 || e.Author != "ann" ||
		e.Tags != nil || e.Attrs["site"][0] != "chub" || e.Attrs["hidden"][0] != "false" {
		t.Fatalf("entry = %+v", e)
	}

	// With its parent row, the entry takes chub's tags, rating, date, and hidden flag.
	parent := map[string]string{"id": "100", "data": `{"rating": 4.5, "topics": ["Fantasy"]}`,
		"added": "2024-05-06 17:36:35+00", "hidden": "t"}
	if err := putParent(ctx, s, "chub_character", parent); err != nil {
		t.Fatal(err)
	}
	e, err = entry(ctx, s, "chub_character_def", def)
	if err != nil {
		t.Fatal(err)
	}
	if e.Tags[0] != "Fantasy" || e.Numbers["rating"] != 4.5 || e.Added.Year() != 2024 || e.Added.Month() != 5 ||
		e.Attrs["hidden"][0] != "true" {
		t.Fatalf("entry with parent = %+v", e)
	}

	// booru rows have no increment, so they are version 1.
	booru := map[string]string{"author": "cat", "name": "Cora", "id": "+p1", "hidden": "f", "added": "2024-05-08 05:10:30+00",
		"definition": def["definition"]}
	if e, err = entry(ctx, s, "booru_character_def", booru); err != nil || e.Version != 1 || e.ID != "booru_character_def:+p1" {
		t.Fatalf("booru entry = %+v, %v", e, err)
	}

	for name, bad := range map[string]map[string]string{
		"definition": {"id": "1", "increment": "1", "added": "2024-01-01 00:00:00+00", "definition": `{"data": "no"}`},
		"increment":  {"id": "1", "increment": "x", "added": "2024-01-01 00:00:00+00", "definition": def["definition"]},
		"added":      {"id": "1", "increment": "1", "added": "soon", "definition": def["definition"]},
		"no id":      {"increment": "1", "added": "2024-01-01 00:00:00+00", "definition": def["definition"]},
	} {
		if _, err := entry(ctx, s, "chub_character_def", bad); !errors.Is(err, errBadRow) {
			t.Errorf("%s: err = %v, want errBadRow", name, err)
		}
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./chub-archive/ -run 'TestPgTime|TestEntry'`
Expected: a compile error, `undefined: catalogConfig`.

- [ ] **Step 3: Write `rows.go`**

Delete the stub from the bottom of `dump.go`, then create `chub-archive/rows.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/davidgibbons/innkeeper-plugins/card"
	"github.com/davidgibbons/innkeeper-plugins/catalogkit"
	"github.com/davidgibbons/innkeeper/protocol"
	"github.com/jackc/pgx/v5"
)

// source is a table whose rows are card or lorebook versions.
type source struct {
	kind  string
	idCol string // the column naming the item
	site  string
}

var sources = map[string]source{
	"chub_character_def":   {protocol.KindCard, "id", "chub"},
	"chub_lorebook_def":    {protocol.KindLorebook, "id", "chub"},
	"booru_character_def":  {protocol.KindCard, "id", "booru"},
	"nyaime_character_def": {protocol.KindCard, "id", "nyaime"},
	"risuai_character_def": {protocol.KindCard, "id", "risuai"},
	// These have no source ID. The hash covers the card's image and definition.
	"webring_character_def": {protocol.KindCard, "card_data_hash", "webring"},
	"generic_character_def": {protocol.KindCard, "card_data_hash", "generic"},
}

// parents are chub's per-item tables, with the table of versions each
// describes. pg_dump writes each before its versions.
var parents = map[string]string{"chub_character": "chub_character_def", "chub_lorebook": "chub_lorebook_def"}

// read reports whether ingest reads table's rows.
func read(table string) bool {
	_, ok := sources[table]
	_, parent := parents[table]
	return ok || parent
}

var sites = []protocol.CatalogOption{
	{Value: "chub", Label: "Chub"},
	{Value: "booru", Label: "Pygmalion Booru"},
	{Value: "nyaime", Label: "Nyai.me"},
	{Value: "risuai", Label: "RisuRealm"},
	{Value: "webring", Label: "Webring"},
	{Value: "generic", Label: "Other (JanitorAI, catbox)"},
}

// pgTime parses a timestamptz as pg_dump writes it, such as
// 2024-05-06 20:00:28.368094+00.
func pgTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05.999999-07", "2006-01-02 15:04:05.999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: timestamp %q", errBadRow, s)
}

// putParent stores a chub per-item row for its versions to read.
func putParent(ctx context.Context, s *catalogkit.Store, table string, row map[string]string) error {
	var data struct {
		Rating *float64 `json:"rating"`
		Topics []string `json:"topics"`
	}
	if err := json.Unmarshal([]byte(row["data"]), &data); err != nil {
		return fmt.Errorf("%w: data: %w", errBadRow, err)
	}
	added, err := pgTime(row["added"])
	if err != nil {
		return err
	}
	if data.Topics == nil {
		data.Topics = []string{}
	}
	id := parents[table] + ":" + row["id"]
	_, err = s.Pool().Exec(ctx, `
		INSERT INTO parent (instance, item_id, tags, rating, added, hidden) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (instance, item_id) DO UPDATE SET tags = excluded.tags, rating = excluded.rating,
			added = excluded.added, hidden = excluded.hidden`,
		s.Instance(), id, data.Topics, data.Rating, added, row["hidden"] == "t")
	return catalogkit.BadData(id, err)
}

// entry turns a row of a source table into one version of its item.
func entry(ctx context.Context, s *catalogkit.Store, table string, row map[string]string) (catalogkit.Entry, error) {
	src := sources[table]
	e := catalogkit.Entry{ID: table + ":" + row[src.idCol], Kind: src.kind, Author: row["author"], Version: 1,
		Attrs: map[string][]string{"site": {src.site}, "hidden": {strconv.FormatBool(row["hidden"] == "t")}}}
	if row[src.idCol] == "" {
		return e, fmt.Errorf("%w: no %s", errBadRow, src.idCol)
	}
	var err error
	if inc, ok := row["increment"]; ok {
		if e.Version, err = strconv.Atoi(inc); err != nil || e.Version < 1 {
			return e, fmt.Errorf("%w: increment %q", errBadRow, inc)
		}
	}
	if e.Updated, err = pgTime(row["added"]); err != nil {
		return e, err
	}
	e.Added = e.Updated
	if src.kind == protocol.KindLorebook {
		e.Data, err = card.DecodeLorebook([]byte(row["definition"]), row["name"])
	} else {
		e.Data, _, err = card.Decode([]byte(row["definition"]))
	}
	if err != nil {
		return e, fmt.Errorf("%w: %w", errBadRow, err)
	}

	var tags []string
	var rating *float64
	var added time.Time
	var hidden bool
	err = s.Pool().QueryRow(ctx, `SELECT tags, rating, added, hidden FROM parent WHERE instance = $1 AND item_id = $2`,
		s.Instance(), e.ID).Scan(&tags, &rating, &added, &hidden)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return e, nil
	case err != nil:
		return e, err
	}
	e.Tags, e.Added = tags, added
	if rating != nil {
		e.Numbers = map[string]float64{"rating": *rating}
	}
	if hidden {
		e.Attrs["hidden"] = []string{"true"}
	}
	return e, nil
}
```

`catalogConfig` comes from Task 9's `main.go`. For now, put it in `rows.go` and move it in Task 9:

```go
var catalogConfig = catalogkit.Config{
	Kinds: []string{protocol.KindCard, protocol.KindLorebook},
	Filters: []catalogkit.Filter{
		{CatalogFilter: protocol.CatalogFilter{Name: "site", Label: "Site", Type: protocol.FilterEnum, Options: sites}, Field: "attrs.site"},
		{CatalogFilter: protocol.CatalogFilter{Name: "tag", Label: "Tag", Type: protocol.FilterText}, Field: "tag"},
		{CatalogFilter: protocol.CatalogFilter{Name: "author", Label: "Author", Type: protocol.FilterText}, Field: "author"},
		{CatalogFilter: protocol.CatalogFilter{Name: "added", Label: "Date added", Type: protocol.FilterDateRange}, Field: "added"},
		{CatalogFilter: protocol.CatalogFilter{Name: "rating", Label: "Rating", Type: protocol.FilterNumberRange}, Field: "nums.rating"},
		{CatalogFilter: protocol.CatalogFilter{Name: "hidden", Label: "Hidden on its site", Type: protocol.FilterEnum,
			Options: []protocol.CatalogOption{{Value: "false", Label: "Shown"}, {Value: "true", Label: "Hidden"}}}, Field: "attrs.hidden"},
	},
	Ingest: true,
	Migrations: []catalogkit.Migration{{Name: "chub-archive/1-tables", SQL: `
CREATE TABLE parent (instance text NOT NULL, item_id text NOT NULL, tags text[] NOT NULL, rating float8,
	added timestamptz NOT NULL, hidden boolean NOT NULL, PRIMARY KEY (instance, item_id));
CREATE TABLE source_row (instance text NOT NULL, item_id text NOT NULL, version text NOT NULL,
	image_hash text NOT NULL, PRIMARY KEY (instance, item_id, version));
`}},
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./chub-archive/ -v`
Expected: every test passes. If `card.DecodeLorebook` refuses the fixture's lorebook in a later task, check the fixture against `card/testdata/eldoria.lorebook.json`, not the decoder.

- [ ] **Step 5: Commit**

```bash
git add chub-archive
git commit -m "feat(chub-archive): turn dump rows into catalog entries"
```

---

### Task 7: Read archive images

**Files:**
- Create: `chub-archive/images.go`
- Test: `chub-archive/images_test.go`

- [ ] **Step 1: Write the failing test**

`chub-archive/images_test.go`:

```go
package main

import "testing"

func TestReadImage(t *testing.T) {
	dirs := []string{"testdata/missing", "testdata/images"}
	if b := readImage(dirs, "abc0000000000000000000000000000d"); len(b) == 0 {
		t.Fatal("didn't find the fixture image in the second folder")
	}
	for _, hash := range []string{"", "0", "fff0000000000000000000000000000f", "../../../../etc/passwd", "ABC0000000000000000000000000000D"} {
		if b := readImage(dirs, hash); b != nil {
			t.Errorf("readImage(%q) read %d bytes", hash, len(b))
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./chub-archive/ -run TestReadImage`
Expected: a compile error, `undefined: readImage`.

- [ ] **Step 3: Write `images.go`**

```go
package main

import (
	"path/filepath"
	"strings"

	"github.com/davidgibbons/innkeeper-plugins/internal/pluginio"
)

// readImage reads the image the archive stores under hash, from the first of
// dirs that has it, or returns nil. The archive keeps each at
// <a>/<b>/<c>/<the rest of the hash>.
func readImage(dirs []string, hash string) []byte {
	// Lowercase hex only, so a hash can't name a path outside dirs.
	if len(hash) < 4 || strings.Trim(hash, "0123456789abcdef") != "" {
		return nil
	}
	for _, dir := range dirs {
		if b, err := pluginio.ReadFile(filepath.Join(dir, hash[:1], hash[1:2], hash[2:3], hash[3:])); err == nil {
			return b
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the test**

Run: `go test ./chub-archive/ -run TestReadImage -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add chub-archive
git commit -m "feat(chub-archive): read images from the archive's hash folders"
```

---

### Task 8: `catalog.ingest`

**Files:**
- Create: `chub-archive/ingest.go`
- Test: `chub-archive/ingest_test.go`

- [ ] **Step 1: Write the failing tests**

`chub-archive/ingest_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper-plugins/catalogkit"
	"github.com/davidgibbons/innkeeper-plugins/internal/pgtest"
	"github.com/davidgibbons/innkeeper/protocol"
)

func testEnv(t *testing.T) *env {
	t.Helper()
	store, err := catalogkit.Open(context.Background(), pgtest.URL(t), "test", catalogConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return &env{store: store, dump: "testdata/dump.sql", images: []string{"testdata/images"}, blobTmp: t.TempDir()}
}

// runIngest ingests from checkpoint. stop, if set, ends the run with an
// error when it returns true for a checkpoint. It returns the counts, the
// checkpoints sent, and the run's error.
func runIngest(t *testing.T, e *env, checkpoint string, stop func(cp string) bool) (protocol.CatalogIngestResult, []string, error) {
	t.Helper()
	var cps []string
	errStop := errors.New("stopped")
	notify := func(method string, params any) error {
		if cp, ok := params.(protocol.Checkpoint); ok {
			cps = append(cps, string(cp.Checkpoint))
			if stop != nil && stop(string(cp.Checkpoint)) {
				return errStop
			}
		}
		return nil
	}
	ip := protocol.CatalogIngestParams{Run: "r"}
	if checkpoint != "" {
		ip.Checkpoint = json.RawMessage(checkpoint)
	}
	params, _ := json.Marshal(ip)
	res, err := ingest(context.Background(), e, notify, params)
	if err != nil && !errors.Is(err, errStop) {
		t.Fatal(err)
	}
	return res, cps, err
}

func search(t *testing.T, e *env, filters string) []string {
	t.Helper()
	p := protocol.CatalogSearchParams{Sort: "name"}
	if filters != "" {
		_ = json.Unmarshal([]byte(filters), &p.Filters)
	}
	res, err := e.store.Search(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, it := range res.Items {
		out = append(out, it.Name)
	}
	return out
}

func TestIngest(t *testing.T) {
	e := testEnv(t)
	res, cps, _ := runIngest(t, e, "", nil)
	if want := (protocol.CatalogIngestResult{Added: 8, Updated: 1, Skipped: 1}); res != want {
		t.Fatalf("first ingest = %+v, want %+v", res, want)
	}
	var last checkpoint
	_ = json.Unmarshal([]byte(cps[len(cps)-1]), &last)
	if last.Table != "" || last.Offset == 0 {
		t.Fatalf("last checkpoint = %+v, want the end of the dump", last)
	}

	all := search(t, e, "")
	if want := []string{"Aria", "Ava", "Brin", "Cora", "Eldoria", "Esther", "Nina", "Vergil"}; !slices.Equal(all, want) {
		t.Fatalf("items = %v, want %v", all, want)
	}
	for filters, want := range map[string][]string{
		`{"site": "chub"}`:                {"Aria", "Brin", "Eldoria"},
		`{"site": "generic"}`:             {"Vergil"},
		`{"hidden": "true"}`:              {"Brin"},
		`{"rating": {"min": 4}}`:          {"Aria"},
		`{"author": "ANN"}`:               {"Aria", "Eldoria"},
		`{"tag": "elf"}`:                  {"Aria"}, // chub's topics, not the card's tags
		`{"added": {"to": "2024-05-06"}}`: {"Aria"}, // chub_character's date, not the version's
	} {
		if got := search(t, e, filters); !slices.Equal(got, want) {
			t.Errorf("%s: got %v, want %v", filters, got, want)
		}
	}

	got, err := e.store.Get(context.Background(), "chub_character_def:100", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "9" || len(got.Versions) != 2 || got.Item.Hashes.ImagePhash == nil ||
		!strings.Contains(string(got.Data), `who says \"hi\".\nShe guards\tthe woods.`) {
		t.Fatalf("Aria = version %q of %d, phash %v, data %s", got.Version, len(got.Versions), got.Item.Hashes.ImagePhash, got.Data)
	}

	// Re-ingesting the same dump changes nothing; the bad row is skipped again.
	if res, _, _ := runIngest(t, e, "", nil); res != (protocol.CatalogIngestResult{Skipped: 1}) {
		t.Fatalf("re-ingest = %+v, want only the bad row skipped", res)
	}
}

// An ingest stopped in the middle of a block resumes from its checkpoint
// and ends with the same items as one that ran through.
func TestIngestResumes(t *testing.T) {
	old := checkpointEvery
	checkpointEvery = 1
	t.Cleanup(func() { checkpointEvery = old })

	e := testEnv(t)
	_, cps, err := runIngest(t, e, "", func(cp string) bool { return strings.Contains(cp, `"table":"chub_character_def"`) })
	if err == nil {
		t.Fatal("the run wasn't stopped inside chub_character_def")
	}
	res, _, _ := runIngest(t, e, cps[len(cps)-1], nil)
	// Aria's first version came before the stop; Brin, Eldoria, and four others after it.
	if want := (protocol.CatalogIngestResult{Added: 6, Updated: 1, Skipped: 1}); res != want {
		t.Fatalf("resumed ingest = %+v, want %+v", res, want)
	}
	if got := search(t, e, ""); len(got) != 8 {
		t.Fatalf("items = %v, want 8", got)
	}
}

func TestIngestNeedsTheDump(t *testing.T) {
	e := testEnv(t)
	e.dump = "testdata/missing.sql"
	params, _ := json.Marshal(protocol.CatalogIngestParams{Run: "r"})
	_, err := ingest(context.Background(), e, func(string, any) error { return nil }, params)
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code != protocol.CodeInvalidParams {
		t.Fatalf("err = %v, want invalid params", err)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./chub-archive/ -run TestIngest`
Expected: a compile error, `undefined: env`.

- [ ] **Step 3: Write `ingest.go`**

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"

	"github.com/davidgibbons/innkeeper-plugins/catalogkit"
	"github.com/davidgibbons/innkeeper/cardhash"
	"github.com/davidgibbons/innkeeper/protocol"
)

// checkpointEvery is how many rows ingest reads between checkpoints. Tests
// set it to 1.
var checkpointEvery = 1000

func invalid(err error) error {
	return protocol.NewError(protocol.CodeInvalidParams, err.Error(), false)
}

// ingest answers catalog.ingest: it reads the dump from the checkpoint to
// the end and stores each card and lorebook version.
// ponytail: one row at a time; decode images in parallel if a full ingest is too slow.
func ingest(ctx context.Context, e *env, notify func(string, any) error, raw json.RawMessage) (protocol.CatalogIngestResult, error) {
	var res protocol.CatalogIngestResult
	var p protocol.CatalogIngestParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return res, invalid(err)
	}
	var at checkpoint
	if len(p.Checkpoint) > 0 && string(p.Checkpoint) != "null" {
		if err := json.Unmarshal(p.Checkpoint, &at); err != nil {
			return res, invalid(fmt.Errorf("checkpoint: %w", err))
		}
	}
	d, err := openDump(e.dump, at)
	if err != nil {
		return res, invalid(fmt.Errorf("config dump: %w", err))
	}
	defer d.Close()
	report := func() error {
		if err := notify(protocol.NotifyProgress, protocol.Progress{Run: p.Run, Done: d.at.Offset, Total: d.size}); err != nil {
			return err
		}
		cp, _ := json.Marshal(d.at)
		return notify(protocol.NotifyCheckpoint, protocol.Checkpoint{Run: p.Run, Checkpoint: cp})
	}
	for n := 1; ; n++ {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		table, row, err := d.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err == nil {
			err = ingestRow(ctx, e, table, row, &res)
		}
		if errors.Is(err, errBadRow) || errors.Is(err, catalogkit.ErrBadData) {
			res.Skipped++
			log.Printf("skipped %s %s increment %s: %v", table, row[idColumn(table)], row["increment"], err)
		} else if err != nil {
			return res, err
		}
		if n%checkpointEvery == 0 {
			if err := report(); err != nil {
				return res, err
			}
		}
	}
	return res, report()
}

// idColumn names the column holding a row's item ID.
func idColumn(table string) string {
	if src, ok := sources[table]; ok {
		return src.idCol
	}
	return "id"
}

// ingestRow stores one row: a chub per-item row for later versions to read,
// or a version.
func ingestRow(ctx context.Context, e *env, table string, row map[string]string, res *protocol.CatalogIngestResult) error {
	if _, ok := parents[table]; ok {
		return putParent(ctx, e.store, table, row)
	}
	en, err := entry(ctx, e.store, table, row)
	if err != nil {
		return err
	}
	version := strconv.Itoa(en.Version)
	pool, instance := e.store.Pool(), e.store.Instance()
	// ponytail: a version already ingested is skipped whole, so a newer dump's rating and tag changes don't reach it; drop this check to refresh them, at the cost of reading every image again.
	var seen bool
	err = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM source_row WHERE instance = $1 AND item_id = $2 AND version = $3)`,
		instance, en.ID, version).Scan(&seen)
	if err != nil || seen {
		return err
	}
	hash := row["image_hash"]
	if b := readImage(e.images, hash); b != nil {
		if en.Image, err = cardhash.DecodeImageBytes(b); err != nil {
			log.Printf("%s version %s: image %s: %v", en.ID, version, hash, err)
		}
	}
	change, err := e.store.Put(ctx, en)
	if err != nil {
		return err
	}
	switch change {
	case catalogkit.Added:
		res.Added++
	case catalogkit.Updated:
		res.Updated++
	}
	_, err = pool.Exec(ctx, `INSERT INTO source_row (instance, item_id, version, image_hash) VALUES ($1, $2, $3, $4)
		ON CONFLICT DO NOTHING`, instance, en.ID, version, hash)
	return err
}
```

`env` comes from Task 9. For now, add it to `ingest.go` and move it in Task 9:

```go
// env is what initialize set up.
type env struct {
	store   *catalogkit.Store // nil without a database_url
	dump    string            // config.dump
	images  []string          // config.images
	blobTmp string
}
```

`en.Image` stays nil when decoding fails, because `DecodeImageBytes` returns a nil image with its error.

- [ ] **Step 4: Run the tests**

Run: `go test ./chub-archive/ -v`
Expected: every test passes.

- [ ] **Step 5: Commit**

```bash
git add chub-archive
git commit -m "feat(chub-archive): ingest the dump with checkpoints"
```

---

### Task 9: The plugin process and `catalog.get` avatars

**Files:**
- Modify: `chub-archive/main.go` (replace the Task 5 stub)
- Modify: `chub-archive/rows.go`, `chub-archive/ingest.go` (move `catalogConfig` and `env` out)
- Test: `chub-archive/main_test.go`

- [ ] **Step 1: Write the failing test**

`chub-archive/main_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/davidgibbons/innkeeper/protocol"
)

func getParams(id, version string) json.RawMessage {
	b, _ := json.Marshal(protocol.CatalogGetParams{ID: id, Version: version, Avatar: true})
	return b
}

func TestGetAvatar(t *testing.T) {
	e := testEnv(t)
	runIngest(t, e, "", nil)
	ctx := context.Background()

	for _, version := range []string{"", "5"} {
		res, err := get(ctx, e, getParams("chub_character_def:100", version))
		if err != nil {
			t.Fatal(err)
		}
		if res.Avatar == "" || filepath.Dir(res.Avatar) != e.blobTmp {
			t.Fatalf("version %q: avatar = %q, want a file in %s", version, res.Avatar, e.blobTmp)
		}
	}
	// Brin's image isn't in the archive; the lorebook has none.
	for _, id := range []string{"chub_character_def:101", "chub_lorebook_def:200"} {
		if res, err := get(ctx, e, getParams(id, "")); err != nil || res.Avatar != "" {
			t.Fatalf("%s: avatar = %q, err %v; want none", id, res.Avatar, err)
		}
	}
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `go test ./chub-archive/ -run TestGetAvatar`
Expected: a compile error, `undefined: get`.

- [ ] **Step 3: Write `main.go`**

Move `catalogConfig` from `rows.go` and `env` from `ingest.go` into it:

```go
// Command chub-archive is a catalog plugin over the chub.ai archive's SQL
// dump.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/davidgibbons/innkeeper-plugins/catalogkit"
	"github.com/davidgibbons/innkeeper-plugins/internal/pluginio"
	"github.com/davidgibbons/innkeeper/protocol"
	"github.com/jackc/pgx/v5"
)

const version = "0.1.0"

// catalogConfig and env as moved here.

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
			e := &env{blobTmp: p.BlobTmp}
			e.dump, _ = p.Config["dump"].(string)
			images, _ := p.Config["images"].([]any)
			for _, dir := range images {
				if s, ok := dir.(string); ok && s != "" {
					e.images = append(e.images, s)
				}
			}
			if p.DatabaseURL != "" {
				var err error
				if e.store, err = catalogkit.Open(ctx, p.DatabaseURL, p.InstanceID, catalogConfig); err != nil {
					return nil, err
				}
			}
			cur.Store(e)
			return protocol.InitializeResult{Protocol: protocol.Version, Name: "chub-archive", Version: version,
				Capabilities: []string{"catalog"}}, nil
		case protocol.MethodShutdown:
			if e := cur.Load(); e != nil && e.store != nil {
				e.store.Close()
			}
			return nil, nil
		case protocol.MethodCatalogDescribe:
			return catalogkit.Describe(catalogConfig), nil
		case protocol.MethodCatalogSearch, protocol.MethodCatalogGet, protocol.MethodCatalogSimilar, protocol.MethodCatalogIngest:
		default:
			return nil, protocol.MethodNotFound(method)
		}
		e := cur.Load()
		if e == nil {
			return nil, fmt.Errorf("%s before initialize", method)
		}
		// Search checks its params first, so plugin check's filter check
		// passes without a database too.
		if method == protocol.MethodCatalogSearch {
			var p protocol.CatalogSearchParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			if err := catalogkit.Describe(catalogConfig).CheckSearch(p); err != nil {
				return nil, invalid(err)
			}
			if e.store == nil {
				return nil, errNoDatabase
			}
			return e.store.Search(ctx, p)
		}
		if e.store == nil {
			return nil, errNoDatabase
		}
		switch method {
		case protocol.MethodCatalogGet:
			return get(ctx, e, params)
		case protocol.MethodCatalogSimilar:
			var p protocol.CatalogSimilarParams
			if err := json.Unmarshal(params, &p); err != nil {
				return nil, invalid(err)
			}
			return e.store.Similar(ctx, p)
		}
		if e.dump == "" {
			return nil, invalid(errors.New("set dump in this instance's config before ingesting"))
		}
		return ingest(ctx, e, conn.Notify, params)
	}
	conn = protocol.NewConn(os.Stdin, os.Stdout, handler)
	if err := conn.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var errNoDatabase = errors.New("no database: the core sends database_url to a plugin whose manifest sets database = true")

// get answers catalog.get, with the version's image from the archive as
// its avatar.
func get(ctx context.Context, e *env, raw json.RawMessage) (protocol.CatalogGetResult, error) {
	var p protocol.CatalogGetParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return protocol.CatalogGetResult{}, invalid(err)
	}
	res, err := e.store.Get(ctx, p.ID, p.Version)
	if err != nil || !p.Avatar || res.Item.Kind != protocol.KindCard {
		return res, err
	}
	var hash string
	err = e.store.Pool().QueryRow(ctx, `SELECT image_hash FROM source_row WHERE instance = $1 AND item_id = $2 AND version = $3`,
		e.store.Instance(), p.ID, res.Version).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return res, nil
	}
	if err != nil {
		return res, err
	}
	if b := readImage(e.images, hash); b != nil {
		res.Avatar, err = pluginio.WriteTmp(e.blobTmp, "avatar-*.png", b)
	}
	return res, err
}
```

- [ ] **Step 4: Run the tests**

Run: `go vet ./chub-archive/ && go test ./chub-archive/ -v`
Expected: every test passes.

- [ ] **Step 5: Commit**

```bash
git add chub-archive
git commit -m "feat(chub-archive): serve the catalog methods with archive avatars"
```

---

### Task 10: Manifest, index entry, docs, and the contract check

**Files:**
- Create: `chub-archive/plugin.toml`, `chub-archive/check-config.json`, `chub-archive/README.md`
- Modify: `index.toml`, `.gitignore`

- [ ] **Step 1: Write the manifest**

`chub-archive/plugin.toml`:

```toml
name = "chub-archive"
version = "0.1.0"
protocol = 1
command = ["./chub-archive"]
capabilities = ["catalog"]
database = true

[config]
type = "object"

[config.properties.dump]
type = "string"
title = "Dump file"
description = "Absolute path of the archive's SQL dump as the core sees it, such as /archive/sql/database_01-05-2025/char_archive_database_01-05-2025.sql."

[config.properties.images]
type = "array"
title = "Image folders"
description = "The archive's hashed-data folders, such as /archive/sql/hashed-data_part1/hashed-data. Without them, items have no image."
items = { type = "string" }
```

`chub-archive/check-config.json`:

```json
{"dump": "testdata/dump.sql", "images": ["testdata/images"]}
```

- [ ] **Step 2: Add the index entry and ignore the binary**

Append to `index.toml`:

```toml

[[plugin]]
name = "chub-archive"
category = "Archives"
path = "chub-archive"
version = "0.1.0"
protocol = 1
capabilities = ["catalog"]
description = "Search and adopt cards and lorebooks from the chub.ai archive's SQL dump."
```

Add `/chub-archive/chub-archive` to `.gitignore` under `/folder/folder`.

- [ ] **Step 3: Write the README**

Invoke the `documentation-writing` skill first. `chub-archive/README.md`:

```markdown
# chub-archive

A catalog over the chub.ai archive takeout's SQL dump
(`char_archive_database_01-05-2025.sql`). Innkeeper searches its cards and
lorebooks and adopts single versions into the library.

## Setup

1. Mount the archive where the core can read it.
2. Set **Dump file** to the dump's path, and **Image folders** to the
   `hashed-data` folders under the archive's `sql` folder.
3. Start an ingest. Until the web UI has an Archive page, start it through
   the API with `POST /catalogs/<instance id>/ingest`.

## Behavior

- Cards come from the chub, Pygmalion Booru, Nyai.me, RisuRealm, webring,
  and generic (JanitorAI and catbox) tables, and lorebooks from chub's.
- An item's ID is `<table>:<id>`. The webring and generic tables have no
  ID, so theirs is the row's card hash and each row is its own item.
- Each row is one version, numbered by its `increment`. Booru items have
  one version, `1`.
- Chub items take their tags, rating, date added, and hidden flag from
  chub's per-item tables.
- An ingest reads the whole dump, about 12 GB, and resumes from its last
  checkpoint if it stops. Re-ingesting skips versions it already has, so
  their ratings and tags don't change.
- Rows that won't decode are skipped and logged with their table, ID, and
  increment.
```

- [ ] **Step 4: Run the index check and the contract check**

```bash
go run ./internal/indexcheck
INNKEEPER_PLUGIN_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres make check
```

Expected: `indexcheck` exits 0, and `make check` passes for every plugin, `chub-archive` included. The kit ingests the fixture first, because `check-config.json` sets a config.

- [ ] **Step 5: Run everything**

```bash
go vet ./... && go test -race ./...
```

Expected: `ok` for every package.

- [ ] **Step 6: Commit**

```bash
git add chub-archive index.toml .gitignore
git commit -m "feat(chub-archive): add the manifest, index entry, and README"
```

---

### Task 11: Ingest the real dump

This checks the plugin against 12 GB of real rows. `innkeeper plugin check` can't do it, because the kit gives each call 5 seconds. Commit nothing from this task.

- [ ] **Step 1: Write an uncommitted test**

`chub-archive/real_test.go`:

```go
package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/davidgibbons/innkeeper-plugins/catalogkit"
)

func TestRealDump(t *testing.T) {
	dump := os.Getenv("CHUB_DUMP")
	if dump == "" {
		t.Skip("CHUB_DUMP is not set")
	}
	ctx := context.Background()
	store, err := catalogkit.Open(ctx, os.Getenv("CHUB_DATABASE_URL"), "real", catalogConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	e := &env{store: store, dump: dump, images: strings.Split(os.Getenv("CHUB_IMAGES"), ":"), blobTmp: t.TempDir()}
	res, err := ingest(ctx, e, func(string, any) error { return nil }, []byte(`{"run": "real"}`))
	t.Logf("result: %+v", res)
	if err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Start it in the background**

```bash
psql postgres://postgres:postgres@localhost:55432/postgres -c 'CREATE SCHEMA chub_real'
A=/Volumes/misc/char-archive/extracted/sql
CHUB_DUMP=$A/database_01-05-2025/char_archive_database_01-05-2025.sql \
CHUB_IMAGES=$A/hashed-data_part1/hashed-data:$A/hashed-data_01-09-2025/hashed-data \
CHUB_DATABASE_URL='postgres://postgres:postgres@localhost:55432/postgres?search_path=chub_real' \
go test ./chub-archive/ -run TestRealDump -timeout 0 -v > <scratchpad>/real-ingest.log 2>&1
```

It runs for hours. Start it with `run_in_background` and report back to the user rather than polling.

- [ ] **Step 3: Read the result**

Expected: `result:` shows about 350,000 added and a small `skipped`. Group the `skipped` log lines by error so a decoder bug shows up as many rows with one cause:

```bash
grep 'skipped ' <scratchpad>/real-ingest.log | sed 's/.*: //' | sort | uniq -c | sort -rn | head
```

Leave the `chub_real` schema for the user to look at, and delete `real_test.go`.

---

### Task 12: Update the spec

**Files:**
- Modify (in `innkeeper`): `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`

Do this in a new `innkeeper` worktree: `git worktree add ../innkeeper-inn-37d-spec -b inn-37d-spec main`.

- [ ] **Step 1: Edit the spec**

Invoke the `documentation-writing` skill first. Make these edits with the Edit tool, keeping the spec's style:

- Decisions table, Images row: replace "Stay in the plugin's schema. The chub dump already stores them as `bytea`. Core keeps one blob store." with "Stay where the catalog's source keeps them. The chub archive keeps them as files named by hash, beside the dump. Core keeps one blob store."
- `catalogkit` list: add "Versions numbered by the plugin, such as the dump's `increment`, or numbered 1, 2, 3 as the data changes." and "Entries can set the author, tags, date added, and numbers for `number_range` filters."
- `chub-archive` section:
  - **Config:** "the path to the dump file, and the archive's image folders."
  - Ingest step 2: append "`webring_character_def` and `generic_character_def` have no source ID, so each row is its own item, named by `card_data_hash`."
  - Ingest step 3: replace with "Reads `chub_character` and `chub_lorebook`, which come before their versions in the dump, for tags, ratings, dates, and the hidden flag."
  - Add after step 3: "4. Reads each version's image from the image folders, at `<a>/<b>/<c>/<rest>` of its `image_hash`."
  - **Item ID:** append "`booru_character_def` has no `increment`, so its items have one version, `1`. Re-ingesting skips versions already stored."
  - **Checkpoint:** replace the `progress` sentence with "`progress` counts bytes of the dump."
- Out of scope: add "Refreshing ratings and tags of versions already ingested."

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-10-09-archive-catalog-design.md
git commit -m "docs: describe chub-archive as built"
```

## Leaves for later plans

- **Deploy:** innkeeper.pureroot.com needs the archive share mounted into the core's pod before this plugin can ingest there.
- **37e (web):** the Archive page builds its filter bar from `describe`, so `enum` and `number_range` filters need inputs there.
- Ingest decodes one image at a time. A full ingest takes hours; parallel decoding is the upgrade if that matters.
