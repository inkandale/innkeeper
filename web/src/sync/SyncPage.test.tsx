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
beforeEach(() => {
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/instances")
        return json([
          { id: 2, name: "Lumiverse", plugin: "lumiverse" },
          { id: 3, name: "Cards folder", plugin: "folder" },
        ]);
      if (u.pathname === "/api/plugins")
        return json([
          { name: "lumiverse", capabilities: ["push", "pull"] },
          { name: "folder", capabilities: ["import"] },
        ]);
      if (u.pathname === "/api/search")
        return json({
          hits: [], total: 10,
          facets: { targets: [{ instance: 2, is: "in_sync", cards: 5 }, { instance: 2, is: "ahead", cards: 3 }, { instance: 2, is: "diverged", cards: 1 }] },
        });
      if (u.pathname === "/api/jobs" && u.searchParams.get("kind") === "target.reconcile")
        return json([{ id: 5, instance_id: 2, kind: "target.reconcile", status: "succeeded", created_at: now, payload: {}, progress: {} }]);
      if (u.pathname === "/api/reconciles" || u.pathname === "/api/pushes") {
        posts.push({ path: u.pathname, body: await req.json() });
        return json({ id: 6, kind: "x", status: "queued" }, 202);
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

test("lists target apps with their counts and last reconcile", async () => {
  renderSync();
  const row = await screen.findByRole("row", { name: /Lumiverse/ });
  expect(within(row).getAllByRole("cell").map((c) => c.textContent).slice(0, 5)).toEqual(["Lumiverse", "lumiverse", "5", "3", "1"]);
  expect(within(row).getByRole("link", { name: /ago/ })).toHaveAttribute("href", "/sync/reconciles/5");
  expect(screen.queryByText("Cards folder")).not.toBeInTheDocument();
});

test("Push all ahead asks again with the count", async () => {
  renderSync();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Push all ahead" }));
  await user.click(screen.getByRole("button", { name: "Push 3 ahead cards" }));
  await waitFor(() =>
    expect(posts).toEqual([{ path: "/api/pushes", body: { instance_id: 2, query: { filter: { target_state: { instance: 2, is: "ahead" } } } } }]),
  );
});

test("Reconcile starts a run and opens its queue", async () => {
  renderSync();
  await userEvent.click(await screen.findByRole("button", { name: "Reconcile" }));
  expect(await screen.findByText("Queue page")).toBeInTheDocument();
  expect(posts).toEqual([{ path: "/api/reconciles", body: { instance_id: 2 } }]);
});
