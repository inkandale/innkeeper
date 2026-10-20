import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Libraries } from "./Libraries";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
let calls: string[];
let refuse: { status: number; detail: string } | undefined;
beforeEach(() => {
  calls = [];
  refuse = undefined;
  localStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method !== "GET") calls.push(`${req.method} ${path} ${await req.text()}`);
      if (req.method !== "GET" && refuse) return json({ detail: refuse.detail }, refuse.status);
      if (path === "/api/libraries" && req.method === "GET")
        return json([
          { id: 1, name: "Library", cards: 12, lorebooks: 1, created_at: now },
          { id: 2, name: "Chub archive", cards: 300, lorebooks: 4, created_at: now },
        ]);
      if (path === "/api/plugins" || path === "/api/instances") return json([]);
      if (path.endsWith("/settings")) {
        const off = { value: null, default: null, overridden: false };
        return json({ score_instance: off, hide_below: off, duplicates: { ...off, value: "off", default: "off" },
          duplicate_distance: { ...off, value: 3, default: 3 }, duplicate_keep: { ...off, value: "best", default: "best" }, duplicate_decide: off });
      }
      if (req.method === "DELETE") return json({ id: 9, kind: "library.delete", status: "running", created_at: now, payload: {}, progress: { done: 100, total: 304 } }, 202);
      if (path === "/api/jobs/9") return json({ id: 9, kind: "library.delete", status: "running", created_at: now, payload: {}, progress: { done: 100, total: 304 } });
      return json({ id: 3, name: "x", cards: 0, lorebooks: 0, created_at: now }, req.method === "POST" ? 201 : 200);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderLibraries() {
  const router = createMemoryRouter([{ path: "/settings/libraries", element: <Toasts><Libraries /></Toasts> }], {
    initialEntries: ["/settings/libraries"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("lists each library with its counts, and library 1 has no delete", async () => {
  renderLibraries();
  const row = (await screen.findByText("Chub archive")).closest("li")!;
  expect(within(row).getByText("300 cards, 4 lorebooks")).toBeInTheDocument();
  expect(screen.getByText("12 cards, 1 lorebook")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Delete Chub archive" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Delete Library" })).not.toBeInTheDocument();
});

test("create and rename", async () => {
  renderLibraries();
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "New library" }), "Drafts{Enter}");
  await user.click(screen.getByRole("button", { name: "Rename Chub archive" }));
  const name = screen.getByRole("textbox", { name: "Name" });
  await user.clear(name);
  await user.type(name, "Archive{Enter}");
  expect(calls).toEqual(['POST /api/libraries {"name":"Drafts"}', 'PATCH /api/libraries/2 {"name":"Archive"}']);
});

async function deleteTyping(name: string) {
  await userEvent.click(await screen.findByRole("button", { name: "Delete Chub archive" }));
  await userEvent.type(screen.getByLabelText("Type Chub archive to confirm"), name);
  await userEvent.click(screen.getByRole("button", { name: "Delete library" }));
}

test("delete sends the typed name and shows the job's progress", async () => {
  localStorage.setItem("innkeeper.library", "2");
  renderLibraries();
  await deleteTyping("Chub archive");
  await waitFor(() => expect(calls).toEqual(['DELETE /api/libraries/2 {"confirm":"Chub archive"}']));
  expect(await screen.findByRole("link", { name: /Deleting Chub archive: running 100 of 304/ })).toHaveAttribute("href", "/settings/jobs/9");
  // The switcher would otherwise open a library that's gone.
  expect(localStorage.getItem("innkeeper.library")).toBe("1");
});

test("a wrong name deletes nothing", async () => {
  renderLibraries();
  await deleteTyping("chub");
  expect(screen.getByRole("button", { name: "Delete library" })).toBeDisabled();
  expect(calls).toEqual([]);
});

test("a refused create says why", async () => {
  refuse = { status: 409, detail: "a library named Drafts already exists" };
  renderLibraries();
  await userEvent.type(await screen.findByRole("textbox", { name: "New library" }), "Drafts{Enter}");
  expect(await screen.findByText("a library named Drafts already exists")).toBeInTheDocument();
});

test("a refused delete says why", async () => {
  refuse = { status: 409, detail: "a delete is already running" };
  renderLibraries();
  await deleteTyping("Chub archive");
  expect(await screen.findByText("a delete is already running")).toBeInTheDocument();
});
