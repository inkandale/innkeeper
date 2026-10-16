# 37e: Archive Web UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Archive page, the archive item view, and the "Found in archives" panel to the web UI, so the owner can search catalogs, adopt items, run ingests, and see where library entries appear in archives.

**Architecture:** The UI is web-only. Every route it needs (`/catalogs`, search, get, adopt, ingest, `catalog-matches`) is already in core and in `web/src/api/schema.d.ts`. All new code lives in `web/src/archives/`. The page keeps its catalog, kind, search text, and filters in the URL. Search results use the `["search", ...]` query key, so `useJobEvents` refreshes them when an ingest finishes.

**Tech Stack:** React 19, TanStack Query 5, react-router 8, openapi-fetch, Vitest with Testing Library.

Spec: `docs/superpowers/specs/2026-10-09-archive-catalog-design.md`, build step 5 ("Web UI" section).

---

## Facts the tasks rely on

- `GET /catalogs` returns `CatalogOut[]`: `id`, `name`, `plugin`, `info?` (`kinds`, `filters`, `sorts`, `ingest`, `similar`), `error?` (why `describe` failed), and `last_ingest?` (a `JobOut`).
- `GET /catalogs/{id}/search` takes `q`, `kind`, `filters` (one JSON object as a string), `sort`, `size`, and `cursor`. It returns `{ catalog_id, items: CatalogHit[], next? }`. Each hit's `library` is a `HashMatch[]` (`id`, `reason`: `content` | `text` | `avatar`, `distance`), closest first. `content` means an exact match.
- `thumbnail` is a base64 JPEG. Show it as `data:image/jpeg;base64,<thumbnail>`.
- Filter values (`protocol/catalog.go`): `enum` and `text` take a non-empty string. `date_range` takes `{from?, to?}` as `YYYY-MM-DD`, and `number_range` takes `{min?, max?}`. A range needs at least one end. The server refuses an empty value, so the UI leaves an empty filter out.
- `GET /catalogs/{id}/items/{item}?version=` returns `{ item, version, versions, data }`. `versions` is newest first. For a card, `data` is a CCv3 card (`{ spec, data: {...} }`); for a lorebook, it's a `character_book` (`{ name, entries: [...] }`). The route never returns an avatar, so the item view shows the thumbnail.
- `POST /catalogs/{id}/items/{item}/adopt?version=` returns an `EntityOut`: 201 when new, 200 when an earlier adopt made it.
- `POST /catalogs/{id}/ingest` returns a `JobOut` with 202, or 409 while an ingest is unfinished.
- A finished ingest's `progress` holds `added`, `updated`, `removed`, and `skipped`.
- `GET /cards/{id}/catalog-matches` (and the lorebook route) returns `{ matches: [{ catalog, catalog_id, items }], unreachable: [{ id, name, error }] }`.
- Item IDs can contain `/` (a `folder` item is a relative path). Links put the item ID in one URL segment with `encodeURIComponent`. openapi-fetch escapes path parameters the same way, so the API sees `%2F`, as the route expects.
- A failed mutation already shows a toast through `Toasts`. A failed query doesn't, so pages render the query's error inline.
- `Panel.tsx` already exports a component named `Archive` (the archive-a-card toggle). The new code uses `Archives`, `ArchiveItem`, and `FoundInArchives` so the names don't collide.

## Files

| File | Responsibility |
|---|---|
| Create `web/src/archives/archive.ts` | Types and small pure helpers: item path, library-match label, thumbnail URL, ingest summary |
| Create `web/src/archives/archive.test.ts` | Tests for those helpers |
| Create `web/src/archives/FilterBar.tsx` | Inputs built from a catalog's declared filters |
| Create `web/src/archives/Archives.tsx` | The Archive page: picker, kind switch, search, filter bar, results, ingest status |
| Create `web/src/archives/Archives.test.tsx` | Page tests: filter bar from `describe`, library badges, inline errors, ingest |
| Create `web/src/archives/ArchiveItem.tsx` | Read-only item view with a version picker and Adopt |
| Create `web/src/archives/ArchiveItem.test.tsx` | Adopt flow and escaped item IDs |
| Create `web/src/archives/FoundInArchives.tsx` | The card and lorebook panel |
| Create `web/src/archives/FoundInArchives.test.tsx` | Panel tests |
| Create `web/src/archives/Archives.css` | Layout for the bar, badges, and item text |
| Modify `web/src/api/queries.ts` | Add `catalogsQuery` |
| Modify `web/src/App.tsx` | Add the `archives` and `archives/:catalog/items/:item` routes |
| Modify `web/src/shell/Shell.tsx` | Add the Archive section to the rail |
| Modify `web/src/card/Panel.tsx`, `web/src/lorebook/Lorebook.tsx` | Show `FoundInArchives` |

All commands run from `web/`.

---

### Task 1: Helpers

**Files:**
- Create: `web/src/archives/archive.ts`
- Test: `web/src/archives/archive.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import type { Job } from "../settings/jobUtil";
import { ingestSummary, itemPath, libraryLabel, type CatalogHit } from "./archive";

const hit = (library: CatalogHit["library"]): CatalogHit => ({
  id: "a", kind: "card", name: "A", latest_version: "1", hashes: { content_hash: "0".repeat(64) }, library,
});

const job = (over: Partial<Job>): Job => ({
  id: 4, kind: "catalog.ingest", status: "succeeded", attempts: 1, max_attempts: 3,
  created_at: "2026-10-16T00:00:00Z", run_after: "2026-10-16T00:00:00Z", payload: {}, progress: {}, ...over,
});

test("itemPath keeps an item ID with a slash in one segment", () => {
  expect(itemPath(2, "sub/a.png")).toBe("/archives/2/items/sub%2Fa.png");
});

test("libraryLabel says In library for a content match, Similar for a near one, and nothing without matches", () => {
  expect(libraryLabel(hit([{ id: 1, reason: "text", distance: 3 }, { id: 2, reason: "content", distance: 0 }]))).toBe("In library");
  expect(libraryLabel(hit([{ id: 1, reason: "avatar", distance: 5 }]))).toBe("Similar in library");
  expect(libraryLabel(hit([]))).toBeUndefined();
  expect(libraryLabel(hit(null))).toBeUndefined();
});

test("ingestSummary gives a finished ingest's counts, a running one's progress, and a failed one's error", () => {
  expect(ingestSummary(job({ progress: { added: 3, updated: 1, removed: 0, skipped: 2 } }))).toBe("Succeeded: 3 added, 1 updated, 0 removed, 2 skipped");
  expect(ingestSummary(job({ status: "running", progress: { done: 10, total: 40 } }))).toBe("Running: 10 of 40");
  expect(ingestSummary(job({ status: "queued", progress: {} }))).toBe("Queued");
  expect(ingestSummary(job({ status: "failed", error: "dump not found" }))).toBe("Failed: dump not found");
});
```

- [ ] **Step 2: Run the test to check it fails**

Run: `npx vitest run src/archives/archive.test.ts`
Expected: FAIL, because `./archive` doesn't exist.

- [ ] **Step 3: Write the helpers**

```ts
import type { components } from "../api/schema";
import { progressText, type Job } from "../settings/jobUtil";

export type Catalog = components["schemas"]["CatalogOut"];
export type CatalogHit = components["schemas"]["CatalogHit"];
export type CatalogFilter = components["schemas"]["CatalogFilter"];
/** FilterValues are a search's filter values by filter name, as the search route's filters parameter takes them. */
export type FilterValues = Record<string, unknown>;

/** itemPath is an item's page. Item IDs can hold "/", so the ID is escaped into one segment. */
export const itemPath = (catalog: number, item: string) => `/archives/${catalog}/items/${encodeURIComponent(item)}`;

/** libraryLabel says how a result matches the library: In library for the same content, Similar in library for a near match. */
export function libraryLabel(hit: CatalogHit): string | undefined {
  const matches = hit.library ?? [];
  if (!matches.length) return undefined;
  return matches.some((m) => m.reason === "content") ? "In library" : "Similar in library";
}

/** thumbnailSrc is the image URL for a catalog item's inline base64 JPEG. */
export const thumbnailSrc = (thumbnail: string) => `data:image/jpeg;base64,${thumbnail}`;

const counts = ["added", "updated", "removed", "skipped"] as const;
const capitalized = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** ingestSummary is an ingest job's state: its counts once it succeeds, its progress while it runs, its error if it failed. */
export function ingestSummary(j: Job): string {
  const state = capitalized(j.status);
  if (j.status === "failed") return `${state}: ${j.error ?? "unknown error"}`;
  if (j.status === "succeeded") {
    const p = (j.progress ?? {}) as Record<string, unknown>;
    return `${state}: ${counts.map((c) => `${Number(p[c] ?? 0)} ${c}`).join(", ")}`;
  }
  const done = progressText(j);
  return done ? `${state}: ${done}` : state;
}
```

- [ ] **Step 4: Run the test to check it passes**

Run: `npx vitest run src/archives/archive.test.ts`
Expected: PASS, 3 tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/archives/archive.ts web/src/archives/archive.test.ts
git commit -m "feat(web): add archive helpers for item paths, library matches, and ingest status"
```

---

### Task 2: Filter bar

The filter bar has no test of its own: Task 3's page tests drive it through the page, which is how the owner uses it.

**Files:**
- Create: `web/src/archives/FilterBar.tsx`

- [ ] **Step 1: Write the component**

```tsx
import { useState } from "react";
import type { CatalogFilter, FilterValues } from "./archive";

/** FilterBar shows an input for each filter a catalog declares. An emptied filter is dropped, since the catalog refuses empty values. */
export function FilterBar({ filters, value, onChange }: { filters: CatalogFilter[]; value: FilterValues; onChange: (v: FilterValues) => void }) {
  const set = (name: string, v: unknown) => {
    const next = { ...value };
    if (v === undefined) delete next[name];
    else next[name] = v;
    onChange(next);
  };
  if (!filters.length) return null;
  return (
    <div className="filter-bar" role="group" aria-label="Filters">
      {filters.map((f) => (
        <FilterInput key={f.name} filter={f} value={value[f.name]} set={(v) => set(f.name, v)} />
      ))}
    </div>
  );
}

type InputProps = { filter: CatalogFilter; value: unknown; set: (v: unknown) => void };

function FilterInput({ filter, value, set }: InputProps) {
  switch (filter.type) {
    case "enum":
      return (
        <select className="input" aria-label={filter.label} value={typeof value === "string" ? value : ""} onChange={(e) => set(e.target.value || undefined)}>
          <option value="">{filter.label}: any</option>
          {(filter.options ?? []).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      );
    case "text":
      return <DraftInput label={filter.label} applied={typeof value === "string" ? value : ""} onApply={(s) => set(s || undefined)} />;
    case "date_range":
      return <RangeFilter filter={filter} value={value} set={set} ends={["from", "to"]} type="date" />;
    case "number_range":
      return <RangeFilter filter={filter} value={value} set={set} ends={["min", "max"]} type="number" />;
    default:
      return null;
  }
}

// Applies on Enter or leaving the box, not on every keystroke, so typing doesn't search per letter
// (and a half-typed range end never reaches the server).
function DraftInput({ label, applied, type = "text", onApply }: { label: string; applied: string; type?: "text" | "date" | "number"; onApply: (v: string) => void }) {
  const [draft, setDraft] = useState(applied);
  const [synced, setSynced] = useState(applied);
  if (applied !== synced) {
    setSynced(applied);
    setDraft(applied);
  }
  const apply = () => draft.trim() !== applied && onApply(draft.trim());
  return (
    <input
      className="input"
      type={type}
      aria-label={label}
      placeholder={type === "date" ? undefined : label}
      value={draft}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={apply}
      onKeyDown={(e) => e.key === "Enter" && apply()}
    />
  );
}

function RangeFilter({ filter, value, set, ends, type }: InputProps & { ends: [string, string]; type: "date" | "number" }) {
  const range = (value ?? {}) as Record<string, unknown>;
  const setEnd = (end: string, raw: string) => {
    const next = { ...range };
    if (raw === "") delete next[end];
    else next[end] = type === "number" ? Number(raw) : raw;
    set(Object.keys(next).length ? next : undefined);
  };
  return (
    <span className="range">
      {ends.map((end) => (
        <DraftInput
          key={end}
          label={`${filter.label} ${end}`}
          type={type}
          applied={range[end] === undefined ? "" : String(range[end])}
          onApply={(raw) => setEnd(end, raw)}
        />
      ))}
    </span>
  );
}
```

The `switch` on `filter.type` is the only place the UI turns a filter type into an input. The protocol fixes the four types, so a plugin can't add a fifth.

- [ ] **Step 2: Typecheck**

Run: `npm run typecheck`
Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add web/src/archives/FilterBar.tsx
git commit -m "feat(web): build an archive filter bar from a catalog's declared filters"
```

---

### Task 3: Archive page

**Files:**
- Modify: `web/src/api/queries.ts`
- Create: `web/src/archives/Archives.tsx`, `web/src/archives/Archives.css`
- Test: `web/src/archives/Archives.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Archives } from "./Archives";

function json(body: unknown, status = 200, type = "application/json") {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": type } });
}

const now = "2026-10-16T00:00:00Z";
const info = {
  kinds: ["card", "lorebook"],
  sorts: [{ value: "added", label: "Date added" }],
  ingest: true,
  similar: true,
  filters: [
    { name: "site", label: "Site", type: "enum", options: [{ value: "chub", label: "Chub" }, { value: "booru", label: "Booru" }] },
    { name: "tag", label: "Tag", type: "text" },
    { name: "added", label: "Added", type: "date_range" },
    { name: "rating", label: "Rating", type: "number_range" },
  ],
};
const ingestJob = {
  id: 4, kind: "catalog.ingest", status: "succeeded", attempts: 1, max_attempts: 3, created_at: now, run_after: now,
  payload: {}, progress: { added: 3, updated: 1, removed: 0, skipped: 2 },
};
const hash = { content_hash: "0".repeat(64) };
const hits = [
  { id: "chub:1", kind: "card", name: "Brakka", latest_version: "2", hashes: hash, library: [{ id: 9, reason: "content", distance: 0 }] },
  { id: "chub:2", kind: "card", name: "Tessa", latest_version: "1", hashes: hash, library: [{ id: 5, reason: "text", distance: 4 }] },
  { id: "chub:3", kind: "card", name: "Orrin", latest_version: "1", hashes: hash, library: [] },
];

let searches: URL[];
let searchResponse: () => Response;
let ingests: number;
beforeEach(() => {
  searches = [];
  ingests = 0;
  searchResponse = () => json({ catalog_id: 1, items: hits });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (url.pathname === "/api/catalogs") return json([{ id: 1, name: "chub", plugin: "chub-archive", info, last_ingest: ingestJob }]);
      if (url.pathname === "/api/catalogs/1/search") {
        searches.push(url);
        return searchResponse();
      }
      if (req.method === "POST" && url.pathname === "/api/catalogs/1/ingest") {
        ingests++;
        return json({ ...ingestJob, id: 5, status: "queued", progress: {} }, 202);
      }
      if (url.pathname === "/api/jobs/4") return json(ingestJob);
      if (url.pathname === "/api/jobs/5") return json({ ...ingestJob, id: 5, status: "queued", progress: {} });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter([{ path: "/archives", element: <Toasts><Archives /></Toasts> }], { initialEntries: [entry] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const lastFilters = () => JSON.parse(searches.at(-1)!.searchParams.get("filters") ?? "{}");

test("the filter bar comes from describe, and its values go in the search's filters", async () => {
  renderAt("/archives");
  await screen.findByText("Brakka");
  expect(searches.at(-1)!.searchParams.get("kind")).toBe("card");
  expect(searches.at(-1)!.searchParams.has("filters")).toBe(false);

  await userEvent.selectOptions(screen.getByLabelText("Site"), "chub");
  await waitFor(() => expect(lastFilters()).toEqual({ site: "chub" }));

  await userEvent.type(screen.getByLabelText("Rating min"), "4{Enter}");
  await waitFor(() => expect(lastFilters()).toEqual({ site: "chub", rating: { min: 4 } }));

  await userEvent.type(screen.getByLabelText("Tag"), "elf{Enter}");
  await waitFor(() => expect(lastFilters()).toEqual({ site: "chub", rating: { min: 4 }, tag: "elf" }));

  await userEvent.clear(screen.getByLabelText("Rating min"));
  await userEvent.type(screen.getByLabelText("Rating min"), "{Enter}");
  await waitFor(() => expect(lastFilters()).toEqual({ site: "chub", tag: "elf" }));
  expect(screen.getByLabelText("Added from")).toHaveAttribute("type", "date");
});

test("the kind switch searches lorebooks", async () => {
  renderAt("/archives");
  await screen.findByText("Brakka");
  await userEvent.click(screen.getByRole("button", { name: "Lorebooks" }));
  await waitFor(() => expect(searches.at(-1)!.searchParams.get("kind")).toBe("lorebook"));
});

test("results say whether the library has each item, and link to the match", async () => {
  renderAt("/archives");
  expect(await screen.findByRole("link", { name: "In library" })).toHaveAttribute("href", "/cards/9");
  expect(screen.getByRole("link", { name: "Similar in library" })).toHaveAttribute("href", "/cards/5");
  expect(screen.getAllByRole("link", { name: /library/ })).toHaveLength(2);
  expect(screen.getAllByRole("link", { name: "Brakka" })[0]).toHaveAttribute("href", "/archives/1/items/chub%3A1");
});

test("a catalog that is down shows its error on the page", async () => {
  searchResponse = () => json({ status: 503, detail: "catalog chub is not answering" }, 503, "application/problem+json");
  renderAt("/archives");
  expect(await screen.findByText("catalog chub is not answering")).toBeInTheDocument();
});

test("the ingest line shows the last run's counts and starts a new run", async () => {
  renderAt("/archives");
  expect(await screen.findByText("Succeeded: 3 added, 1 updated, 0 removed, 2 skipped")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Ingest" }));
  await waitFor(() => expect(ingests).toBe(1));
  expect(await screen.findByText("Ingest started")).toBeInTheDocument();
  expect(await screen.findByText("Queued")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Ingest" })).toBeDisabled();
});

test("More fetches the next page with the cursor", async () => {
  searchResponse = () =>
    searches.at(-1)!.searchParams.get("cursor") === "c2"
      ? json({ catalog_id: 1, items: [hits[1]] })
      : json({ catalog_id: 1, items: [hits[0]], next: "c2" });
  renderAt("/archives");
  await screen.findByText("Brakka");
  await userEvent.click(screen.getByRole("button", { name: "More" }));
  expect(await screen.findByText("Tessa")).toBeInTheDocument();
  expect(searches.at(-1)!.searchParams.get("cursor")).toBe("c2");
  expect(screen.getByText("Brakka")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run the tests to check they fail**

Run: `npx vitest run src/archives/Archives.test.tsx`
Expected: FAIL, because `./Archives` doesn't exist.

- [ ] **Step 3: Add `catalogsQuery` to `web/src/api/queries.ts`**

Add it after `settingsQuery`:

```ts
export const catalogsQuery = queryOptions({ queryKey: ["catalogs"], queryFn: async () => must(await api.GET("/catalogs")) });
```

- [ ] **Step 4: Write the page**

`web/src/archives/Archives.tsx`:

```tsx
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { catalogsQuery } from "../api/queries";
import { finished, jobToast, useJob } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { ingestSummary, itemPath, libraryLabel, thumbnailSrc, type Catalog, type CatalogHit, type FilterValues } from "./archive";
import { FilterBar } from "./FilterBar";
import "../cards/Cards.css";
import "./Archives.css";

/** readFilters parses the filters URL parameter, ignoring one that isn't a JSON object. */
function readFilters(raw: string | null): FilterValues {
  try {
    const v: unknown = JSON.parse(raw ?? "{}");
    return v && typeof v === "object" && !Array.isArray(v) ? (v as FilterValues) : {};
  } catch {
    return {};
  }
}

const plural = (kind: string) => kind.charAt(0).toUpperCase() + kind.slice(1) + "s";

/** Archives is the Archive page. The catalog, kind, search text, and filters live in the URL. */
export function Archives() {
  const [params, setParams] = useSearchParams();
  const catalogs = useQuery(catalogsQuery);
  if (catalogs.isPending) return null;
  if (catalogs.isError) return <p className="error page">{catalogs.error.message}</p>;
  const list = catalogs.data ?? [];
  if (!list.length)
    return (
      <section className="page">
        <h1>Archive</h1>
        <p>
          No archives yet. <Link to="/settings/plugins">Add a catalog plugin</Link> to search one.
        </p>
      </section>
    );
  const catalog = list.find((c) => c.id === Number(params.get("catalog"))) ?? list[0]!;
  const go = (patch: Record<string, string | undefined>) =>
    setParams((p) => {
      for (const [k, v] of Object.entries(patch)) {
        if (v) p.set(k, v);
        else p.delete(k);
      }
      return p;
    });
  return (
    <section className="page archives">
      <div className="toolbar">
        <select
          className="input"
          aria-label="Archive"
          value={catalog.id}
          // Filters and kinds belong to one catalog, so switching drops them.
          onChange={(e) => go({ catalog: e.target.value, kind: undefined, filters: undefined })}
        >
          {list.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        {catalog.info?.ingest && <IngestStatus key={catalog.id} catalog={catalog} />}
      </div>
      {catalog.info ? (
        <Search key={catalog.id} catalog={catalog} info={catalog.info} params={params} go={go} />
      ) : (
        <p className="error">{catalog.error ?? "This archive didn't describe itself."}</p>
      )}
    </section>
  );
}

type SearchProps = {
  catalog: Catalog;
  info: NonNullable<Catalog["info"]>;
  params: URLSearchParams;
  go: (patch: Record<string, string | undefined>) => void;
};

function Search({ catalog, info, params, go }: SearchProps) {
  const kinds = info.kinds ?? [];
  const asked = params.get("kind");
  const kind = asked && kinds.includes(asked) ? asked : (kinds[0] ?? "");
  const q = params.get("q") ?? "";
  const filters = readFilters(params.get("filters"));
  const filtersParam = Object.keys(filters).length ? JSON.stringify(filters) : undefined;
  // The "search" prefix lets a finished ingest's job events refresh these results.
  const res = useInfiniteQuery({
    queryKey: ["search", "catalog", catalog.id, { kind, q, filtersParam }],
    initialPageParam: "",
    queryFn: async ({ pageParam }) =>
      must(
        await api.GET("/catalogs/{id}/search", {
          params: {
            path: { id: catalog.id },
            query: { kind: kind || undefined, q: q || undefined, filters: filtersParam, cursor: pageParam || undefined },
          },
        }),
      ),
    getNextPageParam: (last) => last.next || undefined,
  });
  const items = res.data?.pages.flatMap((p) => p.items ?? []) ?? [];
  return (
    <>
      <div className="toolbar">
        {kinds.length > 1 && (
          <span role="group" aria-label="Kind">
            {kinds.map((k) => (
              <button key={k} className={k === kind ? "button primary" : "button"} aria-pressed={k === kind} onClick={() => go({ kind: k })}>
                {plural(k)}
              </button>
            ))}
          </span>
        )}
        <form
          role="search"
          onSubmit={(e) => {
            e.preventDefault();
            go({ q: String(new FormData(e.currentTarget).get("q") ?? "").trim() || undefined });
          }}
        >
          <input key={q} className="input" type="search" name="q" defaultValue={q} placeholder={`Search ${catalog.name}`} aria-label={`Search ${catalog.name}`} />
        </form>
      </div>
      <FilterBar filters={info.filters ?? []} value={filters} onChange={(v) => go({ filters: Object.keys(v).length ? JSON.stringify(v) : undefined })} />
      {res.isError && <p className="error">{res.error.message}</p>}
      {res.data && !items.length && <p className="muted">Nothing matches.</p>}
      <div className="grid">
        {items.map((hit) => (
          <ArchiveTile key={hit.id} catalog={catalog.id} hit={hit} />
        ))}
      </div>
      {res.hasNextPage && (
        <button className="button" disabled={res.isFetchingNextPage} onClick={() => void res.fetchNextPage()}>
          More
        </button>
      )}
    </>
  );
}

function ArchiveTile({ catalog, hit }: { catalog: number; hit: CatalogHit }) {
  const to = itemPath(catalog, hit.id);
  const match = libraryLabel(hit);
  const tags = hit.tags ?? [];
  return (
    <article className="tile">
      <Link to={to} className="tile-image" aria-label={hit.name}>
        {hit.thumbnail ? (
          <img className="avatar" src={thumbnailSrc(hit.thumbnail)} alt="" loading="lazy" />
        ) : (
          <span className="avatar empty">{hit.name.slice(0, 1)}</span>
        )}
      </Link>
      <Link to={to} className="tile-name">
        {hit.name}
      </Link>
      {hit.author && <p className="muted">{hit.author}</p>}
      {match && (
        <Link className="library-match" to={`/${hit.kind}s/${hit.library![0]!.id}`}>
          {match}
        </Link>
      )}
      <p className="tile-tags">
        {tags.slice(0, 3).map((t) => (
          <span key={t} className="tag">
            {t}
          </span>
        ))}
      </p>
    </article>
  );
}

/** IngestStatus shows the catalog's last ingest, live while it runs, and starts a new one. */
function IngestStatus({ catalog }: { catalog: Catalog }) {
  const qc = useQueryClient();
  const toast = useToast();
  const start = useMutation({
    mutationFn: async () => must(await api.POST("/catalogs/{id}/ingest", { params: { path: { id: catalog.id } } })),
    onSuccess: (j) => {
      toast(jobToast(j, "Ingest started"));
      void qc.invalidateQueries({ queryKey: ["catalogs"] });
    },
  });
  // Follow the run just started; the catalogs list can take a while to refetch.
  const latest = start.data ?? catalog.last_ingest;
  const job = useJob(latest?.id ?? 0, { enabled: !!latest });
  const current = job.data ?? latest;
  return (
    <span className="ingest-status">
      {current ? <Link to={`/settings/jobs/${current.id}`}>{ingestSummary(current)}</Link> : <span className="muted">Never ingested</span>}{" "}
      <button className="button" disabled={start.isPending || (!!current && !finished(current))} onClick={() => start.mutate()}>
        Ingest
      </button>
    </span>
  );
}
```

`web/src/archives/Archives.css`:

```css
.filter-bar {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.filter-bar .range {
  display: inline-flex;
  gap: 0.25rem;
}

.ingest-status {
  margin-left: auto;
}

.library-match {
  font-size: 0.85em;
}

.archive-text {
  white-space: pre-wrap;
}
```

- [ ] **Step 5: Run the tests to check they pass**

Run: `npx vitest run src/archives/Archives.test.tsx`
Expected: PASS, 6 tests.

- [ ] **Step 6: Commit**

```bash
git add web/src/api/queries.ts web/src/archives/Archives.tsx web/src/archives/Archives.css web/src/archives/Archives.test.tsx
git commit -m "feat(web): add the Archive page to search catalogs and run ingests"
```

---

### Task 4: Item view and Adopt

**Files:**
- Create: `web/src/archives/ArchiveItem.tsx`
- Test: `web/src/archives/ArchiveItem.test.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { ArchiveItem } from "./ArchiveItem";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = "2026-10-16T00:00:00Z";
const item = { id: "sub/brakka.png", kind: "card", name: "Brakka", author: "smith", latest_version: "2", hashes: { content_hash: "0".repeat(64) } };
const versions = [{ version: "2", created: now }, { version: "1" }];
const card = (v: string) => ({ spec: "chara_card_v3", data: { name: "Brakka", description: `Forge master v${v}.`, first_mes: "Hello." } });

let gets: URL[];
let adopts: URL[];
beforeEach(() => {
  gets = [];
  adopts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (url.pathname === "/api/catalogs/1/items/sub%2Fbrakka.png") {
        gets.push(url);
        const v = url.searchParams.get("version") ?? "2";
        return json({ item, version: v, versions, data: card(v) });
      }
      if (req.method === "POST" && url.pathname === "/api/catalogs/1/items/sub%2Fbrakka.png/adopt") {
        adopts.push(url);
        return json({ id: 12, slug: "brakka", provenance: "catalog:1:sub/brakka.png@1", created_at: now, revision: { id: 1, content_hash: "x", message: "", source: "plugin:folder", created_at: now, data: {} } }, 201);
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter([{ path: "/archives/:catalog/items/:item", element: <Toasts><ArchiveItem /></Toasts> }], { initialEntries: [entry] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("the item view reads an escaped item ID and shows the card", async () => {
  renderAt("/archives/1/items/sub%2Fbrakka.png");
  expect(await screen.findByRole("heading", { name: "Brakka" })).toBeInTheDocument();
  expect(screen.getByText("Forge master v2.")).toBeInTheDocument();
  expect(gets[0]!.searchParams.has("version")).toBe(false);
});

test("Adopt copies the picked version and then links to the library card", async () => {
  renderAt("/archives/1/items/sub%2Fbrakka.png");
  await screen.findByText("Forge master v2.");
  await userEvent.selectOptions(screen.getByLabelText("Version"), "1");
  expect(await screen.findByText("Forge master v1.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Adopt" }));
  expect(await screen.findByRole("link", { name: "Open in library" })).toHaveAttribute("href", "/cards/12");
  expect(adopts).toHaveLength(1);
  expect(adopts[0]!.searchParams.get("version")).toBe("1");
});

test("picking another version after adopting offers Adopt again", async () => {
  renderAt("/archives/1/items/sub%2Fbrakka.png");
  await screen.findByText("Forge master v2.");
  await userEvent.click(screen.getByRole("button", { name: "Adopt" }));
  await screen.findByRole("link", { name: "Open in library" });
  await userEvent.selectOptions(screen.getByLabelText("Version"), "1");
  await waitFor(() => expect(screen.getByRole("button", { name: "Adopt" })).toBeInTheDocument());
});
```

- [ ] **Step 2: Run the tests to check they fail**

Run: `npx vitest run src/archives/ArchiveItem.test.tsx`
Expected: FAIL, because `./ArchiveItem` doesn't exist.

- [ ] **Step 3: Write the item view**

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { strings, text, type Data } from "../card/draft";
import { thumbnailSrc } from "./archive";
import "../cards/Cards.css";
import "./Archives.css";

/** ArchiveItem is one catalog item, read-only, with a version picker and Adopt. */
export function ArchiveItem() {
  const { catalog, item } = useParams();
  const [params, setParams] = useSearchParams();
  const version = params.get("version") ?? undefined;
  // Keyed by version so an adopt of one version doesn't show as done for another.
  return <ItemView key={version ?? ""} catalog={Number(catalog)} item={item!} version={version} pick={(v) => setParams({ version: v })} />;
}

type ItemProps = { catalog: number; item: string; version?: string; pick: (version: string) => void };

function ItemView({ catalog, item, version, pick }: ItemProps) {
  const qc = useQueryClient();
  const path = { id: catalog, item };
  const got = useQuery({
    queryKey: ["catalog", catalog, "item", item, version],
    queryFn: async () => must(await api.GET("/catalogs/{id}/items/{item}", { params: { path, query: { version } } })),
  });
  const adopt = useMutation({
    mutationFn: async (v: string) => must(await api.POST("/catalogs/{id}/items/{item}/adopt", { params: { path, query: { version: v } } })),
    // Search results badge library matches, and the library now has this item.
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["search"] }),
  });
  if (got.isPending) return null;
  if (got.isError) return <p className="error page">{got.error.message}</p>;
  const g = got.data;
  return (
    <section className="page archive-item">
      <p>
        <Link to={`/archives?catalog=${catalog}`}>Back to the archive</Link>
      </p>
      {g.item.thumbnail && <img className="portrait" src={thumbnailSrc(g.item.thumbnail)} alt="" />}
      <h1>{g.item.name}</h1>
      {g.item.author && <p className="muted">by {g.item.author}</p>}
      <div className="row">
        <label>
          Version{" "}
          <select className="input" value={g.version} onChange={(e) => pick(e.target.value)}>
            {(g.versions ?? []).map((v) => (
              <option key={v.version} value={v.version}>
                {v.version}
                {v.created ? ` (${new Date(v.created).toLocaleDateString()})` : ""}
              </option>
            ))}
          </select>
        </label>
        {adopt.data ? (
          <Link className="button primary" to={`/${g.item.kind}s/${adopt.data.id}`}>
            Open in library
          </Link>
        ) : (
          <button className="button primary" disabled={adopt.isPending} onClick={() => adopt.mutate(g.version)}>
            Adopt
          </button>
        )}
      </div>
      <Preview kind={g.item.kind} data={g.data} />
    </section>
  );
}

const cardSections = [
  ["description", "Description"],
  ["personality", "Personality"],
  ["scenario", "Scenario"],
  ["first_mes", "First message"],
  ["mes_example", "Example messages"],
] as const;

/** Preview shows a version's text: a card's main fields, or a lorebook's entries. */
function Preview({ kind, data }: { kind: string; data: unknown }) {
  if (kind === "lorebook") {
    const entries = ((data as Data | null)?.entries ?? []) as Data[];
    return (
      <section>
        <h2>
          {entries.length} {entries.length === 1 ? "entry" : "entries"}
        </h2>
        <ul className="plain">
          {entries.map((e, i) => (
            <li key={i}>
              <strong>{strings(e.keys).join(", ")}</strong>
              <p className="archive-text">{text(e.content)}</p>
            </li>
          ))}
        </ul>
      </section>
    );
  }
  // A CCv3 card keeps its fields under data.
  const fields = ((data as Data | null)?.data ?? {}) as Data;
  const tags = strings(fields.tags);
  return (
    <>
      {tags.length > 0 && (
        <p className="tile-tags">
          {tags.map((t) => (
            <span key={t} className="tag">
              {t}
            </span>
          ))}
        </p>
      )}
      {cardSections.map(
        ([key, label]) =>
          text(fields[key]) && (
            <section key={key}>
              <h2>{label}</h2>
              <p className="archive-text">{text(fields[key])}</p>
            </section>
          ),
      )}
    </>
  );
}
```

`text` and `strings` come from `web/src/card/draft.ts`, which the card page already uses to read untyped card data. `.tag` and `.tile-tags` come from `Cards.css`.

- [ ] **Step 4: Run the tests to check they pass**

Run: `npx vitest run src/archives/ArchiveItem.test.tsx`
Expected: PASS, 3 tests. If the first test fails on the request path, react-router or openapi-fetch didn't round-trip `%2F`. Check `useParams` gives `sub/brakka.png` before changing `itemPath`.

- [ ] **Step 5: Commit**

```bash
git add web/src/archives/ArchiveItem.tsx web/src/archives/ArchiveItem.test.tsx
git commit -m "feat(web): show an archive item and adopt a version of it"
```

---

### Task 5: Routes and navigation

**Files:**
- Modify: `web/src/App.tsx`
- Modify: `web/src/shell/Shell.tsx`

- [ ] **Step 1: Add the routes**

In `web/src/App.tsx`, import the two pages:

```tsx
import { ArchiveItem } from "./archives/ArchiveItem";
import { Archives } from "./archives/Archives";
```

Add these after the `lorebooks/:id/history` route:

```tsx
          { path: "archives", element: <Archives /> },
          { path: "archives/:catalog/items/:item", element: <ArchiveItem /> },
```

- [ ] **Step 2: Add the rail entry**

In `web/src/shell/Shell.tsx`, add this to `sections` after Lorebooks:

```tsx
  { to: "/archives", label: "Archive", icon: "🗄" },
```

- [ ] **Step 3: Run the whole web suite**

Run: `npm run typecheck && npm test`
Expected: no type errors, and every test passes. If a shell or app test counts rail links, update its expected list to include Archive.

- [ ] **Step 4: Commit**

```bash
git add web/src/App.tsx web/src/shell/Shell.tsx
git commit -m "feat(web): add the Archive section to the rail"
```

---

### Task 6: Found in archives panel

**Files:**
- Create: `web/src/archives/FoundInArchives.tsx`
- Test: `web/src/archives/FoundInArchives.test.tsx`
- Modify: `web/src/card/Panel.tsx`, `web/src/lorebook/Lorebook.tsx`

- [ ] **Step 1: Write the failing tests**

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { FoundInArchives } from "./FoundInArchives";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const hash = { content_hash: "0".repeat(64) };
let body: unknown;
let paths: string[];
beforeEach(() => {
  paths = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      paths.push(new URL(req.url).pathname);
      return json(body);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPanel(kind: "card" | "lorebook") {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <FoundInArchives kind={kind} id={7} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("the panel lists matches by archive, links each to its item, and names archives it couldn't reach", async () => {
  body = {
    matches: [{ catalog: "chub", catalog_id: 1, items: [{ id: "chub:1", kind: "card", name: "Brakka", latest_version: "2", hashes: hash }] }],
    unreachable: [{ id: 2, name: "folder", error: "timeout" }],
  };
  renderPanel("card");
  expect(await screen.findByRole("heading", { name: "Found in archives" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "chub" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Brakka" })).toHaveAttribute("href", "/archives/1/items/chub%3A1");
  expect(screen.getByText("Couldn't reach folder.")).toBeInTheDocument();
  expect(paths).toEqual(["/api/cards/7/catalog-matches"]);
});

test("the panel is hidden when no archive has the entry", async () => {
  body = { matches: [], unreachable: [] };
  const { container } = renderPanel("lorebook");
  await vi.waitFor(() => expect(paths).toEqual(["/api/lorebooks/7/catalog-matches"]));
  expect(container).toBeEmptyDOMElement();
});
```

- [ ] **Step 2: Run the tests to check they fail**

Run: `npx vitest run src/archives/FoundInArchives.test.tsx`
Expected: FAIL, because `./FoundInArchives` doesn't exist.

- [ ] **Step 3: Write the panel**

```tsx
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, route, type Kind } from "../api/client";
import { itemPath } from "./archive";

/** FoundInArchives lists the archive items that match a card or lorebook, by archive. */
export function FoundInArchives({ kind, id }: { kind: Kind; id: number }) {
  const res = useQuery({
    queryKey: [kind, id, "catalog-matches"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/catalog-matches"), { params: { path: { id } } })),
  });
  const matches = res.data?.matches ?? [];
  const unreachable = res.data?.unreachable ?? [];
  if (!res.isError && !matches.length && !unreachable.length) return null;
  return (
    <section>
      <h2>Found in archives</h2>
      {res.isError && <p className="error">{res.error.message}</p>}
      {matches.map((m) => (
        <div key={m.catalog_id}>
          <h3>{m.catalog}</h3>
          <ul className="plain">
            {(m.items ?? []).map((it) => (
              <li key={it.id}>
                <Link to={itemPath(m.catalog_id, it.id)}>{it.name}</Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {unreachable.length > 0 && <p className="muted">Couldn't reach {unreachable.map((u) => u.name).join(", ")}.</p>}
    </section>
  );
}
```

- [ ] **Step 4: Run the tests to check they pass**

Run: `npx vitest run src/archives/FoundInArchives.test.tsx`
Expected: PASS, 2 tests.

- [ ] **Step 5: Show the panel on card and lorebook pages**

In `web/src/card/Panel.tsx`, import it:

```tsx
import { FoundInArchives } from "../archives/FoundInArchives";
```

Then add it after `<Sources kind="card" id={card.id} />`:

```tsx
      <FoundInArchives kind="card" id={card.id} />
```

In `web/src/lorebook/Lorebook.tsx`, import it the same way, then add it after `<Sources kind="lorebook" id={id} />`:

```tsx
        <FoundInArchives kind="lorebook" id={id} />
```

The existing card and lorebook tests stub `fetch` with a fallback of `json([])`. An array has no `matches` or `unreachable`, so the panel renders nothing there. If a test stubs another fallback, give it `catalog-matches` returning `{ matches: [], unreachable: [] }`.

- [ ] **Step 6: Run the whole web suite**

Run: `npm run typecheck && npm test`
Expected: no type errors, and every test passes.

- [ ] **Step 7: Commit**

```bash
git add web/src/archives/FoundInArchives.tsx web/src/archives/FoundInArchives.test.tsx web/src/card/Panel.tsx web/src/lorebook/Lorebook.tsx
git commit -m "feat(web): show where a card or lorebook appears in archives"
```

---

### Task 7: Check it end to end

- [ ] **Step 1: Run what CI runs**

From the repo root:

```bash
go vet ./...
go test -race ./...
cd web && npm run typecheck && npm test
```

Expected: all pass. This plan changes no Go and no API, so `npm run gen` must leave `web/openapi.json` and `web/src/api/schema.d.ts` unchanged. Run it and check `git status` shows nothing new.

- [ ] **Step 2: Try it in the browser**

Start the app as the README describes, with a `folder` catalog instance pointed at a directory of a few card PNGs. Then:

1. Open Archive, run Ingest, and watch the status line finish with counts.
2. Search, set a filter, and open an item.
3. Adopt it, follow "Open in library", and check the card page's "Found in archives" panel lists the item.
4. Back on Archive, check the item now shows "In library".

- [ ] **Step 3: Read the diff once more**

Check for dead code, stale names, and anything this plan didn't ask for.

## Leaves for later

- Sorting: `describe` declares sorts, and the search route takes `sort`, but the spec doesn't ask for a sort control. Add one beside the kind switch when it's wanted.
- The live chub.ai catalog (INN-38) uses this page unchanged. It's a pass-through catalog, so `info.ingest` is false and the ingest line doesn't show.
- Deploy: innkeeper.pureroot.com still needs the archive share mounted into the core pod before `chub-archive` can ingest there (from the 37d plan).
