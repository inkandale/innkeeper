import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import type { Entity } from "./Card";
import { CardLorebooks } from "./CardLorebooks";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const card = {
  id: 7, slug: "brakka", provenance: "api", created_at: now, lorebooks: [3], library_id: 2,
  revision: { id: 11, content_hash: "x", message: "", source: "ui", created_at: now, data: { name: "Brakka" } },
} as unknown as Entity;

let sent: { method: string; path: string; body: unknown }[];
beforeEach(() => {
  sent = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      const body = req.method === "GET" || req.method === "DELETE" ? null : await req.json().catch(() => null);
      sent.push({ method: req.method, path, body });
      if (req.method === "GET" && path === "/api/lorebooks/3") return json({ id: 3, revision: { id: 1, data: { name: "Forge", entries: [{}, {}] } } });
      if (req.method === "POST" && path === "/api/lorebooks/search") return json({ hits: [{ id: 5, fields: { name: "Mines" } }], total: 1 });
      if (req.method === "POST" && path === "/api/lorebooks") return json({ id: 9, revision: { id: 1, data: body } }, 201);
      return new Response(null, { status: 204 });
    }),
  );
});

function renderLorebooks() {
  const router = createMemoryRouter(
    [
      { path: "/cards/7", element: <CardLorebooks card={card} /> },
      { path: "/lorebooks/:id", element: <p>Lorebook page</p> },
    ],
    { initialEntries: ["/cards/7"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const writes = () => sent.filter((s) => s.method !== "GET" && !s.path.endsWith("/search"));

test("a linked lorebook shows its entry count beside its name", async () => {
  renderLorebooks();
  expect(await screen.findByRole("link", { name: "Forge" })).toHaveAttribute("href", "/lorebooks/3");
  expect(await screen.findByText("2 entries")).toBeInTheDocument();
});

test("× unlinks a linked lorebook from the card", async () => {
  renderLorebooks();
  await userEvent.click(await screen.findByRole("button", { name: "Unlink Forge" }));
  await waitFor(() => expect(writes()).toEqual([{ method: "DELETE", path: "/api/cards/7/lorebooks/3", body: null }]));
});

test("Link searches the card's library and links the picked lorebook", async () => {
  renderLorebooks();
  await userEvent.click(screen.getByRole("button", { name: "Link…" }));
  await userEvent.type(screen.getByRole("textbox", { name: "Find a lorebook" }), "mines");
  await userEvent.click(screen.getByRole("button", { name: "Search" }));
  await userEvent.click(await screen.findByRole("button", { name: "Mines" }));
  await waitFor(() => expect(writes()).toEqual([{ method: "PUT", path: "/api/cards/7/lorebooks/5", body: null }]));
  expect(sent.find((s) => s.path === "/api/lorebooks/search")?.body).toMatchObject({ library: 2, text: "mines" });
});

test("Create makes an empty one named for the card in its library, links it, and opens it", async () => {
  renderLorebooks();
  await userEvent.click(screen.getByRole("button", { name: "Create" }));
  expect(await screen.findByText("Lorebook page")).toBeInTheDocument();
  expect(writes()).toEqual([
    {
      method: "POST",
      path: "/api/lorebooks",
      body: { data: { name: "Brakka", entries: [] }, library: 2, source: "ui", message: "New lorebook for Brakka" },
    },
    { method: "PUT", path: "/api/cards/7/lorebooks/9", body: null },
  ]);
});
