import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Collections } from "./Collections";

let list: { id: number; name: string; description: string; created_at: string; cards: number; avatars?: string[] }[];
let calls: string[];
let gets: string[];
let conflict: boolean;
beforeEach(() => {
  list = [{ id: 3, name: "Crew", description: "", created_at: "2026-10-06T00:00:00Z", cards: 5, avatars: ["ab".repeat(32)] }];
  calls = [];
  gets = [];
  conflict = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      const path = url.pathname;
      if (path === "/api/libraries") return new Response(JSON.stringify([1, 2, 3].map((id) => ({ id, name: `Library ${id}`, cards: 0, lorebooks: 0, created_at: "2026-10-01T00:00:00Z" }))), { headers: { "Content-Type": "application/json" } });
      if (req.method === "GET") gets.push(path + url.search);
      else calls.push(`${req.method} ${path} ${await req.text()}`);
      if (req.method === "POST" && conflict)
        return new Response(JSON.stringify({ detail: "name taken" }), {
          status: 409,
          headers: { "Content-Type": "application/problem+json" },
        });
      if (req.method === "DELETE") return new Response(null, { status: 204 });
      return new Response(JSON.stringify(req.method === "GET" ? list : list[0]), {
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  vi.stubGlobal("confirm", () => true);
});
afterEach(() => vi.unstubAllGlobals());

function renderCollections() {
  const router = createMemoryRouter([{ path: "/collections", element: <Toasts><Collections /></Toasts> }], {
    initialEntries: ["/collections?library=2"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("each collection of the current library links to its cards with a mosaic", async () => {
  renderCollections();
  const link = await screen.findByRole("link", { name: /Crew/ });
  expect(gets).toContain("/api/collections?library=2");
  expect(link).toHaveAttribute("href", "/cards?f=collection%3A3&library=2");
  expect(within(link).getByText("5 cards")).toBeInTheDocument();
  expect(link.querySelector("img")).toHaveAttribute("src", `/api/blobs/${"ab".repeat(32)}?w=160`);
});

test("create, rename, and delete", async () => {
  renderCollections();
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "New collection" }), "Villains{Enter}");
  await user.click(screen.getByRole("button", { name: "Rename Crew" }));
  const name = screen.getByRole("textbox", { name: "Name" });
  await user.clear(name);
  await user.type(name, "Sailors{Enter}");
  await user.click(screen.getByRole("button", { name: "Delete Crew" }));
  expect(calls).toEqual([
    'POST /api/collections {"name":"Villains","library":2}',
    'PATCH /api/collections/3 {"name":"Sailors"}',
    "DELETE /api/collections/3 ",
  ]);
});

test("a refused create toasts its error", async () => {
  conflict = true;
  renderCollections();
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "New collection" }), "Crew{Enter}");
  expect(await screen.findByText("name taken")).toBeInTheDocument();
});

test("a blank or unchanged rename sends nothing, and Escape cancels", async () => {
  renderCollections();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Rename Crew" }));
  await user.clear(screen.getByRole("textbox", { name: "Name" }));
  await user.keyboard("{Enter}");
  expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Rename Crew" }));
  await user.keyboard("{Enter}");
  expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Rename Crew" }));
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  expect(calls).toEqual([]);
});

test("a single card is not pluralized", async () => {
  list[0]!.cards = 1;
  renderCollections();
  expect(await screen.findByText("1 card")).toBeInTheDocument();
});
