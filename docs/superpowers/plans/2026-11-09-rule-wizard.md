# Rule Wizard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add and edit rules on the Rules page in four steps (Match, Do, Where, Review), building and saving the rule's search in step 1.

**Architecture:** Web only; the API doesn't change. Step 1 embeds the Cards page's filter sidebar (`Facets`) on local state and saves the search with `POST /saved-searches`; the rest of the wizard works on a `saved_search_id` as the old editor did. A shared hook gives `Facets` and the term chips their inputs on both pages.

**Tech Stack:** React 19, TypeScript, TanStack Query, react-router, Vitest with Testing Library.

Spec: `docs/superpowers/specs/2026-11-09-rule-wizard-design.md`.

## Files

| File | Responsibility |
|---|---|
| `web/src/cards/filters.tsx` (new) | `useFilterInputs`: the keys, names, and browsable keys the sidebar and chips need. `TermChips`: the removable term chips. |
| `web/src/cards/Cards.tsx` | Uses both instead of its own copies |
| `web/src/rules/MatchStep.tsx` (new) | Step 1: build and save a search, or pick a saved one |
| `web/src/rules/RuleWizard.tsx` (new) | The steps, Do, Where, Review, save and preview; replaces `RuleEditor.tsx` |
| `web/src/rules/RuleEditor.tsx` | Deleted |
| `web/src/App.tsx` | Routes `rules/new` and `rules/:id` to `RuleWizard` |
| `web/src/rules/Rules.css` | Step nav and the build layout |
| `web/src/rules/Rules.test.tsx` | Wizard tests, replacing the editor's |
| `docs/using.md` | Building a rule's search on the Rules page |

Run web checks from `web/`: `npm run typecheck && npm test`. Don't run Prettier; the repo doesn't use it.

---

### Task 1: Share the sidebar's inputs and the term chips

A refactor: the Cards page's tests cover it.

**Files:**
- Create: `web/src/cards/filters.tsx`
- Modify: `web/src/cards/Cards.tsx`

- [ ] **Step 1: Create `web/src/cards/filters.tsx`**

Move `scoreLabel` and `termLabel` from `Cards.tsx` (around lines 55-73) here unchanged, and add:

```tsx
import { useQuery } from "@tanstack/react-query";
import { collectionsQuery, enrichmentKeysQuery, instancesQuery, namesQuery, pluginsQuery } from "../api/queries";
import { text } from "../card/draft";
import { Icon } from "../shell/Icon";
import { apps } from "../sync/Logo";
import { stateLabel } from "../sync/sync";
import { enrichmentValue, parseScore, toggleTerm, valueLabel, type CardQuery, type Term } from "./query";
import type { Names } from "./Tile";

// scoreLabel and termLabel, moved from Cards.tsx.

/** useFilterInputs loads what the filter sidebar and its chips need for q: the enrichment keys, the names terms show, and the browsable keys a scored instance offers. */
export function useFilterInputs(q: CardQuery, keysEnabled = true) {
  const keys = useQuery({ ...enrichmentKeysQuery, enabled: keysEnabled });
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const collections = useQuery(collectionsQuery(q.library));
  const lorebookIds = q.terms.filter((t) => t.kind === "lorebook").map((t) => Number(t.value));
  const lorebooks = useQuery(namesQuery("lorebook", q.library, lorebookIds));
  const enrichers = keys.data?.instances ?? [];
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.label])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
    lorebooks: new Map((lorebooks.data?.hits ?? []).map((h) => [h.id, text(h.fields.name)])),
    logos: apps(instances.data, plugins.data),
  };
  const browse = (keys.data?.browse ?? []).filter((b) => b.instances?.some((id) => enrichers.some((e) => e.id === id && e.scored)));
  return { keys, instances, plugins, enrichers, names, browse };
}

/** TermChips shows q's terms as chips that remove themselves. */
export function TermChips({ q, names, onChange }: { q: CardQuery; names: Names; onChange: (q: CardQuery) => void }) {
  return q.terms.map((t) => (
    <button
      key={`${t.not ? "-" : ""}${t.kind}:${t.value}`}
      className={t.not ? "chip excluded" : "chip"}
      aria-label={`Remove ${termLabel(t, names)}`}
      onClick={() => onChange(toggleTerm(q, t))}
    >
      {termLabel(t, names)}
      <Icon name="close" />
    </button>
  ));
}
```

- [ ] **Step 2: Use them in `Cards.tsx`**

- Call `const filters = useFilterInputs(asked, kind === "card");` where `keys` is declared now. Pass `asked`, not `q`: `q` depends on the keys, and `asked` has the same library and lorebook terms.
- Replace `keys`, `enrichers`, `instances`, `plugins`, `collections`, `lorebookIds`, `lorebooks`, and `names` with `filters.keys`, `filters.enrichers`, `filters.instances`, `filters.plugins`, and `filters.names`. Keep `const browse = filters.keys.data?.browse ?? [];` for `withoutUnnamed` and the score sorts.
- `<Facets ... browse={filters.browse} />`.
- Replace the `q.terms.map(...)` chip buttons with `<TermChips q={q} names={filters.names} onChange={go} />`.
- Delete the moved functions and any imports left unused.

- [ ] **Step 3: Check**

Run: `cd web && npm run typecheck && npm test`
Expected: typecheck clean; every test passes, the Cards tests unchanged.

- [ ] **Step 4: Commit**

```bash
git add web/src/cards/filters.tsx web/src/cards/Cards.tsx
git commit -m "refactor(web): share the filter sidebar's inputs and term chips"
```

---

### Task 2: The rule wizard

**Files:**
- Create: `web/src/rules/MatchStep.tsx`, `web/src/rules/RuleWizard.tsx`
- Delete: `web/src/rules/RuleEditor.tsx`
- Modify: `web/src/App.tsx`, `web/src/rules/Rules.css`, `web/src/rules/Rules.test.tsx`

- [ ] **Step 1: Write the failing tests**

In `web/src/rules/Rules.test.tsx`:

- Import `RuleWizard` from `./RuleWizard` in place of `RuleEditor`, and use it in `renderAt`'s `/rules/new` and `/rules/:id` routes.
- In the `beforeEach` fetch mock, before the existing `/api/saved-searches` line, answer the create, the search count, and collections:

```ts
      if (path === "/api/saved-searches" && req.method === "POST") {
        const { name } = body as { name: string };
        if (name === "Taken")
          return new Response(JSON.stringify({ status: 409, title: "Conflict", detail: "a saved search named Taken already exists" }), {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          });
        return json({ id: 4, name, filter: {}, sort: "", created_at: "" });
      }
      if (path === "/api/search") return json({ total: 2, hits: [] });
```

  Find the path `collectionsQuery` fetches in `web/src/api/queries.ts` and answer it with `json([{ id: 7, name: "Faves" }])` plus whatever other fields the schema requires.
- Add a helper beside `renderAt`:

```ts
/** noSearches makes GET /api/saved-searches answer none. */
function noSearches() {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET" ? json([]) : fetch(req),
  );
}
```

- Replace the tests `a new rule starts off, on the saved search it was added from`, `a rule on no library can't be added`, `Add rule on the Rules page opens a new rule`, and `a new rule without saved searches says where to make one` with:

```ts
test("a new rule on a picked saved search starts off", async () => {
  const router = renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("textbox", { name: /^Name/ })).toHaveValue("Junk");
  await user.clear(screen.getByRole("textbox", { name: /^Name/ }));
  await user.type(screen.getByRole("textbox", { name: /^Name/ }), "Hide junk");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([{ method: "POST", path: "/api/rules", body: { name: "Hide junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: false } }]),
  );
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules"));
});

test("a built search is saved, then the rule on it", async () => {
  noSearches();
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: /Shares a source/ }));
  expect(await screen.findByText("2 cards match in this library.")).toBeInTheDocument();
  await user.type(screen.getByRole("textbox", { name: "Search name" }), "Twins");
  await user.click(screen.getByRole("button", { name: "Save and continue" }));
  await user.click(await screen.findByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("textbox", { name: /^Name/ })).toHaveValue("Twins");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/saved-searches", body: { name: "Twins", filter: { and: [{ shares_source: true }] }, sort: "" } },
      { method: "POST", path: "/api/rules", body: { name: "Twins", saved_search_id: 4, action: "archive", all_libraries: true, enabled: false } },
    ]),
  );
});

test("a taken search name shows on step 1", async () => {
  noSearches();
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: /Shares a source/ }));
  await user.type(screen.getByRole("textbox", { name: "Search name" }), "Taken");
  await user.click(screen.getByRole("button", { name: "Save and continue" }));
  expect(await screen.findByText(/already exists/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save and continue" })).toBeInTheDocument();
});

test("Save and turn on previews, then adds the rule and turns it on", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  for (let i = 0; i < 3; i++) await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Save and turn on" }));
  await user.click(await screen.findByRole("button", { name: "Turn on" }));
  await waitFor(() =>
    expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview", "POST /api/rules", "PUT /api/rules/9"]),
  );
  expect((calls.at(-1)!.body as { enabled: boolean }).enabled).toBe(true);
});

test("a collect rule skips Where", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.selectOptions(screen.getByRole("combobox", { name: /Action/ }), "collect");
  await user.selectOptions(await screen.findByRole("combobox", { name: /Collection/ }), "7");
  expect(screen.queryByRole("button", { name: /Where/ })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("textbox", { name: /^Name/ })).toBeInTheDocument();
});

test("a rule on no library can't go on to Review", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("checkbox", { name: /All libraries/ }));
  expect(screen.getByText("Pick at least one library.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: "Main" }));
  expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
});

test("editing a rule opens at Review, with its action fixed", async () => {
  renderAt("/rules/9");
  const user = userEvent.setup();
  expect(await screen.findByRole("textbox", { name: /^Name/ })).toHaveValue("Hide junk");
  await user.click(screen.getByRole("button", { name: "2. Do" }));
  expect(screen.getByRole("combobox", { name: /Action/ })).toBeDisabled();
});

test("Add rule on the Rules page opens the wizard", async () => {
  renderAt("/rules");
  await userEvent.click(await screen.findByRole("link", { name: "Add rule" }));
  expect(await screen.findByRole("heading", { name: "New rule" })).toBeInTheDocument();
});

test("with no saved searches, Match opens on Build", async () => {
  noSearches();
  renderAt("/rules/new");
  expect(await screen.findByRole("checkbox", { name: /Shares a source/ })).toBeInTheDocument();
});
```

Keep `turning a rule on previews it and saves only on confirm` and `saving an enabled rule previews it before the PUT` as they are.

- [ ] **Step 2: Run them to see them fail**

Run: `cd web && npx vitest run src/rules/Rules.test.tsx`
Expected: FAIL, `./RuleWizard` doesn't exist.

- [ ] **Step 3: Create `web/src/rules/MatchStep.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, must } from "../api/client";
import { savedSearchesQuery, searchQuery } from "../api/queries";
import { Facets } from "../cards/Facets";
import { TermChips, useFilterInputs } from "../cards/filters";
import { filterFor, parseQuery, toSearch, type CardQuery } from "../cards/query";
import "../cards/Cards.css";
import { count } from "../shell/count";

type Picked = { id: number; name: string };
type Props = { library: number; picked?: number; onPick: (s: Picked) => void; onSaved: (s: Picked) => void; onNext: () => void };

/** MatchStep picks the rule's saved search, or builds one with the Cards page's filters and saves it. */
export function MatchStep({ library, picked, onPick, onSaved, onNext }: Props) {
  const searches = useQuery(savedSearchesQuery(library));
  const [chosen, setChosen] = useState<"build" | "saved">();
  if (searches.error) return <p className="error">{searches.error.message}</p>;
  if (!searches.data) return null;
  const mode = chosen ?? (searches.data.length ? "saved" : "build");
  return (
    <div className="rule-match">
      <div className="row" role="radiogroup" aria-label="How to match">
        <label className="check">
          <input type="radio" name="match" checked={mode === "build"} onChange={() => setChosen("build")} /> Build a search
        </label>
        <label className="check">
          <input type="radio" name="match" checked={mode === "saved"} onChange={() => setChosen("saved")} /> Use a saved search
        </label>
      </div>
      {mode === "build" ? (
        <Build library={library} onSaved={onSaved} />
      ) : (
        <fieldset>
          <legend>Saved search</legend>
          {searches.data.length === 0 && <p className="muted">No saved searches yet.</p>}
          {searches.data.map((s) => (
            <label key={s.id} className="check">
              <input type="radio" name="saved-search" checked={picked === s.id} onChange={() => onPick({ id: s.id, name: s.name })} /> {s.name}
            </label>
          ))}
          <button className="button primary" disabled={!picked} onClick={onNext}>
            Next
          </button>
        </fieldset>
      )}
    </div>
  );
}

/** Build is the Cards page's filters on local state, saved as a search under the name given. */
function Build({ library, onSaved }: { library: number; onSaved: (s: Picked) => void }) {
  const qc = useQueryClient();
  const [q, setQ] = useState<CardQuery>(() => parseQuery(new URLSearchParams(), library));
  const [name, setName] = useState("");
  const inputs = useFilterInputs(q);
  const filter = filterFor(q.terms, q.sharesSource, q.all);
  const matches = useQuery({ ...searchQuery("card", { ...toSearch(q), page: { size: 1, number: 1 } }), enabled: filter !== undefined });
  const save = useMutation({
    mutationFn: async () => must(await api.POST("/saved-searches", { body: { name: name.trim(), filter: filter!, sort: "" } })),
    onSuccess: (s) => {
      void qc.invalidateQueries({ queryKey: ["saved-searches"] });
      onSaved({ id: s.id, name: s.name });
    },
    meta: { toast: false },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <div className="rule-build">
      <Facets kind="card" q={q} names={inputs.names} browse={inputs.browse} onChange={setQ} />
      <form className="rule-form" onSubmit={submit}>
        <div className="chips">
          <TermChips q={q} names={inputs.names} onChange={setQ} />
        </div>
        <p role="status">
          {!filter
            ? "Pick filters for the cards the rule acts on."
            : matches.data
              ? `${count(matches.data.total, "card")} match in this library.`
              : "Counting cards…"}
        </p>
        <label>
          Search name <input className="input" value={name} required onChange={(e) => setName(e.target.value)} />
        </label>
        {save.error && <p className="error">{save.error.message}</p>}
        <button className="button primary" disabled={!filter || save.isPending}>
          Save and continue
        </button>
      </form>
    </div>
  );
}
```

If the chips' wrapper on the Cards page has a different class than `chips`, use that one.

- [ ] **Step 4: Create `web/src/rules/RuleWizard.tsx`**

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { api, must, mustOk } from "../api/client";
import { collectionsQuery, librariesQuery, savedSearchesQuery } from "../api/queries";
import { useConfirm } from "../shell/ConfirmDialog";
import { useLibrary } from "../shell/library";
import { MatchStep } from "./MatchStep";
import { RulePreview } from "./RulePreview";
import { actionLabel, specOf, type Spec } from "./Rules";
import "./Rules.css";

type Step = "match" | "do" | "where" | "review";
const stepLabels: Record<Step, string> = { match: "Match", do: "Do", where: "Where", review: "Review" };

/** RuleWizard adds a rule step by step, on the saved search ?search= names if any, or edits rule :id from its review. */
export function RuleWizard() {
  const raw = useParams().id;
  const id = raw === undefined ? undefined : Number(raw);
  const [params] = useSearchParams();
  const library = useLibrary();
  const rule = useQuery({
    queryKey: ["rules", "one", id],
    queryFn: async () => must(await api.GET("/rules/{id}", { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  });
  if (rule.error) return <p className="error">{rule.error.message}</p>;
  if (id !== undefined && !rule.data) return null;
  const initial: Spec = rule.data
    ? specOf(rule.data)
    : { name: "", saved_search_id: Number(params.get("search")) || 0, action: "archive", all_libraries: true, enabled: false };
  return <Wizard key={id ?? "new"} id={id} initial={initial} library={library} />;
}

function Wizard({ id, initial, library }: { id?: number; initial: Spec; library: number }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [ask, dialog] = useConfirm();
  const [spec, setSpec] = useState(initial);
  const [step, setStep] = useState<Step>(id === undefined ? "match" : "review");
  const [previewing, setPreviewing] = useState(false);
  const searches = useQuery(savedSearchesQuery(library));
  const libraries = useQuery(librariesQuery);
  const collectLibrary = spec.libraries?.[0] ?? library;
  const collections = useQuery({ ...collectionsQuery(collectLibrary), enabled: spec.action === "collect" });
  const set = (change: Partial<Spec>) => setSpec({ ...spec, ...change });
  // A rule's action and collection can't change.
  const fixed = id !== undefined;
  // A collect rule covers its collection's library alone.
  const steps: Step[] = spec.action === "collect" ? ["match", "do", "review"] : ["match", "do", "where", "review"];
  const searchName = searches.data?.find((s) => s.id === spec.saved_search_id)?.name;
  const noLibrary = !spec.all_libraries && !spec.libraries?.length;
  const done: Record<Step, boolean> = {
    match: spec.saved_search_id > 0,
    do: spec.action === "archive" || spec.collection_id !== undefined,
    where: !noLibrary,
    review: true,
  };
  const reachable = (s: Step) => steps.slice(0, steps.indexOf(s)).every((before) => done[before]);
  const next = () => {
    const to = steps[steps.indexOf(step) + 1]!;
    // The rule takes its search's name until the owner names it.
    if (to === "review" && !spec.name && searchName) set({ name: searchName });
    setStep(to);
  };
  const leave = () => {
    void qc.invalidateQueries({ queryKey: ["rules"] });
    void qc.invalidateQueries({ queryKey: ["saved-searches"] });
    void navigate("/rules");
  };
  const save = useMutation({
    mutationFn: async ({ s, on }: { s: Spec; on: boolean }) => {
      if (id !== undefined) return must(await api.PUT("/rules/{id}", { params: { path: { id } }, body: s }));
      // A new rule starts off, so turning it on is a second call.
      const added = must(await api.POST("/rules", { body: s }));
      if (!on) return added;
      return must(await api.PUT("/rules/{id}", { params: { path: { id: added.id } }, body: { ...specOf(added), enabled: true } }));
    },
    onSuccess: (r) => {
      toast.success(id !== undefined ? `Saved ${r.name}` : r.enabled ? `Added ${r.name} and turned it on` : `Added ${r.name}; it's off until you turn it on`);
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
    if (fixed && spec.enabled) setPreviewing(true);
    else save.mutate({ s: spec, on: false });
  };
  const toggleLibrary = (lib: number, on: boolean) =>
    set({ libraries: on ? [...(spec.libraries ?? []), lib] : (spec.libraries ?? []).filter((l) => l !== lib) });
  const libraryName = (lib: number) => libraries.data?.find((l) => l.id === lib)?.name ?? `Library ${lib}`;
  const title = id === undefined ? "New rule" : initial.name;
  const nextButton = (
    <button className="button primary" disabled={!done[step]} onClick={next}>
      Next
    </button>
  );
  return (
    <section className="page">
      <p className="muted">
        <Link to="/rules">Rules</Link> / {title}
      </p>
      <h1>{title}</h1>
      <nav className="steps" aria-label="Steps">
        {steps.map((s, i) => (
          <button
            key={s}
            className={s === step ? "button primary" : "button"}
            aria-current={s === step ? "step" : undefined}
            disabled={!reachable(s)}
            onClick={() => setStep(s)}
          >
            {i + 1}. {stepLabels[s]}
          </button>
        ))}
      </nav>
      {step === "match" && (
        <MatchStep
          library={library}
          picked={spec.saved_search_id || undefined}
          onPick={(s) => set({ saved_search_id: s.id })}
          onSaved={(s) => {
            setSpec({ ...spec, saved_search_id: s.id, name: spec.name || s.name });
            setStep("do");
          }}
          onNext={next}
        />
      )}
      {step === "do" && (
        <div className="rule-form">
          <label>
            Action{" "}
            <select
              className="input"
              value={spec.action}
              disabled={fixed}
              onChange={(e) => {
                const action = e.target.value as Spec["action"];
                const scope = action === "collect" ? { all_libraries: false, libraries: [library] } : { all_libraries: true, libraries: undefined };
                setSpec({ ...spec, action, collection_id: undefined, ...scope });
              }}
            >
              <option value="archive">Archive</option>
              <option value="collect">Add to a collection</option>
            </select>
          </label>
          {spec.action === "collect" && (
            <>
              <label>
                Library{" "}
                <select className="input" value={collectLibrary} disabled={fixed} onChange={(e) => set({ libraries: [Number(e.target.value)], collection_id: undefined })}>
                  {(libraries.data ?? []).map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Collection{" "}
                <select className="input" value={spec.collection_id ?? ""} required disabled={fixed} onChange={(e) => set({ collection_id: Number(e.target.value) })}>
                  <option value="">Pick one</option>
                  {(collections.data ?? []).map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </label>
            </>
          )}
          {nextButton}
        </div>
      )}
      {step === "where" && (
        <div className="rule-form">
          <fieldset>
            <legend>Libraries</legend>
            <label className="check">
              <input type="checkbox" checked={spec.all_libraries} onChange={(e) => set({ all_libraries: e.target.checked })} /> All libraries, new ones included
            </label>
            {!spec.all_libraries &&
              (libraries.data ?? []).map((l) => (
                <label key={l.id} className="check">
                  <input type="checkbox" checked={spec.libraries?.includes(l.id) ?? false} onChange={(e) => toggleLibrary(l.id, e.target.checked)} /> {l.name}
                </label>
              ))}
            {noLibrary && <p className="muted">Pick at least one library.</p>}
          </fieldset>
          {nextButton}
        </div>
      )}
      {step === "review" && (
        <form className="rule-form" onSubmit={submit}>
          <label>
            Name <input className="input" value={spec.name} required onChange={(e) => set({ name: e.target.value })} />
          </label>
          <dl className="rule-summary">
            <dt>Matches</dt>
            <dd>{searchName ?? `Saved search ${spec.saved_search_id}`}</dd>
            <dt>Does</dt>
            <dd>{actionLabel(spec)}</dd>
            <dt>In</dt>
            <dd>{spec.all_libraries ? "All libraries" : (spec.libraries ?? []).map(libraryName).join(", ")}</dd>
          </dl>
          {fixed && (
            <label className="check">
              <input type="checkbox" checked={spec.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> On
            </label>
          )}
          {save.error && <p className="error">{save.error.message}</p>}
          <div className="row">
            <button className="button primary" disabled={save.isPending}>
              Save
            </button>
            {!fixed && (
              <button type="button" className="button" disabled={save.isPending || !spec.name.trim()} onClick={() => setPreviewing(true)}>
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
      )}
      {previewing && (
        <RulePreview
          id={id}
          spec={{ ...spec, enabled: true }}
          confirmLabel={fixed ? "Save rule" : "Turn on"}
          onConfirm={() => save.mutate({ s: spec, on: !fixed })}
          onClose={() => setPreviewing(false)}
        />
      )}
      {dialog}
    </section>
  );
}
```

- [ ] **Step 5: Route to it, delete the editor, and style it**

In `web/src/App.tsx`, import `RuleWizard` from `./rules/RuleWizard` in place of `RuleEditor`, and use it for `rules/new` and `rules/:id`. Then:

```bash
git rm web/src/rules/RuleEditor.tsx
```

Append to `web/src/rules/Rules.css`:

```css
.steps {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.rule-build {
  display: grid;
  grid-template-columns: minmax(0, 16rem) minmax(0, 1fr);
  gap: 1rem;
}

@media (max-width: 40rem) {
  .rule-build {
    grid-template-columns: minmax(0, 1fr);
  }
}

.rule-summary {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.25rem 1rem;
}
```

- [ ] **Step 6: Run the tests**

Run: `cd web && npm run typecheck && npm test`
Expected: typecheck clean; every test passes.

- [ ] **Step 7: Commit**

```bash
git add web/src/App.tsx web/src/rules
git commit -m "feat(web): add and edit rules in a wizard that can build its search"
```

---

### Task 3: Docs

**Files:**
- Modify: `docs/using.md`

- [ ] **Step 1: Say where rules start**

In `docs/using.md`, replace the line

```
Add one with Add rule on the Rules page, or from a saved search.
```

with

```
Add one with Add rule on the Rules page, which can build and save its
search there, or with Add rule on a saved search.
```

- [ ] **Step 2: Commit**

```bash
git add docs/using.md
git commit -m "docs: rules can build their search on the Rules page"
```
