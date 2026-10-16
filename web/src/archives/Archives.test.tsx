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
});
