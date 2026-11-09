import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Rules } from "./Rules";
import { RuleWizard } from "./RuleWizard";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const rule = { id: 9, name: "Hide junk", saved_search_id: 3, saved_search: "Junk", action: "archive", all_libraries: true, enabled: false, affected: [] };
let calls: { method: string; path: string; body: unknown }[];
let current: typeof rule;
beforeEach(() => {
  calls = [];
  current = rule;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/cards/search") return json({ total: 2, hits: [] });
      const body = req.method === "GET" || req.method === "DELETE" ? undefined : await req.json();
      if (req.method !== "GET") calls.push({ method: req.method, path, body });
      if (path === "/api/libraries") return json([{ id: 1, name: "Main", cards: 0, lorebooks: 0, created_at: "" }]);
      if (path === "/api/collections") return json([{ id: 7, name: "Faves", description: "", cards: 0, created_at: "" }]);
      if (path === "/api/saved-searches" && req.method === "POST") {
        const { name } = body as { name: string };
        if (name === "Taken")
          return new Response(JSON.stringify({ status: 409, title: "Conflict", detail: "a saved search named Taken already exists" }), {
            status: 409,
            headers: { "Content-Type": "application/problem+json" },
          });
        return json({ id: 4, name, filter: {}, sort: "", created_at: "" });
      }
      if (path === "/api/saved-searches") return json([{ id: 3, name: "Junk", filter: { tag: "junk" }, sort: "", created_at: "" }]);
      if (path === "/api/rules/preview")
        return json([{ library: 1, apply: { total: 2, hits: [{ id: 5, slug: "a", revision_id: 1, fields: { name: "Junk a" } }] }, give_back: { total: 0, hits: [] } }]);
      if (path === "/api/rules" && req.method === "GET") return json([current]);
      if (path === "/api/rules" || path === "/api/rules/9") return json({ ...current, ...(body as object) });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/rules", element: <Toasts><Rules /></Toasts> },
      { path: "/rules/new", element: <Toasts><RuleWizard /></Toasts> },
      { path: "/rules/:id", element: <Toasts><RuleWizard /></Toasts> },
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

/** noSearches makes GET /api/saved-searches answer none. */
function noSearches() {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET" ? json([]) : fetch(req),
  );
}

test("turning a rule on previews it and saves only on confirm", async () => {
  renderAt("/rules");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("switch", { name: "On: Hide junk" }));
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

test("saving an enabled rule previews it before the PUT", async () => {
  current = { ...rule, enabled: true };
  renderAt("/rules/9");
  const user = userEvent.setup();
  await screen.findByRole("heading", { name: "Hide junk" });
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText("It would archive 2 cards and unarchive 0 cards.")).toBeInTheDocument();
  expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview"]);
  await user.click(screen.getByRole("button", { name: "Save rule" }));
  await waitFor(() =>
    expect(calls.at(-1)).toEqual({ method: "PUT", path: "/api/rules/9", body: { name: "Hide junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: true } }),
  );
});
test("a new rule on a picked saved search starts off", async () => {
  const router = renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("textbox", { name: /^Name/ })).toHaveValue("Junk");
  await user.clear(screen.getByRole("textbox", { name: /^Name/ }));
  await user.type(screen.getByRole("textbox", { name: /^Name/ }), "Hide junk");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([{ method: "POST", path: "/api/rules", body: { name: "Hide junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: false } }]),
  );
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules"));
});

test("a built search is saved, then the rule on it", async () => {
  noSearches();
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: /Shares a source/ }));
  expect(await screen.findByText("2 cards match in this library.")).toBeInTheDocument();
  await user.type(screen.getByRole("textbox", { name: "Search name" }), "Twins");
  await user.click(screen.getByRole("button", { name: "Save and continue" }));
  await user.click(await screen.findByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("textbox", { name: /^Name/ })).toHaveValue("Twins");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/saved-searches", body: { name: "Twins", filter: { and: [{ shares_source: true }] }, sort: "" } },
      { method: "POST", path: "/api/rules", body: { name: "Twins", saved_search_id: 4, action: "archive", all_libraries: true, enabled: false } },
    ]),
  );
});

test("a taken search name shows on step 1", async () => {
  noSearches();
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: /Shares a source/ }));
  await user.type(screen.getByRole("textbox", { name: "Search name" }), "Taken");
  await user.click(screen.getByRole("button", { name: "Save and continue" }));
  expect(await screen.findByText(/already exists/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save and continue" })).toBeInTheDocument();
});

test("Save and turn on previews, then adds the rule and turns it on", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  for (let i = 0; i < 3; i++) await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Save and turn on" }));
  await user.click(await screen.findByRole("button", { name: "Turn on" }));
  await waitFor(() =>
    expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview", "POST /api/rules", "PUT /api/rules/9"]),
  );
  expect((calls.at(-1)!.body as { enabled: boolean }).enabled).toBe(true);
});

test("a collect rule skips Where", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.selectOptions(screen.getByRole("combobox", { name: /Action/ }), "collect");
  await user.selectOptions(await screen.findByRole("combobox", { name: /Collection/ }), "7");
  expect(screen.queryByRole("button", { name: /Where/ })).not.toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(screen.getByRole("textbox", { name: /^Name/ })).toBeInTheDocument();
});

test("a rule on no library can't go on to Review", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await waitFor(() => expect(screen.getByRole("radio", { name: "Junk" })).toBeChecked());
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("button", { name: "Next" }));
  await user.click(screen.getByRole("checkbox", { name: /All libraries/ }));
  expect(screen.getByText("Pick at least one library.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Next" })).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: "Main" }));
  expect(screen.getByRole("button", { name: "Next" })).toBeEnabled();
});

test("editing a rule opens at Review, with its action fixed", async () => {
  renderAt("/rules/9");
  const user = userEvent.setup();
  expect(await screen.findByRole("textbox", { name: /^Name/ })).toHaveValue("Hide junk");
  await user.click(screen.getByRole("button", { name: "2. Do" }));
  expect(screen.getByRole("combobox", { name: /Action/ })).toBeDisabled();
});

test("Add rule on the Rules page opens the wizard", async () => {
  renderAt("/rules");
  await userEvent.click(await screen.findByRole("link", { name: "Add rule" }));
  expect(await screen.findByRole("heading", { name: "New rule" })).toBeInTheDocument();
});

test("with no saved searches, Match opens on Build", async () => {
  noSearches();
  renderAt("/rules/new");
  expect(await screen.findByRole("checkbox", { name: /Shares a source/ })).toBeInTheDocument();
});
