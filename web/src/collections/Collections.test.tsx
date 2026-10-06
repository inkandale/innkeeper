import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Collections } from "./Collections";

let list: { id: number; name: string; description: string; created_at: string; cards: number; avatars?: string[] }[];
let calls: string[];
beforeEach(() => {
  list = [{ id: 3, name: "Crew", description: "", created_at: "2026-10-06T00:00:00Z", cards: 5, avatars: ["ab".repeat(32)] }];
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method !== "GET") calls.push(`${req.method} ${path} ${await req.text()}`);
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
  const router = createMemoryRouter([{ path: "/collections", element: <Collections /> }], {
    initialEntries: ["/collections"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("each collection links to its cards with a mosaic", async () => {
  renderCollections();
  const link = await screen.findByRole("link", { name: /Crew/ });
  expect(link).toHaveAttribute("href", "/cards?f=collection%3A3");
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
    'POST /api/collections {"name":"Villains"}',
    'PATCH /api/collections/3 {"name":"Sailors"}',
    "DELETE /api/collections/3 ",
  ]);
});
