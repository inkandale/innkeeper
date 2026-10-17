import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Sync } from "./SyncPage";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
let posts: { path: string; body: unknown }[];
let searchBodies: unknown[];
beforeEach(() => {
  searchBodies = [];
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/libraries") return new Response(JSON.stringify([1, 2, 3].map((id) => ({ id, name: `Library ${id}`, cards: 0, lorebooks: 0, created_at: "2026-10-01T00:00:00Z" }))), { headers: { "Content-Type": "application/json" } });
      if (u.pathname === "/api/instances")
        return json([
          { id: 2, name: "Lumiverse", plugin: "lumiverse" },
          { id: 3, name: "Cards folder", plugin: "folder" },
          { id: 4, name: "URL import", plugin: "url-import" },
        ]);
      if (u.pathname === "/api/plugins")
        return json([
          { name: "lumiverse", capabilities: ["push", "pull"] },
          { name: "folder", capabilities: ["import"] },
          { name: "url-import", capabilities: ["import", "sites", "pull"] },
        ]);
      if (u.pathname === "/api/cards/search") searchBodies.push(await req.clone().json());
      if (u.pathname === "/api/cards/search")
        return json({
          hits: [], total: 10,
          facets: { targets: [{ instance: 2, is: "in_sync", cards: 5 }, { instance: 2, is: "ahead", cards: 3 }, { instance: 2, is: "diverged", cards: 1 }, { instance: 2, is: "missing", cards: 2 }, { instance: 2, is: "cant_check", cards: 4 }] },
        });
      if (u.pathname === "/api/jobs" && u.searchParams.get("kind") === "target.reconcile")
        return json([{ id: 5, instance_id: 2, kind: "target.reconcile", status: "succeeded", created_at: now, payload: {}, progress: { totals: { bound: 9, auto_bound: 1, auto_imported: 2, auto_pulled: 3 } } }]);
      if (u.pathname === "/api/sync/changes")
        return json({ rows: [], counts: { "2": { differs: 2, both_changed: 1, error: 1, new: 3, ahead: 3, missing: 2 }, "4": { update: 2, missing: 1, error: 1 } } });
      if (u.pathname === "/api/jobs/6") return json({ id: 6, kind: "x", status: "running", created_at: now, payload: {}, progress: {} });
      if (req.method === "POST" && u.pathname !== "/api/cards/search") {
        posts.push({ path: u.pathname + u.search, body: await req.json().catch(() => undefined) });
        return json({ id: 6, kind: "x", status: "queued", created_at: now, payload: {}, progress: {} }, 202);
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderSync() {
  const router = createMemoryRouter(
    [
      { path: "/sync", element: <Toasts><Sync /></Toasts> },
      { path: "/sync/reconciles/:job", element: <p>Queue page</p> },
    ],
    { initialEntries: ["/sync"] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("lists target apps with their counts and last sync", async () => {
  renderSync();
  const row = await screen.findByRole("row", { name: /Lumiverse/ });
  await waitFor(() => expect(within(row).getAllByRole("cell")[2]).toHaveTextContent("7"));
  expect(screen.getAllByRole("columnheader").map((c) => c.textContent)).toEqual(
    ["Target", "In sync (cards)", "Needs you", "Not pushed", "Missing", "Can't check", "Last sync"],
  );
  expect(within(row).getAllByRole("cell").map((c) => c.textContent).slice(0, 6)).toEqual(["Lumiverse", "5", "7", "3", "2", "4"]);
  expect(within(row).getByRole("link", { name: "7" })).toHaveAttribute("href", "/sync/changes?instance_id=2");
  expect(within(row).getByRole("link", { name: "3" })).toHaveAttribute("href", "/sync/changes?instance_id=2&state=ahead");
  expect(within(row).getByRole("link", { name: "2" })).toHaveAttribute("href", "/sync/changes?instance_id=2&state=missing");
  const last = within(row).getByRole("link", { name: /ago/ });
  expect(last).toHaveAttribute("href", "/sync/reconciles/5");
  expect(last).not.toHaveTextContent("applied");
  expect(screen.getByRole("link", { name: "Changes" })).toHaveAttribute("href", "/sync/changes");
  expect(screen.queryByText("Cards folder")).not.toBeInTheDocument();
});

test("Can't check shows only when it isn't zero", async () => {
  vi.mocked(fetch).mockImplementation(async (req) => {
    const u = new URL((req as Request).url);
    if (u.pathname === "/api/instances") return json([{ id: 2, name: "Lumiverse", plugin: "lumiverse" }]);
    if (u.pathname === "/api/plugins") return json([{ name: "lumiverse", capabilities: ["push"] }]);
    if (u.pathname === "/api/cards/search") return json({ hits: [], total: 5, facets: { targets: [{ instance: 2, is: "in_sync", cards: 5 }] } });
    if (u.pathname === "/api/sync/changes") return json({ rows: [], counts: {} });
    return json([]);
  });
  renderSync();
  const row = await screen.findByRole("row", { name: /Lumiverse/ });
  await waitFor(() => expect(within(row).getAllByRole("cell")[1]).toHaveTextContent("5"));
  expect(screen.queryByRole("columnheader", { name: "Can't check" })).not.toBeInTheDocument();
  expect(within(row).getAllByRole("cell")[2]).toHaveTextContent("0");
});

test("Sync starts one run for every app", async () => {
  renderSync();
  await screen.findByRole("row", { name: /Lumiverse/ });
  expect(screen.getAllByRole("button", { name: "Sync" })).toHaveLength(1);
  expect(screen.queryByRole("button", { name: /Push/ })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Sync" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/syncs", body: {} }]));
  expect(await screen.findByText("Syncing every app…")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/6");
  await waitFor(() => expect(screen.getByRole("button", { name: "Sync" })).toBeDisabled());
});

test("Refresh starts a scan", async () => {
  renderSync();
  await userEvent.click(await screen.findByRole("button", { name: "Refresh" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/scans", body: undefined }]));
  expect(await screen.findByText("Refreshing every app…")).toBeInTheDocument();
});

test("site sources get their own line: their changes and a check of every source in the last library used", async () => {
  localStorage.setItem("innkeeper.library", "2");
  renderSync();
  const updates = await screen.findByRole("link", { name: "4 changes on their sites" });
  expect(updates).toHaveAttribute("href", "/sync/changes?instance_id=4");
  expect(screen.queryByRole("row", { name: /URL import/ })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Check for updates" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/source-checks?library=2", body: undefined }]));
  expect(searchBodies).toContainEqual(expect.objectContaining({ library: 2, facets: ["targets"] }));
  expect(await screen.findByText("Checking every source for updates…")).toBeInTheDocument();
  localStorage.clear();
});

test("with only site sources, the Changes link still shows", async () => {
  vi.mocked(fetch).mockImplementation(async (req) => {
    const u = new URL((req as Request).url);
    if (u.pathname === "/api/instances") return json([{ id: 4, name: "URL import", plugin: "url-import" }]);
    if (u.pathname === "/api/plugins") return json([{ name: "url-import", capabilities: ["import", "sites", "pull"] }]);
    if (u.pathname === "/api/sync/changes") return json({ rows: [], counts: {} });
    return json([]);
  });
  renderSync();
  expect(await screen.findByRole("link", { name: "Changes" })).toHaveAttribute("href", "/sync/changes");
  expect(screen.getByRole("link", { name: "0 changes on their sites" })).toBeInTheDocument();
});
