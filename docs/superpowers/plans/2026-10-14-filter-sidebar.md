# Filter sidebar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Cards and Lorebooks filter sidebar with collapsible Target, State, Tags, Sources and Collections sections. Values in a section combine with OR, and each row has "only" and exclude buttons. A section's counts leave out its own selections.

**Architecture:** The API's `target_state` filter takes lists of instances and states. Everything else is web. `web/src/cards/query.ts` is the one place that knows which section a term belongs to. It builds the filter and the count searches. `Facets.tsx` runs those searches through React Query and renders the sections. Spec: `docs/superpowers/specs/2026-10-13-filter-sidebar-design.md`, section "PR 2".

**Tech Stack:** Go, PostgreSQL, huma; React, TypeScript, TanStack Query, Vitest, Testing Library.

---

## Before you start

Database tests skip silently without a database. Export this in every shell, including subagent shells:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

Use `-count=1`. A DB package that finishes in milliseconds skipped.

Work in `~/git/innkeeper-filter-sidebar-spec` on branch `filter-sidebar-spec`. Never run git in `~/git/innkeeper`. Don't checkout, switch, stash or reset; to watch a test fail, edit the code back by hand.

Commits are conventional (`feat:`, `fix:`, `refactor:`, `test:`). Never `!` or a `BREAKING CHANGE` footer. No attribution lines.

Never hand-edit `web/openapi.json` or `web/src/api/schema.d.ts`; run `cd web && npm run gen`.

Web checks: `cd web && npm run typecheck && npm test`. One file: `cd web && npx vitest run src/cards/query.test.ts`.

## Design notes the tasks rely on

- **Sections are term kinds.** A term is `{ kind, value, not }`, where kind is `target`, `state`, `tag`, `source` or `collection`. Each kind is one sidebar section. Lorebooks have `target`, `state` and `source`.
- **Filter shape.** Included values of one kind are ORed. All included targets and states go into one `target_state { instances, states }`, so a state applies to the selected targets. Each excluded value is its own `not`. The parts are ANDed.
- **Count searches.**
  - Target, Tags, Sources and Collections each send `{ text, filter: <every other section's terms>, facets: [<own facet>], page: { size: 1 } }`. Page size 0 means "default 50" in this API, so use 1.
  - State can't use the `targets` facet: it counts per target, so a card bound to two targets would count twice. Instead each of the six states sends `{ text, filter: <every other section's terms plus that state>, page: { size: 1 } }` and uses `total`.
  - A Target row's count is the sum of the `targets` facet rows for that instance, limited to the selected states when any are selected. A card has one binding per target, so this is exact.
  - Collapsed sections send nothing.
- **URL.** `f=target:3`, `f=state:ahead`, `f=-tag:nsfw`. `parseQuery` drops terms whose value isn't valid for the kind, so old `target:2:ahead` links are ignored.

## Files

| File | Change |
|---|---|
| `internal/library/search.go` | `TargetFilter { Instances, States }` for the `target_state` filter |
| `internal/library/search_test.go`, `internal/targets/filter_test.go` | Tests for the list semantics |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `web/src/cards/query.ts` | Term kinds, validation, `filterFor`, count searches, `onlyTerm`, `sectionsFor` |
| `web/src/cards/query.test.ts` | Tests for the above |
| `web/src/cards/Facets.tsx` | Rewritten sidebar |
| `web/src/cards/Cards.tsx` | Wires the new `Facets`; chip labels |
| `web/src/cards/Cards.css` | Section, row, action button styles |
| `web/src/cards/Cards.test.tsx`, `web/src/cards/Lorebooks.test.tsx` | UI tests |
| `docs/superpowers/specs/2026-10-13-filter-sidebar-design.md` | Counts paragraph matches the build |

---

### Task 1: `target_state` takes lists

**Files:**
- Modify: `internal/library/search.go:38-57` (types), `:220-225` (filter case)
- Test: `internal/targets/filter_test.go:10-42`, `internal/library/search_test.go:156`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`
- Modify: `web/src/cards/query.ts:69`, `web/src/cards/query.test.ts:66`

`TargetState` stays as it is: it is also the shape of `Hit.Targets`. The filter gets its own type.

- [ ] **Step 1: Write the failing test**

Replace `TestFilterTargetState` in `internal/targets/filter_test.go`:

```go
func TestFilterTargetState(t *testing.T) {
	e := setup(t, nil)
	edited, unpushed := e.newCard("Carrow"), e.newCard("Dunmore")
	e.push(e.card)
	e.push(edited)
	e.update(edited, func(ed *library.Edit) { ed.Data = setField(ed.Data, "first_mes", "Well met.") })

	search := func(f library.Filter) []int64 {
		t.Helper()
		ids, err := e.lib.SearchIDs(e.ctx, library.Card, library.Query{Filter: &f})
		if err != nil {
			t.Fatal(err)
		}
		return ids
	}
	match := func(instances []int64, states ...string) library.Filter {
		return library.Filter{TargetState: &library.TargetFilter{Instances: instances, States: states}}
	}
	here := []int64{e.inst}
	for name, tc := range map[string]struct {
		f    library.Filter
		want []int64
	}{
		"state on a target":        {match(here, "ahead"), []int64{edited}},
		"other state on a target":  {match(here, "in_sync"), []int64{e.card}},
		"state on another target":  {match([]int64{e.inst + 1000}, "in_sync"), nil},
		"state on any target":      {match(nil, "ahead"), []int64{edited}},
		"any state on a target":    {match(here), []int64{e.card, edited}},
		"pushed anywhere":          {match(nil), []int64{e.card, edited}},
		"either of two states":     {match(here, "ahead", "in_sync"), []int64{e.card, edited}},
		"not in sync on a target":  {library.Filter{Not: ptr(match(here, "in_sync"))}, []int64{edited, unpushed}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := search(tc.f); !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
```

If `ptr` already exists in package `targets` (`grep -n "func ptr" internal/targets/*_test.go`), don't redeclare it. `slices.Equal(nil, []int64{})` is true, so `nil` works for "no matches".

In `internal/library/search_test.go`, change the `"bad target state"` case to:

```go
		"bad target state": {Filter: &Filter{TargetState: &TargetFilter{States: []string{"pushed"}}}},
```

- [ ] **Step 2: Run it and watch it fail**

Run: `go test -count=1 ./internal/targets -run TestFilterTargetState`
Expected: build failure, `undefined: library.TargetFilter`.

- [ ] **Step 3: Implement**

In `internal/library/search.go`, change the `Filter.TargetState` field and add the type below `TargetState`:

```go
	TargetState *TargetFilter `json:"target_state,omitempty" doc:"Is pushed to one of instances and stands as one of states there. An empty list matches any value."`
```

```go
// TargetFilter matches an entity by its bindings: one on any of Instances
// whose status is any of States.
type TargetFilter struct {
	Instances []int64  `json:"instances,omitempty" maxItems:"100" doc:"Plugin instance IDs"`
	States    []string `json:"states,omitempty" maxItems:"6" doc:"in_sync, ahead, remote_changed, diverged, missing, or cant_check"`
}
```

Replace the `f.TargetState` case in `(*where).filter`:

```go
	case f.TargetState != nil:
		cond := `bs.` + w.k.FK + ` = c.id`
		if len(f.TargetState.Instances) > 0 {
			cond += ` AND bs.instance_id = ANY(` + w.arg(f.TargetState.Instances) + `)`
		}
		for _, s := range f.TargetState.States {
			if !slices.Contains(targetStatuses, s) {
				return "", fmt.Errorf("%w: target_state.states must be from %v", ErrInvalid, targetStatuses)
			}
		}
		if len(f.TargetState.States) > 0 {
			cond += ` AND bs.status = ANY(` + w.arg(f.TargetState.States) + `)`
		}
		return `EXISTS (SELECT 1 FROM binding_status bs WHERE ` + cond + `)`, nil
```

Update the `TargetState` type comment if it still says it's used by the filter. It now describes a hit's status on one target only.

- [ ] **Step 4: Run the Go tests**

Run: `go vet ./... && go test -count=1 ./internal/targets ./internal/library ./internal/api`
Expected: PASS. If an API test sends the old `{instance, is}` shape (`git grep -n '"is":' internal/api/*_test.go`), move it to `{instances: [...], states: [...]}`.

- [ ] **Step 5: Regenerate the schema and keep the web building**

Run: `cd web && npm run gen && npm run typecheck`
Expected: a type error at `web/src/cards/query.ts:69`.

In `web/src/cards/query.ts`, change that line to:

```ts
    f = { target_state: { instances: [instance], states: [is] } };
```

and in `web/src/cards/query.test.ts` change the expected `{ target_state: { instance: 2, is: "ahead" } }` to `{ target_state: { instances: [2], states: ["ahead"] } }`. Task 2 rewrites both.

Run: `cd web && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal web/openapi.json web/src/api/schema.d.ts web/src/cards/query.ts web/src/cards/query.test.ts
git commit -m "feat(search): target_state filter takes lists of instances and states"
```

---

### Task 2: Sections in `query.ts`

`query.ts` learns the five sections, validates values, ORs within a section, and builds count searches. The old Sync section in `Facets.tsx` gets a stopgap so the app keeps working until Task 3 replaces it.

**Files:**
- Modify: `web/src/cards/query.ts`
- Test: `web/src/cards/query.test.ts`
- Modify: `web/src/cards/Facets.tsx` (Sync rows only), `web/src/cards/Cards.tsx` (`termLabel`)

- [ ] **Step 1: Write the failing tests**

Replace `web/src/cards/query.test.ts` with:

```ts
import {
  countSearch,
  filterFor,
  onlyTerm,
  parseQuery,
  sectionsFor,
  stateCountSearch,
  toLorebookSearch,
  toParams,
  toSearch,
  toggleTerm,
  type CardQuery,
  type Term,
} from "./query";

const base: CardQuery = { text: "", terms: [], sort: "updated", page: 1, size: 48, view: "grid" };
const term = (kind: Term["kind"], value: string, not = false): Term => ({ kind, value, not });

test("an empty URL is the default query", () => {
  expect(parseQuery(new URLSearchParams())).toEqual(base);
});

test("a query round-trips through the URL", () => {
  const q: CardQuery = {
    text: "dwarf smith",
    terms: [term("tag", "fantasy"), term("tag", "a:b", true), term("target", "2"), term("state", "ahead"), term("collection", "4")],
    sort: "name",
    page: 3,
    size: 96,
    view: "list",
  };
  expect(parseQuery(toParams(q))).toEqual(q);
});

test("defaults stay out of the URL", () => {
  expect(toParams(base).toString()).toBe("");
  expect(toParams({ ...base, text: "x", sort: "relevance" }).toString()).toBe("q=x");
});

test("text searches default to relevance", () => {
  expect(parseQuery(new URLSearchParams("q=x")).sort).toBe("relevance");
  expect(parseQuery(new URLSearchParams("q=x&sort=name")).sort).toBe("name");
});

test("bad values fall back to defaults", () => {
  const q = parseQuery(new URLSearchParams("page=-2&size=7&sort=size&view=x&f=color:red&f=nocolon"));
  expect(q).toEqual(base);
});

test("terms with values their section can't take are dropped, including old target links", () => {
  const bad = ["target:2:ahead", "target:0", "target:x", "state:pushed", "collection:", "collection:-1", "tag:", "source:"];
  const q = parseQuery(new URLSearchParams(bad.map((f) => ["f", f])));
  expect(q.terms).toEqual([]);
});

test("filterFor ORs within a section, ANDs sections, and negates each excluded value", () => {
  expect(
    filterFor([
      term("tag", "fantasy"),
      term("tag", "pirate"),
      term("tag", "nsfw", true),
      term("source", "chub"),
      term("collection", "3", true),
    ]),
  ).toEqual({
    and: [
      { or: [{ tag: "fantasy" }, { tag: "pirate" }] },
      { source: "chub" },
      { not: { tag: "nsfw" } },
      { not: { collection: 3 } },
    ],
  });
  expect(filterFor([])).toBeUndefined();
});

test("selected targets and states share one target_state", () => {
  expect(filterFor([term("target", "2"), term("target", "5"), term("state", "ahead")])).toEqual({
    and: [{ target_state: { instances: [2, 5], states: ["ahead"] } }],
  });
  expect(filterFor([term("state", "ahead")])).toEqual({ and: [{ target_state: { states: ["ahead"] } }] });
  expect(filterFor([term("target", "2", true)])).toEqual({ and: [{ not: { target_state: { instances: [2] } } }] });
});

test("toSearch builds the API body", () => {
  const body = toSearch({ ...base, text: "dwarf", terms: [term("tag", "fantasy")], page: 2 });
  expect(body).toMatchObject({
    text: "dwarf",
    sort: "updated",
    filter: { and: [{ tag: "fantasy" }] },
    page: { size: 48, number: 2 },
  });
  expect(toSearch(base).filter).toBeUndefined();
});

test("a section's count search leaves out the section's own terms", () => {
  const q = { ...base, text: "dwarf", terms: [term("tag", "fantasy"), term("source", "chub"), term("state", "ahead")] };
  expect(countSearch(q, "tag")).toEqual({
    text: "dwarf",
    filter: { and: [{ target_state: { states: ["ahead"] } }, { source: "chub" }] },
    facets: ["tags"],
    page: { size: 1 },
  });
  expect(countSearch(q, "target").facets).toEqual(["targets"]);
});

test("a state's count search swaps in that state and keeps the selected targets", () => {
  const q = { ...base, terms: [term("target", "2"), term("state", "ahead"), term("tag", "fantasy")] };
  expect(stateCountSearch(q, "in_sync")).toEqual({
    filter: { and: [{ target_state: { instances: [2], states: ["in_sync"] } }, { tag: "fantasy" }] },
    page: { size: 1 },
  });
});

test("toggleTerm adds, flips, and removes, and resets the page", () => {
  const tag = term("tag", "fantasy");
  const added = toggleTerm({ ...base, page: 4 }, tag);
  expect(added.terms).toEqual([tag]);
  expect(added.page).toBe(1);
  expect(toggleTerm(added, { ...tag, not: true }).terms).toEqual([{ ...tag, not: true }]);
  expect(toggleTerm(added, tag).terms).toEqual([]);
});

test("onlyTerm keeps one value in its section and leaves other sections alone", () => {
  const q = { ...base, page: 3, terms: [term("tag", "fantasy"), term("tag", "nsfw", true), term("source", "chub")] };
  const only = onlyTerm(q, term("tag", "pirate"));
  expect(only.terms).toEqual([term("source", "chub"), term("tag", "pirate")]);
  expect(only.page).toBe(1);
});

test("lorebooks have no tag or collection sections", () => {
  expect(sectionsFor("card")).toEqual(["target", "state", "tag", "source", "collection"]);
  expect(sectionsFor("lorebook")).toEqual(["target", "state", "source"]);
});

test("toLorebookSearch asks for linked cards and sync", () => {
  const q = parseQuery(new URLSearchParams("q=anvil&f=source:chub&page=2"));
  expect(toLorebookSearch(q)).toMatchObject({
    text: "anvil",
    filter: { and: [{ source: "chub" }] },
    sort: "relevance",
    fields: ["name", "description"],
    include: ["cards", "targets"],
    page: { size: 48, number: 2 },
  });
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd web && npx vitest run src/cards/query.test.ts`
Expected: FAIL. `filterFor`, `countSearch` and the others aren't exported yet.

- [ ] **Step 3: Implement**

Replace `web/src/cards/query.ts` with the code below. `toSearch` keeps asking for `facets` until Task 3 moves counting into the sidebar.

```ts
import type { components } from "../api/schema";

type Filter = components["schemas"]["Filter"];
type Search = components["schemas"]["Query"];
type TargetIs = components["schemas"]["TargetState"]["is"];

// Typed from the schema so a new state fails the build here.
export const targetStates: TargetIs[] = ["in_sync", "ahead", "remote_changed", "diverged", "missing", "cant_check"];
/** kinds are the filter sections, in sidebar order. */
const kinds = ["target", "state", "tag", "source", "collection"] as const;
const sorts = ["relevance", "updated", "name", "created"] as const;
export const sizes = [24, 48, 96];

export type TermKind = (typeof kinds)[number];
/** Term is one filter: a section's value, or its negation. */
export type Term = { kind: TermKind; value: string; not: boolean };
export type Sort = (typeof sorts)[number];
export type CardQuery = { text: string; terms: Term[]; sort: Sort; page: number; size: number; view: "grid" | "list" };

export const defaultSort = (text: string): Sort => (text ? "relevance" : "updated");

/** sectionsFor lists the sidebar sections a kind of entity has; lorebooks have no tags or collections. */
export const sectionsFor = (entity: "card" | "lorebook"): TermKind[] =>
  entity === "card" ? [...kinds] : ["target", "state", "source"];

const id = (v: string) => /^[1-9]\d*$/.test(v);

/** valid says whether value can be a term of kind. */
function valid(kind: TermKind, value: string): boolean {
  if (kind === "target" || kind === "collection") return id(value);
  if (kind === "state") return targetStates.some((s) => s === value);
  return value !== "";
}

/** parseQuery reads the Cards page state from its URL, ignoring anything malformed. */
export function parseQuery(p: URLSearchParams): CardQuery {
  const terms: Term[] = [];
  for (const raw of p.getAll("f")) {
    const not = raw.startsWith("-");
    const s = not ? raw.slice(1) : raw;
    const i = s.indexOf(":");
    const kind = s.slice(0, i) as TermKind;
    const value = s.slice(i + 1);
    if (i > 0 && kinds.includes(kind) && valid(kind, value)) terms.push({ kind, value, not });
  }
  const text = p.get("q") ?? "";
  const sort = p.get("sort") as Sort;
  const page = Number(p.get("page"));
  const size = Number(p.get("size"));
  return {
    text,
    terms,
    sort: sorts.includes(sort) ? sort : defaultSort(text),
    page: Number.isInteger(page) && page > 0 ? page : 1,
    size: sizes.includes(size) ? size : 48,
    view: p.get("view") === "list" ? "list" : "grid",
  };
}

/** toParams writes the state back, leaving defaults out. */
export function toParams(q: CardQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (q.text) p.set("q", q.text);
  for (const t of q.terms) p.append("f", `${t.not ? "-" : ""}${t.kind}:${t.value}`);
  if (q.sort !== defaultSort(q.text)) p.set("sort", q.sort);
  if (q.page !== 1) p.set("page", String(q.page));
  if (q.size !== 48) p.set("size", String(q.size));
  if (q.view !== "grid") p.set("view", q.view);
  return p;
}

/** condition is the filter for one value of kind. */
function condition(kind: TermKind, value: string): Filter {
  switch (kind) {
    case "target":
      return { target_state: { instances: [Number(value)] } };
    case "state":
      return { target_state: { states: [value as TargetIs] } };
    case "tag":
      return { tag: value };
    case "source":
      return { source: value };
    case "collection":
      return { collection: Number(value) };
  }
}

/** filterFor ANDs the sections: a section's included values OR together, and each excluded value is its own NOT. */
export function filterFor(terms: Term[]): Filter | undefined {
  const included = (kind: TermKind) => terms.filter((t) => t.kind === kind && !t.not).map((t) => t.value);
  const parts: Filter[] = [];
  // One target_state for both sections, so a state applies to the selected targets.
  const instances = included("target").map(Number);
  const states = included("state") as TargetIs[];
  if (instances.length || states.length)
    parts.push({ target_state: { ...(instances.length ? { instances } : {}), ...(states.length ? { states } : {}) } });
  for (const kind of ["tag", "source", "collection"] as const) {
    const values = included(kind);
    if (values.length === 1) parts.push(condition(kind, values[0]));
    if (values.length > 1) parts.push({ or: values.map((v) => condition(kind, v)) });
  }
  for (const t of terms.filter((t) => t.not)) parts.push({ not: condition(t.kind, t.value) });
  return parts.length ? { and: parts } : undefined;
}

/** toSearch is the POST /api/search body for the state. */
export function toSearch(q: CardQuery): Search {
  return {
    text: q.text || undefined,
    filter: filterFor(q.terms),
    sort: q.sort,
    fields: ["name", "description", "avatar"],
    include: ["tags", "lorebooks", "targets"],
    facets: ["tags", "collections", "targets", "sources"],
    page: { size: q.size, number: q.page },
  };
}

/** toLorebookSearch is the POST /api/lorebooks/search body for the state. */
export function toLorebookSearch(q: CardQuery): Search {
  return { ...toSearch(q), fields: ["name", "description"], include: ["cards", "targets"], facets: ["targets", "sources"] };
}

const facetOf = { target: "targets", tag: "tags", source: "sources", collection: "collections" } as const;

/** countSearch counts a section's values over every other section's terms; size 1 because 0 means the default. */
export function countSearch(q: CardQuery, section: keyof typeof facetOf): Search {
  return {
    text: q.text || undefined,
    filter: filterFor(q.terms.filter((t) => t.kind !== section)),
    facets: [facetOf[section]],
    page: { size: 1 },
  };
}

/** stateCountSearch counts the matches in one state; the targets facet can't, as it counts a card once per target. */
export function stateCountSearch(q: CardQuery, is: TargetIs): Search {
  const terms: Term[] = [...q.terms.filter((t) => t.kind !== "state"), { kind: "state", value: is, not: false }];
  return { text: q.text || undefined, filter: filterFor(terms), page: { size: 1 } };
}

/** toggleTerm adds t, flips its negation, or removes it if it's already there as given. */
export function toggleTerm(q: CardQuery, t: Term): CardQuery {
  const i = q.terms.findIndex((x) => x.kind === t.kind && x.value === t.value);
  const terms = q.terms.filter((_, j) => j !== i);
  if (i < 0 || q.terms[i]?.not !== t.not) terms.push(t);
  return { ...q, terms, page: 1 };
}

/** onlyTerm makes t the only value of its section. */
export function onlyTerm(q: CardQuery, t: Term): CardQuery {
  return { ...q, terms: [...q.terms.filter((x) => x.kind !== t.kind), { ...t, not: false }], page: 1 };
}
```

The `text: undefined` keys: `toEqual` treats a property set to `undefined` as missing, so the count search expectations above hold.

- [ ] **Step 4: Keep the old sidebar and chips working**

In `web/src/cards/Facets.tsx`, the Sync section builds terms with value `${t.instance}:${t.is}`, which `parseQuery` now drops. As a stopgap until Task 3, make each Sync row a target term: change its `term` to `{ kind: "target", value: String(t.instance) }`.

In `web/src/cards/Cards.tsx`, replace `termLabel`:

```ts
function termLabel(t: Term, names: Names): string {
  const value =
    t.kind === "collection" ? (names.collections.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "target" ? (names.instances.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "state" ? stateLabel(t.value)
    : t.value;
  return `${t.not ? "not " : ""}${t.kind}: ${value}`;
}
```

Import `stateLabel` from `"../sync/sync"`.

- [ ] **Step 5: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS. If a `Cards.test.tsx` case breaks only because of the Sync stopgap, report it rather than changing the test; Task 3 rewrites those tests.

- [ ] **Step 6: Commit**

```bash
git add web/src/cards
git commit -m "feat(web): filter sections OR within and AND across"
```

---

### Task 3: The new sidebar

`Facets.tsx` is rewritten: one collapsible section per kind, rows with only and exclude buttons, and counts from the section's own searches. The main search stops asking for facets.

**Files:**
- Modify: `web/src/cards/Facets.tsx` (rewrite), `web/src/cards/Cards.tsx`, `web/src/cards/Cards.css`, `web/src/cards/query.ts` (drop `facets` from `toSearch`, `toLorebookSearch`)
- Test: `web/src/cards/Cards.test.tsx`, `web/src/cards/Lorebooks.test.tsx`

- [ ] **Step 1: Write the failing UI tests**

In `web/src/cards/Cards.test.tsx`:

1. Add `localStorage.clear();` as the first line of `beforeEach`.

2. The row's own buttons are named "Only pirate" and "Exclude pirate", so `/pirate/` would now match three buttons. In `"a facet click filters and shift-click excludes"` and `"a facet click clears the selection, and paging keeps it"`, change `/pirate/` to `/^pirate/` and `/fantasy/` to `/^fantasy/`.

3. Add these tests after `"an active facet is pressed"`:

```tsx
test("only keeps one value in its section", async () => {
  const { router } = renderCards("/cards?f=tag:fantasy&f=source:chub");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Only pirate" }));
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["source:chub", "tag:pirate"]);
});

test("the exclude button excludes a value", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Exclude pirate" }));
  expect(router.state.location.search).toBe("?f=-tag%3Apirate");
});

test("a section's counts leave out its own selections", async () => {
  renderCards("/cards?f=tag:fantasy&f=source:chub");
  await screen.findByRole("button", { name: /^pirate/ });
  expect(searches).toContainEqual({ filter: { and: [{ source: "chub" }] }, facets: ["tags"], page: { size: 1 } });
  expect(searches).toContainEqual({
    filter: { and: [{ target_state: { states: ["ahead"] } }, { tag: "fantasy" }, { source: "chub" }] },
    page: { size: 1 },
  });
});

test("target rows name the app and state rows show the status dot", async () => {
  const { container } = renderCards("/cards");
  expect(await screen.findByRole("button", { name: /^Lumiverse/ })).toBeInTheDocument();
  const ahead = await screen.findByRole("button", { name: /^not pushed/ });
  expect(ahead.querySelector(".badge.ahead")).not.toBeNull();
  expect(container.querySelector(".facets")).toHaveTextContent("Target");
});

test("chips name targets and states", async () => {
  renderCards("/cards?f=target:2&f=state:ahead");
  expect(await screen.findByRole("button", { name: "Remove target: Lumiverse" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Remove state: not pushed" })).toBeInTheDocument();
});

test("the main search asks for no facets", async () => {
  renderCards("/cards");
  await screen.findByText("Brakka");
  const main = searches.find((s) => (s as { page?: { size?: number } }).page?.size === 48);
  expect(main).not.toHaveProperty("facets");
});
```

`JSON.stringify` drops `text: undefined`, so the bodies captured from `fetch` have no `text` key, and the `toContainEqual` matches above hold.

In `web/src/cards/Lorebooks.test.tsx`, in `"lists lorebooks with entries, linked cards, and sync"`:
- replace the `searches[0]` expectation with:

```tsx
  expect(searches).toContainEqual(expect.objectContaining({ path: "/api/lorebooks/search", body: expect.objectContaining({ include: ["cards", "targets"] }) }));
  expect(searches).toContainEqual({ path: "/api/lorebooks/search", body: { facets: ["sources"], page: { size: 1 } } });
```

- change `getByRole("button", { name: /chub/ })` to `findByRole("button", { name: /^chub/ })` with `await`.

- [ ] **Step 2: Run them and watch them fail**

Run: `cd web && npx vitest run src/cards/Cards.test.tsx src/cards/Lorebooks.test.tsx`
Expected: FAIL. There are no "Only" or "Exclude" buttons yet, no count searches, and the main search still asks for facets.

- [ ] **Step 3: Drop facets from the main search**

In `web/src/cards/query.ts`, delete the `facets:` line from `toSearch`, and change `toLorebookSearch` to:

```ts
  return { ...toSearch(q), fields: ["name", "description"], include: ["cards", "targets"] };
```

In `web/src/cards/query.test.ts`, nothing changes: the `toSearch` and `toLorebookSearch` tests use `toMatchObject`.

- [ ] **Step 4: Rewrite `Facets.tsx`**

Check the client's kind type first: `grep -n "export type Kind" web/src/api/client.ts`. The code below assumes `Kind` is `"card" | "lorebook"`.

```tsx
import { keepPreviousData, useQueries } from "@tanstack/react-query";
import { useState, type MouseEvent } from "react";
import { api, must, route, type Kind } from "../api/client";
import type { components } from "../api/schema";
import { stateLabel } from "../sync/sync";
import { countSearch, onlyTerm, sectionsFor, stateCountSearch, targetStates, toggleTerm, type CardQuery, type TermKind } from "./query";
import { badgeClass, type Names } from "./Tile";

type Search = components["schemas"]["Query"];
type Row = { value: string; label: string; cards: number; dot?: string };

const titles: Record<TermKind, string> = { target: "Target", state: "State", tag: "Tags", source: "Sources", collection: "Collections" };

/** useRows runs each open section's count searches and turns the results into rows, selected values included. */
function useRows(kind: Kind, q: CardQuery, names: Names, open: (s: TermKind) => boolean): Partial<Record<TermKind, Row[]>> {
  const search = (body: Search, enabled: boolean) => ({
    queryKey: ["search", kind, body],
    queryFn: async () => must(await api.POST(route(kind, "/cards/search"), { body })),
    placeholderData: keepPreviousData,
    enabled,
  });
  const faceted = (["target", "tag", "source", "collection"] as const).filter((s) => sectionsFor(kind).includes(s));
  const facetResults = useQueries({ queries: faceted.map((s) => search(countSearch(q, s), open(s))) });
  const stateResults = useQueries({ queries: targetStates.map((is) => search(stateCountSearch(q, is), open("state"))) });
  const facets = (s: TermKind) => facetResults[faceted.indexOf(s as (typeof faceted)[number])]?.data?.facets;
  const picked = (s: TermKind) => q.terms.filter((t) => t.kind === s).map((t) => t.value);

  // A selected value with no matches still gets a row, so it can be cleared.
  const withPicked = (s: TermKind, rows: Row[] | undefined, label: (v: string) => string): Row[] | undefined =>
    rows && [...rows, ...picked(s).filter((v) => !rows.some((r) => r.value === v)).map((v) => ({ value: v, label: label(v), cards: 0 }))];

  const states = picked("state");
  const targetRows = facets("target")?.targets && (() => {
    const byInstance = new Map<number, number>();
    for (const t of facets("target")?.targets ?? [])
      if (!states.length || states.includes(t.is)) byInstance.set(t.instance, (byInstance.get(t.instance) ?? 0) + t.cards);
    return [...byInstance].map(([id, cards]) => ({ value: String(id), label: names.instances.get(id) ?? "#" + id, cards }));
  })();
  const stateRows = stateResults.every((r) => r.data)
    ? targetStates
        .map((is, i) => ({ value: is, label: stateLabel(is), cards: stateResults[i]?.data?.total ?? 0, dot: badgeClass[is] }))
        .filter((r) => r.cards > 0 || states.includes(r.value))
    : undefined;
  return {
    target: withPicked("target", targetRows, (v) => names.instances.get(Number(v)) ?? "#" + v),
    state: stateRows,
    tag: withPicked("tag", facets("tag")?.tags?.map((t) => ({ value: t.name, label: t.name, cards: t.cards })), (v) => v),
    source: withPicked("source", facets("source")?.sources?.map((s) => ({ value: s.kind, label: s.kind, cards: s.cards })), (v) => v),
    collection: withPicked(
      "collection",
      facets("collection")?.collections?.map((c) => ({ value: String(c.id), label: c.name, cards: c.cards })),
      (v) => names.collections.get(Number(v)) ?? "#" + v,
    ),
  };
}

function FacetRow({ kind, row, q, onChange }: { kind: TermKind; row: Row; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const term = q.terms.find((t) => t.kind === kind && t.value === row.value);
  const toggle = (not: boolean) => onChange(toggleTerm(q, { kind, value: row.value, not }));
  return (
    <li className={"facet" + (term ? (term.not ? " excluded" : " included") : "")}>
      <button className="facet-pick" aria-pressed={!!term} onClick={(e: MouseEvent) => toggle(e.shiftKey)}>
        {row.dot && <span className={"badge " + row.dot} />}
        <span className="facet-label">
          {term?.not ? "not " : ""}
          {row.label}
        </span>{" "}
        <span className="count">{row.cards}</span>
      </button>
      <button className="facet-act" aria-label={`Only ${row.label}`} onClick={() => onChange(onlyTerm(q, { kind, value: row.value, not: false }))}>
        only
      </button>
      <button className="facet-act" aria-label={`Exclude ${row.label}`} onClick={() => toggle(true)}>
        −
      </button>
    </li>
  );
}

type SectionProps = { kind: TermKind; rows?: Row[]; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void };

function Section({ kind, rows, q, open, onOpen, onChange }: SectionProps) {
  const picked = q.terms.filter((t) => t.kind === kind).length;
  if (open && rows?.length === 0 && picked === 0) return null;
  return (
    <section className="facet-section">
      <h2>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {titles[kind]}
          {picked > 0 && ` · ${picked}`}
        </button>
      </h2>
      {open && (
        <ul className="facet-list">
          {(rows ?? []).map((r) => (
            <FacetRow key={r.value} kind={kind} row={r} q={q} onChange={onChange} />
          ))}
        </ul>
      )}
    </section>
  );
}

/** Facets is the filter sidebar: one section per term kind, each counting its values over the other sections' filters. */
export function Facets({ kind, q, names, onChange }: { kind: Kind; q: CardQuery; names: Names; onChange: (q: CardQuery) => void }) {
  const [collapsed, setCollapsed] = useState<TermKind[]>([]);
  const open = (s: TermKind) => !collapsed.includes(s);
  const rows = useRows(kind, q, names, open);
  const flip = (s: TermKind) => setCollapsed(open(s) ? [...collapsed, s] : collapsed.filter((x) => x !== s));
  return (
    <aside className="facets" aria-label="Filters">
      {sectionsFor(kind)
        .filter((s) => s !== "collection" || names.collections.size > 0)
        .map((s) => (
          <Section key={s} kind={s} rows={rows[s]} q={q} open={open(s)} onOpen={() => flip(s)} onChange={onChange} />
        ))}
    </aside>
  );
}
```

Task 4 adds saving the collapsed sections and the Tags browsing.

- [ ] **Step 5: Wire it into the page**

In `web/src/cards/Cards.tsx`, replace the `<Facets … />` line with:

```tsx
      {facetsOpen && <Facets kind={kind} q={q} names={names} onChange={go} />}
```

- [ ] **Step 6: Style the rows**

In `web/src/cards/Cards.css`, replace the rules from `.facet-section h2` through `.facet .count` with:

```css
.facet-section h2 {
  margin: calc(var(--space) * 1.5) 0 calc(var(--space) * 0.5);
}

.facet-head {
  width: 100%;
  background: none;
  border: none;
  padding: 0 var(--space);
  font-size: 0.85em;
  text-transform: uppercase;
  color: var(--text-muted);
  text-align: left;
  cursor: pointer;
}

.facet-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.facet {
  display: flex;
  align-items: center;
  border-radius: calc(var(--radius) * 0.5);
}

.facet:hover,
.facet:focus-within {
  background: var(--surface-2);
}

.facet-pick {
  display: flex;
  align-items: center;
  gap: calc(var(--space) * 0.5);
  flex: 1;
  min-width: 0;
  background: none;
  border: none;
  padding: calc(var(--space) * 0.25 * var(--density)) var(--space);
  color: inherit;
  cursor: pointer;
  text-align: left;
}

.facet-label {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.facet .badge {
  flex: none;
  box-shadow: none;
}

/* The row's own actions show on hover and keyboard focus; visibility keeps them out of the tab order until then. */
.facet-act {
  visibility: hidden;
  background: none;
  border: none;
  padding: 0 calc(var(--space) * 0.5);
  color: var(--text-muted);
  cursor: pointer;
}

.facet:hover .facet-act,
.facet:focus-within .facet-act {
  visibility: visible;
}

.facet.included {
  color: var(--accent);
}

.facet.excluded .facet-label {
  color: var(--drift);
  text-decoration: line-through;
}

.facet .count {
  color: var(--text-muted);
}
```

The spec says excluded rows use the drift color, so `.excluded` moves from `--danger` to `--drift`.

- [ ] **Step 7: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS, including the new tests from Step 1.

- [ ] **Step 8: Look at it**

Run `cd web && npm run dev` (check `web/package.json` for the script name). Open the Cards page against a backend with data, or use the dev proxy setup in the README. Check that:
- the sections collapse
- the action buttons appear on hover and when you Tab through
- an excluded row is struck through in the drift color
- the state dots match the tiles

If you can't run a backend, say so in your report. Don't claim it looked right.

- [ ] **Step 9: Commit**

```bash
git add web/src/cards
git commit -m "feat(web): collapsible filter sections with only and exclude"
```

---

### Task 4: Tags browsing and remembered sections

**Files:**
- Modify: `web/src/cards/Facets.tsx`, `web/src/cards/Cards.css`
- Test: `web/src/cards/Cards.test.tsx`

- [ ] **Step 1: Write the failing tests**

Add to `web/src/cards/Cards.test.tsx`:

```tsx
const manyTags = Array.from({ length: 12 }, (_, i) => ({ name: `t${i + 1}`, cards: 20 - i }));

test("tags show the ten most used, then all, and filter as you type", async () => {
  answer("/api/cards/search", async (req) => {
    const body = await req.json();
    searches.push(body);
    return json({ ...results, facets: { ...results.facets, tags: manyTags } });
  });
  renderCards("/cards?f=tag:t12");
  const user = userEvent.setup();
  expect(await screen.findByRole("button", { name: /^t10 / })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^t11 / })).not.toBeInTheDocument();
  // Selected tags show even outside the top ten.
  expect(screen.getByRole("button", { name: /^t12 / })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Show all 12" }));
  expect(screen.getByRole("button", { name: /^t11 / })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Show fewer" }));
  await user.type(screen.getByRole("searchbox", { name: "Find a tag" }), "t1");
  expect(screen.getByRole("button", { name: /^t11 / })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^t2 / })).not.toBeInTheDocument();
});

test("a collapsed section stays collapsed and shows its selection count", async () => {
  const first = renderCards("/cards?f=tag:fantasy&f=tag:pirate");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Tags · 2" }));
  expect(screen.getByRole("button", { name: "Tags · 2" })).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByRole("button", { name: /^fantasy/ })).not.toBeInTheDocument();
  first.unmount();
  renderCards("/cards");
  expect(await screen.findByRole("button", { name: "Tags" })).toHaveAttribute("aria-expanded", "false");
});
```

`renderCards` returns `{ router, container }`. Add `unmount` to it: keep the `render(...)` result and return `{ router, container, unmount }`.

The row name is "label count", for example "t10 11". The `{" "}` between the two spans in `FacetRow` puts the space there; without it the name would be "t1011". `/^t10 /` therefore doesn't also match `t1`.

The main `searchbox` in the toolbar has no name. Where existing tests call `screen.getByRole("searchbox")` / `findByRole("searchbox")`, they would now find two. Give them the name: `findByRole("searchbox", { name: "Search cards" })`. Add `aria-label={`Search ${noun}`}` to the toolbar `<input>` in `Cards.tsx`; it already has that text as its placeholder.

- [ ] **Step 2: Run them and watch them fail**

Run: `cd web && npx vitest run src/cards/Cards.test.tsx`
Expected: FAIL. No "Show all", no "Find a tag", and collapse isn't saved.

- [ ] **Step 3: Implement the Tags browsing**

In `Facets.tsx`, inside `Section`, add state and filter the rows when `kind === "tag"`:

```tsx
  const [all, setAll] = useState(false);
  const [find, setFind] = useState("");
  const browse = kind === "tag";
  const needle = find.trim().toLowerCase();
  const isPicked = (r: Row) => q.terms.some((t) => t.kind === kind && t.value === r.value);
  const shown = !browse
    ? (rows ?? [])
    : needle
      ? (rows ?? []).filter((r) => r.label.toLowerCase().includes(needle))
      : all
        ? (rows ?? [])
        : (rows ?? []).filter((r, i) => i < 10 || isPicked(r));
```

Hooks must run before the early `return null`, so put these lines above it. Render inside the `open &&` block:

```tsx
        <>
          {browse && (
            <input className="input facet-find" type="search" aria-label="Find a tag" placeholder="Find a tag" value={find} onChange={(e) => setFind(e.target.value)} />
          )}
          <ul className="facet-list">
            {shown.map((r) => (
              <FacetRow key={r.value} kind={kind} row={r} q={q} onChange={onChange} />
            ))}
          </ul>
          {browse && !needle && (rows?.length ?? 0) > 10 && (
            <button className="facet-more" onClick={() => setAll(!all)}>
              {all ? "Show fewer" : `Show all ${rows?.length}`}
            </button>
          )}
        </>
```

Add to `Cards.css`:

```css
.facet-find {
  margin: 0 0 calc(var(--space) * 0.5);
}

.facet-more {
  background: none;
  border: none;
  padding: calc(var(--space) * 0.25) var(--space);
  color: var(--accent);
  cursor: pointer;
}
```

- [ ] **Step 4: Remember collapsed sections**

In `Facets.tsx`, add above `Facets`:

```tsx
const collapsedKey = "innkeeper.facets.collapsed";

// Storage can throw or be empty in private windows; the sidebar then just starts open.
function readCollapsed(): TermKind[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? "[]");
    return Array.isArray(v) ? v.filter((s): s is TermKind => typeof s === "string") : [];
  } catch {
    return [];
  }
}

function saveCollapsed(v: TermKind[]) {
  try {
    localStorage.setItem(collapsedKey, JSON.stringify(v));
  } catch {
    // A layout preference that can't be saved isn't worth an error.
  }
}
```

In `Facets`, start from storage and save on change:

```tsx
  const [collapsed, setCollapsed] = useState<TermKind[]>(readCollapsed);
  ...
  const flip = (s: TermKind) => {
    const next = open(s) ? [...collapsed, s] : collapsed.filter((x) => x !== s);
    setCollapsed(next);
    saveCollapsed(next);
  };
```

- [ ] **Step 5: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/cards
git commit -m "feat(web): browse tags in the sidebar and remember collapsed sections"
```

---

### Task 5: Final check

- [ ] **Step 1: Make the spec match what was built**

Invoke the `documentation-writing` skill, then use Edit on `docs/superpowers/specs/2026-10-13-filter-sidebar-design.md`. In "Counts", replace the paragraph starting "The browser computes this." with:

```markdown
The browser computes this. Alongside the main search, each open section sends `POST /search` with `page.size: 1` (0 means the default size) and the filter built from every other section's terms:

- Target, Tags, Sources, and Collections ask for their own facet. A Target count sums that target's rows over the selected states.
- State sends one search per state and uses its `total`. The `targets` facet counts a card once per target, so it would count a card on two targets twice.

These run in parallel through React Query, and collapsed sections send nothing. `query.ts` is the one place that knows which section a term belongs to. The API gains no per-section logic.
```

- [ ] **Step 2: Run what CI runs**

```bash
go vet ./...
go test -race -count=1 ./...
cd web && npm run typecheck && npm test
```

Expected: all pass.

- [ ] **Step 3: Read the diff once more**

Run `git diff origin/main -- . ':!web/openapi.json' ':!web/src/api/schema.d.ts'`. Look for dead code, comments that restate code, unrelated edits, and anything still reading `res.data.facets` in `Cards.tsx`.

- [ ] **Step 4: Commit**

```bash
git add docs/superpowers/specs/2026-10-13-filter-sidebar-design.md
git commit -m "docs: filter sidebar counts as built"
```
