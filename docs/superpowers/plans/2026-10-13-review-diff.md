# Readable Review Diff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the sync review page show which words changed in each hunk, in one column for prose and two columns for everything else, with unchanged runs labelled and folded.

**Architecture:** A word-level diff runs in the browser on each hunk's displayed text (`wordDiff` in `web/src/sync/sync.ts`). `Review.tsx` renders the result as `<del>` / `<ins>`, which `web/src/card/Card.css` already tints under `.hunks`. Layout follows the field's `kind`. No server change. Spec: `docs/superpowers/specs/2026-10-13-review-diff-design.md`.

**Tech Stack:** React 19, TypeScript (strict, `noUncheckedIndexedAccess`), Vitest + Testing Library. Run all commands from `web/`.

---

## File map

- `web/src/sync/sync.ts`: add `Run`, `wordDiff`, `sentenceCount`, `jsonKey`. Pure functions, no React.
- `web/src/sync/sync.test.ts`: unit tests for those three.
- `web/src/sync/Review.tsx`: replace `HunkChoice` and `FieldChoices` (currently lines 33–102) with the new rendering.
- `web/src/sync/Review.test.tsx`: make the card comparison overridable per test; add three rendering tests.
- `web/src/sync/Sync.css`: one-column grid, decision border, unchanged folds, key labels.

---

### Task 1: Word diff and helpers

**Files:**
- Modify: `web/src/sync/sync.ts` (append after `decidable`, line 74)
- Test: `web/src/sync/sync.test.ts`

- [ ] **Step 1: Write the failing tests**

In `web/src/sync/sync.test.ts`, extend the first import line to include `jsonKey, sentenceCount, wordDiff`:

```ts
import { bulkResolutions, jsonKey, optionLabel, pushSummary, resolution, reviewPath, rows, sentenceCount, snapshotOf, stateCounts, wordDiff, type Comparison, type Snapshot } from "./sync";
```

Append:

```ts
test("wordDiff marks the words each side changed and keeps the rest", () => {
  expect(wordDiff("Bradley shouts at Tiff.", "Bradley yells at Tiff and Dex.")).toEqual([
    { op: "same", text: "Bradley " },
    { op: "del", text: "shouts " },
    { op: "ins", text: "yells " },
    { op: "same", text: "at " },
    { op: "del", text: "Tiff." },
    { op: "ins", text: "Tiff and Dex." },
  ]);
  expect(wordDiff("", "New text")).toEqual([{ op: "ins", text: "New text" }]);
  expect(wordDiff("Gone now", "")).toEqual([{ op: "del", text: "Gone now" }]);
  expect(wordDiff("", "")).toEqual([]);
});

test("wordDiff works on characters, not bytes, so curly quotes and dashes stay whole", () => {
  expect(wordDiff("“Hi,” she said—quietly.", "“Hi,” he said—quietly.")).toEqual([
    { op: "same", text: "“Hi,” " },
    { op: "del", text: "she " },
    { op: "ins", text: "he " },
    { op: "same", text: "said—quietly." },
  ]);
});

test("sentenceCount splits where the server's sentences() does", () => {
  expect(sentenceCount("One. Two. Three. ")).toBe(3);
  expect(sentenceCount("“Stop!” she said. Then left.")).toBe(3);
  expect(sentenceCount("Line one\nline two")).toBe(2);
  expect(sentenceCount(" Four. Five.")).toBe(2);
  expect(sentenceCount("")).toBe(0);
});

test("jsonKey names a hunk by its path inside the field", () => {
  expect(jsonKey("extensions", "extensions.depth_prompt.depth#0")).toBe("depth_prompt.depth");
  expect(jsonKey("extensions", "extensions#0")).toBe("");
});
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `npx vitest run src/sync/sync.test.ts`
Expected: FAIL. The import of `wordDiff` resolves to `undefined`, so the new tests fail with "is not a function".

- [ ] **Step 3: Implement**

In `web/src/sync/sync.ts`, after the `decidable` line (line 74), add:

```ts
export type Run = { op: "same" | "del" | "ins"; text: string };

// Each word keeps its trailing whitespace, as the server's words() does, so runs rejoin to the input.
const words = (s: string) => s.match(/\s*\S+\s*/g) ?? (s ? [s] : []);

/** wordDiff lines up two texts word by word: same runs, then what only the library (del) or the app (ins) has. */
export function wordDiff(library: string, app: string): Run[] {
  const a = words(library);
  const b = words(app);
  const w = b.length + 1;
  // ponytail: O(n·m) LCS table; fine for one hunk, switch to Myers if hunks grow to whole fields.
  const lcs = new Int32Array((a.length + 1) * w);
  const at = (i: number, j: number) => lcs[i * w + j] ?? 0;
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--)
      lcs[i * w + j] = a[i] === b[j] ? at(i + 1, j + 1) + 1 : Math.max(at(i + 1, j), at(i, j + 1));
  const out: Run[] = [];
  const push = (op: Run["op"], text: string) => {
    const last = out.at(-1);
    if (last?.op === op) last.text += text;
    else out.push({ op, text });
  };
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      push("same", a[i++] ?? "");
      j++;
    } else if (j >= b.length || (i < a.length && at(i + 1, j) >= at(i, j + 1))) push("del", a[i++] ?? "");
    else push("ins", b[j++] ?? "");
  }
  return out;
}

/** sentenceCount counts sentences the way the server's sentences() splits them: after a terminator or a line break. */
export const sentenceCount = (s: string) =>
  s.split(/(?<=[.!?…]["'”’*)]*\s+|\n\s*)(?=\S)/).filter((p) => p.trim()).length;

/** jsonKey is a JSON field hunk's key path, without the field's own path or the hunk number. */
export const jsonKey = (path: string, id: string) => id.replace(/#\d+$/, "").slice(path.length + 1);
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `npx vitest run src/sync/sync.test.ts && npm run typecheck`
Expected: all tests PASS; typecheck exits 0.

- [ ] **Step 5: Commit**

```bash
git add src/sync/sync.ts src/sync/sync.test.ts
git commit -m "feat(web): diff a review hunk word by word"
```

---

### Task 2: Render hunks as a word diff, laid out by field kind

**Files:**
- Modify: `web/src/sync/Review.tsx:1-102` (imports, `sideText`, `HunkChoice`, `FieldChoices`)
- Modify: `web/src/sync/Sync.css`
- Test: `web/src/sync/Review.test.tsx`

- [ ] **Step 1: Make the card comparison overridable in tests**

In `web/src/sync/Review.test.tsx`, beside the other `let` declarations (after `let progress: Snapshot;`, line 53), add:

```ts
let cardBody: typeof cardCmp;
```

In `beforeEach`, after `progress = snap;`, add:

```ts
  cardBody = cardCmp;
```

In the `/api/instances/3/items/a/compare` handler, change

```ts
        return json({ ...cardCmp, card_id: Number(u.searchParams.get("card_id")) });
```

to

```ts
        return json({ ...cardBody, card_id: Number(u.searchParams.get("card_id")) });
```

- [ ] **Step 2: Write the failing tests**

Append to `web/src/sync/Review.test.tsx`:

```tsx
test("a prose change reads in one column, with removed words struck and added ones marked", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{ path: "first_mes", kind: "prose", hunks: [{ id: "first_mes#0", status: "conflict", library: "The storm is “crazy” here.", app: "The storm is “wild” here." }] }],
  };
  renderReview();
  const block = (await screen.findByRole("radiogroup", { name: "Keep for first_mes#0" })).parentElement!;
  expect(within(block).getByText("“crazy”", { selector: "del" })).toBeInTheDocument();
  expect(within(block).getByText("“wild”", { selector: "ins" })).toBeInTheDocument();
  // One column: the shared words appear once, not once per side.
  expect(within(block).getAllByText(/The storm is/)).toHaveLength(1);
});

test("a JSON change names its key and diffs its words side by side", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{ path: "extensions", kind: "json", hunks: [{ id: "extensions.talkativeness#0", status: "conflict", library: "0.5", app: "0.7" }] }],
  };
  renderReview();
  const block = (await screen.findByRole("radiogroup", { name: "Keep for extensions.talkativeness#0" })).parentElement!;
  expect(within(block).getByText("talkativeness")).toBeInTheDocument();
  expect(within(block).getByText("0.5", { selector: "del" })).toBeInTheDocument();
  expect(within(block).getByText("0.7", { selector: "ins" })).toBeInTheDocument();
});
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `npx vitest run src/sync/Review.test.tsx`
Expected: the two new tests FAIL (no `del` / `ins` elements, no `talkativeness` label). The existing tests still PASS.

- [ ] **Step 4: Implement the rendering**

In `web/src/sync/Review.tsx`:

Change the React import (line 2) to:

```ts
import { useState, type ReactNode } from "react";
```

Add `jsonKey`, `sentenceCount`, `wordDiff` and `type Run` to the `./sync` import list, keeping it alphabetical:

```ts
import {
  changePath,
  changesFilter,
  changesPath,
  compareItem,
  decidable,
  jsonKey,
  optionKey,
  optionLabel,
  queueReviews,
  resolution,
  resolveItem,
  rows,
  sentenceCount,
  snapshotOf,
  wordDiff,
  type Comparison,
  type Item,
  type ItemKind,
  type Option,
  type Run,
} from "./sync";
```

Replace everything from `const sideText = …` through the end of `FieldChoices` (lines 33–102) with:

```tsx
const absent = <em className="muted">absent</em>;

/** Words renders a word diff: what only the library has struck through, what only the app has marked. */
function Words({ runs }: { runs: Run[] }) {
  return <>{runs.map((r, i) => (r.op === "del" ? <del key={i}>{r.text}</del> : r.op === "ins" ? <ins key={i}>{r.text}</ins> : r.text))}</>;
}

/** HunkText shows one changed hunk, in one column or as a Library | App pair. */
function HunkText({ hunk, inline }: { hunk: Hunk; inline: boolean }) {
  const runs = wordDiff(show(hunk.library), show(hunk.app));
  if (inline)
    return (
      <p className="hunks">
        <Words runs={runs} />
      </p>
    );
  return (
    <>
      <p className="hunks">{hunk.library === undefined ? absent : <Words runs={runs.filter((r) => r.op !== "ins")} />}</p>
      <p className="hunks">{hunk.app === undefined ? absent : <Words runs={runs.filter((r) => r.op !== "del")} />}</p>
    </>
  );
}

function HunkChoice(props: { scope: string; hunk: Hunk; label: string; inline: boolean; list: boolean; choice: string; onChoose?: (c: string) => void }) {
  const { scope, hunk, label, inline, list, choice, onChoose } = props;
  const sides = list ? ["library", "app", "both"] : ["library", "app"];
  return (
    <div className={inline ? "hunk-pair inline decide" : "hunk-pair decide"}>
      {label && <span className="muted">{label}</span>}
      <HunkText hunk={hunk} inline={inline} />
      {onChoose && (
        <span className="row" role="radiogroup" aria-label={`Keep for ${hunk.id}`}>
          {sides.map((s) => (
            <label key={s} className="check">
              <input type="radio" name={`${scope}/${hunk.id}`} checked={choice === s} onChange={() => onChoose(s)} />{" "}
              {s === "library" ? "Library" : s === "app" ? "App" : "Both"}
            </label>
          ))}
        </span>
      )}
    </div>
  );
}

/** Unchanged shows a run both sides share, folding prose longer than two sentences. */
function Unchanged({ text, prose }: { text: string; prose: boolean }) {
  const n = prose ? sentenceCount(text) : 0;
  if (n <= 2) return <p className="hunks same">{text}</p>;
  return (
    <details className="unchanged">
      <summary className="muted">⋯ {n} unchanged sentences</summary>
      <p className="hunks same">{text}</p>
    </details>
  );
}

function FieldChoices({ scope, field, choices, onChoose }: { scope: string; field: Field; choices: Choices; onChoose?: (id: string, c: string) => void }) {
  const hunks = field.hunks ?? [];
  const differs = hunks.some((h) => h.status !== "same");
  // Prose reads best as one column; values and structure read best side by side.
  const inline = (h: Hunk) => field.kind === "prose" || (field.kind === "entries" && entryOf(h.id).field === "content");
  const paired = hunks.some((h) => h.status !== "same" && !inline(h));
  const label = (h: Hunk) => {
    if (field.kind === "json") return jsonKey(field.path, h.id);
    if (field.kind !== "entries") return "";
    const e = entryOf(h.id);
    return e.field ? `${e.entry} · ${e.field}` : e.entry;
  };
  const out: ReactNode[] = [];
  let folded: Hunk[] = [];
  // A JSON field's unchanged keys fold into one line, each keeping its label.
  const fold = () => {
    if (folded.length)
      out.push(
        <details key={`same:${folded[0]?.id}`} className="unchanged">
          <summary className="muted">
            {folded.length} unchanged {folded.length === 1 ? "key" : "keys"}
          </summary>
          {folded.map((h) => (
            <p key={h.id} className="hunks same">
              <span className="hunk-key">{label(h)}</span> {show(h.library)}
            </p>
          ))}
        </details>,
      );
    folded = [];
  };
  for (const h of hunks) {
    if (h.status === "same" && field.kind === "json") {
      folded.push(h);
      continue;
    }
    fold();
    if (h.status === "same") out.push(<Unchanged key={h.id} text={show(h.library)} prose={inline(h)} />);
    else if (decidable(h))
      out.push(
        <HunkChoice
          key={h.id}
          scope={scope}
          hunk={h}
          label={label(h)}
          inline={inline(h)}
          list={field.kind === "list"}
          choice={choices[h.id] ?? "library"}
          onChoose={onChoose && ((c) => onChoose(h.id, c))}
        />,
      );
    else
      out.push(
        <div key={h.id} className={inline(h) ? "hunk-pair inline" : "hunk-pair"}>
          <span className="muted">
            {label(h) && `${label(h)} · `}
            {h.status === "auto_app" ? "App's change, merged" : "Library's change, kept"}
          </span>
          <HunkText hunk={h} inline={inline(h)} />
        </div>,
      );
  }
  fold();
  return (
    <details className="field" open={differs}>
      <summary>
        {field.path}
        {!differs && <span className="muted"> · same</span>}
      </summary>
      {paired && (
        <div className="hunk-pair hunk-head">
          <h4>Library</h4>
          <h4>App</h4>
        </div>
      )}
      {out}
    </details>
  );
}
```

- [ ] **Step 5: Add the layout styles**

In `web/src/sync/Sync.css`, after the `.hunk-pair h4` rule, add:

```css
.hunk-pair.inline {
  grid-template-columns: 1fr;
}

.hunk-pair > p {
  margin: 0;
}

.hunk-pair.decide {
  border-left: 3px solid var(--drift);
  padding-left: var(--space);
}

.hunk-head {
  border-top: none;
  padding-bottom: 0;
}

.unchanged > summary {
  cursor: pointer;
}

.hunk-key {
  font-family: ui-monospace, monospace;
  color: var(--text-muted);
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `npx vitest run src/sync && npm run typecheck`
Expected: all tests PASS, including the existing ones (`findByText("short.")` now matches the `<ins>`); typecheck exits 0.

- [ ] **Step 7: Commit**

```bash
git add src/sync/Review.tsx src/sync/Review.test.tsx src/sync/Sync.css
git commit -m "feat(web): review marks the changed words, prose in one column"
```

---

### Task 3: Fold unchanged runs

The folding code shipped in Task 2. This task pins it with tests.

**Files:**
- Test: `web/src/sync/Review.test.tsx`

- [ ] **Step 1: Write the tests**

Append to `web/src/sync/Review.test.tsx`:

```tsx
test("a JSON field folds its unchanged keys, each still labelled", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{
      path: "extensions", kind: "json",
      hunks: [
        { id: "extensions.depth_prompt.depth#0", status: "same", library: 4 },
        { id: "extensions.depth_prompt.role#0", status: "same", library: "system" },
        { id: "extensions.talkativeness#0", status: "conflict", library: "0.5", app: "0.7" },
      ],
    }],
  } as typeof cardCmp;
  renderReview();
  await screen.findByRole("radiogroup", { name: "Keep for extensions.talkativeness#0" });
  const folded = screen.getByText("2 unchanged keys").closest("details")!;
  expect(folded).not.toHaveAttribute("open");
  expect(within(folded).getByText("depth_prompt.depth")).toBeInTheDocument();
  expect(within(folded).getByText("depth_prompt.role")).toBeInTheDocument();
});

test("prose folds an unchanged run longer than two sentences and keeps a shorter one", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{
      path: "first_mes", kind: "prose",
      hunks: [
        { id: "first_mes#0", status: "same", library: "One. Two. Three. " },
        { id: "first_mes#1", status: "conflict", library: "Old.", app: "New." },
        { id: "first_mes#2", status: "same", library: " Four. Five." },
      ],
    }],
  };
  renderReview();
  await screen.findByRole("radiogroup", { name: "Keep for first_mes#1" });
  const folded = screen.getByText("⋯ 3 unchanged sentences").closest("details")!;
  expect(within(folded).getByText("One. Two. Three.")).toBeInTheDocument();
  expect(screen.getByText("Four. Five.").closest("details.unchanged")).toBeNull();
});
```

The JSON fixture needs `as typeof cardCmp` because `library: 4` is a number where `cardCmp`'s hunks hold strings.

- [ ] **Step 2: Run the tests**

Run: `npx vitest run src/sync/Review.test.tsx && npm run typecheck`
Expected: PASS. If either fails, the fold logic from Task 2 Step 4 is wrong: fix it there, not in the test.

- [ ] **Step 3: Commit**

```bash
git add src/sync/Review.test.tsx
git commit -m "test(web): review folds unchanged prose and JSON keys"
```

---

### Task 4: Full check and look at it

- [ ] **Step 1: Run the whole web suite and build**

Run: `npm test && npm run build`
Expected: all tests PASS; `vite build` writes `dist/`.

- [ ] **Step 2: Look at a real review**

Use the `run` skill to start Innkeeper with the new build, then open a review with a prose conflict and a JSON change: any card on `/sync/changes` in the "differs" state. Check:
- `first_mes` reads as one column, with struck red and green words.
- `extensions` shows key labels, side-by-side highlights, and an "N unchanged keys" fold.
- The Library / App choices still save: resolve one item and confirm the toast.

- [ ] **Step 3: Run the repo check**

From the repo root: `make check` (needs `INNKEEPER_PLUGIN_DATABASE_URL`; see the test-database note in memory).
Expected: exits 0.
