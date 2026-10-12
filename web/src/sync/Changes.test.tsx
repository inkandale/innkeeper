import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Changes } from "./Changes";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const changes = {
  rows: [
    { name: "Zed (Lumiverse only)", apps: [{ instance_id: 2, state: "new", remote_id: "z", name: "Zed", job_id: 7, app_hash: "hz" }] },
    {
      card_id: 1,
      name: "Ann",
      apps: [{ instance_id: 2, state: "differs", remote_id: "a", name: "Ann", job_id: 7, app_hash: "ha", suggested: { action: "bind", card_id: 1 } }],
    },
    { card_id: 4, name: "Bo", apps: [{ instance_id: 3, state: "ahead", remote_id: "b", binding_id: 11 }] },
    { card_id: 5, name: "Cy", apps: [{ instance_id: 3, state: "missing", remote_id: "c", binding_id: 12 }] },
    { card_id: 6, name: "Di", apps: [{ instance_id: 2, state: "error", remote_id: "d", job_id: 7, error: "bad JSON" }] },
  ],
  counts: { "2": { new: 1, differs: 1, error: 1 }, "3": { ahead: 1, missing: 1 } },
};
const cmp = { instance_id: 2, remote_id: "a", card_id: 1, head_revision_id: 118, accepted_hash: "", base: "none", app_hash: "ha", fields: [] };

let queries: URLSearchParams[];
let posts: { path: string; body: unknown }[];
beforeEach(() => {
  queries = [];
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/sync/changes") {
        queries.push(u.searchParams);
        return json(changes);
      }
      if (u.pathname === "/api/instances")
        return json([
          { id: 2, name: "Lumiverse", plugin: "lumiverse" },
          { id: 3, name: "Tavern", plugin: "tavern" },
        ]);
      if (u.pathname === "/api/instances/2/items/a/compare" && u.searchParams.get("card_id") === "1") return json(cmp);
      if (u.pathname === "/api/pushes/preview") {
        posts.push({ path: u.pathname, body: await req.json() });
        return json({ total: 1, new: 0, ahead: 1, in_sync: 0, cant_check: 0, drifted: 0, confirm: "q1" });
      }
      if (req.method === "POST") {
        posts.push({ path: u.pathname, body: await req.json() });
        return json({ id: 9, kind: "bulk", status: "waiting" }, 202);
      }
      return json({}, 404);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderChanges(url = "/sync/changes") {
  const router = createMemoryRouter(
    [
      { path: "/sync/changes", element: <Toasts><Changes /></Toasts> },
      { path: "/sync/:instance/:kind/:remoteId", element: <p>Review page</p> },
    ],
    { initialEntries: [url] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("one row per card, with each app's state under its column, orphans first", async () => {
  renderChanges();
  const ann = await screen.findByRole("row", { name: /Ann/ });
  expect(screen.getByRole("columnheader", { name: "Lumiverse" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Tavern" })).toBeInTheDocument();
  const rows = screen.getAllByRole("row").slice(1);
  expect(rows[0]!).toHaveTextContent("Zed (Lumiverse only)");
  expect(within(ann).getByRole("link", { name: "Ann" })).toHaveAttribute("href", "/cards/1");
  // Checkbox, Card, Lumiverse, Tavern, Action.
  expect(within(ann).getAllByRole("cell")[2]).toHaveTextContent("differs");
  expect(within(ann).getAllByRole("cell")[3]).toHaveTextContent("");
  const bo = screen.getByRole("row", { name: /Bo/ });
  expect(within(bo).getAllByRole("cell")[3]).toHaveTextContent("ahead");
  expect(within(ann).getByRole("link", { name: /Review/ })).toHaveAttribute("href", "/sync/2/items/a?job=7&queue=1");
  expect(within(bo).getByRole("button", { name: /Push/ })).toBeInTheDocument();
  const cy = screen.getByRole("row", { name: /Cy/ });
  expect(within(cy).getByRole("button", { name: /Push again/ })).toBeInTheDocument();
  expect(within(cy).getByRole("button", { name: /Unbind/ })).toBeInTheDocument();
  expect(screen.getByText("bad JSON")).toBeInTheDocument();
  // Checked when every entry has a suggestion or is ahead.
  expect(within(ann).getByRole("checkbox")).toBeChecked();
  expect(within(bo).getByRole("checkbox")).toBeChecked();
  expect(within(cy).getByRole("checkbox")).not.toBeChecked();
  expect(within(rows[0]!).getByRole("checkbox")).not.toBeChecked();
});

test("filters come from the URL and go to the query", async () => {
  renderChanges("/sync/changes?instance_id=3&state=ahead");
  await screen.findByRole("row", { name: /Bo/ });
  expect(queries[0]!.get("instance_id")).toBe("3");
  expect(queries[0]!.get("state")).toBe("ahead");
  expect(screen.getByRole("combobox", { name: "App" })).toHaveDisplayValue("Tavern");
  expect(screen.getByRole("combobox", { name: "State" })).toHaveDisplayValue("ahead");
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "State" }), "missing");
  await waitFor(() => expect(queries.at(-1)?.get("state")).toBe("missing"));
  expect(queries.at(-1)?.get("instance_id")).toBe("3");
});

test("Apply suggested resolves the checked reviews", async () => {
  renderChanges();
  const bo = await screen.findByRole("row", { name: /Bo/ });
  await userEvent.click(within(bo).getByRole("checkbox"));
  await userEvent.click(screen.getByRole("button", { name: /Apply/ }));
  await waitFor(() => expect(posts.map((p) => p.path)).toEqual(["/api/resolutions"]));
  expect(posts[0]!.body).toEqual({
    resolutions: [
      { remote_id: "a", action: "bind", card_id: 1, app_hash: "ha", base_revision_id: 118, accepted_hash: "", default: "library", instance_id: 2 },
    ],
  });
  expect(await screen.findByText(/Job 9 started/)).toBeInTheDocument();
});

test("Apply suggested with an ahead row asks with the push preview first", async () => {
  renderChanges();
  await screen.findByRole("row", { name: /Bo/ });
  await userEvent.click(screen.getByRole("button", { name: /Apply/ }));
  const dialog = await screen.findByRole("dialog");
  expect(posts).toEqual([{ path: "/api/pushes/preview", body: { instance_id: 3, query: { filter: { ids: [4] } } } }]);
  expect(await within(dialog).findByText(/1 ahead/)).toBeInTheDocument();
  await userEvent.click(within(dialog).getByRole("button", { name: "Confirm" }));
  await waitFor(() => expect(posts.map((p) => p.path)).toEqual(["/api/pushes/preview", "/api/resolutions", "/api/pushes"]));
  expect(posts[2]!.body).toEqual({ instance_id: 3, query: { filter: { ids: [4] } }, confirm: "q1" });
});

test("Review one by one opens the first review", async () => {
  const router = renderChanges();
  await userEvent.click(await screen.findByRole("link", { name: "Review one by one" }));
  expect(router.state.location.pathname + router.state.location.search).toBe("/sync/2/items/z?job=7&queue=1");
});

test("nothing to do", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ rows: [], counts: {} })));
  renderChanges();
  expect(await screen.findByText("Nothing needs you.")).toBeInTheDocument();
});
