import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { RuleEditor } from "./RuleEditor";
import { Rules } from "./Rules";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const rule = { id: 9, name: "Hide junk", saved_search_id: 3, saved_search: "Junk", action: "archive", all_libraries: true, enabled: false, affected: [] };
let calls: { method: string; path: string; body: unknown }[];
beforeEach(() => {
  calls = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      const body = req.method === "GET" || req.method === "DELETE" ? undefined : await req.json();
      if (req.method !== "GET") calls.push({ method: req.method, path, body });
      if (path === "/api/libraries") return json([{ id: 1, name: "Main", cards: 0, lorebooks: 0, created_at: "" }]);
      if (path === "/api/saved-searches") return json([{ id: 3, name: "Junk", filter: { tag: "junk" }, sort: "", created_at: "" }]);
      if (path === "/api/rules/preview")
        return json([{ library: 1, apply: { total: 2, hits: [{ id: 5, slug: "a", revision_id: 1, fields: { name: "Junk a" } }] }, give_back: { total: 0, hits: [] } }]);
      if (path === "/api/rules" && req.method === "GET") return json([rule]);
      if (path === "/api/rules" || path === "/api/rules/9") return json({ ...rule, ...(body as object) });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/rules", element: <Toasts><Rules /></Toasts> },
      { path: "/rules/new", element: <Toasts><RuleEditor /></Toasts> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("turning a rule on previews it and saves only on confirm", async () => {
  renderAt("/rules");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("switch", { name: "Enable Hide junk" }));
  expect(await screen.findByText("It would archive 2 cards and unarchive 0 cards.")).toBeInTheDocument();
  expect(screen.getByText("To archive: Junk a")).toBeInTheDocument();
  expect(calls).toEqual([
    { method: "POST", path: "/api/rules/preview", body: { id: 9, rule: { name: "Hide junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: true }, page: 1 } },
  ]);
  await user.click(screen.getByRole("button", { name: "Enable rule" }));
  await waitFor(() =>
    expect(calls.at(-1)).toEqual({ method: "PUT", path: "/api/rules/9", body: { name: "Hide junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: true } }),
  );
});

test("a new rule starts off, on the saved search it was added from", async () => {
  const router = renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: /^Name/ }), "Hide junk");
  await waitFor(() => expect(screen.getByRole("combobox", { name: /Saved search/ })).toHaveValue("3"));
  await user.click(screen.getByRole("button", { name: "Add rule" }));
  await waitFor(() =>
    expect(calls).toEqual([{ method: "POST", path: "/api/rules", body: { name: "Hide junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: false } }]),
  );
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules"));
});
