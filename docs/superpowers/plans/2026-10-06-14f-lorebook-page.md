# Web UI Lorebook Page Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Open a lorebook, edit its name, description, and entries, then save, push, archive, and browse and restore its history.

**Architecture:** The card page's draft, save, stale-save, and leave-guard logic moves into a `useEditor(kind, id)` hook that both pages use. Lorebook routes have the same shapes as card routes, so `route(kind, "/cards/…")` lets the panel pieces and the history view serve either kind. The lorebook response gains the IDs of its linked cards. The right frame is an entry editor: a filter, and one collapsible row per entry with chip inputs for keys, the content, and the flags, plus add, delete, and reorder.

**Tech Stack:** Go 1.27, Huma v2, PostgreSQL, React, React Router, TanStack Query, `openapi-fetch`, Vitest.

**Spec:** `docs/superpowers/specs/2026-10-05-web-ui-design.md`, "Lorebooks", **Lorebook page**. Build order phase 5, second half, after plan 14e.

---

## Design notes

- **Entries are positional.** CCv3 entries have no stable ID; the merge engine matches them by comment or keys. The editor keeps a client-side ID per entry, held in parallel with the array and changed by the same add, delete, and move operations. React keys and open rows follow the entry when it moves.
- **Unsaved entries are marked by content.** An entry is unsaved when no entry in the base revision has the same JSON. Reordering alone marks nothing, but Save still sends the new order, since `entries` differs from the base.
- **Reorder by drag or by button.** Rows are `draggable`, with native drag and drop. Each row also has Move up and Move down buttons, which keyboard users need and jsdom can test.
- **Fields edited:** `keys` and `secondary_keys` as chip inputs, `content`, `constant`, `selective`, `enabled`, `insertion_order` (Order), `position` (`before_char` or `after_char`), and `extensions.depth` (Depth, as SillyTavern stores it). Other entry fields, such as `comment`, `name`, `case_sensitive`, and `use_regex`, pass through unchanged.
- **New entries** get `keys: []`, `content: ""`, `enabled: true`, `insertion_order: 100`, `position: "before_char"`, and `extensions: {}`, the fields CCv3 requires. A new entry opens expanded.
- **One path helper.** `route(kind, path)` swaps `/cards/` for `/lorebooks/` and keeps the card path's type, since the routes share their shapes. Query keys become `[kind, id, …]`. A card's keys stay `["card", id, …]`, so nothing changes for cards.
- **Out of this plan:** linking and unlinking cards from the lorebook page (the card API already does it), lorebook export, and a per-entry diff view in history (entries diff as JSON blocks, as list hunks do).

## Files

| File | Change |
|---|---|
| `internal/library/organize.go`, `organize_test.go` | `LorebookCards` |
| `internal/api/entities.go`, `entities_test.go` | `cards` on lorebooks |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated |
| `web/src/api/client.ts`, `client.test.ts` | `Kind`, `route` |
| `web/src/card/useEditor.ts` | New: the editor hook, moved out of `Card.tsx` |
| `web/src/card/Card.tsx`, `Conflict.tsx` | Use the hook; `Stale` banner |
| `web/src/card/Panel.tsx`, `Organize.tsx`, `History.tsx` | Kind-aware pieces |
| `web/src/lorebook/entries.ts`, `entries.test.ts` | New: entry helpers |
| `web/src/lorebook/ChipInput.tsx`, `ChipInput.test.tsx` | New: chip input |
| `web/src/lorebook/Entries.tsx` | New: the entry editor |
| `web/src/lorebook/Lorebook.tsx`, `Lorebook.test.tsx`, `Lorebook.css` | New: the page |
| `web/src/App.tsx` | Routes |
| `docs/superpowers/specs/2026-10-05-web-ui-design.md` | Match the spec |

Go tests need PostgreSQL. Run them with:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
```

Without it, the database tests skip and a run passes without testing anything. Every `go test` command below assumes it's set.

macOS here is case-insensitive: never create a file whose name differs from an existing one only by case. `web/src/lorebook/` is a new folder next to `web/src/card/`.

---

### Task 1: A lorebook's linked cards

**Files:**
- Modify: `internal/library/organize.go`, `internal/api/entities.go`
- Test: `internal/library/organize_test.go`, `internal/api/entities_test.go`
- Regenerate: `web/openapi.json`, `web/src/api/schema.d.ts`

- [ ] **Step 1: Write the failing tests**

Append to `internal/library/organize_test.go`:

```go
func TestLorebookCards(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	a, _ := newCard(t, lib, "a", `{"name": "A"}`)
	b, _ := newCard(t, lib, "b", `{"name": "B"}`)
	lb := newLorebook(t, lib, "forge")
	if got, err := lib.LorebookCards(ctx, lb); err != nil || len(got) != 0 {
		t.Fatalf("before = %v, %v", got, err)
	}
	for _, c := range []int64{b, a} {
		if err := lib.LinkLorebook(ctx, c, lb); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := lib.LorebookCards(ctx, lb); err != nil || !slices.Equal(got, []int64{a, b}) {
		t.Fatalf("after = %v, %v", got, err)
	}
}
```

In `internal/api/entities_test.go`, add a field to `entityResult` after `Collections`:

```go
	Cards       []int64        `json:"cards"`
```

Append:

```go
func TestLorebookCardsField(t *testing.T) {
	e := newEnv(t)
	c := e.createCard(`{"name": "Brakka"}`)
	var book entityResult
	e.call("POST", "/lorebooks", map[string]any{"data": json.RawMessage(`{"name": "Forge", "entries": []}`)}, 201, &book)
	e.call("PUT", fmt.Sprintf("/cards/%d/lorebooks/%d", c.ID, book.ID), nil, 204, nil)
	var got entityResult
	e.call("GET", fmt.Sprintf("/lorebooks/%d", book.ID), nil, 200, &got)
	if len(got.Cards) != 1 || got.Cards[0] != c.ID {
		t.Fatalf("cards = %v", got.Cards)
	}
	e.call("GET", fmt.Sprintf("/cards/%d", c.ID), nil, 200, &got)
	if got.Cards != nil {
		t.Fatalf("a card lists cards: %v", got.Cards)
	}
}
```

- [ ] **Step 2: Run them to see them fail**

Run: `go test ./internal/library -run TestLorebookCards; go test ./internal/api -run TestLorebookCardsField`
Expected: the library test fails to compile, `lib.LorebookCards undefined`.

- [ ] **Step 3: Add `LorebookCards` and the field**

In `internal/library/organize.go`, add after `CardLorebooks`:

```go
// LorebookCards returns the IDs of the cards a lorebook is linked to.
func (l *Library) LorebookCards(ctx context.Context, lorebookID int64) ([]int64, error) {
	rows, err := l.pool.Query(ctx, `SELECT card_id FROM card_lorebook WHERE lorebook_id = $1
		ORDER BY card_id`, lorebookID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
```

In `internal/api/entities.go`, add a field to `entityOut` after `Collections`:

```go
	Cards       []int64     `json:"cards,omitempty" doc:"Lorebooks only: IDs of linked cards"`
```

In `(*Server).entity`, after the `if ents == s.Lib.Cards { … }` block:

```go
	if ents == s.Lib.Lorebooks {
		if out.Body.Cards, err = s.Lib.LorebookCards(ctx, id); err != nil {
			return nil, s.fail(err)
		}
	}
```

- [ ] **Step 4: Run the tests and regenerate the client**

Run: `go test ./internal/library ./internal/api && cd web && npm run gen && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/library/organize.go internal/library/organize_test.go internal/api/entities.go internal/api/entities_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(api): list the cards a lorebook is linked to"
```

---

### Task 2: One editor for cards and lorebooks

A refactor: no behavior changes, and every existing card test passes unchanged.

**Files:**
- Create: `web/src/card/useEditor.ts`
- Modify: `web/src/api/client.ts`, `web/src/card/Card.tsx`, `web/src/card/Conflict.tsx`
- Test: `web/src/api/client.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `web/src/api/client.test.ts`, adding `route` to its import from `./client`:

```ts
test("route swaps a card path for a lorebook one", () => {
  expect(route("card", "/cards/{id}/revisions")).toBe("/cards/{id}/revisions");
  expect(route("lorebook", "/cards/{id}/revisions")).toBe("/lorebooks/{id}/revisions");
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/api/client.test.ts`
Expected: FAIL, `route` isn't exported.

- [ ] **Step 3: Add `Kind` and `route`**

Append to `web/src/api/client.ts`:

```ts
/** Kind is what a library page shows. */
export type Kind = "card" | "lorebook";

/** route is a card route's path for kind. Lorebook routes have the same shapes, so they keep the card path's types. */
export function route<P extends `/cards/${string}`>(kind: Kind, path: P): P {
  return (kind === "card" ? path : path.replace("/cards/", "/lorebooks/")) as P;
}
```

- [ ] **Step 4: Move the editor state into a hook**

Create `web/src/card/useEditor.ts`. It's `Card.tsx`'s draft, save, stale, and leave-guard code, moved, with paths through `route` and query keys under `kind`:

```ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useBlocker } from "react-router";
import { api, ApiError, must, route, type Kind } from "../api/client";
import { useToast } from "../shell/Toasts";
import { changed, rebase, type Data, type Draft } from "./draft";

/** useEditor loads a card or lorebook and holds its unsaved edits, its save, and the stale-save flow. */
export function useEditor(kind: Kind, id: number) {
  const qc = useQueryClient();
  const toast = useToast();
  const entity = useQuery({
    queryKey: [kind, id],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}"), { params: { path: { id } } })),
  });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [stale, setStale] = useState(false);
  const [comparing, setComparing] = useState(false);
  const dirty = changed(draft);
  useLeaveGuard(dirty.size > 0);

  const save = useMutation({
    mutationFn: async (d: Draft) =>
      must(
        await api.PUT(route(kind, "/cards/{id}"), {
          params: { path: { id } },
          body: { base_revision_id: d.baseId, data: d.data, source: "ui" },
        }),
      ),
    onSuccess: (e, sent) => {
      qc.setQueryData([kind, id], e);
      void qc.invalidateQueries({ queryKey: [kind, id, "revisions"] });
      // Keep edits typed while the save was in flight, on top of the new head.
      setDraft((cur) => {
        if (!cur || cur === sent) return null;
        const next = rebase(cur, e.revision.id, (e.revision.data ?? {}) as Data);
        return changed(next).size ? next : null;
      });
      setStale(false);
      setComparing(false);
      toast({ text: "Saved" });
    },
    onError: (e) => (e instanceof ApiError && e.status === 409 ? setStale(true) : toast({ text: e.message, error: true })),
  });

  const head = entity.data;
  const headData = (head?.revision.data ?? {}) as Data;
  const edit = (key: string, value: unknown) =>
    setDraft((cur) => {
      if (!head) return cur;
      const d = cur ?? { baseId: head.revision.id, base: headData, data: headData };
      return { ...d, data: { ...d.data, [key]: value } };
    });
  const reload = () => {
    setDraft(null);
    setStale(false);
    setComparing(false);
    void entity.refetch();
  };
  const compare = async () => {
    await entity.refetch();
    setComparing(true);
  };
  // Keep moves the draft onto the head that beat it, so Save can go ahead.
  const keep = () => {
    if (draft && head) setDraft(rebase(draft, head.revision.id, headData));
    setStale(false);
    setComparing(false);
  };
  const use = (key: string) => setDraft((cur) => cur && { ...cur, data: { ...cur.data, [key]: headData[key] } });

  return { entity, headData, data: draft?.data ?? headData, draft, setDraft, dirty, edit, save, stale, comparing, reload, compare, keep, use };
}

export type Editor = ReturnType<typeof useEditor>;

// useLeaveGuard asks before leaving the page while on is true: in the app
// through the router, and on reload or close through the browser.
function useLeaveGuard(on: boolean) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => on && currentLocation.pathname !== nextLocation.pathname);
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm("Leave without saving your edits?")) blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    if (!on) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [on]);
}
```

- [ ] **Step 5: Move the banner into `Conflict.tsx`**

Append to `web/src/card/Conflict.tsx`, adding `import type { Kind } from "../api/client";` and `import type { Editor } from "./useEditor";`:

```tsx
/** Stale is the "changed elsewhere" banner and, once asked, the comparison. */
export function Stale({ kind, ed }: { kind: Kind; ed: Editor }) {
  if (!ed.stale) return null;
  return (
    <>
      <div className="banner" role="alert">
        <span>This {kind} changed elsewhere since you started editing.</span>
        <button className="button" onClick={ed.reload}>
          Reload and lose my edits
        </button>
        <button className="button" onClick={() => void ed.compare()}>
          Compare with the latest
        </button>
      </div>
      {ed.comparing && ed.draft && <Conflict latest={ed.headData} draft={ed.draft} onUse={ed.use} onKeep={ed.keep} />}
    </>
  );
}
```

- [ ] **Step 6: Use them in `Card.tsx`**

Replace `web/src/card/Card.tsx` with:

```tsx
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useParams } from "react-router";
import { api, ApiError, must } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { Stale } from "./Conflict";
import { Fields } from "./Fields";
import { Panel } from "./Panel";
import { useEditor } from "./useEditor";
import "./Card.css";

export type Entity = components["schemas"]["EntityOut"];

/** Card is the card page. It's keyed by ID so another card starts with no draft. */
export function Card() {
  const { id } = useParams();
  return <CardEditor key={id} id={Number(id)} />;
}

function CardEditor({ id }: { id: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const ed = useEditor("card", id);

  // An image change saves at once, on the head. A draft started from that
  // head moves onto the new revision, since its data didn't change.
  const avatar = useMutation({
    mutationFn: async ({ sha, base }: { sha: string; base: Entity["revision"] }) =>
      must(
        await api.PUT("/cards/{id}", {
          params: { path: { id } },
          body: { base_revision_id: base.id, data: base.data, avatar: sha, message: "Replace image", source: "ui" },
        }),
      ),
    onSuccess: (e, { base }) => {
      qc.setQueryData(["card", id], e);
      void qc.invalidateQueries({ queryKey: ["card", id, "revisions"] });
      ed.setDraft((d) => (d && d.baseId === base.id ? { ...d, baseId: e.revision.id } : d));
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        // The head moved on, so the button's base is stale; refetch it.
        void qc.invalidateQueries({ queryKey: ["card", id] });
        toast({ text: "This card changed elsewhere. Try again.", error: true });
      } else toast({ text: e.message, error: true });
    },
  });

  if (ed.entity.isPending) return null;
  if (ed.entity.isError) return <p className="error page">{ed.entity.error.message}</p>;
  const head = ed.entity.data;

  return (
    <div className="card-page">
      <Panel
        card={head}
        data={ed.data}
        edit={ed.edit}
        dirty={ed.dirty.size > 0}
        saving={ed.save.isPending || ed.stale || avatar.isPending}
        onSave={() => ed.draft && ed.save.mutate(ed.draft)}
        onAvatar={(sha) => avatar.mutate({ sha, base: head.revision })}
        avatarBusy={avatar.isPending || ed.save.isPending}
      />
      <div className="card-main">
        <Stale kind="card" ed={ed} />
        <Fields data={ed.data} dirty={ed.dirty} edit={ed.edit} />
      </div>
    </div>
  );
}
```

Compare the old `Card.tsx` with this one before deleting it. If it had anything not listed here (a comment, a prop), keep it.

- [ ] **Step 7: Run every web test**

Run: `cd web && npm test && npm run typecheck`
Expected: PASS, with `Card.test.tsx` unchanged.

- [ ] **Step 8: Commit**

```bash
git add web/src/api/client.ts web/src/api/client.test.ts web/src/card/useEditor.ts web/src/card/Card.tsx web/src/card/Conflict.tsx
git commit -m "refactor(web): share the card editor's state as a hook"
```

---

### Task 3: Panel pieces and history for either kind

Another refactor: card behavior and tests stay the same.

**Files:**
- Modify: `web/src/card/Panel.tsx`, `web/src/card/Organize.tsx`, `web/src/card/History.tsx`, `web/src/App.tsx`

- [ ] **Step 1: Make the panel pieces kind-aware**

In `web/src/card/Panel.tsx`, change the client import to `import { api, must, mustOk, route, thumb, upload, type Kind } from "../api/client";`. Then:

1. Export `Archive`, `Push`, `Sync`, and `Revisions`, and add `kind: Kind` as their first prop. `Archive` takes `{ kind, entity }` in place of `{ card }`.
2. In each, replace each `"/cards/{id}…"` path with `route(kind, "/cards/{id}…")`, each query key `["card", id, …]` with `[kind, id, …]`, and each link `` `/cards/${id}…` `` with `` `/${kind}s/${id}…` ``.
3. In `Panel`, pass `kind="card"` to each one, and `entity={card}` to `Archive`.

`Archive` after the change, as an example of the pattern:

```tsx
export function Archive({ kind, entity }: { kind: Kind; entity: Entity }) {
  const qc = useQueryClient();
  const toast = useToast();
  const archived = !!entity.archived_at;
  const set = useMutation({
    mutationFn: async () =>
      mustOk(await api.PUT(route(kind, "/cards/{id}/archived"), { params: { path: { id: entity.id } }, body: { archived: !archived } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [kind, entity.id] }),
    onError: (e) => toast({ text: e.message, error: true }),
  });
  return (
    <button className={archived ? "button" : "button danger"} disabled={set.isPending} onClick={() => set.mutate()}>
      {archived ? "Unarchive" : "Archive"}
    </button>
  );
}
```

In `web/src/card/Organize.tsx`, do the same for `Sources`. It takes `{ kind, id }`, and `Panel` passes `kind="card"`.

- [ ] **Step 2: Make history kind-aware**

In `web/src/card/History.tsx`:

1. Import `route` and `type Kind` from `../api/client`.
2. Change the signature to `export function History({ kind = "card" }: { kind?: Kind })`, and its doc comment to say "a card's or lorebook's revisions".
3. Replace each `"/cards/{id}…"` path with `route(kind, "/cards/{id}…")`, and each query key `["card", id, …]` with `[kind, id, …]`.
4. Change the back link to `` <Link to={`/${kind}s/${id}`}>← Back to {kind}</Link> ``, and the stale-restore toast to `` `The ${kind} changed since you opened this page. Check the new head, then restore again.` ``.

- [ ] **Step 3: Run every web test**

Run: `cd web && npm test && npm run typecheck`
Expected: PASS, with the card and history tests unchanged.

- [ ] **Step 4: Commit**

```bash
git add web/src/card
git commit -m "refactor(web): let the panel pieces and history serve lorebooks"
```

---

### Task 4: Entry helpers

**Files:**
- Create: `web/src/lorebook/entries.ts`
- Test: `web/src/lorebook/entries.test.ts`

- [ ] **Step 1: Write the failing test**

Create `web/src/lorebook/entries.test.ts`:

```ts
import { asEntries, blankEntry, firstLine, matches, moved, strings } from "./entries";

const anvil = { keys: ["anvil", "forge"], secondary_keys: ["iron"], comment: "Smithy", content: "Heavy iron.\nRings when struck." };

test("asEntries keeps the objects in an array", () => {
  expect(asEntries([anvil, "junk", null])).toEqual([anvil]);
  expect(asEntries(undefined)).toEqual([]);
});

test("strings keeps the strings in an array", () => {
  expect(strings(["a", 1, "b"])).toEqual(["a", "b"]);
  expect(strings("a")).toEqual([]);
});

test("firstLine is the content's first line, cut at 80", () => {
  expect(firstLine(anvil)).toBe("Heavy iron.");
  expect(firstLine({ content: "x".repeat(100) })).toBe("x".repeat(80) + "…");
  expect(firstLine({})).toBe("");
});

test("matches looks in keys, secondary keys, comment, and content, ignoring case", () => {
  for (const q of ["ANVIL", "iron", "smithy", "rings", ""]) expect(matches(anvil, q)).toBe(true);
  expect(matches(anvil, "kraken")).toBe(false);
});

test("moved moves one item", () => {
  expect(moved(["a", "b", "c"], 0, 2)).toEqual(["b", "c", "a"]);
  expect(moved(["a", "b", "c"], 2, 1)).toEqual(["a", "c", "b"]);
});

test("blankEntry has the fields CCv3 requires", () => {
  expect(blankEntry()).toEqual({
    keys: [],
    content: "",
    enabled: true,
    insertion_order: 100,
    position: "before_char",
    extensions: {},
  });
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/lorebook/entries.test.ts`
Expected: FAIL, `Failed to resolve import "./entries"`.

- [ ] **Step 3: Write the helpers**

Create `web/src/lorebook/entries.ts`:

```ts
export type Entry = Record<string, unknown>;

/** asEntries returns the entry objects in v, or none. */
export const asEntries = (v: unknown): Entry[] =>
  Array.isArray(v) ? v.filter((e): e is Entry => typeof e === "object" && e !== null && !Array.isArray(e)) : [];

/** strings returns the strings in v, or none. */
export const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((s): s is string => typeof s === "string") : []);

/** firstLine is an entry's first line of content, for its collapsed row. */
export function firstLine(e: Entry): string {
  const line = (typeof e.content === "string" ? e.content : "").split("\n")[0] ?? "";
  return line.length > 80 ? line.slice(0, 80) + "…" : line;
}

/** matches says whether an entry's keys, secondary keys, comment, or content contain q, ignoring case. */
export function matches(e: Entry, q: string): boolean {
  const needle = q.toLowerCase();
  const hay = [...strings(e.keys), ...strings(e.secondary_keys), e.comment, e.content];
  return hay.some((s) => typeof s === "string" && s.toLowerCase().includes(needle));
}

/** moved returns list with the item at from moved to index to. */
export function moved<T>(list: T[], from: number, to: number): T[] {
  const out = [...list];
  const [item] = out.splice(from, 1);
  if (item !== undefined) out.splice(to, 0, item);
  return out;
}

/** blankEntry is a new entry with the fields CCv3 requires. */
export const blankEntry = (): Entry => ({
  keys: [],
  content: "",
  enabled: true,
  insertion_order: 100,
  position: "before_char",
  extensions: {},
});
```

- [ ] **Step 4: Run it to see it pass**

Run: `cd web && npx vitest run src/lorebook/entries.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lorebook/entries.ts web/src/lorebook/entries.test.ts
git commit -m "feat(web): helpers for lorebook entries"
```

---

### Task 5: A chip input for keys

**Files:**
- Create: `web/src/lorebook/ChipInput.tsx`
- Test: `web/src/lorebook/ChipInput.test.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/lorebook/ChipInput.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ChipInput } from "./ChipInput";

function Harness({ start }: { start: string[] }) {
  const [values, setValues] = useState(start);
  return (
    <>
      <ChipInput label="Entry 1 keys" values={values} onChange={setValues} />
      <output>{values.join("|")}</output>
    </>
  );
}

test("Enter and comma add a chip, × removes one, and Backspace on empty removes the last", async () => {
  render(<Harness start={["anvil"]} />);
  const input = screen.getByLabelText("Add to Entry 1 keys");
  await userEvent.type(input, "forge{Enter}iron,");
  expect(screen.getByRole("status")).toHaveTextContent("anvil|forge|iron");
  await userEvent.type(input, "anvil{Enter}");
  expect(screen.getByRole("status")).toHaveTextContent("anvil|forge|iron");
  await userEvent.click(screen.getByRole("button", { name: "Remove forge from Entry 1 keys" }));
  expect(screen.getByRole("status")).toHaveTextContent("anvil|iron");
  await userEvent.type(input, "{Backspace}");
  expect(screen.getByRole("status")).toHaveTextContent("anvil");
});

test("leaving the input adds what was typed", async () => {
  render(<Harness start={[]} />);
  await userEvent.type(screen.getByLabelText("Add to Entry 1 keys"), "kraken");
  await userEvent.tab();
  expect(screen.getByRole("status")).toHaveTextContent("kraken");
});
```

- [ ] **Step 2: Run it to see it fail**

Run: `cd web && npx vitest run src/lorebook/ChipInput.test.tsx`
Expected: FAIL, `Failed to resolve import "./ChipInput"`.

- [ ] **Step 3: Write the input**

Create `web/src/lorebook/ChipInput.tsx`:

```tsx
import { useState } from "react";

/** ChipInput edits a list of strings as chips. Enter, comma, or leaving the input adds; × removes; Backspace on an empty input removes the last. */
export function ChipInput({ label, values, onChange }: { label: string; values: string[]; onChange: (v: string[]) => void }) {
  const [typed, setTyped] = useState("");
  const add = () => {
    const v = typed.trim();
    if (v && !values.includes(v)) onChange([...values, v]);
    setTyped("");
  };
  return (
    <div className="chip-input" role="group" aria-label={label}>
      {values.map((v, i) => (
        <span key={i} className="tag">
          {v}
          <button className="chip-x" aria-label={`Remove ${v} from ${label}`} onClick={() => onChange(values.filter((_, j) => j !== i))}>
            ×
          </button>
        </span>
      ))}
      <input
        className="input"
        aria-label={`Add to ${label}`}
        value={typed}
        onChange={(e) => setTyped(e.target.value)}
        onBlur={add}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          } else if (e.key === "Backspace" && !typed && values.length) {
            onChange(values.slice(0, -1));
          }
        }}
      />
    </div>
  );
}
```

Keys are positions, not values, because imported entries can hold a duplicate or empty key.

- [ ] **Step 4: Run it to see it pass**

Run: `cd web && npx vitest run src/lorebook/ChipInput.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/lorebook/ChipInput.tsx web/src/lorebook/ChipInput.test.tsx
git commit -m "feat(web): add a chip input"
```

---

### Task 6: The lorebook page

**Files:**
- Create: `web/src/lorebook/Entries.tsx`, `web/src/lorebook/Lorebook.tsx`, `web/src/lorebook/Lorebook.css`, `web/src/lorebook/Lorebook.test.tsx`
- Modify: `web/src/App.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/lorebook/Lorebook.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Lorebook } from "./Lorebook";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const anvil = { keys: ["anvil"], content: "Heavy iron.\nRings when struck.", enabled: true, insertion_order: 100, extensions: {} };
const bellows = { keys: ["bellows"], content: "Pumps air.", enabled: true, insertion_order: 100, extensions: { depth: 4 } };
const data = { name: "Forge", description: "Smithing lore.", entries: [anvil, bellows] };
const entity = {
  id: 3, slug: "forge", provenance: "api", created_at: now, cards: [7],
  revision: { id: 21, content_hash: "x", message: "", source: "ui", created_at: now, data },
};

let head: typeof entity;
let puts: { path: string; body: { base_revision_id: number; data: typeof data } }[];
beforeEach(() => {
  head = entity;
  puts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method === "PUT" && path === "/api/lorebooks/3") {
        const body = await req.json();
        puts.push({ path, body });
        head = { ...head, revision: { ...head.revision, id: head.revision.id + 1, data: body.data } };
        return json(head);
      }
      if (path === "/api/lorebooks/3") return json(head);
      if (path === "/api/lorebooks/3/revisions") return json([head.revision]);
      if (path === "/api/search") return json({ total: 1, hits: [{ id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" } }] });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/lorebooks/:id", element: <Toasts><Lorebook /></Toasts> },
      { path: "/lorebooks/:id/history", element: <p>History</p> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("rows show keys and the first line, and the filter narrows them", async () => {
  renderAt("/lorebooks/3");
  expect(await screen.findByText("anvil")).toBeInTheDocument();
  expect(screen.getByText("Heavy iron.")).toBeInTheDocument();
  expect(screen.getByText("bellows")).toBeInTheDocument();
  expect(await screen.findByRole("link", { name: "Brakka" })).toHaveAttribute("href", "/cards/7");
  await userEvent.type(screen.getByLabelText("Filter entries"), "air");
  expect(screen.queryByText("Heavy iron.")).not.toBeInTheDocument();
  expect(screen.getByText("Pumps air.")).toBeInTheDocument();
});

test("editing an entry marks it and saves every entry", async () => {
  renderAt("/lorebooks/3");
  const content = await screen.findByLabelText("Entry 1 content");
  await userEvent.type(content, " Hot.");
  expect(screen.getByText("unsaved")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.base_revision_id).toBe(21);
  expect(puts[0]?.body.data.entries).toEqual([{ ...anvil, content: "Heavy iron.\nRings when struck. Hot." }, bellows]);
  expect(puts[0]?.body.data.name).toBe("Forge");
});

test("keys, flags, depth, add, move, and delete", async () => {
  renderAt("/lorebooks/3");
  await userEvent.type(await screen.findByLabelText("Add to Entry 1 keys"), "forge{Enter}");
  await userEvent.click(screen.getByLabelText("Entry 2 constant"));
  await userEvent.clear(screen.getByLabelText("Entry 2 depth"));
  await userEvent.type(screen.getByLabelText("Entry 2 depth"), "2");
  await userEvent.click(screen.getByRole("button", { name: "Move entry 2 up" }));
  await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
  await userEvent.type(screen.getByLabelText("Entry 3 content"), "New lore.");
  await userEvent.click(screen.getByRole("button", { name: "Delete entry 2" }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.data.entries).toEqual([
    { ...bellows, constant: true, extensions: { depth: 2 } },
    { keys: [], content: "New lore.", enabled: true, insertion_order: 100, position: "before_char", extensions: {} },
  ]);
});

test("the panel links to history", async () => {
  renderAt("/lorebooks/3");
  const history = await screen.findByRole("region", { name: "History" });
  expect(within(history).getByRole("link", { name: "All history" })).toHaveAttribute("href", "/lorebooks/3/history");
});
```

The last test needs the panel's sections to be named regions. If `Revisions` renders a plain `<section>` with an `<h2>`, give it `aria-labelledby` in this task, or query the link directly.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/lorebook/Lorebook.test.tsx`
Expected: FAIL, `Failed to resolve import "./Lorebook"`.

- [ ] **Step 3: Write the entry editor**

Create `web/src/lorebook/Entries.tsx`:

```tsx
import { useState } from "react";
import { text, tokens, type Data } from "../card/draft";
import { ChipInput } from "./ChipInput";
import { asEntries, blankEntry, firstLine, matches, moved, strings, type Entry } from "./entries";

let lastUid = 0;
const uid = () => ++lastUid;
const num = (v: unknown) => (typeof v === "number" ? v : "");
const ext = (e: Entry) => (typeof e.extensions === "object" && e.extensions !== null ? (e.extensions as Record<string, unknown>) : {});

/** Entries edits a lorebook's entries: a filter, then one collapsible row per entry. */
export function Entries({ data, base, edit }: { data: Data; base: Data; edit: (key: string, value: unknown) => void }) {
  const list = asEntries(data.entries);
  const saved = new Set(asEntries(base.entries).map((e) => JSON.stringify(e)));
  // Client-side IDs follow each entry through moves, so keys and open rows stay with it.
  const [uids, setUids] = useState<number[]>(() => list.map(uid));
  // A reload or rebase can change the count; start the IDs over.
  if (uids.length !== list.length) setUids(list.map(uid));
  const [filter, setFilter] = useState("");
  const [opened, setOpened] = useState<number>();
  const [dragging, setDragging] = useState<number>();

  const set = (next: Entry[], nextUids: number[]) => {
    setUids(nextUids);
    edit("entries", next);
  };
  const update = (i: number, patch: Entry) => set(list.map((e, j) => (j === i ? { ...e, ...patch } : e)), uids);
  const move = (from: number, to: number) => set(moved(list, from, to), moved(uids, from, to));
  const remove = (i: number) => set(list.filter((_, j) => j !== i), uids.filter((_, j) => j !== i));
  const add = () => {
    const id = uid();
    setOpened(id);
    set([...list, blankEntry()], [...uids, id]);
  };

  return (
    <>
      <div className="row entries-toolbar">
        <input className="input" type="search" aria-label="Filter entries" placeholder="Filter entries" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button className="button" onClick={add}>
          Add entry
        </button>
      </div>
      {list.map((e, i) => {
        if (!matches(e, filter)) return null;
        const n = i + 1;
        const unsaved = !saved.has(JSON.stringify(e));
        return (
          <details
            key={uids[i]}
            className={unsaved ? "field entry dirty" : "field entry"}
            open={uids[i] === opened || undefined}
            draggable
            onDragStart={() => setDragging(i)}
            onDragOver={(ev) => ev.preventDefault()}
            onDrop={() => {
              if (dragging !== undefined && dragging !== i) move(dragging, i);
              setDragging(undefined);
            }}
          >
            <summary>
              <strong>{strings(e.keys).join(", ") || "(no keys)"}</strong>
              <span className="muted">{firstLine(e)}</span>
              {unsaved && <span className="unsaved">unsaved</span>}
              <span className="tokens">~{tokens(text(e.content))} tokens</span>
            </summary>
            <div className="entry-body">
              <ChipInput label={`Entry ${n} keys`} values={strings(e.keys)} onChange={(v) => update(i, { keys: v })} />
              <ChipInput label={`Entry ${n} secondary keys`} values={strings(e.secondary_keys)} onChange={(v) => update(i, { secondary_keys: v })} />
              <textarea className="input grow" aria-label={`Entry ${n} content`} value={text(e.content)} onChange={(ev) => update(i, { content: ev.target.value })} />
              <div className="row">
                {(["constant", "selective", "enabled"] as const).map((k) => (
                  <label key={k} className="check">
                    <input type="checkbox" aria-label={`Entry ${n} ${k}`} checked={e[k] === true} onChange={(ev) => update(i, { [k]: ev.target.checked })} /> {k}
                  </label>
                ))}
                <label>
                  Order{" "}
                  <input className="input num" type="number" aria-label={`Entry ${n} order`} value={num(e.insertion_order)}
                    onChange={(ev) => update(i, { insertion_order: Number(ev.target.value) })} />
                </label>
                <label>
                  Position{" "}
                  <select className="input" aria-label={`Entry ${n} position`} value={text(e.position) || "before_char"}
                    onChange={(ev) => update(i, { position: ev.target.value })}>
                    <option value="before_char">Before character</option>
                    <option value="after_char">After character</option>
                  </select>
                </label>
                <label>
                  Depth{" "}
                  <input className="input num" type="number" min={0} aria-label={`Entry ${n} depth`} value={num(ext(e).depth)}
                    onChange={(ev) => update(i, { extensions: { ...ext(e), depth: Number(ev.target.value) } })} />
                </label>
              </div>
              <div className="row">
                <button className="button" aria-label={`Move entry ${n} up`} disabled={i === 0} onClick={() => move(i, i - 1)}>
                  ↑
                </button>
                <button className="button" aria-label={`Move entry ${n} down`} disabled={i === list.length - 1} onClick={() => move(i, i + 1)}>
                  ↓
                </button>
                <button className="button danger" aria-label={`Delete entry ${n}`} onClick={() => remove(i)}>
                  Delete
                </button>
              </div>
            </div>
          </details>
        );
      })}
    </>
  );
}
```

In the third test, "Entry 2 depth" is cleared, which sets `depth: 0`, then typed as `2`. If the test sees `depth: 20` or similar, `userEvent.clear` on a number input behaves differently in jsdom. Assert what a person typing gets, and fix the test, not the component.

- [ ] **Step 4: Write the page**

Create `web/src/lorebook/Lorebook.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { Stale } from "../card/Conflict";
import { text } from "../card/draft";
import { Sources } from "../card/Organize";
import { Archive, Push, Revisions, Sync } from "../card/Panel";
import { useEditor } from "../card/useEditor";
import { Entries } from "./Entries";
import "../card/Card.css";
import "./Lorebook.css";

/** Lorebook is the lorebook page. It's keyed by ID so another lorebook starts with no draft. */
export function Lorebook() {
  const { id } = useParams();
  return <LorebookEditor key={id} id={Number(id)} />;
}

function LorebookEditor({ id }: { id: number }) {
  const ed = useEditor("lorebook", id);
  if (ed.entity.isPending) return null;
  if (ed.entity.isError) return <p className="error page">{ed.entity.error.message}</p>;
  const head = ed.entity.data;
  const dirty = ed.dirty.size > 0;
  return (
    <div className="card-page">
      <aside className="card-aside">
        <input className="input name" aria-label="Name" value={text(ed.data.name)} onChange={(e) => ed.edit("name", e.target.value)} />
        <textarea
          className="input grow"
          aria-label="Description"
          placeholder="Description"
          value={text(ed.data.description)}
          onChange={(e) => ed.edit("description", e.target.value)}
        />
        <div className="row">
          <button className="button primary" disabled={!dirty || ed.save.isPending || ed.stale} onClick={() => ed.draft && ed.save.mutate(ed.draft)}>
            Save
          </button>
          <Archive kind="lorebook" entity={head} />
        </div>
        <Push kind="lorebook" id={id} dirty={dirty} />
        <Sync kind="lorebook" id={id} />
        <LinkedCards ids={head.cards ?? []} />
        <Sources kind="lorebook" id={id} />
        <Revisions kind="lorebook" id={id} />
      </aside>
      <div className="card-main">
        <Stale kind="lorebook" ed={ed} />
        <Entries data={ed.data} base={ed.draft?.base ?? ed.headData} edit={ed.edit} />
      </div>
    </div>
  );
}

function LinkedCards({ ids }: { ids: number[] }) {
  const body = { filter: { ids }, fields: ["name"], page: { size: 500 } };
  const cards = useQuery({
    queryKey: ["search", "card", body],
    enabled: ids.length > 0,
    queryFn: async () => must(await api.POST("/search", { body })),
  });
  if (!ids.length) return null;
  const names = new Map((cards.data?.hits ?? []).map((h) => [h.id, text(h.fields.name)]));
  return (
    <section>
      <h2>Cards</h2>
      <ul className="plain">
        {ids.map((id) => (
          <li key={id}>
            <Link to={`/cards/${id}`}>{names.get(id) || `#${id}`}</Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
```

Create `web/src/lorebook/Lorebook.css`:

```css
.entries-toolbar {
  margin-bottom: var(--space);
}

.entries-toolbar .input[type="search"] {
  flex: 1;
}

.entry summary strong {
  white-space: nowrap;
}

.entry summary .muted {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.entry[draggable="true"] summary {
  cursor: grab;
}

.entry-body {
  display: grid;
  gap: var(--space);
  padding: 0 var(--space) var(--space);
}

.chip-input {
  display: flex;
  flex-wrap: wrap;
  gap: calc(var(--space) * 0.5);
  align-items: center;
}

.chip-input .input {
  flex: 1;
  min-width: 8em;
  width: auto;
}

.input.num {
  width: 6em;
}
```

- [ ] **Step 5: Add the routes**

In `web/src/App.tsx`, import `Lorebook` from `./lorebook/Lorebook`, and add after the `lorebooks` route:

```tsx
          { path: "lorebooks/:id", element: <Lorebook /> },
          { path: "lorebooks/:id/history", element: <History kind="lorebook" /> },
```

- [ ] **Step 6: Run the tests**

Run: `cd web && npm test && npm run typecheck && npm run build`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/lorebook web/src/card web/src/App.tsx
git commit -m "feat(web): edit a lorebook's entries"
```

---

### Task 7: Match the spec

**Files:**
- Modify: `docs/superpowers/specs/2026-10-05-web-ui-design.md`

- [ ] **Step 1: Update the lorebook page**

In "Lorebooks", under **Lorebook page**, replace the right-frame bullet with:

```markdown
- Right frame: a filter box and one collapsible row per entry. Collapsed, a
  row shows its keys and the first line of content. Expanded, it has keys and
  secondary keys as chip inputs, content, and constant, selective, enabled,
  order, position, and depth (`extensions.depth`). Entries can be added,
  deleted, and reordered by dragging or with Move up and Move down.
```

- [ ] **Step 2: Add the API row**

Add a row to the "API additions" table:

```markdown
| `cards` on `GET /api/lorebooks/{id}` | The lorebook page's linked cards |
```

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-10-05-web-ui-design.md
git commit -m "docs: match the web UI spec to the lorebook page"
```

---

### Task 8: Check it end to end

- [ ] **Step 1: Run every check CI runs**

```bash
go build ./... && go vet ./... && go test ./...
cd web && npm test && npm run build && npm run gen && git diff --exit-code openapi.json src/api/schema.d.ts
```

Expected: all pass, and regenerating the schema changes nothing.

- [ ] **Step 2: Try it in a browser**

Against a local `innkeeper api` and worker with a lorebook of a dozen entries, linked to a card:

1. Open the lorebook from the list. Rows show keys and first lines. Filter by a word in one entry's content.
2. Expand an entry, add a key with Enter, toggle constant, change depth. The row shows "unsaved". Save.
3. Drag an entry to the top, then use Move down. Save, reload, and check the order.
4. Add an entry; it opens expanded. Delete another. Save.
5. Open History, compare against the previous revision, and restore an older one.
6. Edit in a second tab and save, then save in the first. The banner appears; Compare and Keep work as on the card page.
7. Open the linked card from the panel, and its Lorebooks section links back.
