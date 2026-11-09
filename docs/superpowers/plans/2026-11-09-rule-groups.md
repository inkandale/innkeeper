# Rule Condition Groups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the rule builder's single "matches all/any of" list with groups: each group joins its items with one connector (and/or), any condition or group can be negated, and groups nest.

**Architecture:** Web only. `conditions.ts` models a tree of groups and conditions and turns it into the filter's `and`/`or`/`not` tree and back. `RuleBuilder.tsx` renders the tree with a recursive `GroupView`.

**Tech Stack:** React 19, TypeScript, Vitest with Testing Library.

Spec: `docs/superpowers/specs/2026-11-09-rule-builder-design.md` (the Conditions section). Run checks from `web/`: `npm run typecheck && npm test`. Never run Prettier.

---

### Task 1: The condition tree

**Files:**
- Modify: `web/src/rules/conditions.ts`, `web/src/rules/conditions.test.ts`

- [ ] **Step 1: Write the failing tests**

Replace `web/src/rules/conditions.test.ts`'s round-trip and decode tests (keep the `fields` fixture, the fields test, and the `complete` tests, adjusting them to the new `Condition` shape) with:

```ts
import { fieldsFor, fromFilter, newCondition, newGroup, toFilter, type Condition, type Group, type Item } from "./conditions";

/** bare drops the keys, which are random. */
const bare = (i: Item): unknown =>
  i.kind === "group" ? { kind: i.kind, not: i.not, op: i.op, items: i.items.map(bare) } : { ...i, key: undefined };
const cond = (c: Partial<Condition>): Condition => ({ ...newCondition(), ...c });
const group = (op: Group["op"], items: Item[], not = false): Group => ({ ...newGroup(op), not, items });

const leaves: [string, Partial<Condition>, unknown][] = [
  ["name contains", { field: "name", op: "contains", value: "junk" }, { name: "junk" }],
  ["creator is", { field: "creator", op: "is", value: "bob" }, { creator: "bob" }],
  ["tag is", { field: "tag", op: "is", value: "nsfw" }, { tag: "nsfw" }],
  ["source is", { field: "source", op: "is", value: "chub" }, { source: "chub" }],
  ["shares a source", { field: "shares_source", op: "is", value: "" }, { shares_source: true }],
  ["merged score at least", { field: "score:quality", op: "at_least", value: "70" }, { enrichment: "quality", gte: 70 }],
  ["score at least 0", { field: "score:quality", op: "at_least", value: "0" }, { enrichment: "quality", gte: 0 }],
  ["instance score at most", { field: "score:3:rating", op: "at_most", value: "2" }, { enrichment: "rating", instance: 3, lte: 2 }],
  ["score between", { field: "score:quality", op: "between", value: "10", value2: "40" }, { enrichment: "quality", gte: 10, lte: 40 }],
  ["no score", { field: "score:quality", op: "missing", value: "" }, { enrichment: "quality", missing: true }],
  ["value is", { field: "value:language", op: "is", value: "English" }, { enrichment: "language", is: "English" }],
];

test.each(leaves)("%s round-trips, plain and negated", (_, c, leaf) => {
  for (const not of [false, true]) {
    const root = group("and", [cond({ ...c, not })]);
    const want = { and: [not ? { not: leaf } : leaf] };
    expect(toFilter(root, fields)).toEqual(want);
    expect(bare(fromFilter(want, fields)!)).toEqual(bare(root));
  }
});

test("groups nest, each with its own connector and not", () => {
  const root = group("and", [
    cond({ field: "tag", op: "is", value: "nsfw" }),
    group("or", [cond({ field: "value:language", op: "is", value: "English" }), cond({ field: "source", op: "is", value: "chub" })], true),
  ]);
  const want = { and: [{ tag: "nsfw" }, { not: { or: [{ enrichment: "language", is: "English" }, { source: "chub" }] } }] };
  expect(toFilter(root, fields)).toEqual(want);
  expect(bare(fromFilter(want, fields)!)).toEqual(bare(root));
});

test("incomplete items and emptied groups are left out", () => {
  const root = group("and", [cond({ field: "tag", op: "is", value: "" }), group("or", [cond({ field: "tag", op: "is", value: "" })]), cond({ field: "tag", op: "is", value: "a" })]);
  expect(toFilter(root, fields)).toEqual({ and: [{ tag: "a" }] });
  expect(toFilter(group("and", []), fields)).toBeUndefined();
});

test("a bare condition, an or at the top, and a negated top read as the page's group", () => {
  expect(bare(fromFilter({ tag: "a" }, fields)!)).toEqual(bare(group("and", [cond({ field: "tag", op: "is", value: "a" })])));
  expect(fromFilter({ or: [{ tag: "a" }, { tag: "b" }] }, fields)!.op).toBe("or");
  // The page's group can't be negated, so a negated top goes inside it.
  const top = fromFilter({ not: { and: [{ tag: "a" }] } }, fields)!;
  expect(top.not).toBe(false);
  expect(top.items).toHaveLength(1);
  expect(top.items[0]).toMatchObject({ kind: "group", not: true });
});

test("a Cards-page filter with any-of groups reads as groups", () => {
  const f = { and: [{ or: [{ tag: "a" }, { tag: "b" }] }, { not: { tag: "c" } }] };
  expect(toFilter(fromFilter(f, fields)!, fields)).toEqual(f);
});

test("filters with a condition no field covers read as undefined", () => {
  expect(fromFilter({ and: [{ enrichment: "gone", gte: 1 }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ tag: "a", source: "chub" }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ collection: 4 }] }, fields)).toBeUndefined();
  expect(fromFilter({ or: [{ tag: "a" }, { and: [{ collection: 4 }] }] }, fields)).toBeUndefined();
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/rules/conditions.test.ts`
Expected: FAIL, `newCondition`, `newGroup`, and the `Group` type don't exist.

- [ ] **Step 3: Implement**

In `web/src/rules/conditions.ts`:

- `Op` drops `is_not`; Creator, Tag, and Source take `["is"]`; a text or list key takes `["is", "missing"]`; Name keeps `["contains"]`. Remove `is_not` from `opLabels`.
- Replace `Condition` and `Conditions` with:

```ts
/** Condition is one row: a field's id, an operator, and its value, or two for between. key tells rows apart while they're edited. */
export type Condition = { kind: "condition"; key: string; not: boolean; field: string; op: Op; value: string; value2?: string };
/** Group joins its items with one connector. The page's own group is never negated. */
export type Group = { kind: "group"; key: string; not: boolean; op: "and" | "or"; items: Item[] };
export type Item = Condition | Group;

export const newCondition = (): Condition => ({ kind: "condition", key: crypto.randomUUID(), not: false, field: "tag", op: "is", value: "" });
export const newGroup = (op: Group["op"] = "and"): Group => ({ kind: "group", key: crypto.randomUUID(), not: false, op, items: [] });

/** depth is how many groups deep item's deepest group sits, 0 for a condition. */
export const depth = (item: Item): number => (item.kind === "condition" ? 0 : 1 + Math.max(0, ...item.items.map(depth)));
```

- `complete` takes a `Condition` as before. `condition(row, field)` returns the plain leaf; drop its `is_not` wrapping (negation now lives on `not`).
- Replace `toFilter` with:

```ts
function itemFilter(item: Item, byId: Map<string, Field>): Filter | undefined {
  const f = item.kind === "group" ? groupFilter(item, byId) : complete(item, byId.get(item.field)) ? condition(item, byId.get(item.field)!) : undefined;
  return f && item.not ? { not: f } : f;
}

function groupFilter(g: Group, byId: Map<string, Field>): Filter | undefined {
  const parts = g.items.map((i) => itemFilter(i, byId)).filter((f): f is Filter => f !== undefined);
  if (!parts.length) return undefined;
  return g.op === "and" ? { and: parts } : { or: parts };
}

/** toFilter is the saved search filter for root's complete conditions, or undefined when it has none. */
export function toFilter(root: Group, fields: Field[]): Filter | undefined {
  return itemFilter(root, new Map(fields.map((f) => [f.id, f])));
}
```

- Rename `rowOf` to `conditionOf`, drop its negation handling (it decodes plain leaves only, returning a `Condition` with `not: false` and a fresh key, using `is` where it used `is_not`), and replace `fromFilter` with:

```ts
function itemOf(f: Filter, fields: Field[]): Item | undefined {
  if (f.not && only(f, "not")) {
    const inner = itemOf(f.not, fields);
    if (!inner) return undefined;
    // Not of a not keeps both, as a negated group around the inner one.
    return inner.not ? { ...newGroup(), not: true, items: [inner] } : { ...inner, not: true };
  }
  const parts = f.and && only(f, "and") ? f.and : f.or && only(f, "or") ? f.or : undefined;
  if (parts) {
    const items = parts.map((p) => itemOf(p, fields));
    if (items.some((i) => i === undefined)) return undefined;
    return { ...newGroup(f.and ? "and" : "or"), items: items as Item[] };
  }
  return conditionOf(f, fields);
}

/** fromFilter reads a saved search's filter as the page's group, or returns undefined when a condition has no field, such as a collection. */
export function fromFilter(f: Filter, fields: Field[]): Group | undefined {
  const item = itemOf(f, fields);
  if (!item) return undefined;
  return item.kind === "group" && !item.not ? item : { ...newGroup(), items: [item] };
}
```

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/rules/conditions.test.ts`
Expected: PASS. `RuleBuilder.tsx` won't typecheck until Task 2; don't commit yet.

---

### Task 2: Groups on the page

**Files:**
- Modify: `web/src/rules/RuleBuilder.tsx`, `web/src/rules/Rules.css`, `web/src/rules/Rules.test.tsx`, `docs/using.md`

- [ ] **Step 1: Write the failing tests**

In `web/src/rules/Rules.test.tsx`, update existing tests for the new labels below (a condition's controls are named by its path: `Field 1`, `Field 2.1`; the `Match` select is gone; `is_not` is now the `Not` toggle), and add:

```ts
test("a group mixes or inside an and, and not negates it", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Mixed");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.type(screen.getByLabelText("Value 1"), "nsfw");
  await user.click(screen.getByRole("button", { name: "Add group" }));
  await user.type(screen.getByLabelText("Value 2.1"), "a");
  await user.click(screen.getByRole("button", { name: "Add condition to group 2" }));
  await user.type(screen.getByLabelText("Value 2.2"), "b");
  await user.selectOptions(screen.getByRole("combobox", { name: "Connector in group 2" }), "or");
  await user.click(screen.getByRole("button", { name: "Not 2" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls[0]).toEqual({
      method: "POST",
      path: "/api/saved-searches",
      body: { name: "Mixed", filter: { and: [{ tag: "nsfw" }, { not: { or: [{ tag: "a" }, { tag: "b" }] } }] }, sort: "" },
    }),
  );
});

test("the connector menu sits on the second item and changes the whole group", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  for (let i = 0; i < 3; i++) await user.click(await screen.findByRole("button", { name: "Add condition" }));
  expect(screen.getAllByRole("combobox", { name: "Connector" })).toHaveLength(1);
  await user.selectOptions(screen.getByRole("combobox", { name: "Connector" }), "or");
  expect(screen.getAllByText("or", { selector: ".rule-connector span" })).toHaveLength(1);
});

test("Add group stops two levels down", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Add group" }));
  await user.click(screen.getByRole("button", { name: "Add group to group 1" }));
  expect(screen.queryByRole("button", { name: "Add group to group 1.1" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Add condition to group 1.1" })).toBeInTheDocument();
});

test("a Cards search with any-of groups edits as groups", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET"
      ? json([{ id: 3, name: "Junk", filter: { and: [{ or: [{ tag: "a" }, { tag: "b" }] }] }, sort: "", created_at: "", rules: [] }])
      : fetch(req),
  );
  renderAt("/rules/new?search=3");
  expect(await screen.findByDisplayValue("a")).toBeInTheDocument();
  expect(screen.getByLabelText("Value 1.2")).toHaveValue("b");
  expect(screen.queryByText(/can't show/)).not.toBeInTheDocument();
});
```

Change the existing read-only test to use a collection condition (`{ and: [{ collection: 4 }] }`) instead of any-of groups.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/rules/Rules.test.tsx`
Expected: FAIL (typecheck errors, or no Add group button).

- [ ] **Step 3: Render the tree**

In `RuleBuilder.tsx`:

- State becomes `const [root, setRoot] = useState<Group>(() => decoded ?? newGroup())`, where `decoded` is `fromFilter(...)` (or `newGroup()` with no search). Drop the `Row` type, `nextKey`, `setRow`, and the `Match` select; the sentence reads "When a card in [libraries] matches:".
- `filter = readOnly ? search!.filter : toFilter(root, fields)`; `changed` compares `toFilter(root, fields)` with `decoded && toFilter(decoded, fields)` as before.
- `incomplete`: true when the tree has no conditions, or any condition anywhere isn't complete. Write a small recursive helper in `conditions.ts`, `conditionsOf(item): Condition[]`, and use it here.
- Replace the `<ol className="rule-rows">…</ol>` with `<GroupView group={root} path="" level={0} onChange={setRoot} ctx={…} />`, passing in `ctx` what rows need: `fields`, `byId`, `sources`, the facets' enrichment values, and `ValueInput`'s other inputs.
- Add `GroupView`:

```tsx
type Ctx = { fields: Field[]; byId: Map<string, Field>; sources: { kind: string; cards: number }[]; values: (fieldId: string) => { value: string; cards: number }[] };

/** GroupView is one group's items, joined by its connector, with buttons to add to it. path names it in labels, "" for the page's own. */
function GroupView({ group, path, level, onChange, ctx }: { group: Group; path: string; level: number; onChange: (g: Group) => void; ctx: Ctx }) {
  const at = (i: number) => (path ? `${path}.${i + 1}` : `${i + 1}`);
  const where = path ? ` to group ${path}` : "";
  const set = (i: number, item: Item) => onChange({ ...group, items: group.items.map((x, j) => (j === i ? item : x)) });
  const remove = (i: number) => onChange({ ...group, items: group.items.filter((_, j) => j !== i) });
  return (
    <ol className={level ? "rule-group nested" : "rule-group"}>
      {group.items.map((item, i) => (
        <li key={item.key} className="rule-item">
          <span className="rule-connector">
            {i === 0 ? (
              <span>Where</span>
            ) : i === 1 ? (
              <select className="input" aria-label={path ? `Connector in group ${path}` : "Connector"} value={group.op}
                onChange={(e) => onChange({ ...group, op: e.target.value as Group["op"] })}>
                <option value="and">and</option>
                <option value="or">or</option>
              </select>
            ) : (
              <span>{group.op}</span>
            )}
          </span>
          <button type="button" className="button ghost rule-not" aria-pressed={item.not} aria-label={`Not ${at(i)}`}
            onClick={() => set(i, { ...item, not: !item.not })}>
            Not
          </button>
          {item.kind === "group" ? (
            <GroupView group={item} path={at(i)} level={level + 1} onChange={(g) => set(i, g)} ctx={ctx} />
          ) : (
            <ConditionView n={at(i)} row={item} onChange={(c) => set(i, c)} ctx={ctx} />
          )}
          <button type="button" className="button ghost" aria-label={`Remove ${at(i)}`} onClick={() => remove(i)}>
            ✕
          </button>
        </li>
      ))}
      <li className="rule-add">
        <button type="button" className="button" aria-label={`Add condition${where}`} onClick={() => onChange({ ...group, items: [...group.items, newCondition()] })}>
          + Add condition
        </button>
        {/* Two levels of groups cover real rules and stay readable. */}
        {level < 2 && (
          <button type="button" className="button" aria-label={`Add group${where}`}
            onClick={() => onChange({ ...group, items: [...group.items, { ...newGroup(), items: [newCondition()] }] })}>
            + Add group
          </button>
        )}
      </li>
    </ol>
  );
}
```

- Move the current row's field, operator, and value controls into `ConditionView({ n, row, onChange, ctx })`, labelled `Field ${n}`, `Operator ${n}`, `Value ${n}` (and `Second value ${n}`). Switching the field keeps `kind`, `key`, and `not`, and resets `op` to the field's first and `value`/`value2` to empty. A field with one operator shows it as text.

- [ ] **Step 4: Style the groups**

In `web/src/rules/Rules.css`, replace the `.rule-rows`/`.rule-row` rules with:

```css
.rule-group {
  list-style: none;
  padding: 0;
  margin: 0;
  display: grid;
  gap: 0.5rem;
}

.rule-group.nested {
  flex: 1 1 100%;
  padding: 0.5rem;
  border: 1px solid var(--border);
  border-radius: var(--radius, 6px);
}

.rule-item,
.rule-add {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.rule-connector {
  width: 4.5rem;
  color: var(--text-muted);
}

.rule-connector select.input {
  width: 100%;
}

.rule-not[aria-pressed="true"] {
  color: var(--danger, inherit);
  font-weight: 600;
}

.rule-item .input {
  width: auto;
  min-width: 8rem;
}

.rule-item .rule-number {
  min-width: 0;
  width: 6rem;
}
```

Use the theme's real token names (check `web/src/theme/tokens.css`). Check at phone width that a nested group wraps under its connector and nothing scrolls sideways.

- [ ] **Step 5: Docs**

In `docs/using.md`, in the paragraph that starts "Add one with Add rule on the Rules page", after "and pick what it does." add: "Join conditions with and or or, negate one or a whole group with Not, and group them to mix and with or."

- [ ] **Step 6: Run everything**

Run: `cd web && npm run typecheck && npm test`
Expected: typecheck clean; every test passes.

- [ ] **Step 7: Commit**

```bash
git add web/src/rules docs/using.md
git commit -m "feat(web): join rule conditions with and, or, and not, in groups"
```
