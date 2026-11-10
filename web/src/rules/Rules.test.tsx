import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Rules } from "./Rules";
import { RuleBuilder } from "./RuleBuilder";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const rule = { id: 9, name: "Hide junk", saved_search_id: 3, saved_search: "Junk", action: "archive", all_libraries: true, enabled: false, affected: [] };
let calls: { method: string; path: string; body: unknown }[];
let current: typeof rule & { collection_id?: number; libraries?: number[] };
beforeEach(() => {
  calls = [];
  current = rule;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/cards/search")
        return (await req.json()).facets_only
          ? json({ facets: { tags: [{ name: "junk", cards: 3 }], sources: [{ kind: "chub", cards: 2 }] } })
          : json({ total: 2, hits: [{ id: 5, slug: "a", revision_id: 1, fields: { name: "Junk a" } }] });
      if (path === "/api/enrichments/keys") return json({
          browse: [
            { key: "quality", label: "Quality", type: "score", instances: [1] },
            { key: "rating", label: "Rating", type: "score", instance: 3, instances: [3] },
          ],
          instances: [
            { id: 1, label: "Scorer", keys: [], scored: true },
            { id: 3, label: "Rater", keys: [], scored: true },
          ],
          scorers: [],
        });
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
      const junk = { id: 3, name: "Junk", filter: { and: [{ tag: "junk" }] }, sort: "", created_at: "", rules: [{ id: 9, name: "Hide junk" }] };
      if (path === "/api/saved-searches/4" && req.method === "DELETE") return new Response(null, { status: 204 });
      if (path === "/api/saved-searches/3" && req.method === "PATCH") return json(junk);
      if (path === "/api/saved-searches") return json([junk]);
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
      { path: "/rules/new", element: <Toasts><RuleBuilder /></Toasts> },
      { path: "/rules/:id", element: <Toasts><RuleBuilder /></Toasts> },
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
  await user.click(await screen.findByRole("switch", { name: "On: Hide junk" }));
  expect(await screen.findByText("It would archive 2 cards and unarchive 0 cards.")).toBeInTheDocument();
  expect(screen.getByText("To archive: Junk a, and 1 more")).toBeInTheDocument();
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

test("a rule built from nothing saves its search, then the rule", async () => {
  const router = renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Hide low");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "score:quality");
  await user.selectOptions(screen.getByRole("combobox", { name: "Operator 1" }), "at_most");
  await user.type(screen.getByRole("spinbutton", { name: "Value 1" }), "40");
  expect(await screen.findByText(/2 cards match in all libraries/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/saved-searches", body: { name: "Hide low", filter: { and: [{ enrichment: "quality", lte: 40 }] }, sort: "" } },
      { method: "POST", path: "/api/rules", body: { name: "Hide low", saved_search_id: 4, action: "archive", all_libraries: true, enabled: false } },
    ]),
  );
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules"));
});

test("a rule that fails to save deletes the search it saved", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/rules" && req.method === "POST"
      ? new Response(JSON.stringify({ status: 409, title: "Conflict", detail: "a rule named Hide low already exists" }), { status: 409, headers: { "Content-Type": "application/problem+json" } })
      : fetch(req),
  );
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Hide low");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText(/already exists/)).toBeInTheDocument();
  expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/saved-searches", "DELETE /api/saved-searches/4"]);
});

test("Add rule on a saved search loads its conditions, and editing them patches it", async () => {
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  expect(await screen.findByDisplayValue("junk")).toBeInTheDocument();
  expect(screen.getByText(/also changes Hide junk/)).toBeInTheDocument();
  await user.type(screen.getByRole("textbox", { name: "Rule name" }), "More junk");
  await user.click(screen.getByRole("button", { name: "Not 1" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls).toEqual([
      { method: "POST", path: "/api/rules", body: { name: "More junk", saved_search_id: 3, action: "archive", all_libraries: true, enabled: false } },
      { method: "PATCH", path: "/api/saved-searches/3", body: { filter: { and: [{ not: { tag: "junk" } }] } } },
    ]),
  );
});

test("Save and turn on previews the unsaved rule, then saves and turns it on", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Twins");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  await user.click(screen.getByRole("button", { name: "Save and turn on" }));
  await screen.findByText("It would archive 2 cards and unarchive 0 cards.");
  expect(screen.getByText("To archive: Junk a, and 1 more")).toBeInTheDocument();
  expect(screen.queryByText(/Page 1 of/)).not.toBeInTheDocument();
  expect(calls).toEqual([
    {
      method: "POST",
      path: "/api/rules/preview",
      body: { rule: { name: "Twins", saved_search_id: 0, action: "archive", all_libraries: true, enabled: true }, filter: { and: [{ shares_source: true }] }, page: 1 },
    },
  ]);
  await user.click(screen.getByRole("button", { name: "Turn on" }));
  await waitFor(() =>
    expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview", "POST /api/saved-searches", "POST /api/rules", "PUT /api/rules/9"]),
  );
});

test("editing a rule that's on previews the new conditions before saving anything", async () => {
  current = { ...rule, enabled: true };
  renderAt("/rules/9");
  const user = userEvent.setup();
  expect(await screen.findByDisplayValue("junk")).toBeInTheDocument();
  // A tag input has suggestions, so it's a combobox, not a textbox.
  await user.clear(screen.getByLabelText("Value 1"));
  await user.type(screen.getByLabelText("Value 1"), "spam");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByText("It would archive 2 cards and unarchive 0 cards.");
  expect(calls.map((c) => c.method + " " + c.path)).toEqual(["POST /api/rules/preview"]);
  expect((calls[0]!.body as { filter: unknown }).filter).toEqual({ and: [{ tag: "spam" }] });
});

test("the live count adds up every library the rule covers", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/libraries"
      ? json([
          { id: 1, name: "Main", cards: 0, lorebooks: 0, created_at: "" },
          { id: 2, name: "Chub", cards: 0, lorebooks: 0, created_at: "" },
        ])
      : fetch(req),
  );
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  expect(await screen.findByText(/^4 cards match in all libraries: Junk a, Junk a, …$/)).toBeInTheDocument();
  await user.selectOptions(screen.getByRole("combobox", { name: "Libraries" }), "2");
  expect(await screen.findByText(/^2 cards match in Chub: Junk a, …$/)).toBeInTheDocument();
});

test("a search the page can't show is read-only with Edit on Cards", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET"
      ? json([{ id: 3, name: "Junk", filter: { and: [{ collection: 4 }] }, sort: "", created_at: "", rules: [] }])
      : fetch(req),
  );
  renderAt("/rules/new?search=3");
  expect(await screen.findByText(/Matches saved search/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Edit on Cards" })).toHaveAttribute("href", "/cards?saved=3");
  expect(screen.queryByRole("button", { name: "Add condition" })).not.toBeInTheDocument();
});

test("a collect rule names one library and its collection", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByRole("combobox", { name: "Action" }), "collect");
  expect(screen.getByRole("combobox", { name: "Libraries" })).toHaveValue("1");
  expect(screen.getByText("Pick a collection.")).toBeInTheDocument();
  await user.selectOptions(await screen.findByRole("combobox", { name: "Collection" }), "7");
  await user.type(screen.getByRole("textbox", { name: "Rule name" }), "Faves");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls.at(-1)).toEqual({
      method: "POST",
      path: "/api/rules",
      body: { name: "Faves", saved_search_id: 4, action: "collect", collection_id: 7, all_libraries: false, libraries: [1], enabled: false },
    }),
  );
});

test("Add rule on the Rules page opens the builder", async () => {
  renderAt("/rules");
  await userEvent.click(await screen.findByRole("link", { name: "Add rule" }));
  expect(await screen.findByRole("heading", { name: "New rule" })).toBeInTheDocument();
});

test("a rule that can't be turned on after it's added still lands on Rules", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) => {
    if (new URL(req.url).pathname !== "/api/rules/9" || req.method !== "PUT") return fetch(req);
    calls.push({ method: "PUT", path: "/api/rules/9", body: await req.json() });
    return new Response(JSON.stringify({ status: 500, title: "Internal Server Error", detail: "boom" }), {
      status: 500,
      headers: { "Content-Type": "application/problem+json" },
    });
  });
  const router = renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "More junk");
  await user.click(screen.getByRole("button", { name: "Save and turn on" }));
  await user.click(await screen.findByRole("button", { name: "Turn on" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/rules"));
  expect(calls.filter((c) => c.method === "POST" && c.path === "/api/rules")).toHaveLength(1);
  expect(await screen.findByText(/couldn't turn it on: boom/)).toBeInTheDocument();
});

test("a rule on some libraries keeps every library ticked", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/libraries"
      ? json([
          { id: 1, name: "Main", cards: 0, lorebooks: 0, created_at: "" },
          { id: 2, name: "Spare", cards: 0, lorebooks: 0, created_at: "" },
        ])
      : fetch(req),
  );
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Twins");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.selectOptions(screen.getByRole("combobox", { name: "Field 1" }), "shares_source");
  await user.selectOptions(await screen.findByRole("combobox", { name: "Libraries" }), "some");
  expect(screen.getByText("Pick at least one library.")).toBeInTheDocument();
  await user.click(await screen.findByRole("checkbox", { name: "Main" }));
  await user.click(screen.getByRole("checkbox", { name: "Spare" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls.at(-1)).toEqual({
      method: "POST",
      path: "/api/rules",
      body: { name: "Twins", saved_search_id: 4, action: "archive", all_libraries: false, libraries: [1, 2], enabled: false },
    }),
  );
});

test("a rule that fails to save leaves its shared search as it was", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/rules" && req.method === "POST"
      ? new Response(JSON.stringify({ status: 409, title: "Conflict", detail: "a rule named More junk already exists" }), { status: 409, headers: { "Content-Type": "application/problem+json" } })
      : fetch(req),
  );
  renderAt("/rules/new?search=3");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "More junk");
  await user.click(screen.getByRole("button", { name: "Not 1" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText(/already exists/)).toBeInTheDocument();
  expect(calls.filter((c) => c.method === "PATCH")).toEqual([]);
});

test("saving a rule with untouched conditions leaves its search alone", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET"
      ? json([{ id: 3, name: "Junk", filter: { and: [{ enrichment: "rating", gte: 2, instance: 3 }] }, sort: "", created_at: "", rules: [{ id: 9, name: "Hide junk" }] }])
      : fetch(req),
  );
  renderAt("/rules/9");
  const user = userEvent.setup();
  expect(await screen.findByDisplayValue("2")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(calls.map((c) => c.method + " " + c.path)).toEqual(["PUT /api/rules/9"]));
});

test("removing a group's last item removes the group", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Add group" }));
  await user.click(screen.getByRole("button", { name: "Remove condition 1.1" }));
  expect(screen.queryByRole("button", { name: "Remove group 1" })).not.toBeInTheDocument();
});

test("a group mixes or inside an and, and not negates it", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Rule name" }), "Mixed");
  await user.click(screen.getByRole("button", { name: "Add condition" }));
  await user.type(screen.getByLabelText("Value 1"), "nsfw");
  await user.click(screen.getByRole("button", { name: "Add group" }));
  await user.type(screen.getByLabelText("Value 2.1"), "a");
  await user.click(screen.getByRole("button", { name: "Add condition to group 2" }));
  await user.type(screen.getByLabelText("Value 2.2"), "b");
  await user.selectOptions(screen.getByRole("combobox", { name: "Connector in group 2" }), "or");
  await user.click(screen.getByRole("button", { name: "Not 2" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(calls[0]).toEqual({
      method: "POST",
      path: "/api/saved-searches",
      body: { name: "Mixed", filter: { and: [{ tag: "nsfw" }, { not: { or: [{ tag: "a" }, { tag: "b" }] } }] }, sort: "" },
    }),
  );
});

test("the connector menu sits on the second item and changes the whole group", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  for (let i = 0; i < 3; i++) await user.click(await screen.findByRole("button", { name: "Add condition" }));
  expect(screen.getAllByRole("combobox", { name: "Connector" })).toHaveLength(1);
  await user.selectOptions(screen.getByRole("combobox", { name: "Connector" }), "or");
  expect(screen.getAllByText("or", { selector: ".rule-connector span" })).toHaveLength(1);
});

test("Add group stops two levels down", async () => {
  renderAt("/rules/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Add group" }));
  await user.click(screen.getByRole("button", { name: "Add group to group 1" }));
  expect(screen.queryByRole("button", { name: "Add group to group 1.2" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Add condition to group 1.2" })).toBeInTheDocument();
});

test("a Cards search with any-of groups edits as groups", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/saved-searches" && req.method === "GET"
      ? json([{ id: 3, name: "Junk", filter: { and: [{ or: [{ tag: "a" }, { tag: "b" }] }] }, sort: "", created_at: "", rules: [] }])
      : fetch(req),
  );
  renderAt("/rules/new?search=3");
  expect(await screen.findByDisplayValue("a")).toBeInTheDocument();
  expect(screen.getByLabelText("Value 1.2")).toHaveValue("b");
  expect(screen.queryByText(/can't show/)).not.toBeInTheDocument();
});
