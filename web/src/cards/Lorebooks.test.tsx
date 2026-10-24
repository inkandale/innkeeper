import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Cards } from "./Cards";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const results = {
  total: 1,
  hits: [{ id: 3, slug: "forge", revision_id: 1, fields: { name: "Forge" }, entries: 2, cards: [7], targets: [{ instance: 2, is: "ahead" }] }],
  facets: { targets: [{ instance: 2, is: "ahead", cards: 1 }], sources: [{ kind: "chub", cards: 1 }] },
};

class FakeEventSource {
  static last: FakeEventSource | null = null;
  listener: ((e: { data: string }) => void) | null = null;
  constructor(readonly url: string) {
    FakeEventSource.last = this;
  }
  addEventListener(_: string, f: (e: { data: string }) => void) {
    this.listener = f;
  }
  close() {}
}

let searches: { path: string; body: unknown }[];
let posts: { path: string; body: unknown }[];
beforeEach(() => {
  searches = [];
  posts = [];
  FakeEventSource.last = null;
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/lorebooks/search") {
        searches.push({ path, body: await req.json() });
        return json(results);
      }
      if (path === "/api/cards/search") {
        searches.push({ path, body: await req.json() });
        return json({ total: 1, hits: [{ id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka", avatar: "ab".repeat(32) } }] });
      }
      if (path === "/api/pushes/preview") return json({ total: 1, new: 1, ahead: 0, in_sync: 0, cant_check: 0, drifted: 0, confirm: "q1" });
      if (path === "/api/lorebooks/move/preview" || path === "/api/lorebooks/move") {
        if (path === "/api/lorebooks/move") posts.push({ path, body: await req.json() });
        return json({ moved: [3], archived: 0, variant_links_cleared: [], collection_memberships_dropped: 0, from_libraries: [1], bound_instances: [2] });
      }
      if (path === "/api/libraries") return json([1, 2].map((id) => ({ id, name: `Library ${id}`, cards: 0, lorebooks: 0, created_at: "2026-10-01T00:00:00Z" })));
      if (path === "/api/bulk" || path === "/api/pushes") {
        posts.push({ path, body: await req.json() });
        return json({ id: 5, kind: "bulk", status: "waiting" });
      }
      if (path === "/api/instances") return json([{ id: 2, label: "Lumiverse", name: "Lumiverse", plugin: "lumiverse" }]);
      if (path === "/api/plugins") return json([{ name: "lumiverse", capabilities: ["push"] }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter([{ path: "/lorebooks", element: <Toasts><Cards kind="lorebook" /></Toasts> }], {
    initialEntries: [entry],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("lists lorebooks with entries, linked cards, and sync", async () => {
  renderAt("/lorebooks");
  expect(await screen.findByRole("link", { name: "Forge" })).toHaveAttribute("href", "/lorebooks/3");
  expect(screen.getByText("1 lorebook, variants grouped")).toBeInTheDocument();
  expect(screen.getByText("2 entries")).toBeInTheDocument();
  expect(await screen.findByRole("link", { name: "Brakka" })).toHaveAttribute("href", "/cards/7");
  expect(searches).toContainEqual(expect.objectContaining({ path: "/api/lorebooks/search", body: expect.objectContaining({ include: ["cards", "targets"] }) }));
  expect(searches).toContainEqual({ path: "/api/lorebooks/search", body: { library: 1, facets: ["sources"], facets_only: true } });
  expect(searches).toContainEqual({ path: "/api/cards/search", body: { library: 1, filter: { ids: [7] }, fields: ["name", "avatar"], page: { size: 500 } } });
  expect(screen.getByRole("searchbox", { name: "Search lorebooks" })).toHaveAttribute("placeholder", "Search lorebooks");
  expect(screen.queryByRole("button", { name: "List view" })).not.toBeInTheDocument();
  expect(await screen.findByRole("button", { name: /^chub/ })).toBeInTheDocument();
});

test("bulk archive and push send kind lorebook", async () => {
  renderAt("/lorebooks");
  await userEvent.click(await screen.findByRole("checkbox", { name: "Select Forge" }));
  expect(screen.queryByRole("button", { name: "Tag" })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Archive" }));
  await userEvent.click(screen.getByRole("button", { name: "Archive 1 lorebook" }));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toMatchObject({ path: "/api/bulk", body: { kind: "lorebook", action: "archive", query: { filter: { ids: [3] } } } });

  await userEvent.click(await screen.findByRole("checkbox", { name: "Select Forge" }));
  await userEvent.click(screen.getByRole("button", { name: "Push" }));
  await screen.findByText("1 new");
  await userEvent.click(screen.getByRole("button", { name: "Push 1 lorebook" }));
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts[1]).toMatchObject({ path: "/api/pushes", body: { kind: "lorebook", instance_id: 2, confirm: "q1" } });
});

test("the list reloads when a bulk job it started finishes", async () => {
  renderAt("/lorebooks");
  await userEvent.click(await screen.findByRole("checkbox", { name: "Select Forge" }));
  await userEvent.click(screen.getByRole("button", { name: "Archive" }));
  await userEvent.click(screen.getByRole("button", { name: "Archive 1 lorebook" }));
  await waitFor(() => expect(FakeEventSource.last?.url).toBe("/api/jobs/5/events"));
  const before = searches.filter((s) => s.path === "/api/lorebooks/search").length;
  act(() => FakeEventSource.last?.listener?.({ data: JSON.stringify({ id: 5, kind: "bulk", status: "succeeded" }) }));
  await waitFor(() => expect(searches.filter((s) => s.path === "/api/lorebooks/search").length).toBeGreaterThan(before));
});

test("Move to library… moves the picked lorebooks", async () => {
  renderAt("/lorebooks");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Forge" }));
  await user.click(screen.getByRole("button", { name: "Move to library…" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Library" }), "Library 2");
  expect(await screen.findByText("Moves 1 lorebook")).toBeInTheDocument();
  expect(screen.getByText("Keeps syncing to: Lumiverse")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Move" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/lorebooks/move", body: { ids: [3], library: 2 } }]));
  expect(await screen.findByText("Moved 1 lorebook to Library 2.")).toBeInTheDocument();
});
