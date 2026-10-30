# Card page layout implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Slim the card page's side panel to what you edit or act on, and move read-only data and related cards into the main column.

**Architecture:** Core takes over the main column's tab bar (`PageTabs`). The Forge stops rendering it and returns its tabs and button from a hook, `useForgeView`. A new Metadata tab holds core facts and enrichment values. The side panel groups its sections in `<details>`. Related moves under the tabs, capped at 8 cards, with gallery links built on two new search filters, `creator` and `lorebook`.

**Tech stack:** Go (huma, pgx, PostgreSQL), React 19, TanStack Query, React Router, Vitest, Testing Library.

**Spec:** `docs/superpowers/specs/2026-10-30-card-page-layout-design.md`

**Worktree:** `/Users/dgibbons/git/innkeeper-card-page-layout`, branch `card-page-layout`. Run every command from there.

**Database tests:** Go tests that touch the database skip without `INNKEEPER_TEST_DATABASE_URL`. Export it before Task 1, or a skipped test reads as a pass.

---

## File map

| File | Change |
|---|---|
| `internal/library/search.go` | `Filter.Creator`, `Filter.Lorebook` |
| `internal/library/search_test.go` | Tests for both filters |
| `web/openapi.json`, `web/src/api/schema.d.ts` | Regenerated with `npm run gen`, never by hand |
| `web/src/cards/query.ts` | `creator` and `lorebook` terms |
| `web/src/cards/Tile.tsx`, `web/src/cards/Cards.tsx` | Lorebook names for term chips |
| `web/src/card/PageTabs.tsx` (new) | The tab bar and panel |
| `web/src/forge/Forge.tsx` | `Forge` component becomes the `useForgeView` hook |
| `web/src/forge/Forge.css`, `web/src/card/Card.css` | `.forge-tabs` moves to `.page-tabs`; group, metadata, and enrichment styles |
| `web/src/card/Card.tsx`, `web/src/lorebook/Lorebook.tsx`, `web/src/card/NewCard.tsx` | Compose `PageTabs` |
| `web/src/card/Metadata.tsx` (new) | Core facts |
| `web/src/card/Enrichments.tsx` | One block per instance, laid out by value |
| `web/src/card/Panel.tsx` | `Group`, regrouped panel, `Revisions` as one link, `LibraryName` removed |
| `web/src/card/Organize.tsx`, `web/src/card/Variants.tsx`, `web/src/card/OtherLibraries.tsx` | Sub-headings, Add source toggle, Variant menu |
| `web/src/card/Discover.tsx` | Related takes the card, caps at 8, links to the gallery; Similar pages by 8 |

---

### Task 1: `creator` and `lorebook` search filters

**Files:**
- Modify: `internal/library/search.go` (the `Filter` struct near line 60, `func (w *where) filter` near line 534)
- Test: `internal/library/search_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/library/search_test.go`:

```go
func TestSearchFiltersByCreatorIgnoringCase(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	mine, _ := newCard(t, lib, "mine", `{"name": "Mine", "creator": "OnlyYa"}`)
	newCard(t, lib, "theirs", `{"name": "Theirs", "creator": "someone"}`)
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Creator: ptr(" onlyya ")}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{mine}) {
		t.Fatalf("hits = %v, %v", hitIDs(got), err)
	}
}

func TestSearchFiltersByLorebook(t *testing.T) {
	lib := newLibrary(t)
	ctx := context.Background()
	s := seedSearch(t, lib)
	one := newLorebook(t, lib, "one")
	two := newLorebook(t, lib, "two")
	if err := lib.LinkLorebook(ctx, s.brakka, one); err != nil {
		t.Fatal(err)
	}
	if err := lib.LinkLorebook(ctx, s.carrow, two); err != nil {
		t.Fatal(err)
	}
	got, err := lib.Search(ctx, Card, Query{Filter: &Filter{Lorebook: &one}})
	if err != nil || !slices.Equal(hitIDs(got), []int64{s.brakka}) {
		t.Fatalf("one: hits = %v, %v", hitIDs(got), err)
	}
	either, err := lib.Search(ctx, Card, Query{Filter: &Filter{Or: []Filter{{Lorebook: &one}, {Lorebook: &two}}}})
	if err != nil || !slices.Equal(slices.Sorted(slices.Values(hitIDs(either))), slices.Sorted(slices.Values([]int64{s.brakka, s.carrow}))) {
		t.Fatalf("either: hits = %v, %v", hitIDs(either), err)
	}
	if _, err := lib.Search(ctx, Lorebook, Query{Filter: &Filter{Lorebook: &one}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lorebook search by lorebook: err = %v, want ErrInvalid", err)
	}
}
```

If the lorebook kind's variable isn't named `Lorebook`, run `grep -n "Card *= \|Lorebook *= " internal/library/*.go` and use the name you find.

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `go test ./internal/library -run 'TestSearchFiltersBy' -v`
Expected: build failure, `unknown field Creator in struct literal`.

- [ ] **Step 3: Add the fields**

In the `Filter` struct, after `Collection`:

```go
	Creator       *string       `json:"creator,omitempty" maxLength:"200" doc:"Creator is this, in any case, ignoring spaces around it. Cards only."`
	Lorebook      *int64        `json:"lorebook,omitempty" doc:"Is linked to this lorebook. Cards only."`
```

In `func (w *where) filter`, add both to the list that counts set fields:

```go
	for _, ok := range []bool{f.And != nil, f.Or != nil, f.Not != nil, f.IDs != nil, f.Name != nil, f.Tag != nil, f.Collection != nil, f.Creator != nil, f.Lorebook != nil, f.Source != nil, f.Archived != nil, f.TargetState != nil, f.Enrichment != nil, f.VariantOfRoot != nil, f.SharesSource != nil} {
```

Extend the cards-only check:

```go
	if w.k != Card && (f.Tag != nil || f.Collection != nil || f.Creator != nil || f.Lorebook != nil) {
		return "", fmt.Errorf("%w: tag, collection, creator, and lorebook filter cards only", ErrInvalid)
	}
```

Check whether a test matches the old message: `grep -rn "no tags or collections" internal`. Update any match.

Add the cases after `case f.Collection != nil:`:

```go
	case f.Creator != nil:
		// Matches the Related creator group, which compares lower(btrim(creator)).
		return `lower(btrim(r.data->>'creator')) = lower(btrim(` + w.arg(*f.Creator) + `))`, nil
	case f.Lorebook != nil:
		return `EXISTS (SELECT 1 FROM card_lorebook cl
			WHERE cl.card_id = c.id AND cl.lorebook_id = ` + w.arg(*f.Lorebook) + `)`, nil
```

- [ ] **Step 4: Run the tests and confirm they pass**

Run: `go test ./internal/library -run 'TestSearch' -v`
Expected: PASS, with no SKIP lines.

- [ ] **Step 5: Regenerate the API schema**

Run: `cd web && npm run gen`
Expected: `web/openapi.json` and `web/src/api/schema.d.ts` gain `creator` and `lorebook` on `Filter`. Check with `git diff --stat`.

- [ ] **Step 6: Commit**

```bash
git add internal/library/search.go internal/library/search_test.go web/openapi.json web/src/api/schema.d.ts
git commit -m "feat(search): filter cards by creator and by linked lorebook"
```

---

### Task 2: `creator:` and `lorebook:` gallery terms

**Files:**
- Modify: `web/src/cards/query.ts`, `web/src/cards/Tile.tsx:11`, `web/src/cards/Cards.tsx`
- Test: `web/src/cards/query.test.ts`

- [ ] **Step 1: Write the failing test**

Append to `web/src/cards/query.test.ts`, adding any of `parseQuery`, `toParams`, and `filterFor` that the file doesn't import yet:

```ts
test("creator and lorebook terms survive the URL and OR within their section", () => {
  const q = parseQuery(new URLSearchParams("library=1&f=creator:OnlyYa&f=lorebook:4&f=lorebook:5"), 1);
  expect(q.terms).toEqual([
    { kind: "creator", value: "OnlyYa", not: false },
    { kind: "lorebook", value: "4", not: false },
    { kind: "lorebook", value: "5", not: false },
  ]);
  expect(parseQuery(toParams(q), 1)).toEqual(q);
  expect(filterFor(q.terms)).toEqual({ and: [{ creator: "OnlyYa" }, { or: [{ lorebook: 4 }, { lorebook: 5 }] }] });
});

test("a lorebook term must name an ID", () => {
  expect(parseQuery(new URLSearchParams("f=lorebook:forge"), 1).terms).toEqual([]);
});
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `cd web && npx vitest run src/cards/query.test.ts`
Expected: FAIL, with `terms` equal to `[]`.

- [ ] **Step 3: Add the terms**

In `web/src/cards/query.ts`:

```ts
/** kinds are the filter terms. Sidebar sections are sectionsFor's; creator and lorebook arrive by link only. */
const kinds = ["target", "state", "tag", "source", "collection", "creator", "lorebook", "score", "value"] as const;
```

In `valid`:

```ts
  if (kind === "target" || kind === "collection" || kind === "lorebook") return id(value);
```

In `condition`, after `case "collection":`:

```ts
    case "creator":
      return { creator: value };
    case "lorebook":
      return { lorebook: Number(value) };
```

In `filterFor`:

```ts
  for (const kind of ["tag", "source", "collection", "creator", "lorebook"] as const) join(kind, included(kind).map((v) => condition(kind, v)));
```

- [ ] **Step 4: Run the test and confirm it passes**

Run: `cd web && npx vitest run src/cards/query.test.ts`
Expected: PASS.

- [ ] **Step 5: Name lorebooks in term chips**

In `web/src/cards/Tile.tsx:11`:

```ts
export type Names = { instances: Map<number, string>; collections: Map<number, string>; lorebooks: Map<number, string>; logos: Map<number, App> };
```

In `web/src/cards/Cards.tsx`, before `const names: Names = {`:

```ts
  const lorebookIds = q.terms.filter((t) => t.kind === "lorebook").map((t) => Number(t.value));
  const lorebooks = useQuery({
    ...searchQuery("lorebook", { library: q.library, filter: { ids: lorebookIds }, fields: ["name"], page: { size: lorebookIds.length } }),
    enabled: lorebookIds.length > 0,
  });
```

Add to `names`:

```ts
    lorebooks: new Map((lorebooks.data?.hits ?? []).map((h) => [h.id, String(h.fields.name ?? "")])),
```

In `termLabel`, after the `collection` branch:

```ts
    : t.kind === "lorebook" ? (names.lorebooks.get(Number(t.value)) ?? "#" + t.value)
```

Run `cd web && npm run typecheck`. Fix every other place that builds a `Names` value, such as test fixtures in `Tile.test.tsx` and `Facets`, by adding `lorebooks: new Map()`.

- [ ] **Step 6: Run the gallery tests**

Run: `cd web && npm run typecheck && npx vitest run src/cards`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/cards
git commit -m "feat(web): creator and lorebook terms in the gallery URL"
```

---

### Task 3: `PageTabs`

**Files:**
- Create: `web/src/card/PageTabs.tsx`
- Modify: `web/src/card/Card.css`, `web/src/forge/Forge.css:13-42`
- Test: `web/src/card/PageTabs.test.tsx`

- [ ] **Step 1: Write the failing tests**

Create `web/src/card/PageTabs.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { PageTabs, type PageTab } from "./PageTabs";

const tabs: PageTab[] = [
  { id: "fields", label: "Fields", panel: <p>fields panel</p> },
  { id: "review", label: "Review", disabled: true, panel: <p>review panel</p> },
  { id: "metadata", label: "Metadata", panel: <p>metadata panel</p> },
];

function Harness({ start = "fields" }: { start?: string }) {
  const [tab, setTab] = useState(start);
  return <PageTabs label="Card" tabs={tabs} selected={tab} onSelect={setTab} actions={<button>Reforge</button>} notices={<p>run header</p>} />;
}

test("arrow keys skip a disabled tab and show the next panel", async () => {
  const user = userEvent.setup();
  render(<Harness />);
  await user.click(screen.getByRole("tab", { name: "Fields" }));
  await user.keyboard("{ArrowRight}");
  const metadata = screen.getByRole("tab", { name: "Metadata" });
  expect(metadata).toHaveFocus();
  expect(metadata).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("tabpanel")).toHaveTextContent("metadata panel");
  expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", metadata.id);
});

test("a disabled or unknown selection shows the first usable tab", () => {
  render(<Harness start="review" />);
  expect(screen.getByRole("tab", { name: "Fields" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("tabpanel")).toHaveTextContent("fields panel");
});

test("actions sit in the bar and notices above the panel", () => {
  render(<Harness />);
  expect(screen.getByRole("button", { name: "Reforge" })).toBeInTheDocument();
  expect(screen.getByText("run header")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run the tests and confirm they fail**

Run: `cd web && npx vitest run src/card/PageTabs.test.tsx`
Expected: FAIL, because `./PageTabs` can't be resolved.

- [ ] **Step 3: Write `PageTabs`**

Create `web/src/card/PageTabs.tsx`:

```tsx
import type { ReactNode } from "react";

export type PageTab = { id: string; label: string; disabled?: boolean; panel: ReactNode };

type PageTabsProps = {
  label: string;
  tabs: PageTab[];
  /** selected falls back to the first usable tab when it names none. */
  selected: string;
  onSelect: (id: string) => void;
  /** actions sit at the bar's right end. */
  actions?: ReactNode;
  /** notices sit between the bar and the panel. */
  notices?: ReactNode;
};

/** PageTabs is a card or lorebook page's main column: its tab bar and the selected tab's panel. */
export function PageTabs({ label, tabs, selected, onSelect, actions, notices }: PageTabsProps) {
  const usable = tabs.filter((t) => !t.disabled);
  const shown = usable.find((t) => t.id === selected) ?? usable[0]!;
  const tabId = (id: string) => `page-tab-${id}`;
  const arrow = (key: string) => {
    const step = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    const next = usable[(usable.indexOf(shown) + step + usable.length) % usable.length]!;
    onSelect(next.id);
    document.getElementById(tabId(next.id))?.focus();
  };
  return (
    <>
      <div className="page-tabs">
        <div role="tablist" aria-label={label} onKeyDown={(e) => arrow(e.key)}>
          {tabs.map((t) => (
            <button
              key={t.id}
              id={tabId(t.id)}
              role="tab"
              aria-selected={shown.id === t.id}
              aria-controls="page-tabpanel"
              tabIndex={shown.id === t.id ? 0 : -1}
              className={shown.id === t.id ? "on" : undefined}
              disabled={t.disabled}
              onClick={() => onSelect(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
        {actions}
      </div>
      {notices}
      <div id="page-tabpanel" role="tabpanel" aria-labelledby={tabId(shown.id)}>
        {shown.panel}
      </div>
    </>
  );
}
```

- [ ] **Step 4: Move the CSS**

Cut the four `.forge-tabs` rules from `web/src/forge/Forge.css` (lines 13–42) and paste them at the end of `web/src/card/Card.css`, renaming `.forge-tabs` to `.page-tabs`:

```css
.page-tabs {
  position: sticky;
  top: var(--editor-bar-height, 0px);
  z-index: 5;
  display: flex;
  gap: 2px;
  align-items: center;
  background: var(--bg);
  border-bottom: 1px solid var(--border);
  margin-bottom: var(--space);
}

.page-tabs [role="tab"] {
  background: none;
  border: 0;
  border-bottom: 2px solid transparent;
  color: var(--text-muted);
  padding: var(--space) calc(var(--space) * 1.5);
  cursor: pointer;
}

.page-tabs [role="tab"].on {
  color: var(--text);
  border-bottom-color: var(--accent);
}

.page-tabs [role="tab"]:disabled {
  opacity: 0.4;
  cursor: default;
}
```

Leave `.forge-open` in `Forge.css`. Its `margin-left: auto` keeps the Reforge button at the bar's right end.

- [ ] **Step 5: Run the tests and confirm they pass**

Run: `cd web && npx vitest run src/card/PageTabs.test.tsx`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/card/PageTabs.tsx web/src/card/PageTabs.test.tsx web/src/card/Card.css web/src/forge/Forge.css
git commit -m "feat(web): core tab bar for card and lorebook pages"
```

---

### Task 4: The Forge adds to the tab bar instead of owning it

**Files:**
- Modify: `web/src/forge/Forge.tsx` (the `Forge` component, lines 1–235; leave `Badges`, `FieldMenu`, and the rest alone)
- Modify: `web/src/card/Card.tsx`, `web/src/lorebook/Lorebook.tsx`, `web/src/card/NewCard.tsx`
- Test: `web/src/forge/Forge.test.tsx`, `web/src/lorebook/Lorebook.test.tsx`

`Forge.test.tsx` renders whole pages, so it already covers this refactor. It must pass unchanged, apart from the one assertion below.

- [ ] **Step 1: Turn `Forge` into `useForgeView`**

In `web/src/forge/Forge.tsx`:

1. Remove `type Tab = ...` and the `useState<Tab>()` line. Import `PageTab` with `import type { PageTab } from "../card/PageTabs";`.
2. Replace the `Forge` doc comment and signature with:

```tsx
/** ForgeView is the Forge's part of a card or lorebook page. Without an action plugin for the page's kind, it adds no tabs or actions. */
export type ForgeView = {
  /** start is a new card's Start tab, which goes before Fields. */
  start?: PageTab;
  /** tabs go after Fields: Review, and Report on a saved card or lorebook. */
  tabs: PageTab[];
  actions?: ReactNode;
  /** notices are the run headers. */
  notices: ReactNode;
  /** lock and extra go to the Fields tab. */
  lock?: Lock;
  extra?: (key: string) => ReactNode;
  /** overlays are the Reforge panel and the repeat-run dialog. */
  overlays: ReactNode;
};

/** useForgeView builds the Forge's tabs, actions, and overlays. setTab selects a page tab: the Forge opens Review, Report, Start, and Fields itself. */
export function useForgeView(props: {
  forge: ForgeState;
  isNew?: boolean;
  data: Data;
  edit: (key: string, value: unknown) => void;
  /** save saves the draft with a reviewed proposal's changes; without it, Review only applies them to the draft. */
  save?: (changes: Data) => void;
  avatar?: Blob | string;
  setTab: (tab: string) => void;
}): ForgeView {
  const { forge, data, isNew, setTab } = props;
```

3. Leave the body's hooks, `open`, the ⌘K effect, the Review/Report effects, `toReport`, `pickField`, `extra`, `pending`, `proposed`, `reviewCount`, and `revise` as they are. `setTab` now refers to the prop.
4. Delete `tabs`, `shown`, `tabId`, `usable`, and `arrow`.
5. Replace the whole `return (...)` with:

```tsx
  const review = pending && proposed && (
    <Review
      key={pending.id}
      title={pending.field ? `${pending.label} on ${label(pending.field)}` : pending.label}
      proposed={proposed}
      warnings={pending.result?.warnings}
      instruction={pending.instruction}
      stale={changedKeys(pending.start, data).length ? kind : undefined}
      saves={!!props.save}
      preview={(changes) => forge.subject.preview({ ...data, ...changes })}
      onApply={(changes) => {
        if (props.save) props.save(changes);
        else for (const [k, v] of Object.entries(changes)) props.edit(k, v);
        forge.settlePending();
        setTab("fields");
      }}
      onDiscard={() => {
        forge.settlePending();
        setTab("fields");
      }}
    />
  );
  const report = (
    <>
      {forge.report && (
        <Report key={forge.reportId} report={forge.report} subject={forge.subject} changed={changedKeys(forge.reportStart ?? data, data)} onRefine={revise && ((text) => forge.start({ action: revise, instruction: text }))} />
      )}
      <div className="forge-empty">
        {!forge.report && <p>No report yet.</p>}
        {forge.mine
          .filter((a) => a.applies === kind && a.report_only)
          .map((a) => (
            <button key={a.id} className={forge.report ? "button" : "button primary"} onClick={() => (asks(a) ? open({ action: a }) : forge.start({ action: a }))}>
              <Hammer /> {forge.report ? `${a.label} again` : a.label}
            </button>
          ))}
      </div>
    </>
  );
  const notices = (
    <>
      {forge.runs.map((r) => (
        <RunHeader key={r.id} run={r} label={label} onStop={() => forge.stop(r.id)} onDismiss={() => forge.settle(r.id)} />
      ))}
      {forge.waiting.map((w) => (
        <WaitingHeader key={w.key} w={w} label={label} onCancel={() => forge.unwait(w.key)} />
      ))}
    </>
  );
  const overlays = (
    <>
      {forge.repeat && (
        <ConfirmDialog
          title="A proposal from this run is waiting"
          body={<p>Review already has a proposal from the same {kind} and instruction.</p>}
          confirmLabel="Run again"
          cancelLabel="Open Review"
          onConfirm={forge.runAgain}
          onCancel={() => setTab("review")}
          onClose={forge.unrepeat}
        />
      )}
      {panel && (
        <Reforge
          forge={forge}
          name={name}
          verb={verb}
          target={target}
          setTarget={setTarget}
          instruction={instruction}
          setInstruction={setInstruction}
          avatar={props.avatar}
          onClose={() => setPanel(false)}
        />
      )}
    </>
  );
  if (!on) return { tabs: [], notices, overlays };
  const reviewTab: PageTab = { id: "review", label: `Review${pending ? ` (${reviewCount})` : ""}`, disabled: !pending, panel: review };
  return {
    start: isNew ? { id: "start", label: "Start", panel: <Start forge={forge} avatar={props.avatar} /> } : undefined,
    tabs: isNew ? [reviewTab] : [reviewTab, { id: "report", label: "Report", panel: report }],
    actions: (
      <button className="button primary forge-open" onClick={() => open()} title={`${verb} (Ctrl+K)`}>
        <Hammer /> {verb} <kbd>⌘K</kbd>
      </button>
    ),
    notices,
    lock: forge.lock,
    extra,
    overlays,
  };
}
```

Copy the Review and Report JSX from the current file rather than retyping it, in case it changed after this plan was written. The only change is `props.setTab` in place of the local setter. Remove imports that are now unused.

- [ ] **Step 2: Compose the card page**

In `web/src/card/Card.tsx`, import `useState`, `useForgeView` (in place of `Forge`), and `PageTabs`. Before the `if (ed.entity.isPending) return null;` line:

```tsx
  const [tab, setTab] = useState<string>();
  const view = useForgeView({ forge, data: ed.data, edit: ed.edit, save: ed.saveWith, avatar: ed.entity.data?.revision.avatar, setTab });
```

Replace the `<Forge ... />` element with:

```tsx
        <PageTabs
          label="Card"
          selected={tab ?? ""}
          onSelect={setTab}
          tabs={[
            { id: "fields", label: "Fields", panel: <Fields data={ed.data} dirty={ed.dirty} edit={ed.edit} lock={view.lock} extra={view.extra} /> },
            ...view.tabs,
          ]}
          actions={view.actions}
          notices={view.notices}
        />
        {view.overlays}
```

- [ ] **Step 3: Compose the lorebook page**

In `web/src/lorebook/Lorebook.tsx`, add the same `tab` state and hook before the early returns:

```tsx
  const [tab, setTab] = useState<string>();
  const view = useForgeView({ forge, data: ed.data, edit: ed.edit, save: ed.saveWith, setTab });
```

Replace `<Forge ... />` with a `PageTabs` labeled `"Lorebook"`, whose Fields panel is the current `fields` render prop's JSX with `lock?.(` replaced by `view.lock?.(` and `extra?.(` by `view.extra?.(`:

```tsx
        <PageTabs
          label="Lorebook"
          selected={tab ?? ""}
          onSelect={setTab}
          tabs={[
            {
              id: "fields",
              label: "Fields",
              panel: (
                <>
                  <Section
                    label="Description"
                    dirty={ed.dirty.has("description")}
                    count={tokens(text(ed.data.description))}
                    lock={view.lock?.("description")}
                    extra={view.extra?.("description")}
                  >
                    <MacroTextarea label="Description" value={text(ed.data.description)} onChange={(v) => ed.edit("description", v)} />
                  </Section>
                  <Entries data={ed.data} base={ed.draft?.base ?? ed.headData} edit={ed.edit} lock={view.lock?.("entries")} />
                </>
              ),
            },
            ...view.tabs,
          ]}
          actions={view.actions}
          notices={view.notices}
        />
        {view.overlays}
```

- [ ] **Step 4: Compose the new-card page**

In `web/src/card/NewCard.tsx`, after `const forgeBusy = ...`:

```tsx
  const [tab, setTab] = useState<string>();
  const view = useForgeView({ forge, isNew: true, data, edit, avatar: avatar ?? undefined, setTab });
```

Replace `<Forge ... />` with:

```tsx
          <PageTabs
            label="New card"
            selected={tab ?? ""}
            onSelect={setTab}
            tabs={[
              ...(view.start ? [view.start] : []),
              { id: "fields", label: "Fields", panel: <Fields data={data} dirty={dirty} edit={edit} lock={view.lock} extra={view.extra} /> },
              ...view.tabs,
            ]}
            actions={view.actions}
            notices={view.notices}
          />
          {view.overlays}
```

- [ ] **Step 5: Update the lorebook test that expected no tabs**

In `web/src/lorebook/Lorebook.test.tsx`, in "a plugin with only card actions shows no Forge on the lorebook page", replace `expect(screen.queryByRole("tab")).not.toBeInTheDocument();` with:

```tsx
  expect(screen.getByRole("tab", { name: "Fields" })).toBeInTheDocument();
  expect(screen.queryByRole("tab", { name: /^Review/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("tab", { name: "Report" })).not.toBeInTheDocument();
```

In `web/src/forge/Forge.test.tsx`, in "without an action plugin the card page is the plain editor", add:

```tsx
  expect(screen.getByRole("tab", { name: "Fields" })).toBeInTheDocument();
  expect(screen.queryByRole("tab", { name: "Report" })).not.toBeInTheDocument();
```

- [ ] **Step 6: Run the tests**

Run: `cd web && npm run typecheck && npx vitest run src/forge src/card src/lorebook`
Expected: PASS. If a Forge test fails, compare the new tab ids and labels with the old ones; they must match (`fields`, `review`, `report`, `start`).

- [ ] **Step 7: Commit**

```bash
git add web/src/forge/Forge.tsx web/src/forge/Forge.test.tsx web/src/card/Card.tsx web/src/card/NewCard.tsx web/src/lorebook/Lorebook.tsx web/src/lorebook/Lorebook.test.tsx
git commit -m "refactor(web): the Forge adds tabs to the page's tab bar instead of owning it"
```

---

### Task 5: Metadata tab

**Files:**
- Create: `web/src/card/Metadata.tsx`
- Modify: `web/src/card/Enrichments.tsx`, `web/src/card/Card.tsx`, `web/src/lorebook/Lorebook.tsx`, `web/src/card/Card.css` (replace `.enrichment-list` rules near line 444)
- Test: `web/src/card/Metadata.test.tsx`, `web/src/card/Enrichments.test.tsx`

- [ ] **Step 1: Write the failing Metadata test**

Create `web/src/card/Metadata.test.tsx`:

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import type { Entity } from "./Card";
import { Metadata } from "./Metadata";

const entity = {
  id: 7,
  slug: "brakka",
  provenance: "chub",
  created_at: "2026-10-12T10:00:00Z",
  library_id: 2,
  variants: null,
  revision: { id: 505702, content_hash: "abcdef0123456789abcdef", message: "", source: "ui", created_at: "2026-10-30T09:00:00Z" },
} as Entity;

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const body = new URL(req.url).pathname === "/api/libraries" ? [{ id: 2, name: "Chub archive", cards: 1, lorebooks: 0, created_at: "2026-10-01T00:00:00Z" }] : [];
      return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

test("lists the stored facts, with the last edit linking to its revision", async () => {
  const router = createMemoryRouter([{ path: "*", element: <Metadata kind="card" entity={entity} /> }]);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  const row = (label: string) => screen.getByText(label).parentElement!;
  expect(await within(row("Library")).findByText("Chub archive")).toBeInTheDocument();
  expect(within(row("Slug")).getByText("brakka")).toBeInTheDocument();
  expect(within(row("Provenance")).getByText("chub")).toBeInTheDocument();
  expect(within(row("Revision")).getByText("505702 (ui)")).toBeInTheDocument();
  expect(within(row("Last edited")).getByRole("link")).toHaveAttribute("href", "/cards/7/history?rev=505702");
  expect(within(row("Content hash")).getByRole("button", { name: "abcdef012345" })).toHaveAttribute("title", "abcdef0123456789abcdef");
});
```

- [ ] **Step 2: Run it and confirm it fails**

Run: `cd web && npx vitest run src/card/Metadata.test.tsx`
Expected: FAIL, because `./Metadata` can't be resolved.

- [ ] **Step 3: Write `Metadata`**

Create `web/src/card/Metadata.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import type { Kind } from "../api/client";
import { librariesQuery } from "../api/queries";
import { toastError } from "../shell/Toasts";
import type { Entity } from "./Card";

/** Metadata lists a card's or lorebook's stored facts: dates, IDs, library, and provenance. */
export function Metadata({ kind, entity }: { kind: Kind; entity: Entity }) {
  const library = useQuery(librariesQuery).data?.find((l) => l.id === entity.library_id);
  const rev = entity.revision;
  const hash = rev.content_hash;
  const copy = () =>
    navigator.clipboard.writeText(hash).then(
      () => toast.success("Copied the content hash"),
      (e: Error) => toastError(e.message),
    );
  const rows: [string, ReactNode][] = [
    ["Created", new Date(entity.created_at).toLocaleString()],
    ["Last edited", <Link to={`/${kind}s/${entity.id}/history?rev=${rev.id}`}>{new Date(rev.created_at).toLocaleString()}</Link>],
    ["Revision", `${rev.id} (${rev.source})`],
    ["Slug", entity.slug],
    ["ID", entity.id],
    ["Library", library?.name ?? `#${entity.library_id}`],
    ["Provenance", entity.provenance],
    [
      "Content hash",
      <button className="button ghost" title={hash} onClick={() => void copy()}>
        {hash.slice(0, 12)}
      </button>,
    ],
  ];
  return (
    <section>
      <h2>Details</h2>
      <dl className="metadata-list">
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
```

- [ ] **Step 4: Run it and confirm it passes**

Run: `cd web && npx vitest run src/card/Metadata.test.tsx`
Expected: PASS.

- [ ] **Step 5: Write the failing Enrichments tests**

In `web/src/card/Enrichments.test.tsx`, change the fetch stub's enrichments line to add a list value and a long text value from the same instance, plus one value from a second instance:

```ts
      if (path === "/api/cards/7/enrichments")
        return json([
          value("quality", "Quality", 78),
          value("voice", "Voice", 40, true),
          value("contradictions", "Contradictions", ["description vs first_mes", "scenario vs mes_example"]),
          value("summary", "Summary", "A grumpy dwarf blacksmith who owes everyone in the harbor money and a favor."),
          { ...value("lint", "Lint", 100), instance_id: 6, instance_name: "Card lint" },
        ]);
```

Return both instances from `/api/instances`, and give `card-lint` the `enrich` capability in `/api/plugins`:

```ts
      if (path === "/api/instances") return json([{ id: 5, label: "Judge", name: "Judge", plugin: "judge" }, { id: 6, label: "Card lint", name: "Card lint", plugin: "card-lint" }]);
      if (path === "/api/plugins") return json([{ name: "judge", capabilities: ["enrich"] }, { name: "card-lint", capabilities: ["enrich"] }]);
```

Add `within` to the Testing Library import. In both existing tests, change the `Rescore` lookup to the Judge block:

```ts
  const judge = (await screen.findByRole("heading", { name: "Judge" })).closest(".enrichment-instance") as HTMLElement;
  await userEvent.setup().click(within(judge).getByRole("button", { name: "Rescore" }));
```

Add:

```tsx
test("a list value shows as bullets and long text at full width, each instance with its own Rescore", async () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Enrichments id={7} />
    </QueryClientProvider>,
  );
  const judge = (await screen.findByRole("heading", { name: "Judge" })).closest(".enrichment-instance") as HTMLElement;
  expect(within(judge).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["description vs first_mes", "scenario vs mes_example"]);
  expect(within(judge).getByText(/grumpy dwarf/).closest(".enrichment-long")).not.toBeNull();
  expect(within(judge).getByText("78").closest(".enrichment-grid")).not.toBeNull();
  const lint = screen.getByRole("heading", { name: "Card lint" }).closest(".enrichment-instance") as HTMLElement;
  expect(within(lint).getByText("100")).toBeInTheDocument();
  expect(within(lint).getByRole("button", { name: "Rescore" })).toBeInTheDocument();
});
```

- [ ] **Step 6: Run them and confirm they fail**

Run: `cd web && npx vitest run src/card/Enrichments.test.tsx`
Expected: FAIL, with no heading named "Judge" (today the heading shows only with two instances, and there is one shared Rescore).

- [ ] **Step 7: Rewrite `Enrichments` by instance**

Replace `web/src/card/Enrichments.tsx` with:

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, must } from "../api/client";
import { useInstancesWith } from "../api/queries";
import type { components } from "../api/schema";
import { enrichmentText } from "../cards/Tile";
import { finished, useJob } from "../settings/jobUtil";

type Value = components["schemas"]["EnrichmentValue"];

// Past this length a value reads better at full width than in the score grid.
const gridLength = 40;
const isLong = (v: Value) => Array.isArray(v.value) || String(v.value).length > gridLength;

/** Enrichments shows each enrich instance's values for the card, with a Rescore per instance that still exists. */
export function Enrichments({ id }: { id: number }) {
  const enrichers = useInstancesWith("enrich");
  const values = useQuery({
    queryKey: ["card", id, "enrichments"],
    queryFn: async () => must(await api.GET("/cards/{id}/enrichments", { params: { path: { id } } })),
  });
  const list = values.data ?? [];
  if (!enrichers.length && !list.length) return null;
  const instances = [...new Set([...list.map((v) => v.instance_id), ...enrichers.map((e) => e.id)])];
  const name = (inst: number) =>
    enrichers.find((e) => e.id === inst)?.label ?? (list.find((v) => v.instance_id === inst)?.instance_name || `Deleted instance #${inst}`);
  return (
    <section>
      <h2>Enrichments</h2>
      {values.data && !list.length && <p className="muted">Not scored yet.</p>}
      {instances.map((inst) => (
        <InstanceValues
          key={inst}
          card={id}
          instance={inst}
          name={name(inst)}
          values={list.filter((v) => v.instance_id === inst)}
          rescore={enrichers.some((e) => e.id === inst)}
        />
      ))}
    </section>
  );
}

type InstanceValuesProps = { card: number; instance: number; name: string; values: Value[]; rescore: boolean };

/** InstanceValues is one instance's values: short ones in a grid, lists and long text at full width. */
function InstanceValues({ card, instance, name, values, rescore }: InstanceValuesProps) {
  const qc = useQueryClient();
  const [jobId, setJobId] = useState<number>();
  const job = useJob(jobId ?? 0, { enabled: jobId !== undefined });
  const done = !!job.data && finished(job.data);
  useEffect(() => {
    if (done) void qc.invalidateQueries({ queryKey: ["card", card, "enrichments"] });
  }, [done, qc, card]);
  const start = useMutation({
    meta: { toast: false },
    mutationFn: async () => must(await api.POST("/enrichments/runs", { body: { instance_id: instance, card_ids: [card], rescore: true, thorough: true } })),
    onSuccess: (j) => setJobId(j.id),
  });
  const running = start.isPending || (jobId !== undefined && !done);
  const failed = job.data?.status === "failed" ? job.data.error : undefined;
  const short = values.filter((v) => !isLong(v));
  const long = values.filter(isLong);
  const staleTitle = (v: Value) => (v.stale ? "Stale: from an older revision" : undefined);
  return (
    <div className="enrichment-instance">
      <div className="row">
        <h3>{name}</h3>
        {rescore && (
          <button className="button" disabled={running} onClick={() => start.mutate()}>
            {running ? "Rescoring…" : "Rescore"}
          </button>
        )}
      </div>
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
      {(start.error || failed) && <p className="error">{start.error?.message ?? failed}</p>}
    </div>
  );
}
```

- [ ] **Step 8: Replace the enrichment CSS**

In `web/src/card/Card.css`, replace the four `.enrichment-list` rules with:

```css
.metadata-list,
.enrichment-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 4px calc(var(--space) * 2);
  margin: 0 0 var(--space);
}

.metadata-list > div,
.enrichment-grid > div {
  display: flex;
  justify-content: space-between;
  gap: var(--space);
}

.metadata-list dt,
.enrichment-grid dt {
  color: var(--text-muted);
}

.metadata-list dd,
.enrichment-grid dd {
  margin: 0;
  font-weight: 700;
  text-align: right;
}

.enrichment-instance {
  margin-bottom: calc(var(--space) * 2);
}

.enrichment-long h4 {
  margin: var(--space) 0 calc(var(--space) * 0.5);
  font-size: 0.9em;
  color: var(--text-muted);
}

.enrichment-long ul,
.enrichment-long p {
  margin: 0;
}

.enrichment-grid .stale dd,
.enrichment-long.stale {
  opacity: 0.55;
  font-weight: 400;
}
```

Run `grep -rn "enrichment-list\|\.enrichments" web/src`. Nothing should still use the old class names.

- [ ] **Step 9: Add the tab to both pages**

In `web/src/card/Card.tsx`, add after `...view.tabs,`:

```tsx
            { id: "metadata", label: "Metadata", panel: <><Metadata kind="card" entity={head} /><Enrichments id={head.id} /></> },
```

In `web/src/lorebook/Lorebook.tsx`, add after `...view.tabs,`:

```tsx
            { id: "metadata", label: "Metadata", panel: <Metadata kind="lorebook" entity={head} /> },
```

Remove `<Enrichments id={card.id} />` from `Panel` in `web/src/card/Panel.tsx` and its import there.

- [ ] **Step 10: Run the tests**

Run: `cd web && npm run typecheck && npx vitest run src/card src/lorebook src/forge`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add web/src/card web/src/lorebook
git commit -m "feat(web): Metadata tab with core facts and enrichment values by instance"
```

---

### Task 6: Group the side panel

**Files:**
- Modify: `web/src/card/Panel.tsx`, `web/src/card/Organize.tsx`, `web/src/card/Variants.tsx`, `web/src/card/OtherLibraries.tsx`, `web/src/lorebook/Lorebook.tsx`, `web/src/card/Card.css`
- Test: `web/src/card/Card.test.tsx`, `web/src/lorebook/Lorebook.test.tsx`

- [ ] **Step 1: Write the failing tests**

Add to `web/src/card/Card.test.tsx`:

```tsx
test("the panel groups links, sync, and collections, with Related left to the main column", async () => {
  renderAt("/cards/7");
  const links = (await screen.findByText("Links", { selector: "summary" })).closest("details")!;
  expect(links).toHaveAttribute("open");
  expect(within(links).getByRole("link", { name: "chub: dg/brakka" })).toBeInTheDocument();
  expect(within(links).getByRole("link", { name: /^History/ })).toHaveAttribute("href", "/cards/7/history");
  const collections = screen.getByText("Collections", { selector: "summary" }).closest("details")!;
  expect(collections).not.toHaveAttribute("open");
  const aside = links.closest("aside")!;
  expect(within(aside).queryByRole("heading", { name: "Related" })).not.toBeInTheDocument();
  expect(within(aside).queryByText(/^Library:/)).not.toBeInTheDocument();
});

test("adding a source opens its URL box on demand", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "+ Add source" }));
  expect(screen.getByRole("textbox", { name: "Add source URL" })).toHaveFocus();
});
```

Add `within` to the file's Testing Library import if it isn't there.

In "leaving with unsaved edits asks first", change `{ name: "All history" }` to `{ name: /^History/ }`.

In `web/src/lorebook/Lorebook.test.tsx`, replace the two panel tests:

```tsx
test("the Metadata tab names the lorebook's library", async () => {
  renderAt("/lorebooks/3");
  await userEvent.click(await screen.findByRole("tab", { name: "Metadata" }));
  expect(await screen.findByText("Chub archive")).toBeInTheDocument();
});

test("the panel links to history", async () => {
  renderAt("/lorebooks/3");
  expect(await screen.findByRole("link", { name: /^History/ })).toHaveAttribute("href", "/lorebooks/3/history");
});
```

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd web && npx vitest run src/card/Card.test.tsx src/lorebook/Lorebook.test.tsx`
Expected: FAIL. The new tests find no "Links" summary and no "+ Add source" button, and the history link is still named "All history".

- [ ] **Step 3: Add `Group` and regroup `Panel`**

In `web/src/card/Panel.tsx`, add:

```tsx
/** Group is a collapsible block of the side panel. */
export function Group({ title, open = true, children }: { title: string; open?: boolean; children: ReactNode }) {
  return (
    <details className="aside-group" open={open}>
      <summary>{title}</summary>
      {children}
    </details>
  );
}
```

Replace `Panel`'s `<aside>` contents with:

```tsx
    <aside className="card-aside">
      {avatar ? <img className={nsfw(data.tags) ? "portrait nsfw" : "portrait"} src={thumb(avatar, 640)} alt="" /> : <div className="portrait" />}
      <Image sha={avatar} busy={avatarBusy} onAvatar={onAvatar} />
      <Identity data={data} edit={edit} lock={lock} />
      <Group title="Links">
        <Sources kind="card" id={card.id} />
        <VariantOf kind="card" entity={card} />
        <Variants kind="card" entity={card} />
        <OtherLibraries kind="card" id={card.id} />
        <Lorebooks ids={card.lorebooks ?? []} />
        <Revisions kind="card" id={card.id} />
      </Group>
      <Sync kind="card" id={card.id} />
      <Collections id={card.id} library={card.library_id} member={card.collections ?? []} />
    </aside>
```

Remove `Related` from `Panel`'s imports; Task 7 renders it in `Card.tsx`. Delete `LibraryName`, since Metadata now shows the library.

In `Sync`, replace `<section>` and `<h2>Sync</h2>` with `<Group title="Sync">`, closing with `</Group>`.

In `Lorebooks`, change `<h2>Lorebooks</h2>` to `<h3>Lorebooks</h3>`.

Replace `Revisions` with:

```tsx
/** Revisions links to the history page with the revision count. */
export function Revisions({ kind, id }: { kind: Kind; id: number }) {
  const revs = useQuery({
    queryKey: [kind, id, "revisions"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/revisions"), { params: { path: { id } } })),
  });
  const n = revs.data?.length;
  return <Link to={`/${kind}s/${id}/history`}>History{n ? ` (${n} revision${n === 1 ? "" : "s"})` : ""}</Link>;
}
```

Add `type ReactNode` to the `react` import.

- [ ] **Step 4: Sources: Check for updates in the heading, Add source on demand**

In `web/src/card/Organize.tsx`, in `Sources`, add `const [adding, setAdding] = useState(false);`. In `add`'s `onSuccess`, add `setAdding(false);`. Replace the `<h2>Sources</h2>` through the closing `</form>` with:

```tsx
      <div className="row aside-heading">
        <h3>Sources</h3>
        {!!list.data?.length && !!followers?.size && (
          <button className="button ghost" disabled={edits.check.isPending} onClick={() => edits.check.mutate()}>
            Check for updates
          </button>
        )}
      </div>
      <ul className="plain">
        {(list.data ?? []).map((s) => {
          const b = following(s);
          return (
            <li key={s.id}>
              {/* Only https links, so a stored URL can't run script. */}
              {s.url?.startsWith("https://") ? (
                <a className="truncate" href={s.url} title={s.url} target="_blank" rel="noreferrer">
                  {s.kind}: {s.ref}
                </a>
              ) : (
                `${s.kind}: ${s.ref}`
              )}
              <button className="chip-x" aria-label={`Remove source ${s.ref}`} disabled={remove.isPending} onClick={() => remove.mutate(s.id)}>
                ×
              </button>
              {b && <Follow b={b} label={`${s.kind}: ${s.ref}`} edits={edits} />}
            </li>
          );
        })}
      </ul>
      {adding ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (url.trim()) add.mutate(url.trim());
          }}
        >
          <input className="input" aria-label="Add source URL" placeholder="Source URL" autoFocus value={url} onChange={(e) => setUrl(e.target.value)} />
        </form>
      ) : (
        <button className="button ghost" onClick={() => setAdding(true)}>
          + Add source
        </button>
      )}
```

Check that the existing source tests that type into "Add source URL" first click "+ Add source": `grep -n "Add source URL" web/src/card/Card.test.tsx`. Add that click where it's missing.

In `Collections`, replace `<section>` and `<h2>Collections</h2>` with `<Group title="Collections" open={false}>`, closing with `</Group>`, and import `Group` from `./Panel`.

- [ ] **Step 5: Variants: one menu**

In `web/src/card/Variants.tsx`, import `Menu` from `../shell/Menu`. Replace `<h2>Variants</h2>` and the `<div className="row">` holding the three buttons with:

```tsx
      <div className="row aside-heading">
        <h3>Variants</h3>
        <Menu label="Variant actions">
          <button disabled={fork.isPending} onClick={() => fork.mutate(undefined)}>
            New variant
          </button>
          <button aria-expanded={forking} onClick={() => setForking(!forking)}>
            Fork into…
          </button>
          <button aria-expanded={linking} onClick={() => setLinking(!linking)}>
            Link existing…
          </button>
        </Menu>
      </div>
```

In `web/src/card/OtherLibraries.tsx`, change `<h2>In other libraries</h2>` to `<h3>In other libraries</h3>`. In `web/src/lorebook/Lorebook.tsx`, change `LinkedCards`' `<h2>` to `<h3>` too.

The Card tests "Link existing makes…" and "Fork into… lists every library…" click buttons that are now inside the popover. jsdom doesn't hide popovers, so they should still pass. If one doesn't, click `{ name: "Variant actions" }` first.

- [ ] **Step 6: Regroup the lorebook panel**

In `web/src/lorebook/Lorebook.tsx`, import `Group` and replace the `<aside>` contents with:

```tsx
      <aside className="card-aside">
        <Group title="Links">
          <Sources kind="lorebook" id={id} />
          <VariantOf kind="lorebook" entity={head} />
          <Variants kind="lorebook" entity={head} />
          <OtherLibraries kind="lorebook" id={id} />
          <LinkedCards ids={head.cards ?? []} library={head.library_id} />
          <Revisions kind="lorebook" id={id} />
        </Group>
        <Sync kind="lorebook" id={id} />
      </aside>
```

Drop the `LibraryName` import.

- [ ] **Step 7: Panel CSS**

In `web/src/card/Card.css`, after the `.card-aside h2` rule:

```css
.aside-group > summary {
  font-size: 0.85em;
  text-transform: uppercase;
  color: var(--text-muted);
  cursor: pointer;
  margin: var(--space) 0 calc(var(--space) * 0.5);
}

.aside-group {
  display: grid;
  gap: calc(var(--space) * 0.5);
}

.card-aside h3 {
  font-size: 0.9em;
  margin: calc(var(--space) * 0.5) 0 0;
}

.aside-heading {
  justify-content: space-between;
  align-items: center;
}

.card-aside .truncate {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: bottom;
}
```

`<details>` ignores `display: grid` in some browsers. If the group's children don't space out, wrap `Group`'s children in `<div className="aside-group-body">` and move the grid rule to that class.

- [ ] **Step 8: Run the tests**

Run: `cd web && npm run typecheck && npx vitest run src/card src/lorebook`
Expected: PASS. If "the panel shows sync, lorebooks, collections, and sources" can't find the collection checkboxes inside the closed `<details>`, click its summary first: `await userEvent.click(screen.getByText("Collections", { selector: "summary" }))`.

- [ ] **Step 9: Commit**

```bash
git add web/src/card web/src/lorebook
git commit -m "feat(web): group the card and lorebook side panels"
```

---

### Task 7: Related under the tabs, capped, with gallery links

**Files:**
- Modify: `web/src/card/Discover.tsx`, `web/src/card/Card.tsx`
- Test: `web/src/card/Discover.test.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/card/Discover.test.tsx`, add the fixture and import:

```tsx
import type { Entity } from "./Card";

const card = {
  id: 7,
  slug: "brakka",
  provenance: "ui",
  created_at: now,
  library_id: 1,
  variants: null,
  lorebooks: [3, 4],
  collections: [9],
  revision: { id: 1, content_hash: "x", message: "", source: "ui", created_at: now, data: { name: "Brakka", creator: "OnlyYa" } },
} as Entity;
```

Replace the three Related tests with:

```tsx
test("Related asks for 8 cards a group and links each group's rest to the gallery", async () => {
  routes["/api/cards/7/related?limit=8"] = {
    groups: [
      { group: "creator", items: [{ card_id: 8, library_id: 1, name: "Dorna" }], next_cursor: "8" },
      { group: "lorebook", items: [{ card_id: 10, library_id: 1, name: "Ilse" }], next_cursor: "8" },
      { group: "collection", items: [{ card_id: 11, library_id: 1, name: "Pell" }] },
    ],
  };
  renderIt(<Related card={card} />);
  expect(await screen.findByRole("heading", { name: "Same creator" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Dorna" })).toHaveAttribute("href", "/cards/8");
  const all = screen.getAllByRole("link", { name: "See all" }).map((a) => new URL(a.getAttribute("href")!, "http://x"));
  expect(all.map((u) => u.pathname)).toEqual(["/cards", "/cards"]);
  expect(all[0]!.searchParams.getAll("f")).toEqual(["creator:OnlyYa"]);
  expect(all[1]!.searchParams.getAll("f")).toEqual(["lorebook:3", "lorebook:4"]);
  expect(all[0]!.searchParams.get("library")).toBe("1");
});

test("Related with no groups renders nothing", async () => {
  routes["/api/cards/7/related?limit=8"] = { groups: [] };
  const { container } = renderIt(<Related card={card} />);
  await waitFor(() => expect(requested).toContain("/api/cards/7/related?limit=8"));
  await new Promise((r) => setTimeout(r, 50));
  expect(container).toBeEmptyDOMElement();
});
```

The collection group has no `next_cursor`, so it has no See all link. That is why the test expects two links.

In "Similar links each card and shows more on demand", change each route key `"/api/cards/7/similar?scope=library..."` so its query string starts with `limit=8&`. Run `grep -n "similar?" web/src/card/Discover.test.tsx` to list them all. Check the exact order the client writes in `requested` after Step 3 and match it.

- [ ] **Step 2: Run them and confirm they fail**

Run: `cd web && npx vitest run src/card/Discover.test.tsx`
Expected: FAIL, because `Related` doesn't take `card` and requests no `limit`.

- [ ] **Step 3: Rewrite `Related` and cap `Similar`**

In `web/src/card/Discover.tsx`:

Add imports:

```tsx
import { parseQuery, toParams, type Term } from "../cards/query";
import type { Entity } from "./Card";
import { text, type Data } from "./draft";
```

Add, below `groupNames`:

```tsx
// Two rows of tiles in the main column.
const shown = 8;

/** galleryTerms are the gallery terms that list a related group in full. */
function galleryTerms(group: RelatedPage["group"], card: Entity): Term[] {
  const term = (kind: Term["kind"], value: string): Term => ({ kind, value, not: false });
  if (group === "creator") return [term("creator", text((card.revision.data as Data).creator))];
  if (group === "lorebook") return (card.lorebooks ?? []).map((id) => term("lorebook", String(id)));
  return (card.collections ?? []).map((id) => term("collection", String(id)));
}
```

In `Similar`'s `queryFn`, add `limit: shown` to the query: `query: { scope, cursor: pageParam, limit: shown }`.

Delete `RelatedGroup`. Replace `Related` with:

```tsx
/** Related shows the first cards linked to this one by creator, lorebook, or collection, each group linking to the gallery for the rest. */
export function Related({ card }: { card: Entity }) {
  const related = useQuery({
    queryKey: ["card", card.id, "related"],
    queryFn: async () => must(await api.GET("/cards/{id}/related", { params: { path: { id: card.id }, query: { limit: shown } } })),
  });
  const groups = related.data?.groups ?? [];
  if (!related.isError && !groups.length) return null;
  // Search covers one library, so the gallery shows the rest from the card's own library.
  const seeAll = (g: RelatedPage) => `/cards?${toParams({ ...parseQuery(new URLSearchParams(), card.library_id), terms: galleryTerms(g.group, card) })}`;
  return (
    <section>
      <h2>Related</h2>
      {related.isError && <p className="error">{related.error.message}</p>}
      {groups.map((g) => (
        <div key={g.group}>
          <h3>{groupNames[g.group]}</h3>
          <CardList items={g.items ?? []} library={card.library_id} />
          {g.next_cursor && <Link to={seeAll(g)}>See all</Link>}
        </div>
      ))}
    </section>
  );
}
```

Remove the imports that are now unused, such as `useMutation` if nothing else uses it.

- [ ] **Step 4: Render Related on the card page**

In `web/src/card/Card.tsx`, import `Related` beside `Similar`, and render it before Similar:

```tsx
        {view.overlays}
        <Related card={head} />
        <Similar id={head.id} library={head.library_id} />
```

- [ ] **Step 5: Run the tests**

Run: `cd web && npm run typecheck && npx vitest run src/card`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add web/src/card
git commit -m "feat(web): Related moves under the tabs, capped at 8 with gallery links"
```

---

### Task 8: Check everything

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass. `go test` must report `ok` for `internal/library`, not `[no tests to run]` or skips.

- [ ] **Step 2: Look at it in the app**

Use the `run` skill to start the app. Open a card with sources, sync, and enrichments, and check:
- The panel shows Identity, then Links (open), Sync (open), and Collections (closed).
- The tab bar shows Fields, Review, Report, and Metadata, with Reforge ⌘K on the right.
- Metadata shows the core facts, then a block for each enrich instance. Crucible's contradictions and suggestions show as full-width bullets or paragraphs.
- Related and Similar sit below the tabs with at most 8 tiles each. See all opens the gallery with a `creator:` or `lorebook:` chip that names the lorebook.
- A lorebook page shows Links, Sync, and a Metadata tab with core facts only.
- At a phone width, the panel stacks above the main column.

- [ ] **Step 3: Read the diff**

Run: `git diff origin/develop --stat`, then read the full diff. Look for dead code (`LibraryName`, `RelatedGroup`, `.forge-tabs`, `.enrichment-list`), stale comments, and edits outside this plan.
