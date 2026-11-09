# Rule Builder Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the four-step rule wizard with one page that reads as a sentence: condition rows (field, operator, value), match all or any, then an action.

**Architecture:** A rule's conditions are its saved search's filter, as one level of `and`/`or` over simple conditions. `conditions.ts` turns rows into that filter and back. `RuleBuilder.tsx` is the page; it saves the search, then the rule. Core's only change: `POST /rules/preview` takes an unsaved `filter`.

**Tech Stack:** Go (Huma, pgx), React 19, TypeScript, TanStack Query, react-router, Vitest with Testing Library.

Spec: `docs/superpowers/specs/2026-11-09-rule-builder-design.md`.

## Files

| File | Responsibility |
|---|---|
| `internal/api/rules.go` | Preview takes `filter` |
| `internal/api/rules_test.go` | Its test |
| `web/src/rules/conditions.ts` (new) | Fields from the browsable keys; rows to filter and back |
| `web/src/rules/conditions.test.ts` (new) | Its tests |
| `web/src/rules/RuleBuilder.tsx` (new) | The page |
| `web/src/rules/RulePreview.tsx` | Passes `filter` |
| `web/src/rules/RuleWizard.tsx`, `web/src/rules/MatchStep.tsx` | Deleted |
| `web/src/App.tsx` | Routes to `RuleBuilder` |
| `web/src/rules/Rules.css` | Row layout; the wizard's styles go |
| `web/src/rules/Rules.test.tsx` | Builder tests replace the wizard's |
| `docs/using.md` | Adding a rule |

Checks: Go with `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api` and `go vet ./...`; web from `web/` with `npm run typecheck && npm test`. Never run Prettier; the repo doesn't use it. Regenerate the API schema with `npm run gen` in `web/`, never by hand.

---

### Task 1: Preview an unsaved filter

**Files:**
- Modify: `internal/api/rules.go` (`previewRuleIn` near line 34; the preview handler near line 202)
- Test: `internal/api/rules_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing test**

Add to `internal/api/rules_test.go`, beside the other `TestRulePreview…` tests:

```go
func TestRulePreviewReadsAnUnsavedFilter(t *testing.T) {
	e := newEnv(t)
	e.createCard(`{"name": "Junk one"}`)
	e.createCard(`{"name": "Junk two"}`)
	e.createCard(`{"name": "Keeper"}`)
	spec := map[string]any{"name": "Junk", "saved_search_id": 0, "action": "archive", "all_libraries": true}

	var out []rulePreview
	e.call("POST", "/rules/preview", map[string]any{"rule": spec, "filter": map[string]any{"name": "junk"}}, 200, &out)
	if len(out) != 1 || out[0].Apply.Total != 2 {
		t.Fatalf("preview of name junk = %+v, want 2 to archive", out)
	}
	e.call("POST", "/rules/preview", map[string]any{"rule": spec, "filter": map[string]any{"name": "keeper"}}, 200, &out)
	if len(out) != 1 || out[0].Apply.Total != 1 {
		t.Fatalf("preview of name keeper = %+v, want 1 to archive", out)
	}
	// Without a filter, saved search 0 still has to exist.
	e.call("POST", "/rules/preview", map[string]any{"rule": spec}, 422, nil)
}
```

- [ ] **Step 2: Run it to see it fail**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test ./internal/api -run TestRulePreviewReadsAnUnsavedFilter`
Expected: FAIL, the first call answers 422 (saved search 0 doesn't exist) or rejects the unknown `filter` field.

- [ ] **Step 3: Implement**

In `previewRuleIn`'s body, after `Rule`:

```go
		Filter *library.Filter `json:"filter,omitempty" doc:"Previews this filter in place of the rule's saved search's, so a rule can be previewed before it or its search is saved. saved_search_id may then be 0."`
```

In the handler, replace the `GetSavedSearch` lookup and the `r := …` line with:

```go
			filter := in.Body.Filter
			if filter == nil {
				search, err := s.Lib.GetSavedSearch(ctx, spec.SavedSearchID)
				if errors.Is(err, library.ErrNotFound) {
					return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("saved search %d doesn't exist", spec.SavedSearchID))
				}
				if err != nil {
					return nil, s.fail(err)
				}
				filter = &search.Filter
			}
			r := library.Rule{ID: in.Body.ID, RuleSpec: spec, Filter: *filter}
```

Keep the rest of the handler as is. Extend the operation's `Description` with: `" With filter, it previews that filter instead of the saved search's."`

- [ ] **Step 4: Run the tests**

Run: `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./internal/api && go vet ./...`
Expected: PASS.

- [ ] **Step 5: Regenerate the schema**

Run: `cd web && npm run gen && npm run typecheck`
Expected: `openapi.json` and `schema.d.ts` gain `filter` on the preview body; typecheck clean.

- [ ] **Step 6: Commit**

```bash
git add internal/api/rules.go internal/api/rules_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): preview a rule with an unsaved filter"
```

---

### Task 2: Conditions to filter and back

**Files:**
- Create: `web/src/rules/conditions.ts`, `web/src/rules/conditions.test.ts`

- [ ] **Step 1: Write the failing tests**

`web/src/rules/conditions.test.ts`:

```ts
import { fieldsFor, fromFilter, toFilter, type Condition } from "./conditions";

const fields = fieldsFor([
  { key: "quality", label: "Quality", type: "score", instances: [1] },
  { key: "language", label: "Language", type: "text", instances: [2] },
  { key: "rating", label: "Rating", type: "score", instance: 3, instances: [3] },
]);

test("fields are the fixed ones, then each browsable key", () => {
  expect(fields.map((f) => f.id)).toEqual(["name", "creator", "tag", "source", "shares_source", "score:quality", "value:language", "score:3:rating"]);
  expect(fields.find((f) => f.id === "score:3:rating")).toMatchObject({ label: "Rating", input: "number", key: "rating", instance: 3 });
});

const cases: [string, Condition, unknown][] = [
  ["name contains", { field: "name", op: "contains", value: "junk" }, { name: "junk" }],
  ["creator is not", { field: "creator", op: "is_not", value: "bob" }, { not: { creator: "bob" } }],
  ["tag is", { field: "tag", op: "is", value: "nsfw" }, { tag: "nsfw" }],
  ["source is", { field: "source", op: "is", value: "chub" }, { source: "chub" }],
  ["shares a source", { field: "shares_source", op: "is", value: "" }, { shares_source: true }],
  ["merged score at least", { field: "score:quality", op: "at_least", value: "70" }, { enrichment: "quality", gte: 70 }],
  ["instance score at most", { field: "score:3:rating", op: "at_most", value: "2" }, { enrichment: "rating", instance: 3, lte: 2 }],
  ["score between", { field: "score:quality", op: "between", value: "10", value2: "40" }, { enrichment: "quality", gte: 10, lte: 40 }],
  ["no score", { field: "score:quality", op: "missing", value: "" }, { enrichment: "quality", missing: true }],
  ["value is not", { field: "value:language", op: "is_not", value: "English" }, { not: { enrichment: "language", is: "English" } }],
];

test.each(cases)("%s round-trips", (_, row, cond) => {
  const all = toFilter({ match: "all", rows: [row] }, fields);
  expect(all).toEqual({ and: [cond] });
  expect(fromFilter(all!, fields)).toEqual({ match: "all", rows: [row] });
  const any = toFilter({ match: "any", rows: [row] }, fields);
  expect(any).toEqual({ or: [cond] });
  expect(fromFilter(any!, fields)).toEqual({ match: "any", rows: [row] });
});

test("incomplete rows are left out, and none at all is no filter", () => {
  expect(toFilter({ match: "all", rows: [{ field: "tag", op: "is", value: "" }] }, fields)).toBeUndefined();
  expect(toFilter({ match: "all", rows: [{ field: "score:quality", op: "between", value: "1", value2: "" }] }, fields)).toBeUndefined();
});

test("a bare condition reads as one row", () => {
  expect(fromFilter({ tag: "nsfw" }, fields)).toEqual({ match: "all", rows: [{ field: "tag", op: "is", value: "nsfw" }] });
});

test("filters rows can't show read as undefined", () => {
  expect(fromFilter({ and: [{ or: [{ tag: "a" }, { tag: "b" }] }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ enrichment: "gone", gte: 1 }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ tag: "a", source: "chub" }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ not: { name: "x" } }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ collection: 4 }] }, fields)).toBeUndefined();
});
```

If the generated `BrowseKey` type requires more fields than these objects give, add them to the test objects rather than loosening the type.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/rules/conditions.test.ts`
Expected: FAIL, `./conditions` doesn't exist.

- [ ] **Step 3: Implement `web/src/rules/conditions.ts`**

```ts
import type { components } from "../api/schema";
import { keyRef } from "../cards/query";

type Filter = components["schemas"]["Filter"];
type BrowseKey = components["schemas"]["BrowseKey"];

export type Op = "contains" | "is" | "is_not" | "at_least" | "at_most" | "between" | "missing";
/** Condition is one row: a field's id, an operator, and its value, or two for between. */
export type Condition = { field: string; op: Op; value: string; value2?: string };
export type Conditions = { match: "all" | "any"; rows: Condition[] };
/** Field is what a row can test. input says which control takes its value. */
export type Field = {
  id: string;
  label: string;
  ops: Op[];
  input: "text" | "tag" | "source" | "number" | "value" | "none";
  key?: string;
  instance?: number;
};

const opLabels: Record<Op, string> = {
  contains: "contains",
  is: "is",
  is_not: "is not",
  at_least: "is at least",
  at_most: "is at most",
  between: "is between",
  missing: "has no value",
};

/** opLabel names op for field, as the operator menu shows it. */
export const opLabel = (op: Op, field: Field) => (op === "missing" && field.input === "number" ? "has no score" : opLabels[op]);

/** fieldsFor lists what a row can test: the card's own fields, then each browsable enrichment key. */
export function fieldsFor(browse: BrowseKey[]): Field[] {
  const fixed: Field[] = [
    { id: "name", label: "Name", ops: ["contains"], input: "text" },
    { id: "creator", label: "Creator", ops: ["is", "is_not"], input: "text" },
    { id: "tag", label: "Tag", ops: ["is", "is_not"], input: "tag" },
    { id: "source", label: "Source", ops: ["is", "is_not"], input: "source" },
    { id: "shares_source", label: "Shares a source with another card", ops: ["is"], input: "none" },
  ];
  const keys = browse.map((b): Field => {
    const at = { key: b.key, ...(b.instance !== undefined && { instance: b.instance }) };
    const ref = keyRef(b.instance, b.key);
    return b.type === "score"
      ? { id: `score:${ref}`, label: b.label, ops: ["at_least", "at_most", "between", "missing"], input: "number", ...at }
      : { id: `value:${ref}`, label: b.label, ops: ["is", "is_not", "missing"], input: "value", ...at };
  });
  return [...fixed, ...keys];
}

/** complete says whether row has every value its operator needs. */
export function complete(row: Condition, field: Field | undefined): boolean {
  if (!field) return false;
  if (field.input === "none" || row.op === "missing") return true;
  if (field.input === "number") {
    const ok = (v?: string) => v !== undefined && v.trim() !== "" && Number.isFinite(Number(v));
    return row.op === "between" ? ok(row.value) && ok(row.value2) : ok(row.value);
  }
  return row.value.trim() !== "";
}

function condition(row: Condition, field: Field): Filter {
  const v = row.value.trim();
  const at = { enrichment: field.key!, ...(field.instance !== undefined && { instance: field.instance }) };
  let c: Filter;
  switch (field.input) {
    case "text":
      c = field.id === "name" ? { name: v } : { creator: v };
      break;
    case "tag":
      c = { tag: v };
      break;
    case "source":
      c = { source: v };
      break;
    case "none":
      c = { shares_source: true };
      break;
    case "number":
      c =
        row.op === "missing" ? { ...at, missing: true }
        : row.op === "at_least" ? { ...at, gte: Number(v) }
        : row.op === "at_most" ? { ...at, lte: Number(v) }
        : { ...at, gte: Number(v), lte: Number(row.value2!.trim()) };
      return c;
    case "value":
      c = row.op === "missing" ? { ...at, missing: true } : { ...at, is: v };
      break;
  }
  return row.op === "is_not" ? { not: c } : c;
}

/** toFilter is the saved search filter for the complete rows, or undefined when none are. */
export function toFilter(c: Conditions, fields: Field[]): Filter | undefined {
  const byId = new Map(fields.map((f) => [f.id, f]));
  const parts = c.rows.filter((r) => complete(r, byId.get(r.field))).map((r) => condition(r, byId.get(r.field)!));
  if (!parts.length) return undefined;
  return c.match === "all" ? { and: parts } : { or: parts };
}

const only = (f: object, ...keys: string[]) => Object.keys(f).every((k) => keys.includes(k));

/** rowOf reads one condition back as a row, or undefined if no row makes it. */
function rowOf(f: Filter, fields: Field[]): Condition | undefined {
  const negated = f.not !== undefined && only(f, "not");
  const c = negated ? f.not! : f;
  const is: Op = negated ? "is_not" : "is";
  if (c.name !== undefined && only(c, "name") && !negated) return { field: "name", op: "contains", value: c.name };
  if (c.creator !== undefined && only(c, "creator")) return { field: "creator", op: is, value: c.creator };
  if (c.tag !== undefined && only(c, "tag")) return { field: "tag", op: is, value: c.tag };
  if (c.source !== undefined && only(c, "source")) return { field: "source", op: is, value: c.source };
  if (c.shares_source === true && only(c, "shares_source") && !negated) return { field: "shares_source", op: "is", value: "" };
  if (c.enrichment === undefined) return undefined;
  const ref = keyRef(c.instance, c.enrichment);
  const score = fields.find((x) => x.id === `score:${ref}`);
  const value = fields.find((x) => x.id === `value:${ref}`);
  const base = ["enrichment", "instance"];
  if (c.missing === true && only(c, ...base, "missing") && !negated) {
    const field = score ?? value;
    return field && { field: field.id, op: "missing", value: "" };
  }
  if (score && !negated && only(c, ...base, "gte", "lte")) {
    if (c.gte !== undefined && c.lte !== undefined) return { field: score.id, op: "between", value: String(c.gte), value2: String(c.lte) };
    if (c.gte !== undefined) return { field: score.id, op: "at_least", value: String(c.gte) };
    if (c.lte !== undefined) return { field: score.id, op: "at_most", value: String(c.lte) };
  }
  if (value && c.is !== undefined && only(c, ...base, "is")) return { field: value.id, op: is, value: c.is };
  return undefined;
}

/** fromFilter reads a saved search's filter as rows, or returns undefined when rows can't show it, as with nested any-of groups. */
export function fromFilter(f: Filter, fields: Field[]): Conditions | undefined {
  const group = f.and !== undefined && only(f, "and") ? { match: "all" as const, parts: f.and } : f.or !== undefined && only(f, "or") ? { match: "any" as const, parts: f.or } : undefined;
  const parts = group?.parts ?? [f];
  const rows = parts.map((p) => rowOf(p, fields));
  if (rows.some((r) => r === undefined)) return undefined;
  return { match: group?.match ?? "all", rows: rows as Condition[] };
}
```

Fix types against the generated `Filter` (for example, if `and` is `Filter[] | null`, guard for null), and report each fix.

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/rules/conditions.test.ts && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/rules/conditions.ts web/src/rules/conditions.test.ts
git commit -m "feat(web): read a rule's conditions to and from its filter"
```

---

### Task 3: The rule builder page

**Files:**
- Create: `web/src/rules/RuleBuilder.tsx`
- Modify: `web/src/rules/RulePreview.tsx`, `web/src/App.tsx`, `web/src/rules/Rules.css`, `web/src/rules/Rules.test.tsx`
- Delete: `web/src/rules/RuleWizard.tsx`, `web/src/rules/MatchStep.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/rules/Rules.test.tsx`:

- Import `RuleBuilder` from `./RuleBuilder` in place of `RuleWizard`, and use it in `renderAt`'s `/rules/new` and `/rules/:id` routes.
- In the `beforeEach` mock, answer `GET /api/enrichments/keys` with `json({ browse: [{ key: "quality", label: "Quality", type: "score", instances: [1] }], instances: [], scorers: [] })`, and make the saved search `Junk` filter `{ and: [{ tag: "junk" }] }` with `rules: [{ id: 9, name: "Hide junk" }]`. Answer `POST /api/cards/search` without recording it in `calls`: when the body has `facets_only`, `json({ facets: { tags: [{ name: "junk", cards: 3 }], sources: [{ kind: "chub", cards: 2 }] } })`, otherwise `json({ total: 2, hits: [{ id: 5, slug: "a", revision_id: 1, fields: { name: "Junk a" } }] })`. Answer `DELETE /api/saved-searches/4` with 204, and `PATCH /api/saved-searches/3` with the search. Keep the existing mocks for rules, preview, libraries, and collections, and the 409 for a saved search named `Taken`.
- Delete every wizard test (those added with `RuleWizard`), keeping `turning a rule on previews it and saves only on confirm` and `saving an enabled rule previews it before the PUT` (update the latter's clicks to this page if needed).
- Add:

```ts
test("a rule built from nothing saves its search, then the rule", async () => {
  const router = renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Hide low");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "score:quality");
  await user.selectOptions(screen.getByRole("combobox", { name: "Operator 1" }), "at_most");
  await user.type(screen.getByRole("spinbutton", { name: "Value 1" }), "40");
  expect(await screen.findByText(/2 cards match in Main/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/saved-searches", body: { name: "Hide low", filter: { and: [{ enrichment: "quality", lte: 40 }] }, sort: "" } },
      { method: "POST", path: "/api/rules", body: { name: "Hide low", saved_search_id: 4, action: "archive", all_libraries: true, enabled: false } },
    ]),
  );
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules"));
});

test("a rule that fails to save deletes the search it saved", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/rules" && req.method === "POST"
      ? new Response(JSON.stringify({ status: 409, title: "Conflict", detail: "a rule named Hide low already exists" }), { status: 409, headers: { "Content-Type": "application/problem+json" } })
      : fetch(req),
  );
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Hide low");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText(/already exists/)).toBeInTheDocument();
  expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/saved-searches", "DELETE /api/saved-searches/4"]);
});

test("Add rule on a saved search loads its conditions, and editing them patches it", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  expect(await screen.findByDisplayValue("junk")).toBeInTheDocument();
  expect(screen.getByText(/also changes Hide junk/)).toBeInTheDocument();
  await user.type(screen.getByRole("textbox", { name: "Rule name" }), "More junk");
  await user.selectOptions(screen.getByRole("combobox", { name: "Operator 1" }), "is_not");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "PATCH", path: "/api/saved-searches/3", body: { filter: { and: [{ not: { tag: "junk" } }] } } },
      { method: "POST", path: "/api/rules", body: { name: "More junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: false } },
    ]),
  );
});

test("Save and turn on previews the unsaved rule, then saves and turns it on", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Twins");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  await user.click(screen.getByRole("button", { name: "Save and turn on" }));
  await screen.findByText("It would archive 2 cards and unarchive 0 cards.");
  expect(calls).toEqual([
    {
      method: "POST",
      path: "/api/rules/preview",
      body: { rule: { name: "Twins", saved_search_id: 0, action: "archive", all_libraries: true, enabled: true }, filter: { and: [{ shares_source: true }] }, page: 1 },
    },
  ]);
  await user.click(screen.getByRole("button", { name: "Turn on" }));
  await waitFor(() =>
    expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview", "POST /api/saved-searches", "POST /api/rules", "PUT /api/rules/9"]),
  );
});

test("editing a rule that's on previews the new conditions before saving anything", async () => {
  current = { ...rule, enabled: true };
  renderAt("/rules/9");
  const user = userEvent.setup();
  expect(await screen.findByDisplayValue("junk")).toBeInTheDocument();
  // A tag input has suggestions, so it's a combobox, not a textbox.
  await user.clear(screen.getByLabelText("Value 1"));
  await user.type(screen.getByLabelText("Value 1"), "spam");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByText("It would archive 2 cards and unarchive 0 cards.");
  expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview"]);
  expect((calls[0]!.body as { filter: unknown }).filter).toEqual({ and: [{ tag: "spam" }] });
});

test("a search rows can't show is read-only with Edit on Cards", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET"
      ? json([{ id: 3, name: "Junk", filter: { and: [{ or: [{ tag: "a" }, { tag: "b" }] }] }, sort: "", created_at: "", rules: [] }])
      : fetch(req),
  );
  renderAt("/rules/new?search=3");
  expect(await screen.findByText(/Matches saved search/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Edit on Cards" })).toHaveAttribute("href", "/cards?saved=3");
  expect(screen.queryByRole("button", { name: "Add condition" })).not.toBeInTheDocument();
});

test("a collect rule names one library and its collection", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByRole("combobox", { name: "Action" }), "collect");
  expect(screen.getByRole("combobox", { name: "Libraries" })).toHaveValue("1");
  await user.selectOptions(await screen.findByRole("combobox", { name: "Collection" }), "7");
  expect(screen.getByRole("combobox", { name: "Collection" })).toHaveValue("7");
});

test("Add rule on the Rules page opens the builder", async () => {
  renderAt("/rules");
  await userEvent.click(await screen.findByRole("link", { name: "Add rule" }));
  expect(await screen.findByRole("heading", { name: "New rule" })).toBeInTheDocument();
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/rules/Rules.test.tsx`
Expected: FAIL, `./RuleBuilder` doesn't exist.

- [ ] **Step 3: Let `RulePreview` take a filter**

In `web/src/rules/RulePreview.tsx`, add `filter?: Filter` to `Props` (with `type Filter = components["schemas"]["Filter"]` imported from `../api/schema`), take it in the function's parameters, include it in the query key after `spec`, and send it: `body: { ...(id !== undefined && { id }), rule: spec, ...(filter && { filter }), page }`.

- [ ] **Step 4: Create `web/src/rules/RuleBuilder.tsx`**

```tsx
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { api, must, mustOk } from "../api/client";
import { collectionsQuery, enrichmentKeysQuery, librariesQuery, savedSearchesQuery, searchQuery } from "../api/queries";
import type { components } from "../api/schema";
import { text } from "../card/draft";
import { useConfirm } from "../shell/ConfirmDialog";
import { count } from "../shell/count";
import { useLibrary } from "../shell/library";
import { toastError } from "../shell/toastError";
import { complete, fieldsFor, fromFilter, opLabel, toFilter, type Condition, type Conditions, type Field } from "./conditions";
import { RulePreview } from "./RulePreview";
import { specOf, type Spec } from "./Rules";
import "./Rules.css";

type SavedSearch = components["schemas"]["SavedSearchItem"];

/** RuleBuilder adds a rule, on the saved search ?search= names if any, or edits rule :id. */
export function RuleBuilder() {
  const raw = useParams().id;
  const id = raw === undefined ? undefined : Number(raw);
  const [params] = useSearchParams();
  const library = useLibrary();
  const rule = useQuery({
    queryKey: ["rules", "one", id],
    queryFn: async () => must(await api.GET("/rules/{id}", { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  });
  const searches = useQuery(savedSearchesQuery(library));
  const keys = useQuery(enrichmentKeysQuery);
  const failed = rule.error ?? searches.error ?? keys.error;
  if (failed) return <p className="error">{failed.message}</p>;
  if ((id !== undefined && !rule.data) || !searches.data || !keys.data) return null;
  const fields = fieldsFor(keys.data.browse ?? []);
  const searchId = rule.data?.saved_search_id ?? (Number(params.get("search")) || undefined);
  const search = searches.data.find((s) => s.id === searchId);
  const initial: Spec = rule.data
    ? specOf(rule.data)
    : { name: "", saved_search_id: search?.id ?? 0, action: "archive", all_libraries: true, enabled: false };
  return <Builder key={id ?? `new-${searchId ?? ""}`} id={id} initial={initial} search={search} fields={fields} library={library} />;
}

type Props = { id?: number; initial: Spec; search?: SavedSearch; fields: Field[]; library: number };

function Builder({ id, initial, search, fields, library }: Props) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [ask, dialog] = useConfirm();
  const [spec, setSpec] = useState(initial);
  const decoded = search ? fromFilter(search.filter, fields) : { match: "all" as const, rows: [] };
  // A search rows can't show stays as it is; the rest of the rule still edits.
  const readOnly = search !== undefined && decoded === undefined;
  const [conds, setConds] = useState<Conditions>(decoded ?? { match: "all", rows: [] });
  const [previewing, setPreviewing] = useState<"save" | "turn-on">();
  const libraries = useQuery(librariesQuery);
  const ruleLibrary = spec.libraries?.[0] ?? library;
  const collections = useQuery({ ...collectionsQuery(ruleLibrary), enabled: spec.action === "collect" });
  const byId = new Map(fields.map((f) => [f.id, f]));
  const filter = readOnly ? search!.filter : toFilter(conds, fields);
  const changed = !readOnly && JSON.stringify(filter) !== JSON.stringify(search?.filter);
  const fixed = id !== undefined;
  const set = (change: Partial<Spec>) => setSpec({ ...spec, ...change });
  const setRow = (i: number, row: Condition) => setConds({ ...conds, rows: conds.rows.map((r, j) => (j === i ? row : r)) });
  const others = (search?.rules ?? []).filter((r) => r.id !== id);
  const libraryName = (lib: number) => libraries.data?.find((l) => l.id === lib)?.name ?? `Library ${lib}`;

  const matches = useQuery({
    ...searchQuery("card", { library, filter, fields: ["name"], page: { size: 5, number: 1 } }),
    enabled: filter !== undefined,
    placeholderData: keepPreviousData,
  });
  const valueRefs = fields.filter((f) => f.input === "value").map((f) => f.id.slice("value:".length));
  const facets = useQuery(searchQuery("card", { library, facets: ["tags", "sources", ...valueRefs.map((r) => `enrichment:${r}`)], facets_only: true }));
  const tags = facets.data?.facets?.tags ?? [];
  const sources = facets.data?.facets?.sources ?? [];

  const incomplete = !readOnly && (conds.rows.length === 0 || conds.rows.some((r) => !complete(r, byId.get(r.field))));
  const noLibrary = !spec.all_libraries && !spec.libraries?.length;
  const noCollection = spec.action === "collect" && !spec.collection_id;
  const blocked = !spec.name.trim() || incomplete || noLibrary || noCollection;

  const leave = () => {
    void qc.invalidateQueries({ queryKey: ["rules"] });
    void qc.invalidateQueries({ queryKey: ["saved-searches"] });
    void navigate("/rules");
  };
  const save = useMutation({
    mutationFn: async (turnOn: boolean) => {
      let searchId = search?.id;
      let created: number | undefined;
      if (searchId === undefined) {
        const s = must(await api.POST("/saved-searches", { body: { name: spec.name.trim(), filter: filter!, sort: "" } }));
        searchId = created = s.id;
      } else if (changed) {
        must(await api.PATCH("/saved-searches/{id}", { params: { path: { id: searchId } }, body: { filter: filter! } }));
      }
      const body: Spec = { ...spec, name: spec.name.trim(), saved_search_id: searchId };
      let saved;
      try {
        saved = id !== undefined ? must(await api.PUT("/rules/{id}", { params: { path: { id } }, body })) : must(await api.POST("/rules", { body }));
      } catch (e) {
        // The search was only for this rule.
        if (created !== undefined) await api.DELETE("/saved-searches/{id}", { params: { path: { id: created } } });
        throw e;
      }
      if (!turnOn) return saved;
      try {
        return must(await api.PUT("/rules/{id}", { params: { path: { id: saved.id } }, body: { ...specOf(saved), enabled: true } }));
      } catch (e) {
        // The rule exists now, so a retry would add it twice.
        toastError(`Added ${saved.name}, but couldn't turn it on: ${(e as Error).message}`);
        leave();
        return undefined;
      }
    },
    onSuccess: (r) => {
      if (!r) return;
      toast.success(fixed ? `Saved ${r.name}` : r.enabled ? `Added ${r.name} and turned it on` : `Added ${r.name}; it's off until you turn it on`);
      leave();
    },
    meta: { toast: false },
  });
  const remove = useMutation({
    mutationFn: async () => mustOk(await api.DELETE("/rules/{id}", { params: { path: { id: id! } } })),
    onSuccess: () => {
      toast.success(`Deleted ${initial.name}`);
      leave();
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (fixed && spec.enabled) setPreviewing("save");
    else save.mutate(false);
  };

  const scope = spec.all_libraries ? "all" : spec.libraries?.length === 1 ? String(spec.libraries[0]) : "some";
  const setScope = (v: string) =>
    v === "all" ? set({ all_libraries: true, libraries: undefined })
    : v === "some" ? set({ all_libraries: false, libraries: spec.libraries ?? [] })
    : set({ all_libraries: false, libraries: [Number(v)], collection_id: spec.action === "collect" ? undefined : spec.collection_id });
  const title = fixed ? initial.name : "New rule";

  return (
    <section className="page">
      <p className="muted">
        <Link to="/rules">Rules</Link> / {title}
      </p>
      <h1>{title}</h1>
      <form className="rule-builder" onSubmit={submit}>
        <label>
          Rule name <input className="input" value={spec.name} onChange={(e) => set({ name: e.target.value })} />
        </label>

        <p className="rule-sentence">
          When a card in{" "}
          <select className="input" aria-label="Libraries" value={scope} onChange={(e) => setScope(e.target.value)}>
            {spec.action === "archive" && <option value="all">all libraries</option>}
            {(libraries.data ?? []).map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
            {spec.action === "archive" && <option value="some">some libraries…</option>}
          </select>{" "}
          matches{" "}
          <select className="input" aria-label="Match" value={conds.match} disabled={readOnly} onChange={(e) => setConds({ ...conds, match: e.target.value as Conditions["match"] })}>
            <option value="all">all</option>
            <option value="any">any</option>
          </select>{" "}
          of:
        </p>
        {scope === "some" && (
          <fieldset className="rule-libraries">
            <legend>Libraries</legend>
            {(libraries.data ?? []).map((l) => (
              <label key={l.id} className="check">
                <input
                  type="checkbox"
                  checked={spec.libraries?.includes(l.id) ?? false}
                  onChange={(e) => set({ libraries: e.target.checked ? [...(spec.libraries ?? []), l.id] : (spec.libraries ?? []).filter((x) => x !== l.id) })}
                />{" "}
                {l.name}
              </label>
            ))}
          </fieldset>
        )}

        {readOnly ? (
          <p className="rule-row">
            Matches saved search <strong>{search!.name}</strong>, which has more than one level of conditions.{" "}
            <Link to={`/cards?saved=${search!.id}`}>Edit on Cards</Link>
          </p>
        ) : (
          <ol className="rule-rows">
            {conds.rows.map((row, i) => {
              const field = byId.get(row.field);
              const n = i + 1;
              return (
                <li key={i} className="rule-row">
                  <select
                    className="input"
                    aria-label={`Field ${n}`}
                    value={row.field}
                    onChange={(e) => {
                      const next = byId.get(e.target.value)!;
                      setRow(i, { field: next.id, op: next.ops[0]!, value: "" });
                    }}
                  >
                    {fields.map((f) => (
                      <option key={f.id} value={f.id}>
                        {f.label}
                      </option>
                    ))}
                  </select>
                  {field && field.ops.length > 1 && (
                    <select className="input" aria-label={`Operator ${n}`} value={row.op} onChange={(e) => setRow(i, { ...row, op: e.target.value as Condition["op"] })}>
                      {field.ops.map((op) => (
                        <option key={op} value={op}>
                          {opLabel(op, field)}
                        </option>
                      ))}
                    </select>
                  )}
                  {field && row.op !== "missing" && (
                    <ValueInput n={n} field={field} row={row} tags={tags} sources={sources} values={facets.data?.facets?.enrichments?.[row.field.slice("value:".length)] ?? []} onChange={(r) => setRow(i, r)} />
                  )}
                  <button type="button" className="button ghost" aria-label={`Remove condition ${n}`} onClick={() => setConds({ ...conds, rows: conds.rows.filter((_, j) => j !== i) })}>
                    ✕
                  </button>
                </li>
              );
            })}
            <li>
              <button type="button" className="button" onClick={() => setConds({ ...conds, rows: [...conds.rows, { field: "tag", op: "is", value: "" }] })}>
                Add condition
              </button>
            </li>
          </ol>
        )}
        {others.length > 0 && !readOnly && (
          <p className="muted">Changing these conditions also changes {others.map((r) => r.name).join(", ")}, which use the same saved search.</p>
        )}

        <p className="rule-sentence">
          then{" "}
          <select
            className="input"
            aria-label="Action"
            value={spec.action}
            disabled={fixed}
            onChange={(e) => {
              const action = e.target.value as Spec["action"];
              setSpec({ ...spec, action, collection_id: undefined, ...(action === "collect" ? { all_libraries: false, libraries: [ruleLibrary] } : { all_libraries: true, libraries: undefined }) });
            }}
          >
            <option value="archive">archive it</option>
            <option value="collect">add it to</option>
          </select>
          {spec.action === "collect" && (
            <>
              {" "}
              <select className="input" aria-label="Collection" value={spec.collection_id ?? ""} disabled={fixed} onChange={(e) => set({ collection_id: Number(e.target.value) || undefined })}>
                <option value="">a collection…</option>
                {(collections.data ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </>
          )}
          .
        </p>
        {collections.error && <p className="error">{collections.error.message}</p>}

        <p role="status" className="muted">
          {filter === undefined
            ? "Add a condition to see which cards match."
            : matches.error
              ? matches.error.message
              : matches.data
                ? `${count(matches.data.total, "card")} match in ${libraryName(library)}${matches.data.hits?.length ? ": " + matches.data.hits.map((h) => text(h.fields.name) || `#${h.id}`).join(", ") : ""}${matches.data.total > (matches.data.hits?.length ?? 0) ? ", …" : ""}`
                : "Counting cards…"}
        </p>

        {fixed && (
          <label className="check">
            <input type="checkbox" checked={spec.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> On
          </label>
        )}
        {save.error && <p className="error">{save.error.message}</p>}
        <div className="row">
          <Link className="button" to="/rules">
            Cancel
          </Link>
          <button className="button primary" disabled={blocked || save.isPending}>
            Save
          </button>
          {!fixed && (
            <button type="button" className="button" disabled={blocked || save.isPending} onClick={() => setPreviewing("turn-on")}>
              Save and turn on
            </button>
          )}
          {fixed && (
            <button
              type="button"
              className="button danger"
              onClick={() =>
                ask({
                  title: `Delete ${initial.name}?`,
                  body: <p>Its archived cards come back, and the cards it collected leave the collection.</p>,
                  confirmLabel: "Delete rule",
                  danger: true,
                  onConfirm: () => remove.mutate(),
                })
              }
            >
              Delete
            </button>
          )}
        </div>
      </form>
      {previewing && (
        <RulePreview
          id={id}
          spec={{ ...spec, name: spec.name.trim(), saved_search_id: search?.id ?? 0, enabled: true }}
          filter={filter}
          confirmLabel={previewing === "turn-on" ? "Turn on" : "Save rule"}
          onConfirm={() => save.mutate(previewing === "turn-on")}
          onClose={() => setPreviewing(undefined)}
        />
      )}
      {dialog}
    </section>
  );
}

type ValueProps = {
  n: number;
  field: Field;
  row: Condition;
  tags: { name: string; cards: number }[];
  sources: { kind: string; cards: number }[];
  values: { value: string; cards: number }[];
  onChange: (row: Condition) => void;
};

/** ValueInput is the control a field's value takes: text, a tag with suggestions, a choice, or numbers. */
function ValueInput({ n, field, row, tags, sources, values, onChange }: ValueProps) {
  const label = `Value ${n}`;
  switch (field.input) {
    case "none":
      return null;
    case "text":
      return <input className="input" aria-label={label} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })} />;
    case "tag":
      return (
        <>
          <input className="input" aria-label={label} list={`rule-tags-${n}`} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })} />
          <datalist id={`rule-tags-${n}`}>
            {tags.slice(0, 200).map((t) => (
              <option key={t.name} value={t.name}>{`${t.name} (${t.cards})`}</option>
            ))}
          </datalist>
        </>
      );
    case "source":
    case "value": {
      const options = field.input === "source" ? sources.map((s) => ({ value: s.kind, cards: s.cards })) : values;
      return (
        <select className="input" aria-label={label} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })}>
          <option value="">Pick one</option>
          {/* A value no card has now still shows, so an older rule reads true. */}
          {row.value && !options.some((o) => o.value === row.value) && <option value={row.value}>{row.value}</option>}
          {options.map((o) => (
            <option key={o.value} value={o.value}>{`${o.value} (${o.cards})`}</option>
          ))}
        </select>
      );
    }
    case "number":
      return (
        <>
          <input className="input rule-number" type="number" aria-label={label} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })} />
          {row.op === "between" && (
            <>
              {" and "}
              <input className="input rule-number" type="number" aria-label={`Second value ${n}`} value={row.value2 ?? ""} onChange={(e) => onChange({ ...row, value2: e.target.value })} />
            </>
          )}
        </>
      );
  }
}
```

Fix names against the real code and report each: the generated saved-search item type name (`SavedSearchItem` or similar), where `toastError` lives, the search response's `hits`/`total` types, and the facets' field names. Keep behavior as written.

- [ ] **Step 5: Route to it, delete the wizard, restyle**

- In `web/src/App.tsx`, import `RuleBuilder` from `./rules/RuleBuilder` in place of `RuleWizard`, for `rules/new` and `rules/:id`.
- `git rm web/src/rules/RuleWizard.tsx web/src/rules/MatchStep.tsx`
- In `web/src/rules/Rules.css`, delete `.steps`, `.rule-build` (and its media query), and `.rule-summary` rules, and add:

```css
.rule-builder {
  display: grid;
  gap: var(--space);
  max-width: 52rem;
}

.rule-builder > label {
  display: grid;
  gap: 0.25rem;
  max-width: 24rem;
}

.rule-sentence {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
  margin: 0;
}

.rule-sentence select.input {
  width: auto;
}

.rule-rows {
  list-style: none;
  padding: 0 0 0 1.5rem;
  margin: 0;
  display: grid;
  gap: 0.5rem;
}

.rule-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.rule-row .input {
  width: auto;
  min-width: 8rem;
}

.rule-row .rule-number {
  min-width: 0;
  width: 6rem;
}
```

Check the result in a browser at phone width too: rows wrap, nothing scrolls sideways.

- [ ] **Step 6: Run the tests**

Run: `cd web && npm run typecheck && npm test`
Expected: typecheck clean; every test passes. Nothing outside the plan and spec still names `RuleWizard` or `MatchStep`.

- [ ] **Step 7: Commit**

```bash
git add web/src/App.tsx web/src/rules
git commit -m "feat(web): build rules on one page as conditions and an action"
```

---

### Task 4: Docs

**Files:**
- Modify: `docs/using.md`

- [ ] **Step 1: Say how rules are built**

In `docs/using.md`, replace

```
Add one with Add rule on the Rules page, which can build and save its
search there, or with Add rule on a saved search.
```

with

```
Add one with Add rule on the Rules page: name it, add conditions on tags,
sources, names, creators, and plugin scores and values, and pick what it
does. Its conditions are saved as a saved search under the rule's name. Add
rule on a saved search starts from that search's conditions.
```

- [ ] **Step 2: Commit**

```bash
git add docs/using.md
git commit -m "docs: build rules from conditions on the Rules page"
```
