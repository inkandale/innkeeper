import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import type { Entity } from "./Card";
import { Metadata } from "./Metadata";

const entity = {
  id: 7,
  slug: "brakka",
  provenance: "chub",
  created_at: "2026-10-12T10:00:00Z",
  library_id: 2,
  variants: null,
  revision: { id: 505702, content_hash: "abcdef0123456789abcdef", message: "", source: "ui", created_at: "2026-10-30T09:00:00Z" },
} as Entity;

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const body = new URL(req.url).pathname === "/api/libraries" ? [{ id: 2, name: "Chub archive", cards: 1, lorebooks: 0, created_at: "2026-10-01T00:00:00Z" }] : [];
      return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

test("lists the stored facts, with the last edit linking to its revision", async () => {
  const router = createMemoryRouter([{ path: "*", element: <Metadata kind="card" entity={entity} /> }]);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  const row = (label: string) => screen.getByText(label).parentElement!;
  expect(await within(row("Library")).findByText("Chub archive")).toBeInTheDocument();
  expect(within(row("Slug")).getByText("brakka")).toBeInTheDocument();
  expect(within(row("Provenance")).getByText("chub")).toBeInTheDocument();
  expect(within(row("Revision")).getByText("505702 (ui)")).toBeInTheDocument();
  expect(within(row("Last edited")).getByRole("link")).toHaveAttribute("href", "/cards/7/history?rev=505702");
  expect(within(row("Content hash")).getByRole("button", { name: "abcdef012345" })).toHaveAttribute("title", "abcdef0123456789abcdef");
});
