# Browse sections Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a plugin mark which enrichment keys are worth browsing and which section they belong to, and merge same-named keys across plugins, so the filter sidebar and sort menu shrink to about eight entries grouped by section.

**Architecture:** `protocol.Enrichment` gains `section` and `browse`, and `Manifest.Browsable` holds the rule for which keys are browsable. `libsettings.BrowseKeys` merges browsable keys across enabled enrich instances, ranked the way `Scorers` ranks grid keys, and `GET /enrichments/keys` returns the merged list. `library.Query` gains `Browsable`, a map from key to its ranked scorers, which instance-less filters, sorts, and facets read through the existing `gridSQL`. The web UI renders the merged list under section headings, lists merged score keys in the sort menu, and shows each instance's other scores on the card page as a bar list.

**Tech Stack:** Go (Huma, pgx, BurntSushi/toml), PostgreSQL, React and TypeScript (TanStack Query, Vitest, Testing Library).

Spec: [`docs/superpowers/specs/2026-11-06-browse-sections-design.md`](../specs/2026-11-06-browse-sections-design.md).

---

## Decisions

Where the code showed the spec to be silent or wrong, the plan takes the simplest reading:

| Topic | Decision |
|---|---|
| Grid key filters today | An instance-less term on a grid key reads the **grid score**, which mixes every grid key: `{"enrichment": "lint"}` can read forge-ai's `quality`. Instance-less terms become per key, so `lint` reads only `lint`. `Query.Scorers` stays for the grid score that `include: score` and the Hide job read. |
| Where merging happens | On the server. `GET /enrichments/keys` gains `browse`, the merged list, so the merge rule lives in one place. The UI can't merge on its own: it never sees `priority`. Each key also gains `section` and `browse`, as the spec asks. |
| Priority for non-grid keys | `priority` still needs `grid`. A merged non-grid key's instances count as priority 0, so they order by instance ID. |
| `section` on a grid key | Allowed without `browse`, since the grid key is always browsable. |
| Vector keys | Never browsable, even under the old-manifest rule. |
| A name declared with two types | Doesn't merge: `browse` lists it once per instance with `instance` set, and an instance-less term on that name is a 400. |
| Value facet count searches | Every open value section with nothing picked shares one count search; a picked one gets its own. The per-instance split goes, since merged keys have no instance. |
| Which keys the sidebar shows | A browse key shows once any of its instances has scored a card, as instances do today. The sort menu lists every merged score key. |
| Card page bars | Each bar is a native `<meter>` with the key's range. |
| `no_char_macro` | Stays in `problems`, at 0 points. |
| Plugin manifests | A manifest that uses `section` or `browse` fails to load on a core before protocol 1.2, which reports unknown keys. Each changed plugin sets `protocol_version = "1.2"` in `plugin.toml` and `index.toml`. |
| Plugins checkout | Local `main` in `/Users/dgibbons/git/innkeeper-plugins` is 4 commits behind `origin/main`, which has `indexcheck`'s `protocol_version` support and forge-ai 0.14.1. Pull first. |

## Files

Core, `/Users/dgibbons/git/innkeeper-rules-spec`:

| File | Change |
|---|---|
| `protocol/manifest.go` | `Enrichment.Section`, `Enrichment.Browse`, `enrichmentSections`, validation, `Manifest.Browsable` |
| `protocol/manifest_test.go` | Two error rows, `TestManifestBrowsable` |
| `protocol/messages.go` | `Minor = 2` |
| `docs/protocol.md` | Version row 1.2, manifest row, **Browse sections** under `enrich` |
| `internal/libsettings/libsettings.go` | `declarations`, `Scorers` on top of it, `BrowseKey`, `BrowseKeys` |
| `internal/api/instances.go` | `EnrichmentOut.Section`, `EnrichmentOut.Browse`; `toEnrichmentOut` caller |
| `internal/api/enrichments.go` | `toEnrichmentOut(e, browse)`, `browseKey`, `browse` in `/enrichments/keys`, `withScores` sets `Browsable` |
| `internal/api/facetcache.go` | `facetKey` includes `Browsable` |
| `internal/api/enrichments_test.go` | `editManifest`, `addEnricher`, `editKey`; three tests; one line of `TestGridScoreReadsTheHighestPriorityScorer` |
| `internal/library/search.go` | `Query.Browsable`, `browsableScorers`, per-key instance-less terms, `mergedFacetSQL`, `mergedFacets`, `UsesScore`, doc strings |
| `internal/library/search_test.go` | `browsable` helper, edits to existing tests, `TestSearchMergedKeyReadsTheFirstInstanceWithAValue` |
| `internal/library/enrichment_test.go` | One query gains `Browsable` |
| `internal/enrich/enrich.go` | `Payload.Browsable` |
| `internal/mcp/tools.go` | `search_cards` doc string |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated with `npm run gen`, never by hand |
| `web/src/cards/query.ts` | `withoutUnnamed` doc and parameter name |
| `web/src/cards/Cards.tsx` | Merged sort menu, linked sort, `browse` into `Facets` |
| `web/src/cards/Facets.tsx` | Section groups, merged score and value keys, one shared count search |
| `web/src/cards/Cards.css` | Group styles; drop `.score-instance h3` |
| `web/src/cards/Cards.test.tsx` | Fixtures and tests for sections, merged keys, sort menu |
| `web/src/card/Enrichments.tsx` | `Values`, `Bars`; browsable keys, then bars, then the rest |
| `web/src/card/Card.css` | `.enrichment-bars` |
| `web/src/card/Enrichments.test.tsx` | `browse` in the fixture, bar list test |

Plugins, `/Users/dgibbons/git/innkeeper-plugins`:

| File | Change |
|---|---|
| `go.mod`, `go.sum` | Pin innkeeper to the develop commit with protocol 1.2 |
| `card-lint/lint.go`, `card-lint/main_test.go` | `no_char_macro` costs 0 points |
| `card-lint/plugin.toml` | `browse`, `section`, `protocol_version`, version 0.5.0 |
| `crucible/plugin.toml` | `browse`, `section`, labels, `protocol_version`, version 0.3.1 |
| `forge-ai/plugin.toml` | `quality` browsable, `protocol_version`, version 0.14.2 |
| `chub-archive/plugin.toml` | `browse`, `section`, `protocol_version`, version 0.3.5 |
| `index.toml` | Each changed entry's `version` and `protocol_version` |

Database tests need PostgreSQL. Before any `go test` step:

```bash
docker start innkeeper-pg 2>/dev/null || docker run -d --name innkeeper-pg -e POSTGRES_PASSWORD=postgres -p 55432:5432 pgvector/pgvector:pg18
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

---

## Task 1: `section` and `browse` in the manifest

**Files:**
- Modify: `protocol/manifest.go`
- Modify: `protocol/messages.go:14`
- Modify: `docs/protocol.md`
- Test: `protocol/manifest_test.go`

- [ ] **Step 1: Write the failing tests**

In `protocol/manifest_test.go`, add two rows to the `tests` table in `TestManifestErrors`, after the `"priority off the grid"` row:

```go
		{"unknown section", `label = "AI summary" }`, `label = "AI summary", browse = true, section = "mood" }`, `section "mood" must be one of`},
		{"section without browse", `label = "AI summary" }`, `label = "AI summary", section = "kind" }`, "section needs browse"},
```

Add this test after `TestManifestStillLoadsTheRetiredEmbedFlag`:

```go
func TestManifestBrowsable(t *testing.T) {
	m := &Manifest{Enrichments: map[string]Enrichment{
		"quality":   {Type: "score", Grid: true},
		"voice":     {Type: "score"},
		"summary":   {Type: "text"},
		"embedding": {Type: "vector"},
	}}
	// A manifest that sets browse on no key lists every key but its vector.
	for key, want := range map[string]bool{"quality": true, "voice": true, "summary": true, "embedding": false, "unknown": false} {
		if got := m.Browsable(key); got != want {
			t.Errorf("without browse, Browsable(%q) = %v, want %v", key, got, want)
		}
	}
	m.Enrichments["summary"] = Enrichment{Type: "text", Browse: true}
	// One that sets browse lists only those keys and its grid key.
	for key, want := range map[string]bool{"quality": true, "voice": false, "summary": true, "embedding": false} {
		if got := m.Browsable(key); got != want {
			t.Errorf("with browse, Browsable(%q) = %v, want %v", key, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./protocol/ -run 'TestManifestErrors|TestManifestBrowsable' -v`
Expected: build failure, `unknown field Browse in struct literal` and `m.Browsable undefined`.

- [ ] **Step 3: Add the fields, the validation, and `Browsable`**

In `protocol/manifest.go`, after `var enrichmentTypes = ...`, add:

```go
// enrichmentSections are the filter sidebar headings a browsable key can
// name, in sidebar order.
var enrichmentSections = []string{"score", "popularity", "kind", "warnings", "problems"}
```

In `type Enrichment struct`, after the `Priority` field, add:

```go
	// Section is the filter sidebar heading a browsable key sits under, or
	// empty for Other.
	Section string `toml:"section"`
	// Browse lists the key in the filter sidebar and sort menu; see
	// Manifest.Browsable.
	Browse bool `toml:"browse"`
```

After `func (m *Manifest) DisplayName()`, add:

```go
// Browsable reports whether key shows in the filter sidebar and sort menu.
// The grid key always does; when no key sets browse, every key but a vector
// does, so plugins from before browse keep their filters.
func (m *Manifest) Browsable(key string) bool {
	e, ok := m.Enrichments[key]
	switch {
	case !ok || e.Type == "vector":
		return false
	case e.Grid || e.Browse:
		return true
	}
	for _, other := range m.Enrichments {
		if other.Browse {
			return false
		}
	}
	return true
}
```

In `Validate`, inside `for key, e := range m.Enrichments`, after the `priority needs grid` check, add:

```go
		if e.Section != "" && !slices.Contains(enrichmentSections, e.Section) {
			errs = append(errs, fmt.Errorf("enrichment %q: section %q must be one of %v", key, e.Section, enrichmentSections))
		}
		if e.Section != "" && !e.Browse && !e.Grid {
			errs = append(errs, fmt.Errorf("enrichment %q: section needs browse", key))
		}
```

- [ ] **Step 4: Bump the protocol minor**

In `protocol/messages.go`, change `const Minor = 1` to:

```go
const Minor = 2
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./protocol/ ./internal/plugins/ -v -run 'TestManifest|TestCheckVersion|TestInstall'`
Expected: PASS. The version tests build their versions from `Minor`, so they follow the bump.

- [ ] **Step 6: Document protocol 1.2**

In `docs/protocol.md`, add a row to the version table after the `1.1` row:

```markdown
| 1.2 | `section` and `browse` on enrichment keys (see [Browse sections](#browse-sections)) |
```

In the manifest table's `enrichments` row, replace `` `embed` is retired and ignored. `` with:

```markdown
`browse` and `section` list a key in the filter sidebar and sort menu; see [Browse sections](#browse-sections). `embed` is retired and ignored.
```

Under `### \`enrich\``, insert this before the line `**\`enrich.run\`** computes values for up to 20 revisions.`:

````markdown
#### Browse sections

The filter sidebar and sort menu list only browsable keys:

- A key with `browse = true` is browsable.
- The grid key always is.
- In a plugin that sets `browse` on no key, every key but a `vector` one is.

Other keys show only on the card page. Filters, sort, and facets can still
use them by naming the instance.

`section` puts a browsable key under a sidebar heading; a key without one
goes under Other. `section` needs `browse` or `grid`.

| Section | Holds |
|---|---|
| `score` | How good the card is, such as a quality score |
| `popularity` | How the card does with readers, such as a site's rating |
| `kind` | What the card is: type, format, point of view, language, topics |
| `warnings` | Content ratings and content flags |
| `problems` | Defects to fix, such as lint problems |

Browsable keys with the same name and type merge across plugins into one
entry, labeled as the highest-priority instance declares it. A filter, sort,
or facet that names no instance, such as `{"enrichment": "quality", "gte": 70}`,
reads each card's value from the first instance that has one: by the key's
`priority`, highest first, then oldest instance first. Only a grid key has a
`priority`; other keys count as 0. A key whose type differs between plugins
doesn't merge, so filters on it name the instance.

```toml
protocol_version = "1.2"

[enrichments]
quality = { type = "score", range = [0, 100], label = "Quality", grid = true, priority = 300, browse = true, section = "score" }
pov = { type = "text", label = "Point of view", browse = true, section = "kind" }
voice = { type = "score", range = [0, 100], label = "Voice" }
```
````

- [ ] **Step 7: Commit**

```bash
git add protocol/manifest.go protocol/manifest_test.go protocol/messages.go docs/protocol.md
git commit -m "feat(protocol): let enrichment keys name a browse section"
```

## Task 2: Merged browse keys in `GET /enrichments/keys`

**Files:**
- Modify: `internal/libsettings/libsettings.go:52-75`
- Modify: `internal/api/instances.go:23-28,158`
- Modify: `internal/api/enrichments.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`
- Test: `internal/api/enrichments_test.go`

- [ ] **Step 1: Write the test helpers and the failing test**

In `internal/api/enrichments_test.go`, add `"github.com/inkandale/innkeeper/protocol"` to the imports, after the `tier` import. After `func (e *enrichEnv) search`, add:

```go
// editManifest replaces plugin name's manifest with a copy that edit changes.
func (e *enrichEnv) editManifest(name string, edit func(m *protocol.Manifest)) {
	m := *e.manifests[name]
	m.Enrichments = maps.Clone(m.Enrichments)
	edit(&m)
	e.manifests[name] = &m
}

// addEnricher adds a copy of judge as plugin name, changed by edit, with one
// instance, and returns the instance's ID.
func (e *enrichEnv) addEnricher(name string, edit func(m *protocol.Manifest)) int64 {
	e.t.Helper()
	m := *e.manifests["judge"]
	m.Name, m.Command = name, []string{m.Command[0], "-caps=enrich", "-name=" + name}
	m.Enrichments = maps.Clone(m.Enrichments)
	edit(&m)
	e.manifests[name] = &m
	id, err := e.s.Registry.CreateInstance(context.Background(), plugins.NewInstance{Plugin: name, Name: new(name)})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// editKey changes one key of m.
func editKey(m *protocol.Manifest, key string, edit func(d *protocol.Enrichment)) {
	d := m.Enrichments[key]
	edit(&d)
	m.Enrichments[key] = d
}
```

After `TestEnrichmentKeysSayWhetherAnInstanceScoredAnything`, add:

```go
func TestEnrichmentKeysListBrowsableKeysMergedAcrossInstances(t *testing.T) {
	e := newEnrichEnv(t)
	judge := e.instID
	// keys returns the browse list as key@instance|label|section|instances, and the instance keys' browse and section.
	keys := func() ([]string, map[string]string) {
		t.Helper()
		var out enrichmentKeysOut
		e.call("GET", "/enrichments/keys", nil, 200, &out.Body)
		browse := []string{}
		for _, b := range out.Body.Browse {
			browse = append(browse, fmt.Sprintf("%s@%d|%s|%s|%v", b.Key, b.Instance, b.Label, b.Section, b.Instances))
		}
		flags := map[string]string{}
		for _, k := range out.Body.Instances[0].Keys {
			flags[k.Key] = fmt.Sprintf("%t|%s", k.Browse, k.Section)
		}
		return browse, flags
	}
	check := func(want ...string) map[string]string {
		t.Helper()
		got, flags := keys()
		if !slices.Equal(got, want) {
			t.Fatalf("browse = %v, want %v", got, want)
		}
		return flags
	}

	// judge sets browse on no key, so every key but its vector is browsable.
	check(fmt.Sprintf("quality@0|Quality||[%d]", judge), fmt.Sprintf("summary@0|Summary||[%d]", judge), fmt.Sprintf("voice@0|Voice||[%d]", judge))

	// With a browse key, only those and the grid key are browsable.
	e.editManifest("judge", func(m *protocol.Manifest) {
		editKey(m, "summary", func(d *protocol.Enrichment) { d.Browse, d.Section = true, "kind" })
	})
	flags := check(fmt.Sprintf("quality@0|Quality||[%d]", judge), fmt.Sprintf("summary@0|Summary|kind|[%d]", judge))
	if want := map[string]string{"quality": "true|", "summary": "true|kind", "voice": "false|", "embedding": "false|"}; !maps.Equal(flags, want) {
		t.Fatalf("instance keys = %v, want %v", flags, want)
	}

	// quality merges under the higher-priority instance's label; summary's types differ, so it doesn't merge.
	second := e.addEnricher("critic", func(m *protocol.Manifest) {
		editKey(m, "quality", func(d *protocol.Enrichment) { d.Priority, d.Label = 200, "Critic quality" })
		editKey(m, "summary", func(d *protocol.Enrichment) { d.Type = "list" })
	})
	check(fmt.Sprintf("quality@0|Critic quality||[%d %d]", second, judge),
		fmt.Sprintf("summary@%d|Summary|kind|[%d]", judge, judge),
		fmt.Sprintf("summary@%d|Summary|kind|[%d]", second, second))

	// A disabled instance drops out.
	e.call("PATCH", fmt.Sprintf("/instances/%d", second), map[string]any{"disabled": true}, 200, nil)
	check(fmt.Sprintf("quality@0|Quality||[%d]", judge), fmt.Sprintf("summary@0|Summary|kind|[%d]", judge))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestEnrichmentKeysListBrowsableKeysMergedAcrossInstances -v`
Expected: build failure, `out.Body.Browse undefined` and `k.Browse undefined`.

- [ ] **Step 3: Rank declarations once, and merge browse keys**

In `internal/libsettings/libsettings.go`, replace the whole `Scorers` function (the comment `// Scorers are the enabled enrich instances' grid keys, ...` through its closing brace) with:

```go
// declared is a key an enabled enrich instance's plugin declares.
type declared struct {
	library.Scorer
	protocol.Enrichment
}

// declarations are the enabled enrich instances' keys that keep accepts,
// highest priority first, then oldest instance first.
func (s *Settings) declarations(ctx context.Context, keep func(m *protocol.Manifest, key string) bool) ([]declared, error) {
	insts, err := s.Enrichers(ctx)
	if err != nil {
		return nil, err
	}
	manifests := s.reg.Manifests()
	var all []declared
	for _, inst := range insts {
		m := manifests[inst.Plugin]
		for k, d := range m.Enrichments {
			if keep(m, k) {
				all = append(all, declared{library.Scorer{Instance: inst.ID, Key: k}, d})
			}
		}
	}
	slices.SortFunc(all, func(a, b declared) int {
		return cmp.Or(cmp.Compare(b.Priority, a.Priority), cmp.Compare(a.Instance, b.Instance), cmp.Compare(a.Key, b.Key))
	})
	return all, nil
}

// Scorers are the enabled enrich instances' grid keys, highest priority
// first, then oldest instance first.
func (s *Settings) Scorers(ctx context.Context) ([]library.Scorer, error) {
	all, err := s.declarations(ctx, func(m *protocol.Manifest, key string) bool { return m.Enrichments[key].Grid })
	if err != nil {
		return nil, err
	}
	out := make([]library.Scorer, len(all))
	for i, d := range all {
		out[i] = d.Scorer
	}
	return out, nil
}

// BrowseKey is one entry of the filter sidebar and sort menu.
type BrowseKey struct {
	Key string
	// Instance is 0 for a key merged across the instances that declare it
	// browsable. A key they declare with different types doesn't merge: it
	// gets one BrowseKey per instance, naming it.
	Instance int64
	// Enrichment is the highest-priority instance's declaration.
	protocol.Enrichment
	// Scorers are the instances the key reads, highest priority first.
	Scorers []library.Scorer
}

// BrowseKeys are the enabled enrich instances' browsable keys, merged by
// name, in the order of each key's highest-priority instance.
func (s *Settings) BrowseKeys(ctx context.Context) ([]BrowseKey, error) {
	all, err := s.declarations(ctx, (*protocol.Manifest).Browsable)
	if err != nil {
		return nil, err
	}
	var names []string
	byName := map[string][]declared{}
	for _, d := range all {
		if byName[d.Key] == nil {
			names = append(names, d.Key)
		}
		byName[d.Key] = append(byName[d.Key], d)
	}
	var out []BrowseKey
	for _, name := range names {
		ds := byName[name]
		if slices.ContainsFunc(ds, func(d declared) bool { return d.Type != ds[0].Type }) {
			for _, d := range ds {
				out = append(out, BrowseKey{Key: name, Instance: d.Instance, Enrichment: d.Enrichment, Scorers: []library.Scorer{d.Scorer}})
			}
			continue
		}
		merged := BrowseKey{Key: name, Enrichment: ds[0].Enrichment}
		for _, d := range ds {
			merged.Scorers = append(merged.Scorers, d.Scorer)
		}
		out = append(out, merged)
	}
	return out, nil
}
```

- [ ] **Step 4: Return `section`, `browse`, and the browse list**

In `internal/api/instances.go`, replace the `EnrichmentOut` struct with:

```go
type EnrichmentOut struct {
	Type    string    `json:"type"`
	Label   string    `json:"label"`
	Range   []float64 `json:"range,omitempty"`
	Grid    bool      `json:"grid,omitempty" doc:"Whether the library grid shows this key."`
	Section string    `json:"section,omitempty" doc:"The filter sidebar section a browsable key sits under: score, popularity, kind, warnings, or problems. Empty for Other."`
	Browse  bool      `json:"browse,omitempty" doc:"Whether the filter sidebar and sort menu list this key."`
}
```

In the same file, change `p.Enrichments[k] = toEnrichmentOut(e)` to:

```go
					p.Enrichments[k] = toEnrichmentOut(e, m.Browsable(k))
```

In `internal/api/enrichments.go`, replace `toEnrichmentOut` with:

```go
func toEnrichmentOut(e protocol.Enrichment, browse bool) EnrichmentOut {
	return EnrichmentOut{Type: e.Type, Label: e.Label, Range: e.Range, Grid: e.Grid, Section: e.Section, Browse: browse}
}
```

In the `get-card-enrichments` handler, change `ev.EnrichmentOut = toEnrichmentOut(d)` to:

```go
							ev.EnrichmentOut = toEnrichmentOut(d, m.Browsable(v.Key))
```

After `type enrichmentInstance struct {...}`, add:

```go
type browseKey struct {
	Key      string `json:"key"`
	Instance int64  `json:"instance,omitempty" doc:"Set when the key isn't merged, as plugins declare it with different types: a filter, sort, or facet on it then names this instance."`
	EnrichmentOut
	Instances []int64 `json:"instances" doc:"The enrich instances the key reads, highest priority first."`
}
```

In `enrichmentKeysOut`'s `Body`, after `Scorers`, add:

```go
		Browse    []browseKey          `json:"browse" doc:"The filter sidebar's and sort menu's keys: each browsable key, merged across the instances that declare it with one type, labeled as the highest-priority one declares it."`
```

In the `list-enrichment-keys` registration, change the `Description` to:

```go
		Description: "The scorers, the browsable keys merged across instances, and the keys each enabled enrich instance's plugin declares: those that enrichment filters and sort can use, naming the instance."},
```

In its handler, after the `Scorers` lookup, add:

```go
			browse, err := s.libSettings().BrowseKeys(ctx)
			if err != nil {
				return nil, s.fail(err)
			}
			out.Body.Browse = make([]browseKey, len(browse))
			for i, b := range browse {
				out.Body.Browse[i] = browseKey{Key: b.Key, Instance: b.Instance, EnrichmentOut: toEnrichmentOut(b.Enrichment, true)}
				for _, sc := range b.Scorers {
					out.Body.Browse[i].Instances = append(out.Body.Browse[i].Instances, sc.Instance)
				}
			}
```

Replace the key loop in the same handler:

```go
				keys := []enrichmentKey{}
				for k, e := range s.Registry.Manifests()[inst.Plugin].Enrichments {
					keys = append(keys, enrichmentKey{k, toEnrichmentOut(e)})
				}
```

with:

```go
				keys := []enrichmentKey{}
				m := s.Registry.Manifests()[inst.Plugin]
				for k, e := range m.Enrichments {
					keys = append(keys, enrichmentKey{k, toEnrichmentOut(e, m.Browsable(k))})
				}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ -run 'TestEnrichmentKeys|TestGridScore' -v`
Expected: PASS.

- [ ] **Step 6: Regenerate the API schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` gain `BrowseKey`, `section`, and `browse`; typecheck passes.

- [ ] **Step 7: Commit**

```bash
git add internal/libsettings/libsettings.go internal/api/instances.go internal/api/enrichments.go internal/api/enrichments_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): list browsable enrichment keys merged across instances"
```

## Task 3: Search reads merged browsable keys

**Files:**
- Modify: `internal/library/search.go`
- Modify: `internal/api/enrichments.go:22-30` (`withScores`)
- Modify: `internal/api/facetcache.go:40-47`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`
- Test: `internal/library/search_test.go`, `internal/library/enrichment_test.go`, `internal/api/enrichments_test.go`

- [ ] **Step 1: Point the existing library tests at `Browsable`**

In `internal/library/search_test.go`, after `func ptr[T any]`, add:

```go
// browsable is scorers as Query.Browsable: each key's scorers, in order.
func browsable(scorers ...Scorer) map[string][]Scorer {
	out := map[string][]Scorer{}
	for _, s := range scorers {
		out[s.Key] = append(out[s.Key], s)
	}
	return out
}
```

Make these edits in `TestSearchEnrichments`:

| Old | New |
|---|---|
| `tc.q.Scorers = scorers` | `tc.q.Scorers, tc.q.Browsable = scorers, browsable(scorers...)` |
| `Query{Include: []string{"score"}, Sort: "enrichment:quality", Scorers: scorers,` | `Query{Include: []string{"score"}, Sort: "enrichment:quality", Scorers: scorers, Browsable: browsable(scorers...),` |
| `Query{Include: []string{"score"}, Sort: "enrichment:quality", Scorers: ranked})` | `Query{Include: []string{"score"}, Sort: "enrichment:quality", Scorers: ranked, Browsable: browsable(ranked...)})` |
| `Query{Sort: "enrichment:quality", Reverse: true, Scorers: scorers, Filter: all}` | `Query{Sort: "enrichment:quality", Reverse: true, Browsable: browsable(scorers...), Filter: all}` |
| `Query{Filter: &Filter{Enrichment: quality, Lte: ptr(75.0)}, Scorers: scorers}` | `Query{Filter: &Filter{Enrichment: quality, Lte: ptr(75.0)}, Browsable: browsable(scorers...)}` |

In the same test, replace the first three rows of the `ErrInvalid` table:

```go
		"filter without scorers":        {Filter: &Filter{Enrichment: quality}},
		"sort without scorers":          {Sort: "enrichment:quality"},
		"unnamed key that isn't a grid": {Filter: &Filter{Enrichment: ptr("voice")}, Scorers: scorers},
```

with:

```go
		"filter without browsable keys":    {Filter: &Filter{Enrichment: quality}},
		"sort without browsable keys":      {Sort: "enrichment:quality"},
		"the grid score doesn't stand in":  {Filter: &Filter{Enrichment: quality}, Scorers: scorers},
		"unnamed key that isn't browsable": {Filter: &Filter{Enrichment: ptr("voice")}, Browsable: browsable(scorers...)},
```

In the family test, change `Facets: []string{"tags"}, Scorers: []Scorer{{score, "quality"}}})` to:

```go
		Facets: []string{"tags"}, Scorers: []Scorer{{score, "quality"}}, Browsable: browsable(Scorer{score, "quality"})})
```

In the paging test, change `q := Query{Sort: "enrichment:quality", Scorers: []Scorer{{score, "quality"}}, Page: Page{Size: 2}}` to:

```go
	q := Query{Sort: "enrichment:quality", Browsable: browsable(Scorer{score, "quality"}), Page: Page{Size: 2}}
```

In `TestQueryUsesScore`, add a row after the `enrichment:7:language` row:

```go
		{Facets: []string{"enrichment:language"}}:                                                    true,
```

In `internal/library/enrichment_test.go`, change `Query{Scorers: []Scorer{{1, "quality"}}, Include:` to:

```go
	res, err := lib.Search(ctx, Card, Query{Scorers: []Scorer{{1, "quality"}}, Browsable: browsable(Scorer{1, "quality"}), Include: []string{"score"}, Filter: &Filter{Enrichment: ptr("quality"), Gte: ptr(70.0)}})
```

- [ ] **Step 2: Write the failing library test**

In `internal/library/search_test.go`, after `TestSearchEnrichmentValues`, add:

```go
func TestSearchMergedKeyReadsTheFirstInstanceWithAValue(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	const first, second = 7, 8 // in priority order
	add := func(inst, card int64, key, value string) {
		t.Helper()
		c, err := lib.Cards.Get(ctx, card)
		if err != nil {
			t.Fatal(err)
		}
		if err := lib.AddEnrichments(ctx, inst, []NewEnrichment{{CardID: card, RevisionID: c.HeadRevisionID, Key: key, Value: json.RawMessage(value)}}); err != nil {
			t.Fatal(err)
		}
	}
	// Both scored Brakka; only second scored Carrow, so Carrow falls back to it.
	add(first, s.brakka, "quality", "80")
	add(first, s.brakka, "pov", `"first"`)
	add(second, s.brakka, "quality", "20")
	add(second, s.brakka, "pov", `"third"`)
	add(second, s.carrow, "quality", "60")
	add(second, s.carrow, "pov", `"second"`)
	keys := browsable(Scorer{first, "quality"}, Scorer{second, "quality"}, Scorer{first, "pov"}, Scorer{second, "pov"})

	for name, tc := range map[string]struct {
		f    Filter
		want []int64
	}{
		"the first instance wins":     {Filter{Enrichment: ptr("quality"), Gte: ptr(70.0)}, []int64{s.brakka}},
		"a later value doesn't count": {Filter{Enrichment: ptr("quality"), Lte: ptr(30.0)}, []int64{}},
		"a card falls back":           {Filter{Enrichment: ptr("quality"), Gte: ptr(50.0)}, []int64{s.brakka, s.carrow}},
		"text from the first":         {Filter{Enrichment: ptr("pov"), Is: ptr("third")}, []int64{}},
	} {
		t.Run(name, func(t *testing.T) {
			ids, err := lib.SearchIDs(ctx, Card, Query{Browsable: keys, Filter: &tc.f})
			if err != nil || !slices.Equal(ids, tc.want) {
				t.Fatalf("ids = %v, %v; want %v", ids, err, tc.want)
			}
		})
	}

	got, err := lib.Search(ctx, Card, Query{Browsable: keys, Sort: "enrichment:quality", Include: []string{"score"}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.brakka, s.carrow}) {
		t.Fatalf("sorted = %v, %v", hitIDs(got), err)
	}
	if b, c := got.Hits[0].Score, got.Hits[1].Score; b == nil || b.Instance != first || string(b.Value) != "80" || c == nil || c.Instance != second || string(c.Value) != "60" {
		t.Fatalf("sorted scores = %+v, %+v", b, c)
	}

	got, err = lib.Search(ctx, Card, Query{Browsable: keys, Facets: []string{"enrichment:pov", "enrichment:pov"}})
	if err != nil || !slices.Equal(got.Facets.Enrichments["pov"], []ValueCount{{"first", 1}, {"second", 1}}) {
		t.Fatalf("pov facet = %+v, %v", got.Facets, err)
	}
	if _, err := lib.Search(ctx, Card, Query{Browsable: keys, Facets: []string{"enrichment:voice"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("facet on a key that isn't browsable: %v, want ErrInvalid", err)
	}
}
```

- [ ] **Step 3: Run the library tests to verify they fail**

Run: `go test ./internal/library/ -run 'TestSearchEnrichment|TestSearchMergedKey|TestQueryUsesScore|TestSearchFamil|TestEnrichment' -v`
Expected: build failure, `unknown field Browsable in struct literal of type Query`.

- [ ] **Step 4: Add `Query.Browsable` and read it for instance-less terms**

In `internal/library/search.go`, after the `Scorers []Scorer` field of `Query`, add:

```go
	// Browsable are each merged browsable key's scorers, highest priority
	// first. An enrichment filter, sort, or facet that names no instance reads
	// the first of its key's scorers that has a value. The caller resolves
	// them from the enrich instances.
	Browsable map[string][]Scorer `json:"-"`
```

Change these doc strings in `Query`:

- In `Include`, replace `under sort enrichment:<instance>:<key> the value sorted by` with `under an enrichment sort the value sorted by`.
- In `Sort`, replace `enrichment:<key>, naming a grid key, sorts by the grid score.` with `enrichment:<key>, naming a browsable key, sorts by the value of the highest-priority instance that declares it and has one.`
- In `Facets`, replace `or enrichment:<instance>:<key> for the values of a text or list key from that enrich instance.` with `or enrichment:<instance>:<key> for the values of a text or list key from that enrich instance, or enrichment:<key> for a browsable key's, each card's from the highest-priority instance that has one.`

In `Filter`, change the `Instance` doc to:

```go
	Instance      *int64        `json:"instance,omitempty" doc:"With enrichment: the enrich instance whose value to read. Without it, enrichment must name a browsable key, and reads the value of the highest-priority instance that declares it and has one."`
```

Replace `shownScorers` and `enrichmentSQL` (from `// shownScorers are the scorers include score reads` through the end of `enrichmentSQL`) with:

```go
// shownScorers are the scorers include score reads: those of the key a sort
// names, so a badge shows what the grid is sorted by, else q's.
func shownScorers(q Query) []Scorer {
	ref, ok := strings.CutPrefix(q.Sort, "enrichment:")
	if !ok {
		return q.Scorers
	}
	inst, key, err := enrichmentRef(ref)
	switch {
	case err != nil:
		return q.Scorers
	case inst != 0:
		return []Scorer{{inst, key}}
	case len(q.Browsable[key]) > 0:
		return q.Browsable[key]
	}
	return q.Scorers
}

// browsableScorers are the scorers a term naming no instance reads for key.
func browsableScorers(browsable map[string][]Scorer, key string) ([]Scorer, error) {
	if scorers := browsable[key]; len(scorers) > 0 {
		return scorers, nil
	}
	return nil, fmt.Errorf("%w: enrichment %q names no instance and is no browsable key", ErrInvalid, key)
}

// enrichmentSQL is the current value of key from enrich instance inst for
// card c, or NULL, as a jsonb expression. Instance 0 reads a browsable key:
// the first of its scorers with a value.
func (w *where) enrichmentSQL(key string, inst int64) (string, error) {
	if w.k != Card {
		return "", fmt.Errorf("%w: lorebooks have no enrichments", ErrInvalid)
	}
	if inst == 0 {
		scorers, err := browsableScorers(w.browsable, key)
		if err != nil {
			return "", err
		}
		return w.gridScoreSQL(`e.value`, scorers), nil
	}
	return `(SELECT e.value FROM enrichment e WHERE e.card_id = c.id AND e.key = ` + w.arg(key) +
		` AND e.instance_id = ` + w.arg(inst) + ` ORDER BY e.revision_id DESC, e.created_at DESC, e.id DESC LIMIT 1)`, nil
}
```

Change the `enrichmentRef` comment's last line from `// instance 0, for the grid score.` to `// instance 0, for a browsable key.`

In `type where struct`, replace `scorers []Scorer` with:

```go
	browsable map[string][]Scorer
```

In `compile`, replace `w := &where{k: k, scorers: q.Scorers}` with:

```go
	w := &where{k: k, browsable: q.Browsable}
```

- [ ] **Step 5: Count merged facets**

In `internal/library/search.go`, in `enrichmentFacetSQL`, replace:

```go
		LATERAL (SELECT m.value #>> '{}' AS v WHERE jsonb_typeof(m.value) = 'string'
			UNION SELECT i #>> '{}' FROM jsonb_array_elements(CASE jsonb_typeof(m.value) WHEN 'array' THEN m.value ELSE '[]' END) i
			WHERE jsonb_typeof(i) = 'string') x
```

with:

```go
		LATERAL ` + facetValues + ` x
```

Before `// enrichmentFacetSQL counts`, add:

```go
// facetValues is m.value's string, or its list's string items, as rows v.
const facetValues = `(SELECT m.value #>> '{}' AS v WHERE jsonb_typeof(m.value) = 'string'
			UNION SELECT i #>> '{}' FROM jsonb_array_elements(CASE jsonb_typeof(m.value) WHEN 'array' THEN m.value ELSE '[]' END) i
			WHERE jsonb_typeof(i) = 'string')`

// mergedFacetSQL counts the matches of cond per string value, or list item,
// of each card's value from the first of the scorers in args $instances and
// $keys that has one, the top 100 values.
func mergedFacetSQL(k *Kind, cond, instances, keys string) string {
	return `SELECT x.v, count(*)::int FROM ` + from(k) + `,
		LATERAL (SELECT ` + gridSQL(`e.value`, instances, keys) + ` AS value) m,
		LATERAL ` + facetValues + ` x
		WHERE ` + cond + `
		GROUP BY x.v ORDER BY count(*) DESC, x.v LIMIT 100`
}
```

After `enrichmentFacets`, add:

```go
// mergedFacets fills f's facets on the browsable keys in keys, one query per key.
func (l *Library) mergedFacets(ctx context.Context, k *Kind, cond string, args []any, browsable map[string][]Scorer, keys []string, f *Facets) error {
	for _, key := range keys {
		instances, scorerKeys := scorerArgs(browsable[key])
		n := len(args)
		rows, err := l.pool.Query(ctx, mergedFacetSQL(k, cond, "$"+strconv.Itoa(n+1), "$"+strconv.Itoa(n+2)),
			append(slices.Clone(args), instances, scorerKeys)...)
		if err != nil {
			return err
		}
		counts, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ValueCount])
		if err != nil {
			return err
		}
		f.Enrichments[key] = append(f.Enrichments[key], counts...)
	}
	return nil
}
```

Change the `facets` signature to take the browsable keys:

```go
func (l *Library) facets(ctx context.Context, k *Kind, names []string, cond string, args []any, browsable map[string][]Scorer) (*Facets, error) {
	f := &Facets{}
	refs := map[int64]map[string][]string{}
	var merged []string
```

In its enrichment branch, replace:

```go
			f.Enrichments[ref] = []ValueCount{}
			if refs[inst] == nil {
```

with:

```go
			f.Enrichments[ref] = []ValueCount{}
			if inst == 0 {
				merged = append(merged, key)
				continue
			}
			if refs[inst] == nil {
```

At its end, replace:

```go
	if err := l.enrichmentFacets(ctx, k, cond, args, refs, f); err != nil {
		return nil, err
	}
	return f, nil
```

with:

```go
	if err := l.enrichmentFacets(ctx, k, cond, args, refs, f); err != nil {
		return nil, err
	}
	if err := l.mergedFacets(ctx, k, cond, args, browsable, merged, f); err != nil {
		return nil, err
	}
	return f, nil
```

In `Search`, change the call to:

```go
		if res.Facets, err = l.facets(ctx, k, q.Facets, facetCond, condArgs, q.Browsable); err != nil {
```

In `Search`'s facet check, replace:

```go
			inst, _, err := enrichmentRef(ref)
			if err != nil {
				return Results{}, err
			}
			if inst == 0 {
				return Results{}, fmt.Errorf("%w: facet %q must name its enrich instance", ErrInvalid, name)
			}
			continue
```

with:

```go
			inst, key, err := enrichmentRef(ref)
			if err == nil && inst == 0 {
				_, err = browsableScorers(q.Browsable, key)
			}
			if err != nil {
				return Results{}, err
			}
			continue
```

Replace `UsesScore` with:

```go
// UsesScore reports whether q may read the grid score or a browsable key,
// and so needs Scorers and Browsable.
func (q Query) UsesScore() bool {
	return strings.HasPrefix(q.Sort, "enrichment:") || slices.Contains(q.Include, "score") || filtersEnrichments(q.Filter) ||
		slices.ContainsFunc(q.Facets, func(name string) bool {
			ref, ok := strings.CutPrefix(name, enrichmentFacetPrefix)
			return ok && !strings.Contains(ref, ":")
		})
}
```

- [ ] **Step 6: Run the library tests to verify they pass**

Run: `gofmt -l internal/ && go test ./internal/library/ -v -run 'TestSearch|TestQueryUsesScore|TestEnrichment'`
Expected: `gofmt -l` prints nothing (run `gofmt -w internal/library` if it lists files), tests PASS.

- [ ] **Step 7: Write the failing API test**

In `internal/api/enrichments_test.go`, in `TestGridScoreReadsTheHighestPriorityScorer`, replace:

```go
	// A key naming no instance must be a grid key.
	e.search(map[string]any{"filter": map[string]any{"enrichment": "voice", "gte": 1}}, 400)
```

with:

```go
	// judge sets browse on no key, so voice is browsable and filters by name alone.
	if got := ids(e.search(map[string]any{"filter": map[string]any{"enrichment": "voice", "gte": 1}}, 200)); !slices.Equal(slices.Sorted(slices.Values(got)), []int64{high, low}) {
		t.Fatalf("voice gte 1 = %v", got)
	}
```

After `TestEnrichmentKeysListBrowsableKeysMergedAcrossInstances`, add:

```go
func TestSearchReadsMergedBrowsableKeys(t *testing.T) {
	e := newEnrichEnv(t)
	high := e.card("High", strings.Repeat("x", 90))
	low := e.card("Low", strings.Repeat("x", 20))
	e.evaluate(e.instID, high, low)
	voice := map[string]any{"enrichment": "voice", "gte": 1}
	summary := map[string]any{"enrichment": "summary", "is": "About High"}
	summaryFacet := map[string]any{"facets": []string{"enrichment:summary"}, "facets_only": true}

	e.editManifest("judge", func(m *protocol.Manifest) {
		editKey(m, "summary", func(d *protocol.Enrichment) { d.Browse, d.Section = true, "kind" })
	})
	// Once judge names its browse keys, voice needs its instance.
	e.search(map[string]any{"filter": voice}, 400)
	if n := e.search(map[string]any{"filter": map[string]any{"enrichment": "voice", "gte": 1, "instance": e.instID}}, 200).Total; n != 2 {
		t.Fatalf("voice naming its instance = %d cards", n)
	}
	if n := e.search(map[string]any{"filter": summary}, 200).Total; n != 1 {
		t.Fatalf("summary is About High = %d cards", n)
	}
	if res := e.search(summaryFacet, 200); res.Facets == nil || len(res.Facets.Enrichments["summary"]) != 2 {
		t.Fatalf("summary facet = %+v", res.Facets)
	}

	second := e.addEnricher("critic", func(m *protocol.Manifest) {
		editKey(m, "quality", func(d *protocol.Enrichment) { d.Priority = 200 })
		editKey(m, "summary", func(d *protocol.Enrichment) { d.Type = "list" })
	})
	e.evaluate(second, low)
	// Each card's value comes from the highest-priority instance that has one.
	res := e.search(map[string]any{"sort": "enrichment:quality", "include": []string{"score"}}, 200)
	if len(res.Hits) != 2 || res.Hits[0].ID != high || res.Hits[0].Score == nil || res.Hits[0].Score.Instance != e.instID ||
		res.Hits[1].ID != low || res.Hits[1].Score == nil || res.Hits[1].Score.Instance != second {
		t.Fatalf("merged sort = %+v", res.Hits)
	}
	// summary's types now differ, so it doesn't merge, and the cached counts don't answer for it.
	e.search(map[string]any{"filter": summary}, 400)
	e.search(summaryFacet, 400)
}
```

- [ ] **Step 8: Run the API tests to verify they fail**

Run: `go test ./internal/api/ -run 'TestSearchReadsMergedBrowsableKeys|TestGridScore' -v`
Expected: FAIL. `voice gte 1` returns 400, as `withScores` doesn't set `Browsable` yet.

- [ ] **Step 9: Resolve browsable keys per request, and key the facet cache on them**

In `internal/api/enrichments.go`, replace `withScores` with:

```go
// withScores sets q's scorers and merged browsable keys, if q may read them.
func (s *Server) withScores(ctx context.Context, q *library.Query) error {
	if !q.UsesScore() {
		return nil
	}
	var err error
	if q.Scorers, err = s.libSettings().Scorers(ctx); err != nil {
		return err
	}
	keys, err := s.libSettings().BrowseKeys(ctx)
	if err != nil {
		return err
	}
	q.Browsable = map[string][]library.Scorer{}
	for _, k := range keys {
		if k.Instance == 0 {
			q.Browsable[k.Key] = k.Scorers
		}
	}
	return nil
}
```

In `internal/api/facetcache.go`, replace `facetKey` with:

```go
// facetKey names q's counts; Scorers and Browsable aren't in q's JSON but change them.
func facetKey(k *library.Kind, q library.Query) string {
	body, _ := json.Marshal(struct {
		Kind      string
		Scorers   []library.Scorer
		Browsable map[string][]library.Scorer
		Query     library.Query
	}{k.Name, q.Scorers, q.Browsable, q})
	return string(body)
}
```

- [ ] **Step 10: Run the tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ ./internal/library/ -run 'Enrich|Search|Grid|Facet|UsesScore' -v`
Expected: PASS.

- [ ] **Step 11: Regenerate the API schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: the `Query` and `Filter` doc strings change in `web/openapi.json` and `web/src/api/schema.d.ts`; typecheck passes.

- [ ] **Step 12: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go internal/library/enrichment_test.go internal/api/enrichments.go internal/api/facetcache.go internal/api/enrichments_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(search): read a browsable key by name across the instances that declare it"
```

## Task 4: Enrich runs and MCP

**Files:**
- Modify: `internal/enrich/enrich.go:46-47,71`
- Modify: `internal/api/enrichments.go:123`
- Modify: `internal/mcp/tools.go:46`
- Test: `internal/api/enrichments_test.go`

- [ ] **Step 1: Write the failing test**

In `internal/api/enrichments_test.go`, after `TestEnrichRunOverAQuery`, add:

```go
func TestEnrichRunOverAQueryKeepsItsBrowsableKeys(t *testing.T) {
	e := newEnrichEnv(t)
	scored := e.card("Brakka", "x")
	e.card("Carrow", "x")
	e.evaluate(e.instID, scored)
	query := map[string]any{"filter": map[string]any{"enrichment": "voice", "gte": 1}}
	j, p := e.startRun(map[string]any{"instance_id": e.instID, "query": query, "rescore": true})
	var payload enrich.Payload
	if err := json.Unmarshal(j.Payload, &payload); err != nil || !slices.Equal(payload.Browsable["voice"], []library.Scorer{{Instance: e.instID, Key: "voice"}}) {
		t.Fatalf("payload = %s, %v", j.Payload, err)
	}
	if j.Status != jobs.StatusSucceeded || p.Total != 1 || p.Done != 1 {
		t.Fatalf("job = %s, progress = %+v", j.Status, p)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/api/ -run TestEnrichRunOverAQueryKeepsItsBrowsableKeys -v`
Expected: build failure, `payload.Browsable undefined`.

- [ ] **Step 3: Carry `Browsable` in the payload**

In `internal/enrich/enrich.go`, replace:

```go
	// Scorers are the Query's scorers, which its JSON leaves out.
	Scorers []library.Scorer `json:"scorers,omitempty"`
```

with:

```go
	// Scorers and Browsable are the Query's, which its JSON leaves out.
	Scorers   []library.Scorer            `json:"scorers,omitempty"`
	Browsable map[string][]library.Scorer `json:"browsable,omitempty"`
```

In `batches`, replace `q.Scorers = p.Scorers` with:

```go
	q.Scorers, q.Browsable = p.Scorers, p.Browsable
```

In `internal/api/enrichments.go`, replace `p.Query, p.Scorers = &q, q.Scorers` with:

```go
				p.Query, p.Scorers, p.Browsable = &q, q.Scorers, q.Browsable
```

- [ ] **Step 4: Update the MCP doc string**

In `internal/mcp/tools.go`, in `searchIn.Query`'s `jsonschema` tag, replace:

```
one naming no instance, such as {\"enrichment\": \"quality\", \"gte\": 70}, must name a grid key and reads the grid score: each card's value from the highest-priority scorer that has one. Facet enrichment:<instance>:<key> counts a text or list key's values
```

with:

```
one naming no instance, such as {\"enrichment\": \"quality\", \"gte\": 70}, must name a browsable key from the API's GET /enrichments/keys browse list, and reads each card's value from the highest-priority instance that declares the key and has one. Facet enrichment:<instance>:<key> counts a text or list key's values, and enrichment:<key> a browsable key's
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./... && go test ./internal/api/ ./internal/enrich/ ./internal/mcp/ -run 'Enrich|MCP' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/enrich/enrich.go internal/api/enrichments.go internal/api/enrichments_test.go internal/mcp/tools.go
git commit -m "feat(enrich): keep a query's browsable keys in an enrich run"
```

## Task 5: Sectioned filter sidebar and merged sort menu

**Files:**
- Modify: `web/src/cards/query.ts:91-101`
- Modify: `web/src/cards/Cards.tsx`
- Modify: `web/src/cards/Facets.tsx`
- Modify: `web/src/cards/Cards.css`
- Test: `web/src/cards/Cards.test.tsx`

- [ ] **Step 1: Update the fixtures and write the failing tests**

In `web/src/cards/Cards.test.tsx`, in `beforeEach`, change `keys = { scorers: [], instances: [] };` to:

```ts
  keys = { scorers: [], instances: [], browse: [] };
```

Replace the `judgeKeys` and `scored` constants with:

```ts
const judgeKeys = [
  { key: "quality", label: "Quality", type: "score", range: [0, 100], grid: true, browse: true, section: "score" },
  { key: "summary", label: "Summary", type: "text" },
];
const voiceKey = { key: "voice", label: "Voice", type: "score", range: [0, 100] };
const qualityKey = { key: "quality", label: "Quality", type: "score", range: [0, 100], grid: true, browse: true, section: "score", instances: [5] };
const scored = { scorers: [{ instance: 5, key: "quality" }], instances: [{ id: 5, label: "Judge", scored: true, keys: judgeKeys }], browse: [qualityKey] };
const problemsKey = { key: "problems", label: "Problems", type: "list", browse: true, section: "problems", instances: [5] };
const languageKey = { key: "language", label: "Language", type: "text", browse: true, section: "kind", instances: [5] };
```

In `"an instance that has scored nothing gets no filters, and a hint on how to run it"`, change the first line to `keys = { ...scored, instances: [{ id: 5, label: "Judge", scored: false, keys: judgeKeys }] };` and change the last line to:

```ts
  expect(screen.queryByRole("button", { name: "Score" })).not.toBeInTheDocument();
```

In `"a score range filters, and Not evaluated replaces it"`, merged keys name no instance:

| Old | New |
|---|---|
| `["score:5:quality:70.."]` | `["score:quality:70.."]` |
| `{ enrichment: "quality", instance: 5, gte: 70 }` | `{ enrichment: "quality", gte: 70 }` |
| `["score:5:quality:none"]` | `["score:quality:none"]` |
| `{ enrichment: "quality", instance: 5, missing: true }` | `{ enrichment: "quality", missing: true }` |

In `"a score slider applies its range on release"`, change `["score:5:quality:40..80"]` to `["score:quality:40..80"]`.

Replace `"each score key is a sort"` with:

```ts
test("a browsable score key is a sort, named without its instance", async () => {
  keys = scored;
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await screen.findByRole("option", { name: "Quality" });
  await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "Quality");
  expect(router.state.location.search).toBe("?sort=enrichment%3Aquality&library=1");
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ sort: "enrichment:quality" })));
});

test("a link sorting by a key that isn't browsable keeps its sort, and the menu shows it", async () => {
  keys = { ...scored, instances: [{ id: 5, label: "Judge", scored: true, keys: [...judgeKeys, voiceKey] }] };
  renderCards("/cards?sort=enrichment:5:voice");
  expect(await screen.findByRole("option", { name: "Voice · Judge" })).toBeInTheDocument();
  expect(screen.getByRole("combobox", { name: "Sort" })).toHaveValue("enrichment:5:voice");
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ sort: "enrichment:5:voice" })));
});
```

Replace `"a list key's values count in the sidebar, and a click filters on one"`, `"value sections with nothing picked share one count search"`, and `"each instance's value sections share their own count search"` with:

```ts
test("a list key's values count in the sidebar, and a click filters on one", async () => {
  keys = { scorers: [], instances: [{ id: 5, label: "Lint", scored: true, keys: [problemsKey] }], browse: [problemsKey] };
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /^Markup\s*3/ }));
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["value:problems=markup"]);
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ filter: { and: [{ enrichment: "problems", is: "markup" }] } })));
  expect(screen.getByRole("button", { name: "Remove problems: Markup" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Exclude Oversized" }));
  await waitFor(() =>
    expect(searches).toContainEqual(
      expect.objectContaining({ filter: { and: [{ enrichment: "problems", is: "markup" }, { not: { enrichment: "problems", is: "oversized" } }] } }),
    ),
  );
  // The section counts over every filter but its own.
  const counts = searches.filter((s) => (s as { facets?: string[] }).facets?.includes("enrichment:problems"));
  expect((counts.at(-1) as { filter?: unknown }).filter).toBeUndefined();
});

test("value sections with nothing picked share one count search", async () => {
  keys = { scorers: [], instances: [{ id: 5, label: "Lint", scored: true, keys: [problemsKey, languageKey] }], browse: [problemsKey, languageKey] };
  renderCards("/cards");
  const user = userEvent.setup();
  const valueCounts = () => searches.map((s) => (s as { facets?: string[] }).facets ?? []).filter((f) => f.some((n) => n.startsWith("enrichment:")));
  await user.click(await screen.findByRole("button", { name: /^Markup\s*3/ }));
  // In sidebar order: Kind before Problems.
  expect(valueCounts()[0]).toEqual(["enrichment:language", "enrichment:problems"]);
  // A picked section counts without its own terms, so it leaves the shared search.
  await waitFor(() =>
    expect(searches).toContainEqual(expect.objectContaining({ facets: ["enrichment:language"], filter: { and: [{ enrichment: "problems", is: "markup" }] } })),
  );
  const own = searches.filter((s) => (s as { facets?: string[] }).facets?.join() === "enrichment:problems");
  expect(own).toHaveLength(1);
  expect((own[0] as { filter?: unknown }).filter).toBeUndefined();
});

test("enrichment filters sit under section headings in section order, then Other, and each section collapses", async () => {
  const tokensKey = { key: "tokens", label: "Tokens", type: "score", range: [0, 10000], browse: true, instances: [5] };
  keys = { ...scored, browse: [problemsKey, tokensKey, languageKey, qualityKey] };
  renderCards("/cards");
  const user = userEvent.setup();
  await screen.findByRole("slider", { name: "Quality at least" });
  const sections = ["Score", "Popularity", "Kind", "Content warnings", "Problems", "Other"];
  const headings = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent ?? "");
  expect(headings.filter((h) => sections.includes(h))).toEqual(["Score", "Kind", "Problems", "Other"]);
  await user.click(screen.getByRole("button", { name: "Score" }));
  expect(screen.queryByRole("slider", { name: "Quality at least" })).not.toBeInTheDocument();
  expect(screen.getByRole("slider", { name: "Tokens at least" })).toBeInTheDocument();
});
```

Replace `"a score filter and sort from an older link on a grid key read the grid score"`'s name with `"a score filter and sort from a link naming no instance read the merged key"`; its body stays.

Delete the `lintKeys` constant, and replace `"with two instances, both instances' keys filter and sort"` with:

```ts
test("a key two instances declare is one filter and one sort, under the merged label", async () => {
  keys = {
    scorers: [{ instance: 7, key: "quality" }, { instance: 5, key: "quality" }],
    instances: [
      { id: 5, label: "Judge", scored: true, keys: [...judgeKeys, voiceKey] },
      { id: 7, label: "Critic", scored: true, keys: [{ ...judgeKeys[0], label: "Critic quality" }] },
    ],
    browse: [{ ...qualityKey, label: "Critic quality", instances: [7, 5] }],
  };
  const { router } = renderCards("/cards");
  const lo = await screen.findByRole("slider", { name: "Critic quality at least" });
  expect(screen.getAllByRole("slider", { name: /quality at least/i })).toHaveLength(1);
  expect(screen.getAllByRole("option", { name: /quality/i })).toHaveLength(1);
  // A key that isn't browsable is neither a filter nor a sort.
  expect(screen.queryByRole("slider", { name: "Voice at least" })).not.toBeInTheDocument();
  expect(screen.queryByRole("option", { name: /Voice/ })).not.toBeInTheDocument();
  fireEvent.change(lo, { target: { value: "70" } });
  fireEvent.pointerUp(lo);
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["score:quality:70.."]);
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ filter: { and: [{ enrichment: "quality", gte: 70 }] } })));
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd web && npx vitest run src/cards/Cards.test.tsx`
Expected: FAIL. The sliders still write `score:5:quality:...`, the sort is `enrichment:5:quality`, and no `Score` heading exists.

- [ ] **Step 3: Generalize `withoutUnnamed`**

In `web/src/cards/query.ts`, replace the `withoutUnnamed` comment and signature:

```ts
/**
 * withoutUnnamed drops the score and value terms and enrichment sort that name no instance, from older links,
 * unless they name one of gridKeys, which the core reads as the grid score. It returns q itself when nothing changes.
 */
export function withoutUnnamed(q: CardQuery, gridKeys: string[]): CardQuery {
  const kept = (ref?: { instance?: number; key: string }) => !ref || ref.instance !== undefined || gridKeys.includes(ref.key);
```

with:

```ts
/**
 * withoutUnnamed drops the score and value terms and enrichment sort that name no instance, from older links,
 * unless they name one of merged, the merged browsable keys the core reads by name. It returns q itself when nothing changes.
 */
export function withoutUnnamed(q: CardQuery, merged: string[]): CardQuery {
  const kept = (ref?: { instance?: number; key: string }) => !ref || ref.instance !== undefined || merged.includes(ref.key);
```

In the same function, change `!gridKeys.includes(key)` to `!merged.includes(key)`. Change the `keyRef` comment to:

```ts
/** keyRef names an enrichment key as terms, sorts, and facets do: instance:key, or a merged browsable key alone. */
```

and the `ref` comment near the top to:

```ts
// Score and value terms name their key as instance:key; a merged browsable key goes alone.
```

- [ ] **Step 4: Build the sort menu from the browse list**

In `web/src/cards/Cards.tsx`, add after the other imports:

```ts
import type { components } from "../api/schema";
```

After `const sortLabels = ...`, add:

```ts
type Enricher = components["schemas"]["EnrichmentInstance"];

/** linkedSortLabel names an enrichment sort the menu doesn't list, from a link: the key's label and its instance's. */
function linkedSortLabel(sort: string, enrichers: Enricher[]): string {
  const ref = sort.slice("enrichment:".length);
  const at = ref.indexOf(":");
  const key = ref.slice(at + 1);
  const e = at > 0 ? enrichers.find((x) => String(x.id) === ref.slice(0, at)) : undefined;
  const k = e?.keys?.find((x) => x.key === key);
  return e && k ? `${k.label} · ${e.label}` : key;
}
```

Replace:

```ts
  // The core refuses a term naming no instance unless it names a grid key.
  const scoredQ = keys.data ? withoutUnnamed(asked, (keys.data.scorers ?? []).map((s) => s.key)) : asked;
```

with:

```ts
  const browse = keys.data?.browse ?? [];
  // The core refuses a term naming no instance unless it names a merged browsable key.
  const scoredQ = keys.data ? withoutUnnamed(asked, browse.filter((b) => !b.instance).map((b) => b.key)) : asked;
```

Replace the `sorts` constant:

```ts
  const sorts: [Sort, string][] = [
    ...(Object.entries(sortLabels) as [Sort, string][]),
    ...enrichers.flatMap((e) =>
      (e.keys ?? [])
        .filter((k) => k.type === "score")
        .map((k): [Sort, string] => [`enrichment:${keyRef(e.id, k.key)}`, enrichers.length > 1 ? `${k.label} · ${e.label}` : k.label]),
    ),
  ];
```

with:

```ts
  const scoreSorts = browse.filter((b) => b.type === "score").map((b): [Sort, string] => [`enrichment:${keyRef(b.instance, b.key)}`, b.label]);
  const sorts: [Sort, string][] = [
    ...(Object.entries(sortLabels) as [Sort, string][]),
    ...scoreSorts,
    // A link may sort by a key the menu doesn't list, such as one that isn't browsable.
    ...(q.sort.startsWith("enrichment:") && !scoreSorts.some(([s]) => s === q.sort) ? [[q.sort, linkedSortLabel(q.sort, enrichers)] as [Sort, string]] : []),
  ];
```

Replace `<Facets kind={kind} q={q} names={names} enrichers={enrichers} onChange={go} />` with:

```tsx
<Facets kind={kind} q={q} names={names} browse={browse.filter((b) => b.instances?.some((id) => enrichers.some((e) => e.id === id && e.scored)))} onChange={go} />
```

- [ ] **Step 5: Group the sidebar's enrichment keys by section**

In `web/src/cards/Facets.tsx`, replace:

```ts
const titles: Record<FacetKind | "score", string> = { target: "App", state: "State", tag: "Tags", source: "Sources", collection: "Collections", score: "Scores" };

type EnrichmentKey = components["schemas"]["EnrichmentKey"];
type Enricher = components["schemas"]["EnrichmentInstance"];
```

with:

```ts
const titles: Record<FacetKind, string> = { target: "App", state: "State", tag: "Tags", source: "Sources", collection: "Collections" };

type BrowseKey = components["schemas"]["BrowseKey"];
```

In `Section`, after `const name = title ?? (kind === "value" ? "" : titles[kind]);`, add:

```ts
  // A value key's section sits inside its enrichment section's heading.
  const Heading = kind === "value" ? "h3" : "h2";
```

and replace:

```tsx
      <h2>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {name}
          {instance && <> <span className="facet-instance">· {instance}</span></>}
          {picked > 0 && ` · ${picked}`}
        </button>
      </h2>
```

with:

```tsx
      <Heading>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {name}
          {instance && <> <span className="facet-instance">· {instance}</span></>}
          {picked > 0 && ` · ${picked}`}
        </button>
      </Heading>
```

Replace everything from the comment that starts `/**\n * useValueCounts counts each open value section's values` to the end of the file with:

```tsx
/** groups are the enrichment sections, in sidebar order; a key that names none goes under Other. */
const groups = [
  ["score", "Score"],
  ["popularity", "Popularity"],
  ["kind", "Kind"],
  ["warnings", "Content warnings"],
  ["problems", "Problems"],
  ["", "Other"],
] as const;

const groupOf = (b: BrowseKey) => `group:${b.section || "other"}`;
const isValueKey = (b: BrowseKey) => b.type === "text" || b.type === "list";

/** useValueCounts counts each open value section's values, ref to counts. Sections with nothing picked share one search, as their filters are the same. */
function useValueCounts(q: CardQuery, refs: string[]): Map<string, { counts?: ValueCount[]; approx?: boolean }> {
  const picked = (ref: string) => q.terms.some((t) => sectionOf(t) === `value:${ref}`);
  const shared = refs.filter((ref) => !picked(ref));
  const groupsOfRefs = [...(shared.length ? [shared] : []), ...refs.filter(picked).map((ref) => [ref])];
  const results = useQueries({ queries: groupsOfRefs.map((g) => countQuery("card", valueCountSearch(q, g))) });
  return new Map(
    groupsOfRefs.flatMap((g, i) => {
      const facets = results[i]?.data && (results[i].data.facets ?? {});
      return g.map((ref) => [ref, { counts: facets && (facets.enrichments?.[ref] ?? []), approx: facets?.approximate }] as const);
    }),
  );
}

/** ValueSection lists one text or list key's values, each a row to include or exclude. */
function ValueSection({ b, instanceLabel, counts, approx, q, open, onOpen, onChange }: { b: BrowseKey; instanceLabel?: string; counts?: ValueCount[]; approx?: boolean; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void }) {
  const ref = keyRef(b.instance, b.key);
  const section = `value:${ref}`;
  const counted = counts?.map((v) => ({ value: `${ref}=${v.value}`, label: valueLabel(v.value), cards: v.cards, approx }));
  // A selected value with no matches still gets a row, so it can be cleared.
  const rows = counted && [
    ...counted,
    ...q.terms
      .filter((t) => sectionOf(t) === section && !counted.some((r) => r.value === t.value))
      .map((t) => ({ value: t.value, label: valueLabel(t.value.slice(ref.length + 1)), cards: 0 })),
  ];
  return <Section kind="value" title={b.label} instance={instanceLabel} section={section} rows={rows} q={q} open={open} onOpen={onOpen} onChange={onChange} />;
}

/** scoreTermOf is the score term for a key, if any: an instance's, or a merged key's, which names none. */
function scoreTermOf(q: CardQuery, instance: number | undefined, key: string) {
  return q.terms.find((t) => {
    const s = t.kind === "score" ? parseScore(t.value) : undefined;
    return s?.key === key && s.instance === instance;
  });
}

/** ScoreFilter narrows to cards whose value of a score key is in a range, or that have none. */
function ScoreFilter({ b, q, onChange }: { b: BrowseKey; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const at = { instance: b.instance, key: b.key };
  const term = scoreTermOf(q, b.instance, b.key);
  const score = term ? parseScore(term.value) : undefined;
  const [low = 0, high = 100] = b.range ?? [];
  const step = high - low >= 10 ? 1 : (high - low) / 100;
  const [range, setRange] = useState<[number, number]>([score?.gte ?? low, score?.lte ?? high]);
  const [lo, hi] = range;
  // A thumb at either end means no bound on that side.
  const commit = () => {
    const s = { ...at, gte: lo > low ? lo : undefined, lte: hi < high ? hi : undefined };
    if (scoreTerm(s)?.value !== term?.value) onChange(setScore(q, s));
  };
  const pct = (v: number) => `${((v - low) / (high - low)) * 100}%`;
  const thumb = { disabled: score?.none, min: low, max: high, step, onPointerUp: commit, onKeyUp: commit };
  return (
    <div className={"score-filter" + (term ? " included" : "") + (score?.none ? " none" : "")}>
      <span className="score-name" title={b.label}>{b.label}</span>
      <span className="score-value">{!score?.none && (lo > low || hi < high) && `${lo}–${hi}`}</span>
      <label className="score-none" title="Not scored">
        <input type="checkbox" aria-label={`${b.label} not evaluated`} checked={!!score?.none}
          onChange={(e) => onChange(setScore(q, { ...at, none: e.target.checked }))} />
        none
      </label>
      <div className="score-range" style={{ "--lo": pct(lo), "--hi": pct(hi) } as CSSProperties}>
        {/* At the top end the min thumb must sit above the max one, or it can't be dragged back down. */}
        <input type="range" aria-label={`${b.label} at least`} value={lo} {...thumb} style={lo === high ? { zIndex: 2 } : undefined}
          onChange={(e) => setRange([Math.min(Number(e.target.value), hi), hi])} />
        <input type="range" aria-label={`${b.label} at most`} value={hi} {...thumb}
          onChange={(e) => setRange([lo, Math.max(Number(e.target.value), lo)])} />
      </div>
    </div>
  );
}

/**
 * Facets is the filter sidebar: one section per term kind, then the browsable enrichment keys under their section headings,
 * each counting its values over the other sections' filters.
 */
export function Facets({ kind, q, names, browse = [], onChange }: { kind: Kind; q: CardQuery; names: Names; browse?: BrowseKey[]; onChange: (q: CardQuery) => void }) {
  const [collapsed, setCollapsed] = useState<string[]>(readCollapsed);
  const sections = sectionsFor(kind).filter((s) => s !== "collection" || names.collections.size > 0);
  const open = (s: string) => !collapsed.includes(s);
  const rows = useRows(kind, q, names, sections, open);
  const listed = groups.flatMap(([id]) => browse.filter((b) => (b.section ?? "") === id));
  const valueRefs = listed
    .filter((b) => isValueKey(b) && open(groupOf(b)))
    .map((b) => keyRef(b.instance, b.key))
    .filter((ref) => open(`value:${ref}`));
  const valueCounts = useValueCounts(q, valueRefs);
  const flip = (s: string) => {
    const next = open(s) ? [...collapsed, s] : collapsed.filter((x) => x !== s);
    setCollapsed(next);
    saveCollapsed(next);
  };
  return (
    <aside className="facets" aria-label="Filters">
      <label className="check" title="Shows only cards or lorebooks with a source another one also has, such as the same page imported twice. A fork doesn't count.">
        <input type="checkbox" checked={q.sharesSource} onChange={(e) => onChange({ ...q, sharesSource: e.target.checked, page: 1 })} /> Shares a source
      </label>
      {sections.map((s) => (
        <Section key={s} kind={s} rows={rows[s]} q={q} open={open(s)} onOpen={() => flip(s)} onChange={onChange} />
      ))}
      {groups.map(([id, title]) => {
        const keys = browse.filter((b) => (b.section ?? "") === id);
        if (!keys.length) return null;
        const group = `group:${id || "other"}`;
        return (
          <section key={group} className="facet-section facet-group">
            <h2>
              <button className="facet-head" aria-expanded={open(group)} onClick={() => flip(group)}>
                {title}
              </button>
            </h2>
            {open(group) &&
              keys.map((b) => {
                const ref = keyRef(b.instance, b.key);
                // Keyed by the term, so a cleared chip resets the slider.
                if (!isValueKey(b)) return <ScoreFilter key={ref + (scoreTermOf(q, b.instance, b.key)?.value ?? "")} b={b} q={q} onChange={onChange} />;
                const section = `value:${ref}`;
                const instanceLabel = b.instance ? names.instances.get(b.instance) : undefined;
                return <ValueSection key={section} b={b} instanceLabel={instanceLabel} {...valueCounts.get(ref)} q={q} open={open(section)} onOpen={() => flip(section)} onChange={onChange} />;
              })}
          </section>
        );
      })}
    </aside>
  );
}
```

- [ ] **Step 6: Style the groups**

In `web/src/cards/Cards.css`, replace the `.score-instance h3 { ... }` block with:

```css
.facet-group .facet-section h3 {
  margin: calc(var(--space) * 0.75) 0 calc(var(--space) * 0.25);
  padding-left: var(--space);
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd web && npm run typecheck && npx vitest run src/cards`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add web/src/cards/query.ts web/src/cards/Cards.tsx web/src/cards/Facets.tsx web/src/cards/Cards.css web/src/cards/Cards.test.tsx
git commit -m "feat(web): group enrichment filters by section, and merge keys across plugins"
```

## Task 6: Bar list on the card page

**Files:**
- Modify: `web/src/card/Enrichments.tsx`
- Modify: `web/src/card/Card.css`
- Test: `web/src/card/Enrichments.test.tsx`

- [ ] **Step 1: Write the failing test**

In `web/src/card/Enrichments.test.tsx`, replace the `value` helper with:

```ts
const value = (key: string, label: string, v: unknown, stale = false) => ({
  instance_id: 5, instance_name: "Judge", revision_id: 1, key, label, type: "score", value: v, meta: {}, created_at: "2026-10-15T00:00:00Z", current: true, stale, browse: true,
});
```

In the `/api/cards/7/enrichments` fixture, add two values after `value("summary", ...)`:

```ts
          { ...value("consistency", "Consistency", 62), browse: false, range: [0, 100] },
          { ...value("pov", "Point of view", "First person"), type: "text", browse: false },
```

Add this test at the end of the file:

```ts
test("an instance's other scores show as one bar list after its browsable keys, and its other text below", async () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Enrichments id={7} />
    </QueryClientProvider>,
  );
  const judge = (await screen.findByRole("heading", { name: "Judge" })).closest(".enrichment-instance") as HTMLElement;
  const bar = within(judge).getByLabelText("Consistency") as HTMLMeterElement;
  expect(bar.tagName).toBe("METER");
  expect(bar.value).toBe(62);
  expect(bar.closest(".enrichment-bars")).not.toBeNull();
  const quality = within(judge).getByText("78");
  const pov = within(judge).getByText("First person");
  expect(quality.compareDocumentPosition(bar) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(bar.compareDocumentPosition(pov) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd web && npx vitest run src/card/Enrichments.test.tsx`
Expected: FAIL, `Unable to find a label with the text of: Consistency`.

- [ ] **Step 3: Split each instance's values**

In `web/src/card/Enrichments.tsx`, replace `InstanceValues`'s doc comment with:

```ts
/** InstanceValues is one instance's values: its browsable keys, then its other scores as bars, then its other text and lists. */
```

In `InstanceValues`, replace:

```ts
  const short = values.filter((v) => !isLong(v));
  const long = values.filter(isLong);
  const staleTitle = (v: Value) => (v.stale ? "Stale: from an older revision" : undefined);
```

with:

```ts
  const other = values.filter((v) => !v.browse);
  const bars = other.filter((v) => v.type === "score" && typeof v.value === "number");
```

Replace everything in its returned JSX from `{short.length > 0 && (` through the `))}` that closes `long.map`, with:

```tsx
      <Values values={values.filter((v) => v.browse)} />
      <Bars values={bars} />
      <Values values={other.filter((v) => !bars.includes(v))} />
```

At the end of the file, add:

```tsx
const staleTitle = (v: Value) => (v.stale ? "Stale: from an older revision" : undefined);

/** Values shows short values in a grid, and lists and long text at full width. */
function Values({ values }: { values: Value[] }) {
  const short = values.filter((v) => !isLong(v));
  const long = values.filter(isLong);
  return (
    <>
      {short.length > 0 && (
        <dl className="enrichment-grid">
          {short.map((v) => (
            <div key={v.key} className={v.stale ? "stale" : undefined} title={staleTitle(v)}>
              <dt>{v.label}</dt>
              <dd>
                {enrichmentText(v.value, v.range)}
                {v.stale && <span className="muted"> (stale)</span>}
              </dd>
            </div>
          ))}
        </dl>
      )}
      {long.map((v) => (
        <div key={v.key} className={v.stale ? "enrichment-long stale" : "enrichment-long"} title={staleTitle(v)}>
          <h4>
            {v.label}
            {v.stale && <span className="muted"> (stale)</span>}
          </h4>
          {Array.isArray(v.value) ? (
            <ul>
              {v.value.map((item, i) => (
                <li key={i}>{String(item)}</li>
              ))}
            </ul>
          ) : (
            <p>{String(v.value)}</p>
          )}
        </div>
      ))}
    </>
  );
}

/** Bars shows score values as one compact list of bars. */
function Bars({ values }: { values: Value[] }) {
  if (!values.length) return null;
  return (
    <div className="enrichment-bars">
      {values.map((v) => (
        <div key={v.key} className={v.stale ? "stale" : undefined} title={staleTitle(v)}>
          <span>{v.label}</span>
          <meter aria-label={v.label} min={v.range?.[0]} max={v.range?.[1]} value={Number(v.value)} />
          <span>{enrichmentText(v.value, v.range)}</span>
        </div>
      ))}
    </div>
  );
}
```

- [ ] **Step 4: Style the bars**

In `web/src/card/Card.css`, after the `.enrichment-grid .stale dd, .enrichment-long.stale` block, add:

```css
.enrichment-bars {
  margin: 0 0 var(--space);
  font-size: 0.9em;
}

.enrichment-bars > div {
  display: grid;
  grid-template-columns: minmax(0, 12em) minmax(60px, 1fr) 3em;
  gap: var(--space);
  align-items: center;
}

.enrichment-bars > div > span:first-child {
  color: var(--text-muted);
}

.enrichment-bars > div > span:last-child {
  text-align: right;
}

.enrichment-bars meter {
  width: 100%;
}

.enrichment-bars .stale {
  opacity: 0.55;
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd web && npm run typecheck && npx vitest run src/card`
Expected: PASS, including the existing Enrichments tests, which mark their values browsable.

- [ ] **Step 6: Commit**

```bash
git add web/src/card/Enrichments.tsx web/src/card/Enrichments.test.tsx web/src/card/Card.css
git commit -m "feat(web): show a card's other enrichment scores as one bar list"
```

## Task 7: Run what CI runs

- [ ] **Step 1: Run the full checks**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass. `git status` shows no changes to `web/openapi.json` or `web/src/api/schema.d.ts` after another `npm run gen`.

- [ ] **Step 2: Read the diff once more**

Run: `git diff develop...HEAD --stat` and read the diff for dead code (no references to `ScoreSection`, `scoreKeys`, `.score-instance`, or `where.scorers` remain: `grep -rn 'ScoreSection\|scoreKeys\|score-instance\|w.scorers' internal web/src` prints nothing).

## Task 8: Pin the plugins repo to the new core

Start this once the core change is merged to `develop` and pushed. Work in `/Users/dgibbons/git/innkeeper-plugins`.

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Bring `main` up to date**

```bash
cd /Users/dgibbons/git/innkeeper-plugins
git checkout main && git pull --ff-only
grep -n protocol_version forge-ai/plugin.toml internal/indexcheck/main.go
```

Expected: `forge-ai/plugin.toml` has `protocol_version = "1.1"` and `indexcheck` checks it. Without both, stop: the plugin changes below need them.

- [ ] **Step 2: Pin innkeeper**

```bash
sha=$(git ls-remote https://github.com/inkandale/innkeeper refs/heads/develop | cut -c1-12)
go get github.com/inkandale/innkeeper@$sha && go mod tidy
go vet ./... && go test -race ./...
```

Expected: `go.mod` names a pseudo-version ending in `$sha`, and the tests pass.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: pin innkeeper to develop ${sha:0:7}"
```

## Task 9: card-lint

**Files:**
- Modify: `card-lint/lint.go:77`
- Modify: `card-lint/plugin.toml`, `index.toml`
- Test: `card-lint/main_test.go:102-105`

- [ ] **Step 1: Write the failing test**

In `card-lint/main_test.go`, in `TestEachCheckListsItsCodeAndTakesItsPoints`, change the `no_char_macro` row's points from `10` to `0`:

```go
		"no_char_macro": {func(c map[string]string) {
			c["description"] = englishText
			c["first_mes"] = "\"You are dripping on the floor.\""
		}, 0},
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./card-lint/ -run TestEachCheckListsItsCodeAndTakesItsPoints/no_char_macro -v`
Expected: FAIL, `lint = 90, want 100`.

- [ ] **Step 3: Take no points for `no_char_macro`**

In `card-lint/lint.go`, replace `	{"no_char_macro", 10, func(f facts) bool {` with:

```go
	// Listed, but free: it fires on many good cards.
	{"no_char_macro", 0, func(f facts) bool {
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./card-lint/`
Expected: PASS.

- [ ] **Step 5: Commit the score change**

```bash
git add card-lint/lint.go card-lint/main_test.go
git commit -m "fix(card-lint): leave no_char_macro out of the lint score"
```

- [ ] **Step 6: Mark the browsable keys**

In `card-lint/plugin.toml`, add `protocol_version = "1.2"` after `protocol = 1`, and replace the `[enrichments]` keys with:

```toml
lint = { type = "score", range = [0, 100], label = "Lint", grid = true, priority = 100, browse = true, section = "score" }
tokens = { type = "score", range = [0, 1000000], label = "Always-on tokens" }
problems = { type = "list", label = "Problems", browse = true, section = "problems" }
language = { type = "text", label = "Language", browse = true, section = "kind" }
```

In `index.toml`, replace:

```toml
path = "card-lint"
version = "0.4.4"
protocol = 1
```

with:

```toml
path = "card-lint"
version = "0.4.4"
protocol = 1
protocol_version = "1.2"
```

Run: `go run ./internal/indexcheck && make check`
Expected: both pass.

```bash
git add card-lint/plugin.toml index.toml
git commit -m "feat(card-lint): list lint, language, and problems in the browse sections"
```

- [ ] **Step 7: Release 0.5.0**

Change `version = "0.4.4"` to `version = "0.5.0"` in both `card-lint/plugin.toml` and the `card-lint` entry of `index.toml`.

Run: `go run ./internal/indexcheck`
Expected: PASS.

```bash
git add card-lint/plugin.toml index.toml
git commit -m "chore(card-lint): release 0.5.0"
```

## Task 10: crucible

**Files:**
- Modify: `crucible/plugin.toml`, `index.toml`

- [ ] **Step 1: Mark the browsable keys and relabel**

In `crucible/plugin.toml`, add `protocol_version = "1.2"` after `protocol = 1`, and replace these `[enrichments]` lines, leaving the others as they are:

| Old | New |
|---|---|
| `quality = { type = "score", range = [0, 100], label = "Crucible quality", grid = true, priority = 200 }` | `quality = { type = "score", range = [0, 100], label = "Quality", grid = true, priority = 200, browse = true, section = "score" }` |
| `rating = { type = "text", label = "Rating" }` | `rating = { type = "text", label = "Content rating", browse = true, section = "warnings" }` |
| `card_type = { type = "text", label = "Card type" }` | `card_type = { type = "text", label = "Card type", browse = true, section = "kind" }` |
| `pov = { type = "text", label = "Point of view" }` | `pov = { type = "text", label = "Point of view", browse = true, section = "kind" }` |
| `format = { type = "text", label = "Format" }` | `format = { type = "text", label = "Format", browse = true, section = "kind" }` |
| `flags = { type = "list", label = "Flags" }` | `flags = { type = "list", label = "Flags", browse = true, section = "warnings" }` |

In `index.toml`, add `protocol_version = "1.2"` after `protocol = 1` in the block that starts `path = "crucible"`.

Run: `go run ./internal/indexcheck && make check && go test -race ./crucible/`
Expected: all pass.

```bash
git add crucible/plugin.toml index.toml
git commit -m "feat(crucible): list its quality, kind, and content keys in the browse sections"
```

- [ ] **Step 2: Release 0.3.1**

Change `version = "0.3.0"` to `version = "0.3.1"` in both `crucible/plugin.toml` and the `crucible` entry of `index.toml`.

Run: `go run ./internal/indexcheck`
Expected: PASS.

```bash
git add crucible/plugin.toml index.toml
git commit -m "chore(crucible): release 0.3.1"
```

## Task 11: forge-ai

**Files:**
- Modify: `forge-ai/plugin.toml`, `index.toml`

- [ ] **Step 1: Mark quality browsable**

In `forge-ai/plugin.toml`, change `protocol_version = "1.1"` to `protocol_version = "1.2"`, and replace the `quality` line with:

```toml
quality = { type = "score", range = [0, 100], label = "Quality", grid = true, priority = 300, browse = true, section = "score" }
```

In `index.toml`'s `forge-ai` entry, change `protocol_version = "1.1"` to `protocol_version = "1.2"`.

Run: `go run ./internal/indexcheck && make check`
Expected: both pass.

```bash
git add forge-ai/plugin.toml index.toml
git commit -m "feat(forge-ai): list quality alone in the browse sections"
```

- [ ] **Step 2: Release 0.14.2**

Change `version = "0.14.1"` to `version = "0.14.2"` in both `forge-ai/plugin.toml` and the `forge-ai` entry of `index.toml`.

Run: `go run ./internal/indexcheck`
Expected: PASS.

```bash
git add forge-ai/plugin.toml index.toml
git commit -m "chore(forge-ai): release 0.14.2"
```

## Task 12: chub-archive

**Files:**
- Modify: `chub-archive/plugin.toml`, `index.toml`

- [ ] **Step 1: Mark the browsable keys**

In `chub-archive/plugin.toml`, add `protocol_version = "1.2"` after `protocol = 1`, and replace the `rating` and `topics` lines with:

```toml
rating = { type = "score", range = [0, 5], label = "Chub rating", grid = true, browse = true, section = "popularity" }
topics = { type = "list", label = "Chub topics", browse = true, section = "kind" }
```

In `index.toml`, replace:

```toml
path = "chub-archive"
version = "0.3.4"
protocol = 1
```

with:

```toml
path = "chub-archive"
version = "0.3.4"
protocol = 1
protocol_version = "1.2"
```

Run: `go run ./internal/indexcheck && make check`
Expected: both pass.

```bash
git add chub-archive/plugin.toml index.toml
git commit -m "feat(chub-archive): list rating and topics in the browse sections"
```

- [ ] **Step 2: Release 0.3.5**

Change `version = "0.3.4"` to `version = "0.3.5"` in `chub-archive/plugin.toml` and in the `index.toml` block that starts `path = "chub-archive"`.

Run: `go run ./internal/indexcheck`
Expected: PASS.

```bash
git add chub-archive/plugin.toml index.toml
git commit -m "chore(chub-archive): release 0.3.5"
```

## Task 13: Run what the plugins CI runs

- [ ] **Step 1: Run the checks**

```bash
cd /Users/dgibbons/git/innkeeper-plugins
go vet ./...
go test -race ./...
go run ./internal/indexcheck
make check
make test-ui
```

Expected: all pass. A push to `main` publishes each new version, so push only after the core with protocol 1.2 is deployed or ready to deploy: until then the live Innkeeper shows these versions as needing a newer Innkeeper.
