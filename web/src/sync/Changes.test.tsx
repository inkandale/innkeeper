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
    { name: "Zed (Lumiverse only)", apps: [{ instance_id: 2, push: true, state: "new", remote_id: "z", name: "Zed", job_id: 7, app_hash: "hz" }] },
    {
      card_id: 1,
      name: "Ann",
      apps: [{ instance_id: 2, push: true, state: "differs", remote_id: "a", name: "Ann", job_id: 7, app_hash: "ha", suggested: { action: "bind", card_id: 1 } }],
    },
    { card_id: 4, name: "Bo", apps: [{ instance_id: 3, push: true, state: "ahead", remote_id: "b", binding_id: 11 }] },
    { card_id: 5, name: "Cy", apps: [{ instance_id: 3, push: true, state: "missing", remote_id: "c", binding_id: 12 }] },
    { card_id: 6, name: "Di", apps: [{ instance_id: 2, push: true, state: "error", remote_id: "d", job_id: 7, error: "bad JSON" }] },
  ],
  counts: { "2": { new: 1, differs: 1, error: 1 }, "3": { ahead: 1, missing: 1 } },
};
const cmp = { instance_id: 2, remote_id: "a", card_id: 1, head_revision_id: 118, accepted_hash: "", base: "none", app_hash: "ha", fields: [] };
const followed = {
  rows: [
    {
      lorebook_id: 9,
      name: "Forge lore",
      apps: [{ instance_id: 4, state: "missing", remote_id: "chub:alice/forge", binding_id: 22, push: false, source_id: 30 }],
    },
    {
      card_id: 8,
      name: "Vyr",
      apps: [{ instance_id: 4, state: "update", remote_id: "chub:alice/vyr", binding_id: 21, push: false, source_id: 31, app_hash: "hv" }],
    },
  ],
  counts: { "4": { update: 1, missing: 1 } },
};

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
          { id: 2, label: "Lumiverse", plugin: "lumiverse" },
          { id: 3, label: "Tavern", name: "Tavern", plugin: "tavern" },
        ]);
      if (u.pathname === "/api/instances/2/cards/a/compare" && u.searchParams.get("card_id") === "1") return json(cmp);
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
  expect(screen.getByRole("columnheader", { name: "Card or lorebook" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Lumiverse" })).toBeInTheDocument();
  expect(screen.getByRole("columnheader", { name: "Tavern" })).toBeInTheDocument();
  const rows = screen.getAllByRole("row").slice(1);
  expect(rows[0]!).toHaveTextContent("Zed (Lumiverse only)");
  expect(within(ann).getByRole("link", { name: "Ann" })).toHaveAttribute("href", "/cards/1");
  // Checkbox, Card or lorebook, Lumiverse, Tavern, Action.
  expect(within(ann).getAllByRole("cell")[2]).toHaveTextContent("differs");
  expect(within(ann).getAllByRole("cell")[3]).toHaveTextContent("");
  const bo = screen.getByRole("row", { name: /Bo/ });
  expect(within(bo).getAllByRole("cell")[3]).toHaveTextContent("not pushed");
  expect(within(ann).getByRole("link", { name: /Review/ })).toHaveAttribute("href", "/sync/2/card/a?job=7&queue=1");
  expect(within(bo).getByRole("button", { name: /Push/ })).toBeInTheDocument();
  expect(within(bo).getByRole("link", { name: /Review/ })).toHaveAttribute("href", "/sync/3/card/b?card=4&base=none&queue=1");
  const cy = screen.getByRole("row", { name: /Cy/ });
  expect(within(cy).getByRole("button", { name: /Push again/ })).toBeInTheDocument();
  expect(within(cy).getByRole("button", { name: /Unbind/ })).toBeInTheDocument();
  expect(screen.getByText("bad JSON")).toBeInTheDocument();
  // Checked when every entry has a suggestion; a push waits to be checked.
  expect(within(ann).getByRole("checkbox")).toBeChecked();
  expect(within(bo).getByRole("checkbox")).not.toBeChecked();
  expect(within(cy).getByRole("checkbox")).not.toBeChecked();
  expect(within(rows[0]!).getByRole("checkbox")).not.toBeChecked();
});

test("filters come from the URL and go to the query", async () => {
  renderChanges("/sync/changes?instance_id=3&state=ahead");
  await screen.findByRole("row", { name: /Bo/ });
  expect(queries[0]!.get("instance_id")).toBe("3");
  expect(queries[0]!.get("state")).toBe("ahead");
  expect(screen.getByRole("combobox", { name: "App" })).toHaveDisplayValue("Tavern");
  expect(screen.getByRole("combobox", { name: "State" })).toHaveDisplayValue("not pushed");
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "State" }), "missing");
  await waitFor(() => expect(queries.at(-1)?.get("state")).toBe("missing"));
  expect(queries.at(-1)?.get("instance_id")).toBe("3");
});

test("Apply suggested resolves the checked reviews", async () => {
  renderChanges();
  await screen.findByRole("row", { name: /Bo/ });
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
  const bo = await screen.findByRole("row", { name: /Bo/ });
  await userEvent.click(within(bo).getByRole("checkbox"));
  await userEvent.click(screen.getByRole("button", { name: /Apply/ }));
  const dialog = await screen.findByRole("dialog");
  expect(posts).toEqual([{ path: "/api/pushes/preview", body: { instance_id: 3, query: { filter: { ids: [4] } } } }]);
  expect(await within(dialog).findByText(/1 not pushed/)).toBeInTheDocument();
  await userEvent.click(within(dialog).getByRole("button", { name: "Confirm" }));
  await waitFor(() => expect(posts.map((p) => p.path)).toEqual(["/api/pushes/preview", "/api/resolutions", "/api/pushes"]));
  expect(posts[2]!.body).toEqual({ instance_id: 3, query: { filter: { ids: [4] } }, confirm: "q1" });
});

test("Review one by one opens the first review", async () => {
  const router = renderChanges();
  await userEvent.click(await screen.findByRole("link", { name: "Review one by one" }));
  expect(router.state.location.pathname + router.state.location.search).toBe("/sync/2/card/z?job=7&queue=1");
});

test("Review one by one keeps the filter", async () => {
  const router = renderChanges("/sync/changes?instance_id=2&state=differs");
  await userEvent.click(await screen.findByRole("link", { name: "Review one by one" }));
  expect(router.state.location.pathname + router.state.location.search).toBe(
    "/sync/2/card/z?job=7&queue=1&instance_id=2&state=differs",
  );
});

test("nothing to do", async () => {
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => json(new URL(req.url).pathname === "/api/sync/changes" ? { rows: [], counts: {} } : [])));
  renderChanges();
  expect(await screen.findByText("Nothing needs you.")).toBeInTheDocument();
});

test("Unbind asks first, and deletes the binding only once confirmed", async () => {
  const deletes: string[] = [];
  const base = fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.method === "DELETE") {
        deletes.push(new URL(req.url).pathname);
        return new Response(null, { status: 204 });
      }
      return base(req);
    }),
  );
  const ask = vi.fn(() => false);
  vi.stubGlobal("confirm", ask);
  renderChanges();
  const cy = await screen.findByRole("row", { name: /Cy/ });
  await userEvent.click(within(cy).getByRole("button", { name: /Unbind/ }));
  expect(ask).toHaveBeenCalledTimes(1);
  expect(deletes).toEqual([]);
  ask.mockReturnValue(true);
  await userEvent.click(within(cy).getByRole("button", { name: /Unbind/ }));
  await waitFor(() => expect(deletes).toEqual(["/api/bindings/12"]));
});

test("a followed source's entries offer review, ignoring, and removing the source", async () => {
  const base = fetch;
  const sent: { method: string; path: string; body?: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/sync/changes") return json(followed);
      if (u.pathname === "/api/instances") return json([{ id: 4, label: "URL import", name: "URL import", plugin: "url-import" }]);
      if (u.pathname === "/api/plugins") return json([{ name: "url-import", capabilities: ["pull"] }]);
      if (req.method !== "GET") {
        sent.push({ method: req.method, path: u.pathname, ...(req.method === "DELETE" ? {} : { body: await req.json() }) });
        return req.method === "DELETE" ? new Response(null, { status: 204 }) : json({ id: 21, instance_id: 4, remote_id: "chub:alice/vyr" });
      }
      return base(req);
    }),
  );
  renderChanges();
  const vyr = await screen.findByRole("row", { name: /Vyr/ });
  // Checkbox, Card or lorebook, URL import, Action.
  await waitFor(() => expect(within(vyr).getAllByRole("cell")[2]).toHaveTextContent("update on chub"));
  expect(await screen.findByRole("option", { name: "URL import (source)" })).toBeInTheDocument();
  expect(within(vyr).getByRole("link", { name: "Review" })).toHaveAttribute("href", "/sync/4/card/chub%3Aalice%2Fvyr?card=8&queue=1");
  const forge = screen.getByRole("row", { name: /Forge lore/ });
  expect(within(forge).getByRole("link", { name: "Forge lore" })).toHaveAttribute("href", "/lorebooks/9");
  expect(within(forge).getAllByRole("cell")[2]).toHaveTextContent("no longer on chub");
  expect(within(forge).queryByRole("button", { name: /Push again|Unbind/ })).not.toBeInTheDocument();

  const click = async (row: HTMLElement, name: string) => {
    const b = within(row).getByRole("button", { name });
    await waitFor(() => expect(b).toBeEnabled());
    await userEvent.click(b);
  };
  await click(vyr, "Ignore this change");
  await click(vyr, "Ignore all");
  await click(forge, "Remove source");
  await waitFor(() =>
    expect(sent).toEqual([
      { method: "POST", path: "/api/bindings/21/dismiss", body: { app_hash: "hv" } },
      { method: "PATCH", path: "/api/bindings/21", body: { paused: true } },
      { method: "DELETE", path: "/api/lorebooks/9/sources/30" },
    ]),
  );
});

test("ignoring a change the site has changed again reloads the queue and says so", async () => {
  const base = fetch;
  let loads = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/sync/changes") {
        loads++;
        return json(followed);
      }
      if (u.pathname === "/api/instances") return json([{ id: 4, label: "URL import", name: "URL import", plugin: "url-import" }]);
      if (u.pathname === "/api/bindings/21/dismiss") return json({ detail: "the site changed", status: 409 }, 409);
      return base(req);
    }),
  );
  renderChanges();
  const vyr = await screen.findByRole("row", { name: /Vyr/ });
  await userEvent.click(within(vyr).getByRole("button", { name: "Ignore this change" }));
  expect(await screen.findByText("Vyr changed on chub again. Check the new change.")).toBeInTheDocument();
  expect(screen.queryByText("the site changed")).not.toBeInTheDocument();
  await waitFor(() => expect(loads).toBe(2));
});
