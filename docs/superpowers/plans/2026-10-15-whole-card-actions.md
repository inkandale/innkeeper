# AI Services Part 4a: Whole-Card Field Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A field action can also declare `whole_card`, so it shows in each listed field's menu and in the Reforge panel, and a run without a field may change any of its `fields`.

**Architecture:** This is the core half of section 4 of `docs/superpowers/specs/2026-10-15-ai-services-design.md`. The plugin half, forge-ai's plan-then-sections runs, is `innkeeper-plugins/docs/plans/2026-10-15-forge-ai-services.md`.
- `protocol.Action` gains `WholeCard bool` (JSON `whole_card`).
- `actions.Payload.Check` accepts a run with no field for such an action.
- `actions.Scope` limits that run to the action's `fields`.
- The web form counts such an action as whole-entity in the Reforge panel and locks only its `fields` while it runs.

No new endpoint. The API carries the field through `PluginAction`, which embeds `protocol.Action`.

**Tech Stack:** Go, Huma (schema generation), React + TypeScript, Vitest.

**Order:** This plan is independent of parts 1 to 3. Merge it before the forge-ai plan, which declares `WholeCard`.

---

## File map

| File | Change |
|---|---|
| `docs/protocol.md` | `whole_card` row; `field` param; scope rule |
| `protocol/action.go`, `protocol/action_test.go` | `WholeCard` and its validation |
| `internal/actions/actions.go`, `internal/actions/scope.go`, `internal/actions/actions_test.go` | Check and Scope |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated with `npm run gen`. Never hand-edit |
| `web/src/forge/actions.ts`, `web/src/forge/actions.test.ts` | `scopeOf`, `instructionAction`, `wholeActions` |
| `web/src/forge/Reforge.tsx` | Use `wholeActions` |
| `web/src/forge/Forge.test.tsx` | Reforge panel lists a whole-card field action |

---

### Task 1: Protocol documentation

**Files:**
- Modify: `docs/protocol.md`

- [ ] **Step 1: Add the `whole_card` row**

In the `action.list` table, after the `fields` row, add:

```markdown
| `whole_card` | With `fields`: `true` if the action also runs on the whole entity, on all its `fields` at once. The UI offers it in each field's menu and with the whole-entity actions |
```

- [ ] **Step 2: Update the `field` param**

In the `action.run` table, replace the `field` row with:

```markdown
| `field` | For a field action: the key it runs on, one of its `fields`. Absent for a `whole_card` action run on the whole entity |
```

- [ ] **Step 3: Update the scope rules**

Replace the first bullet under "The core then keeps only the changes in the action's scope:" with:

```markdown
- A field action may change only its `field`. Run without a field, a
  `whole_card` action may change any of its `fields`.
```

- [ ] **Step 4: Commit**

```bash
git add docs/protocol.md
git commit -m "docs(protocol): let a field action run on the whole card"
```

---

### Task 2: `Action.WholeCard`

**Files:**
- Modify: `protocol/action.go`, `protocol/action_test.go`

- [ ] **Step 1: Write the failing tests**

In `TestActionValidate`'s `tests` table (`protocol/action_test.go`), add:

```go
		{"whole-card field action", with(func(a *Action) { a.WholeCard = true }), ""},
		{"whole card without fields", Action{ID: "tighten-card", Label: "Tighten", Applies: "card", WholeCard: true}, "whole_card"},
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./protocol/ -run TestActionValidate`
Expected: FAIL to compile, with `unknown field WholeCard`.

- [ ] **Step 3: Add the field and its check**

In `protocol/action.go`, in `Action` after `Fields`:

```go
	// WholeCard means a field action also runs on the whole entity, changing
	// any of its Fields.
	WholeCard bool `json:"whole_card,omitempty"`
```

In `Validate`, after the `fields can't go with applies new` check:

```go
	if a.WholeCard && len(a.Fields) == 0 {
		errs = append(errs, fmt.Errorf("action %s: whole_card needs fields", a.ID))
	}
```

An action with `Applies: "new"` and `WholeCard` already fails, because `fields` isn't allowed with `new`.

- [ ] **Step 4: Run the tests**

Run: `go test ./protocol/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add protocol/action.go protocol/action_test.go
git commit -m "feat(protocol): let a field action declare whole_card"
```

---

### Task 3: Check and Scope

**Files:**
- Modify: `internal/actions/actions.go:62-65`, `internal/actions/scope.go`, `internal/actions/actions_test.go`

- [ ] **Step 1: Write the failing tests**

In `TestPayloadCheck` (`internal/actions/actions_test.go`), after `field := ...` add:

```go
	both := protocol.Action{ID: "tighten", Applies: "card", Fields: []string{"description"}, WholeCard: true}
```

and add these cases to its table:

```go
		{"whole-card field action without a field", both, Payload{Entity: card}, ""},
		{"whole-card field action on its field", both, Payload{Field: "description", Entity: card}, ""},
		{"whole-card field action on another field", both, Payload{Field: "scenario", Entity: card}, "scenario"},
```

In `TestScope`, after `whole := ...` add:

```go
	both := protocol.Action{ID: "tighten", Applies: "card", Fields: []string{"description", "scenario"}, WholeCard: true}
```

and add these cases to its table:

```go
		{"whole-card field action changes its fields and drops a rename", both, "", "card", base,
			`{"name": "Grok", "description": "Huge.", "scenario": "A pit."}`,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A pit."}`, []string{"name"}},
		{"whole-card field action on one field keeps to it", both, "description", "card", base,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A pit."}`,
			`{"name": "Brakka", "description": "Huge.", "scenario": "A cave."}`, []string{"scenario"}},
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/actions/ -run 'TestPayloadCheck|TestScope'`
Expected: FAIL. "whole-card field action without a field" gets `runs on the field`. In "changes its fields", `key == field` is false for every key when `field` is empty, so both prose changes drop.

- [ ] **Step 3: Accept a run without a field**

In `internal/actions/actions.go`, replace the case:

```go
	case len(a.Fields) > 0 && !slices.Contains(a.Fields, p.Field):
```

with:

```go
	case len(a.Fields) > 0 && !slices.Contains(a.Fields, p.Field) && (p.Field != "" || !a.WholeCard):
```

- [ ] **Step 4: Scope it to the fields**

In `internal/actions/scope.go`, in `allowed`, replace:

```go
		case len(a.Fields) > 0:
			return key == field
```

with:

```go
		case len(a.Fields) > 0 && field != "":
			return key == field
		case len(a.Fields) > 0:
			return slices.Contains(a.Fields, key)
```

Update `Scope`'s doc comment, keeping its length:

```go
// Scope keeps the changes proposal makes to base that action a may make, and
// returns a warning per key it dropped. field is the run's field, or "" for
// the whole entity; kind is the entity's. A nil base is a new card's empty
// draft. A report-only action's proposal is dropped whole.
```

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/actions/ ./internal/contract/ ./internal/api/`
Expected: PASS. `internal/contract/action.go:71` still sends `Fields[0]`, which is valid for a `whole_card` action.

- [ ] **Step 6: Commit**

```bash
git add internal/actions/
git commit -m "feat(actions): run a whole_card field action on all its fields"
```

---

### Task 4: Regenerate the API schema

**Files:**
- Modify (generated): `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Generate**

Run: `cd web && npm run gen`
Expected: `PluginAction` in `web/src/api/schema.d.ts` gains `whole_card?: boolean`.

- [ ] **Step 2: Commit**

```bash
git add web/openapi.json web/src/api/schema.d.ts
git commit -m "chore(web): regenerate the API schema"
```

---

### Task 5: The Forge UI

**Files:**
- Modify: `web/src/forge/actions.ts`, `web/src/forge/actions.test.ts`, `web/src/forge/Reforge.tsx:32`, `web/src/forge/Forge.test.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/forge/actions.test.ts`, change the import to add `wholeActions`:

```ts
import { changedKeys, instructionAction, lockOf, newAction, overlaps, refineInstruction, scopeOf, wholeActions, type Action } from "./actions";
```

After the `generate` constant, add:

```ts
const tightenAll = act({ id: "tighten", label: "Tighten", fields: ["description", "personality"], whole_card: true });
const reviseAll = act({ id: "revise", label: "Revise", instruction: true, fields: ["name", "description"], whole_card: true });
```

Add these tests:

```ts
test("a whole-card field action locks its fields when run without one", () => {
  expect(scopeOf(tightenAll)).toEqual(["description", "personality"]);
  expect(scopeOf(tightenAll, "description")).toEqual(["description"]);
});

test("the instruction box and the whole-entity list take whole-card field actions", () => {
  expect(instructionAction([rewrite, reviseAll], "card")?.id).toBe("revise");
  expect(wholeActions([tighten, tightenAll, evaluate, generate, rewrite], "card").map((a) => a.id)).toEqual(["tighten", "evaluate"]);
  expect(wholeActions([tightenAll], "card")[0]).toBe(tightenAll);
});
```

In `web/src/forge/Forge.test.tsx`, add:

```ts
test("the Reforge panel offers a whole-card field action, run without a field", async () => {
  actions = [...allActions, { ...decl, id: "expand", label: "Expand", description: "Add detail.", fields: ["description", "personality"], whole_card: true }];
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  await user.click(screen.getByRole("button", { name: /Expand/ }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ action: "expand" });
  expect(runs[0]).not.toHaveProperty("field");
});
```

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/forge/actions.test.ts src/forge/Forge.test.tsx`
Expected: FAIL. `wholeActions` isn't exported, `scopeOf(tightenAll)` is `"all"`, and the panel doesn't list Expand.

- [ ] **Step 3: Update `web/src/forge/actions.ts`**

Replace `scopeOf`:

```ts
/** scopeOf is the keys a run may change: its field, a whole-card field action's fields, everything, or nothing for a report. */
export const scopeOf = (a: Action | undefined, field?: string): Scope =>
  a?.report_only ? [] : field ? [field] : a?.fields?.length ? a.fields : "all";
```

Replace `instructionAction`:

```ts
/** whole reports whether a runs on the whole entity: no fields, or fields with whole_card. */
const whole = (a: Action) => !a.fields?.length || !!a.whole_card;

/** wholeActions are the actions on kind that run on the whole entity, in order. */
export const wholeActions = (list: Action[], kind: string) => list.filter((a) => a.applies === kind && whole(a));

/** instructionAction is the whole-entity action on kind that the Reforge panel's instruction box runs. */
export const instructionAction = (list: Action[], kind: string) => wholeActions(list, kind).find((a) => a.instruction);
```

- [ ] **Step 4: Use it in the Reforge panel**

In `web/src/forge/Reforge.tsx`, change the import to:

```ts
import { instructionAction, wholeActions, type Action } from "./actions";
```

and replace:

```ts
  const others = forge.mine.filter((a) => a.applies === kind && !a.fields?.length && a !== revise);
```

with:

```ts
  const others = wholeActions(forge.mine, kind).filter((a) => a !== revise);
```

The field menu in `Forge.tsx:94` already lists any action whose `fields` include the key, so it needs no change.

- [ ] **Step 5: Run the web checks**

Run: `cd web && npm run typecheck && npm test`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/forge/
git commit -m "feat(web): offer whole_card field actions in the Reforge panel"
```

---

### Task 6: Run what CI runs

- [ ] **Step 1: Run the checks**

```bash
go vet ./...
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all PASS.

- [ ] **Step 2: Read the diff**

Run: `git diff origin/main --stat` and read the diff once for dead code, stale names, and changes the task didn't ask for.

---

## Contracts for later plans

- `protocol.Action.WholeCard bool` (JSON `whole_card`), valid only with `Fields`.
- A `whole_card` action's run with `Field == ""` may change any key in `Fields`. Every other key the proposal changes is dropped with a warning.
- The web lists such an action in each field's menu and in the Reforge panel. The panel's instruction box runs the first whole-entity action with `instruction`.
