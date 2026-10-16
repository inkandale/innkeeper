# Forge phase 1: editor additions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the missing V3 fields, macro highlighting, and a `/cards/new` page whose draft survives a reload, as phase 1 of the Forge.

**Architecture:** Web only. `POST /cards` already takes `data` and an `avatar` blob SHA, so the API doesn't change. A new `MacroTextarea` replaces every text box on the card page. `NewCard` holds its data in `localStorage` and its avatar in memory, and the first Save uploads the avatar, creates the card, and moves to `/cards/:id`. Spec: `docs/superpowers/specs/2026-10-14-forge-design.md`, sections "New card" and "Editor additions".

**Tech Stack:** React, TypeScript, TanStack Query, React Router, Vitest, Testing Library.

---

## Before you start

Work in `~/git/innkeeper-forge-1` on branch `forge-1-editor`. Never run git in `~/git/innkeeper`. Don't checkout, switch, stash or reset; to watch a test fail, edit the code back by hand.

Commits are conventional (`feat(web):`, `test(web):`). Never `!` or a `BREAKING CHANGE` footer. No attribution lines.

Web checks: `cd web && npm run typecheck && npm test`. One file: `cd web && npx vitest run src/card/macro.test.ts`.

## Design notes the tasks rely on

- **Macros.** `macroParts(text, start)` splits text with a capturing regex, so macros sit at odd indexes. A macro is `{{` then no braces then `}}`, so in `{{random::{{user}}}}` only `{{user}}` is tinted. `<START>` is a macro only when `start` is true, which only Example dialogue sets. Nothing is an error.
- **Mirror.** `MacroTextarea` stacks a `div` with the same font, padding, border and wrapping behind a textarea with a transparent background. The `div`'s text is transparent; only its `<mark>` backgrounds show. A trailing `"\n"` keeps a final empty line the same height in both. On scroll, the mirror copies the textarea's `scrollTop`.
- **New-card draft.** One `localStorage` key, `innkeeper.newCard`, holds the draft's `data` as JSON. Every read and write is in `try`/`catch`; without storage the page still works, it just forgets on reload. The avatar is a `Blob` in memory: a cropped PNG can be several megabytes, past what `localStorage` holds, so it doesn't survive a reload.
- **First Save.** Upload the avatar with `upload()` (`POST /blobs`), then `POST /cards` with `{ data, avatar, source: "ui" }`. On success: remove the key, seed the `["card", id]` query, invalidate `["search"]` so the grid lists it, and `navigate("/cards/:id", { replace: true })` so Back doesn't return to an empty new-card page.
- **`creator_notes_multilingual`** is a CCv3 object from language code to notes. The UI keeps rows of `[code, notes]` in local state and writes `Object.fromEntries` of the rows that have a code. While two rows share a code, both codes are marked `aria-invalid` with a message, and nothing is written, so the rows never collapse into one key. Add language is disabled while a row has an empty code. Task 3's code below is the first version; the branch's later fix commit has the final one.
- **Left panel.** `Identity` renders Name, Nickname, Creator, Version and Tags. `Panel` and `NewCard` both use it.

## Files

| File | Change |
|---|---|
| `web/src/card/macro.ts`, `macro.test.ts` | `macroParts` and its tests |
| `web/src/card/MacroTextarea.tsx` | The highlighted textarea |
| `web/src/card/Card.css` | Mirror styles, row layout |
| `web/src/card/Fields.tsx` | `MacroTextarea` everywhere; `Greetings` for alternate and group-only; `Translations` |
| `web/src/card/Panel.tsx` | `Identity`, with nickname and version |
| `web/src/card/NewCard.tsx`, `NewCard.test.tsx` | The new-card page |
| `web/src/App.tsx` | `/cards/new` route |
| `web/src/cards/Cards.tsx`, `Cards.test.tsx` | New card button |
| `web/src/card/Card.test.tsx` | Tests for the new fields |

---

### Task 1: Macro tokenizing

**Files:**
- Create: `web/src/card/macro.ts`
- Test: `web/src/card/macro.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { macroParts } from "./macro";

test("macroParts puts each {{…}} token at an odd index", () => {
  expect(macroParts("Hi {{user}}, I'm {{char}}.")).toEqual(["Hi ", "{{user}}", ", I'm ", "{{char}}", "."]);
  expect(macroParts("{{random::a,b}}")).toEqual(["", "{{random::a,b}}", ""]);
});

test("macroParts leaves unclosed braces as text", () => {
  expect(macroParts("{{user and {char}}")).toEqual(["{{user and {char}}"]);
});

test("macroParts tints the innermost token of a nested macro", () => {
  expect(macroParts("{{pick::{{user}}}}")).toEqual(["{{pick::", "{{user}}", "}}"]);
});

test("macroParts marks <START> only in example dialogue", () => {
  expect(macroParts("<START>\n{{char}}: Hi")).toEqual(["<START>\n", "{{char}}", ": Hi"]);
  expect(macroParts("<START>\n{{char}}: Hi", true)).toEqual(["", "<START>", "\n", "{{char}}", ": Hi"]);
});
```

- [ ] **Step 2: Run it and watch it fail**

Run: `cd web && npx vitest run src/card/macro.test.ts`
Expected: FAIL, `./macro` doesn't exist.

- [ ] **Step 3: Implement**

```ts
const macro = /(\{\{[^{}]*\}\})/;
const macroOrStart = /(\{\{[^{}]*\}\}|<START>)/;

/** macroParts splits text into plain runs and macros; macros sit at odd indexes. start counts <START> as a macro, for example dialogue. */
export const macroParts = (s: string, start = false) => s.split(start ? macroOrStart : macro);
```

- [ ] **Step 4: Run it and watch it pass**, then commit `feat(web): split card text into macros and plain runs`.

### Task 2: `MacroTextarea` in every card text field

**Files:**
- Create: `web/src/card/MacroTextarea.tsx`
- Modify: `web/src/card/Fields.tsx`, `web/src/card/Card.css`
- Test: `web/src/card/Card.test.tsx`

- [ ] **Step 1: Write the failing test** in `Card.test.tsx`:

```tsx
test("macros in a field get a highlight behind the text", async () => {
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), " {{{{char}}");
  const mark = document.querySelector(".macro-mirror mark");
  expect(mark).toHaveTextContent("{{char}}");
});
```

`userEvent.type` treats `{` as a key descriptor; `{{` types one `{`.

- [ ] **Step 2: Run it and watch it fail** (no `.macro-mirror`).

- [ ] **Step 3: Implement `MacroTextarea`**

```tsx
import { useRef } from "react";
import { macroParts } from "./macro";

/** MacroTextarea is a textarea whose macros get a tinted background, drawn by a mirror layer behind its transparent box. */
export function MacroTextarea(props: { label: string; value: string; onChange: (v: string) => void; start?: boolean }) {
  const mirror = useRef<HTMLDivElement>(null);
  return (
    <div className="macro">
      <div ref={mirror} className="input macro-mirror" aria-hidden="true">
        {macroParts(props.value, props.start).map((p, i) => (i % 2 ? <mark key={i}>{p}</mark> : p))}
        {/* A trailing empty line needs a character to take up height. */}
        {"\n"}
      </div>
      <textarea
        className="input grow"
        aria-label={props.label}
        value={props.value}
        onChange={(e) => props.onChange(e.target.value)}
        onScroll={(e) => mirror.current && (mirror.current.scrollTop = e.currentTarget.scrollTop)}
      />
    </div>
  );
}
```

In `Fields.tsx`, replace both `<textarea>`s with `MacroTextarea`; `mes_example` passes `start`.

CSS in `Card.css`:

```css
.macro {
  position: relative;
  background: var(--bg);
  border-radius: calc(var(--radius) * 0.75);
}

.macro-mirror {
  position: absolute;
  inset: 0;
  overflow: hidden;
  color: transparent;
  white-space: pre-wrap;
  overflow-wrap: break-word;
  pointer-events: none;
}

.macro-mirror mark {
  color: transparent;
  background: color-mix(in srgb, var(--accent) 30%, transparent);
  border-radius: 3px;
}

.macro > textarea {
  position: relative;
  display: block;
  background: transparent;
}
```

`.field > .macro` gets the same width and margin as `.field > .grow`, which lorebook entries still use, and `.greeting > .macro { flex: 1; }`.

- [ ] **Step 4: Run `npx vitest run src/card`** and watch everything pass, then commit `feat(web): highlight macros in card text fields`.

### Task 3: Group-only greetings and notes in other languages

**Files:**
- Modify: `web/src/card/Fields.tsx`, `web/src/card/Card.css`
- Test: `web/src/card/Card.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
test("group-only greetings save as their own list", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "Add group greeting" }));
  await userEvent.type(screen.getByLabelText("Group greeting 1"), "Hello, all.");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ body: { data: { alternate_greetings: ["Hail."], group_only_greetings: ["Hello, all."] } } });
});

test("creator notes in other languages save as code and notes pairs", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "Add language" }));
  expect(screen.getByRole("button", { name: "Add language" })).toBeDisabled();
  await userEvent.type(screen.getByLabelText("Language 1"), "de");
  await userEvent.type(screen.getByLabelText("Notes 1"), "Ein Zwerg.");
  await userEvent.click(screen.getByRole("button", { name: "Add language" }));
  // A code another row has would merge the two rows, so it's refused.
  await userEvent.type(screen.getByLabelText("Language 2"), "de");
  expect(screen.getByLabelText("Language 2")).toHaveValue("d");
  await userEvent.click(screen.getByRole("button", { name: "Remove language 2" }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ body: { data: { creator_notes_multilingual: { de: "Ein Zwerg." } } } });
});
```

- [ ] **Step 2: Run them and watch them fail.**

- [ ] **Step 3: Implement.** Extract the alternate greetings section into `Greetings`, parameterized by field key, section label and item noun ("greeting", "group greeting"), and render it twice. Add `Translations` after Creator notes:

```tsx
function Translations({ value, dirty, edit }: { value: unknown; dirty: boolean; edit: (key: string, value: unknown) => void }) {
  const rows = Object.entries(value && typeof value === "object" ? value : {}).map(([c, n]): [string, string] => [c, text(n)]);
  const set = (next: [string, string][]) => edit("creator_notes_multilingual", Object.fromEntries(next));
  const put = (i: number, row: [string, string]) => set(rows.map((r, j) => (j === i ? row : r)));
  return (
    <Section label="Creator notes in other languages" dirty={dirty} count={tokens(rows.map(([, n]) => n).join(""))}>
      {rows.map(([code, notes], i) => (
        <div key={i} className="greeting">
          <input
            className="input lang"
            aria-label={`Language ${i + 1}`}
            placeholder="Code"
            value={code}
            // Two rows with one code would merge into one key, losing a row's notes.
            onChange={(e) => rows.every(([c], j) => j === i || c !== e.target.value) && put(i, [e.target.value, notes])}
          />
          <MacroTextarea label={`Notes ${i + 1}`} value={notes} onChange={(v) => put(i, [code, v])} />
          <button className="button" aria-label={`Remove language ${i + 1}`} onClick={() => set(rows.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button className="button" disabled={rows.some(([c]) => c === "")} onClick={() => set([...rows, ["", ""]])}>
        Add language
      </button>
    </Section>
  );
}
```

`.lang { width: 6em; flex: none; }` in `Card.css`.

- [ ] **Step 4: Run `npx vitest run src/card`**, then commit `feat(web): edit group-only greetings and creator notes in other languages`.

### Task 4: Nickname and version in the left panel

**Files:**
- Modify: `web/src/card/Panel.tsx`
- Test: `web/src/card/Card.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("nickname and version save with the card", async () => {
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Nickname"), "Brak");
  await userEvent.type(screen.getByLabelText("Version"), "2");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ body: { data: { nickname: "Brak", character_version: "2" } } });
});
```

- [ ] **Step 2: Run it and watch it fail.**

- [ ] **Step 3: Implement** `Identity` in `Panel.tsx` and use it in `Panel` in place of the Name, Creator and Tags inputs:

```tsx
/** Identity edits the card's name, nickname, creator, version, and tags. */
export function Identity({ data, edit }: { data: Data; edit: (key: string, value: unknown) => void }) {
  const field = (key: string, label: string) => (
    <input className="input" aria-label={label} placeholder={label} value={text(data[key])} onChange={(e) => edit(key, e.target.value)} />
  );
  return (
    <>
      <input className="input name" aria-label="Name" placeholder="Name" value={text(data.name)} onChange={(e) => edit("name", e.target.value)} />
      {field("nickname", "Nickname")}
      {field("creator", "Creator")}
      {field("character_version", "Version")}
      <ChipInput label="Tags" values={strings(data.tags)} onChange={(v) => edit("tags", v)} />
    </>
  );
}
```

- [ ] **Step 4: Run `npx vitest run src/card`**, then commit `feat(web): edit a card's nickname and version`.

### Task 5: The new-card page

**Files:**
- Create: `web/src/card/NewCard.tsx`
- Modify: `web/src/App.tsx`
- Test: `web/src/card/NewCard.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { NewCard } from "./NewCard";

const now = new Date().toISOString();
let posts: { path: string; body: unknown }[];
beforeEach(() => {
  localStorage.clear();
  posts = [];
  // jsdom has no object URLs.
  URL.createObjectURL = () => "blob:avatar";
  URL.revokeObjectURL = () => {};
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/blobs") {
        posts.push({ path, body: await req.text() });
        return json({ sha256: "cd".repeat(32), mime: "image/png", size: 3, store: "local", created_at: now }, 201);
      }
      if (path === "/api/cards" && req.method === "POST") {
        const body = await req.json();
        posts.push({ path, body });
        return json({ id: 9, slug: "brakka", provenance: "api", created_at: now, revision: { id: 1, content_hash: "x", message: "", source: "ui", created_at: now, data: body.data } }, 201);
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderNew() {
  const router = createMemoryRouter(
    [
      { path: "/cards/new", element: <Toasts><NewCard /></Toasts> },
      { path: "/cards/:id", element: <p>Card page</p> },
    ],
    { initialEntries: ["/cards/new"] },
  );
  render(<QueryClientProvider client={new QueryClient()}><RouterProvider router={router} /></QueryClientProvider>);
  return router;
}

test("a new card's draft survives a reload", async () => {
  renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.type(screen.getByLabelText("Description"), "A grumpy dwarf.");
  cleanup();
  renderNew();
  expect(screen.getByLabelText("Name")).toHaveValue("Brakka");
  expect(screen.getByLabelText("Description")).toHaveValue("A grumpy dwarf.");
});

test("the first Save uploads the avatar, creates the card, clears the draft, and opens it", async () => {
  const router = renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.upload(screen.getByLabelText("Choose image"), new File(["png"], "a.png", { type: "image/png" }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/cards/9"));
  expect(posts).toEqual([
    { path: "/api/blobs", body: "png" },
    { path: "/api/cards", body: { data: { name: "Brakka" }, avatar: "cd".repeat(32), source: "ui" } },
  ]);
  expect(localStorage.getItem("innkeeper.newCard")).toBeNull();
});

test("a crop replaces the new card's image before Save", async () => {
  // jsdom can't draw, so the canvas hands back a stand-in PNG.
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((done) => done(new Blob(["cropped"])));
  renderNew();
  await userEvent.upload(screen.getByLabelText("Choose image"), new File(["png"], "a.png", { type: "image/png" }));
  await userEvent.click(screen.getByRole("button", { name: "Crop" }));
  const dialog = document.querySelector<HTMLElement>('dialog[aria-label="Crop image"]')!;
  fireEvent.load(dialog.querySelector("img")!);
  await userEvent.click(within(dialog).getByRole("button", { name: "Save crop", hidden: true }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(posts[0]).toEqual({ path: "/api/blobs", body: "cropped" }));
});

test("the page works when storage is blocked", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("blocked"); });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("blocked"); });
  vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => { throw new Error("blocked"); });
  const router = renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/cards/9"));
});
```

- [ ] **Step 2: Run them and watch them fail.**

- [ ] **Step 3: Implement `NewCard.tsx`**

```tsx
const draftKey = "innkeeper.newCard";

// Storage can throw or be empty in private windows; the page then just forgets on reload.
function readDraft(): Data {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(draftKey) ?? "{}");
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Data) : {};
  } catch {
    return {};
  }
}

function writeDraft(d: Data | null) {
  try {
    if (d) localStorage.setItem(draftKey, JSON.stringify(d));
    else localStorage.removeItem(draftKey);
  } catch {
    // The draft still lives in this tab.
  }
}

/** NewCard is the card page for a card not yet saved. Its first Save creates the card. */
export function NewCard() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [data, setData] = useState(readDraft);
  const [avatar, setAvatar] = useState<Blob | null>(null);
  useEffect(() => writeDraft(data), [data]);
  const edit = (key: string, value: unknown) => setData((d) => ({ ...d, [key]: value }));
  const dirty = changed({ baseId: 0, base: {}, data });
  const save = useMutation({
    mutationFn: async () => {
      const sha = avatar ? await upload(avatar) : undefined;
      return must(await api.POST("/cards", { body: { data, avatar: sha, source: "ui" } }));
    },
    onSuccess: (card) => {
      writeDraft(null);
      qc.setQueryData(["card", card.id], card);
      void qc.invalidateQueries({ queryKey: ["search"] });
      navigate(`/cards/${card.id}`, { replace: true });
    },
  });
  return (
    <div className="card-page">
      <aside className="card-aside">
        <Avatar image={avatar} busy={save.isPending} onChange={setAvatar} />
        <Identity data={data} edit={edit} />
        <div className="row">
          <button className="button primary" disabled={(!dirty.size && !avatar) || save.isPending} onClick={() => save.mutate()}>
            Save
          </button>
        </div>
      </aside>
      <div className="card-main">
        <Fields data={data} dirty={dirty} edit={edit} />
      </div>
    </div>
  );
}
```

`Avatar` shows the image through an object URL (made in an effect and revoked on change), a "Choose image" file button, and a Crop button that opens the existing `Crop` dialog on that URL and replaces the image with its PNG.

Add `{ path: "cards/new", element: <NewCard /> }` to `App.tsx` before `cards/:id`.

- [ ] **Step 4: Run `npx vitest run src/card`**, then commit `feat(web): create a card from a new-card page`.

### Task 6: New card button

**Files:**
- Modify: `web/src/cards/Cards.tsx`
- Test: `web/src/cards/Cards.test.tsx`

- [ ] **Step 1: Write the failing test**

```tsx
test("New card links to the new-card page", async () => {
  renderAt("/cards");
  expect(await screen.findByRole("link", { name: "New card" })).toHaveAttribute("href", "/cards/new");
});
```

- [ ] **Step 2: Run it and watch it fail.**

- [ ] **Step 3: Implement.** In the toolbar, before the import button, for cards only:

```tsx
{kind === "card" && (
  <Link className="button" to="/cards/new">
    New card
  </Link>
)}
```

- [ ] **Step 4: Run it**, then commit `feat(web): add a New card button to the Cards page`.

### Task 7: Checks

- [ ] Run what CI runs, with the test database set:

```bash
export INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres
go vet ./... && go test -race ./...
cd web && npm run typecheck && npm test
```

- [ ] Read `git diff origin/main...HEAD` for dead code, stale names, and changes outside the task.
