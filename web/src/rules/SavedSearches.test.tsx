import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { SavedSearches } from "./SavedSearches";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

let calls: string[];
let bodies: unknown[];
beforeEach(() => {
  calls = [];
  bodies = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/libraries") return json([1, 2].map((id) => ({ id, name: `Library ${id}`, cards: 0, lorebooks: 0, created_at: "" })));
      if (req.method !== "GET") {
        calls.push(`${req.method} ${path}`);
        if (req.method === "PATCH") {
          const body = await req.json();
          bodies.push(body);
          return json({ id: 3, name: body.name, filter: {}, sort: "", created_at: "" });
        }
        return new Response(null, { status: 204 });
      }
      if (path === "/api/saved-searches")
        return json([
          { id: 3, name: "Junk", filter: { tag: "junk" }, sort: "name", created_at: "", cards: 12, rules: [{ id: 9, name: "Hide junk" }] },
          { id: 4, name: "Old", filter: { enrichment: "quality", instance: 99, gte: 1 }, sort: "", created_at: "" },
        ]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPage() {
  const router = createMemoryRouter([{ path: "/saved-searches", element: <SavedSearches /> }], { initialEntries: ["/saved-searches?library=2"] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("rename sends a changed name only, and closes on blur", async () => {
  renderPage();
  const user = userEvent.setup();
  const open = async () => {
    await user.click(await screen.findByRole("button", { name: "Rename Junk" }));
    return screen.getByRole("textbox", { name: "Name" });
  };
  await user.type(await open(), "{Enter}");
  expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  await user.clear(await open());
  await user.keyboard("{Enter}");
  expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  await open();
  await user.click(document.body);
  expect(screen.queryByRole("textbox", { name: "Name" })).not.toBeInTheDocument();
  expect(calls).toEqual([]);
  const input = await open();
  await user.clear(input);
  await user.type(input, "  Trash {Enter}");
  await waitFor(() => expect(calls).toEqual(["PATCH /api/saved-searches/3"]));
  expect(bodies).toEqual([{ name: "Trash" }]);
});

test("each saved search opens in its sort, with its count and its rules", async () => {
  const router = createMemoryRouter([{ path: "/saved-searches", element: <SavedSearches /> }], { initialEntries: ["/saved-searches?library=2"] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  const user = userEvent.setup();
  expect(await screen.findByRole("link", { name: "Junk" })).toHaveAttribute("href", "/cards?saved=3&sort=name&library=2");
  expect(screen.getByText("12 cards")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Hide junk" })).toHaveAttribute("href", "/rules/9");
  expect(screen.getByRole("link", { name: "Add rule to Junk" })).toHaveAttribute("href", "/rules/new?search=3");
  expect(screen.getByText("Its filter no longer reads")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Delete Junk" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "Delete Old" }));
  await user.click(screen.getByRole("button", { name: "Delete saved search" }));
  await waitFor(() => expect(calls).toEqual(["DELETE /api/saved-searches/4"]));
});
