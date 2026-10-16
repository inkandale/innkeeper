import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Cards } from "./Cards";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const results = {
  total: 49,
  hits: [
    {
      id: 7,
      slug: "brakka",
      revision_id: 1,
      fields: { name: "Brakka", description: "A grumpy dwarf.", avatar: "ab".repeat(32) },
      tags: ["fantasy", "dwarf", "smith", "grumpy"],
      lorebooks: [3],
      targets: [{ instance: 2, is: "ahead" }],
    },
    { id: 8, slug: "carrow", revision_id: 1, fields: { name: "Carrow" } },
    { id: 9, slug: "dellin", revision_id: 1, fields: { name: "Dellin" } },
  ],
  facets: {
    tags: [
      { name: "fantasy", cards: 40 },
      { name: "pirate", cards: 9 },
    ],
    targets: [{ instance: 2, is: "ahead", cards: 1 }],
  },
};

let keys: unknown;
let searches: unknown[];
let posts: { path: string; body: unknown }[];
let previews: unknown[];
let family: unknown;
let puts: { path: string; body: unknown }[];
beforeEach(() => {
  localStorage.clear();
  keys = { instance_id: null, keys: [] };
  searches = [];
  posts = [];
  previews = [];
  family = undefined;
  puts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method === "PUT") {
        puts.push({ path, body: await req.json() });
        return new Response(null, { status: 204 });
      }
      if (path === "/api/cards/search") {
        const body = await req.json();
        searches.push(body);
        if (body.page?.size === 1 && body.facets?.includes("targets"))
          return json({ hits: [], total: 1, facets: { targets: [{ instance: 2, is: "ahead", cards: 1 }] } });
        if (body.include?.includes("enrichments")) {
          const scores: Record<number, unknown> = { 7: { quality: { value: 78, stale: false } }, 8: { quality: { value: 40, stale: true } } };
          return json({ ...results, hits: results.hits.map((h) => ({ ...h, enrichments: scores[h.id] })) });
        }
        return json(family ? { ...results, hits: [{ ...results.hits[0]!, family }, ...results.hits.slice(1)] } : results);
      }
      if (path === "/api/pushes/preview") {
        previews.push(await req.json());
        return json({ total: 1, new: 0, ahead: 1, in_sync: 0, cant_check: 0, drifted: 0, confirm: "q1" });
      }
      if (path === "/api/imports") {
        posts.push({ path, body: await req.json() });
        return json({ id: 11, kind: "import", status: "queued" });
      }
      if (path === "/api/bulk" || path === "/api/pushes" || path === "/api/enrichments/runs") {
        posts.push({ path, body: await req.json() });
        return json({ id: 5, kind: "bulk", status: "waiting" });
      }
      if (path === "/api/instances")
        return json([
          { id: 2, name: "Lumiverse", plugin: "lumiverse" },
          { id: 3, name: "Cards folder", plugin: "folder" },
          { id: 6, name: "url-import", plugin: "url-import" },
          { id: 5, name: "Judge", plugin: "judge" },
        ]);
      if (path === "/api/plugins")
        return json([
          { name: "lumiverse", capabilities: ["push", "pull"] },
          { name: "folder", capabilities: ["import"] },
          { name: "url-import", capabilities: ["import"], inputs: ["url"] },
          { name: "judge", capabilities: ["enrich"] },
        ]);
      if (path === "/api/collections") return json([{ id: 4, name: "Elves", cards: 0 }]);
      if (path === "/api/enrichments/keys") return json(keys);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderCards(entry: string) {
  const router = createMemoryRouter([{ path: "/cards", element: <Toasts><Cards /></Toasts> }], { initialEntries: [entry] });
  const { container, unmount } = render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, container, unmount };
}

test("the URL becomes the search, and hits become tiles", async () => {
  const { container } = renderCards("/cards?f=tag:fantasy&page=2");
  expect(await screen.findByText("Brakka")).toBeInTheDocument();
  expect(searches).toContainEqual(expect.objectContaining({ filter: { and: [{ tag: "fantasy" }] }, page: { size: 48, number: 2 } }));
  expect(screen.getByText("+1")).toBeInTheDocument();
  expect(screen.getByTitle("Lumiverse: not pushed")).toBeInTheDocument();
  expect(screen.getByText("49 results")).toBeInTheDocument();
  expect(container.querySelector("img")).toHaveAttribute("src", `/api/blobs/${"ab".repeat(32)}?w=320`);
});

test("a facet click filters and shift-click excludes", async () => {
  const { router } = renderCards("/cards?page=3");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /^pirate/ }));
  expect(router.state.location.search).toBe("?f=tag%3Apirate");
  await user.keyboard("{Shift>}");
  await user.click(screen.getByRole("button", { name: /^fantasy/ }));
  await user.keyboard("{/Shift}");
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["tag:pirate", "-tag:fantasy"]);
});

test("a chip removes its filter", async () => {
  const { router } = renderCards("/cards?f=tag:fantasy");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Remove tag: fantasy" }));
  expect(router.state.location.search).toBe("");
});

test("the search box sets the text", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("searchbox", { name: "Search cards" }), "dwarf{Enter}");
  expect(router.state.location.search).toBe("?q=dwarf");
});

test("a new search keeps a chosen sort", async () => {
  const { router } = renderCards("/cards?sort=name");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("searchbox", { name: "Search cards" }), "x{Enter}");
  expect(router.state.location.search).toBe("?q=x&sort=name");
});

test("a new search from the default sort stays default", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("searchbox", { name: "Search cards" }), "x{Enter}");
  expect(router.state.location.search).toBe("?q=x");
});

test("an active facet is pressed", async () => {
  renderCards("/cards?f=tag:pirate");
  expect(await screen.findByRole("button", { name: /^pirate/ })).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("button", { name: /^fantasy/ })).toHaveAttribute("aria-pressed", "false");
});

test("only keeps one value in its section", async () => {
  const { router } = renderCards("/cards?f=tag:fantasy&f=source:chub");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Only pirate" }));
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["source:chub", "tag:pirate"]);
});

test("the exclude button excludes a value", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Exclude pirate" }));
  expect(router.state.location.search).toBe("?f=-tag%3Apirate");
});

test("a section's counts leave out its own selections", async () => {
  renderCards("/cards?f=tag:fantasy&f=source:chub");
  await screen.findByRole("button", { name: /^pirate/ });
  expect(searches).toContainEqual({ filter: { and: [{ source: "chub" }] }, facets: ["tags"], page: { size: 1 } });
  expect(searches).toContainEqual({
    filter: { and: [{ target_state: { states: ["ahead"] } }, { tag: "fantasy" }, { source: "chub" }] },
    page: { size: 1 },
  });
});

test("target rows name the app and state rows show the status dot", async () => {
  const { container } = renderCards("/cards");
  expect(await screen.findByRole("button", { name: /^Lumiverse/ })).toBeInTheDocument();
  const ahead = await screen.findByRole("button", { name: /^not pushed/ });
  expect(ahead.querySelector(".badge.ahead")).not.toBeNull();
  expect(container.querySelector(".facets")).toHaveTextContent("Target");
});

test("chips name targets and states", async () => {
  renderCards("/cards?f=target:2&f=state:ahead");
  expect(await screen.findByRole("button", { name: "Remove target: Lumiverse" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Remove state: not pushed" })).toBeInTheDocument();
});

test("the main search asks for no facets", async () => {
  renderCards("/cards");
  await screen.findByText("Brakka");
  const main = searches.find((s) => (s as { page?: { size?: number } }).page?.size === 48);
  expect(main).not.toHaveProperty("facets");
});

test("a page past the end offers the last page", async () => {
  renderCards("/cards?page=5");
  expect(await screen.findByRole("button", { name: "Go to page 2" })).toBeInTheDocument();
});

test("shift-click selects a range and tags it", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Brakka" }));
  await user.keyboard("{Shift>}");
  await user.click(screen.getByRole("checkbox", { name: "Select Dellin" }));
  await user.keyboard("{/Shift}");
  expect(screen.getByText("3 selected")).toBeInTheDocument();
  expect(screen.queryByRole("searchbox", { name: "Search cards" })).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Tag" }));
  await user.type(screen.getByRole("textbox", { name: "Tags" }), "elf, sage");
  await user.click(screen.getByRole("button", { name: "Add tags" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/bulk", body: { query: { filter: { ids: [7, 8, 9] } }, action: "tag", tags: ["elf", "sage"] } }]));
  expect(await screen.findByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/5");
  expect(screen.getByRole("searchbox", { name: "Search cards" })).toBeInTheDocument();
});

test("select all matching archives the search", async () => {
  localStorage.setItem("innkeeper.library.ungrouped", "true");
  renderCards("/cards?f=tag:fantasy");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Select all 49 matching" }));
  expect(screen.getByText("All 49 matching cards")).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Select Brakka" })).toBeChecked();
  await user.click(screen.getByRole("button", { name: "Archive" }));
  await user.click(screen.getByRole("button", { name: "Archive 49 cards" }));
  await waitFor(() =>
    expect(posts[0]).toEqual({ path: "/api/bulk", body: { query: { filter: { and: [{ tag: "fantasy" }] } }, action: "archive", unpublish: false } }),
  );
  expect(posts[0]!.body).not.toHaveProperty("kind");
});

test("archive can also delete the cards from linked apps", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Archive" }));
  await user.click(screen.getByRole("checkbox", { name: "Also delete from linked apps, chats included" }));
  await user.click(screen.getByRole("button", { name: "Archive 1 card" }));
  await waitFor(() => expect(posts[0]?.body).toMatchObject({ action: "archive", unpublish: true }));
});

test("push offers only targets that push", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Push" }));
  const targets = await screen.findByRole("group", { name: "Targets" });
  expect(within(targets).getAllByRole("checkbox").map((c) => c.closest("label")!.textContent!.trim())).toEqual(["Lumiverse"]);
  expect(await screen.findByText("1 not pushed")).toBeInTheDocument();
  expect(previews).toEqual([{ instance_id: 2, query: { filter: { ids: [8] } } }]);
  await user.click(screen.getByRole("button", { name: "Push 1 card" }));
  await waitFor(() =>
    expect(posts[0]).toEqual({ path: "/api/pushes", body: { instance_id: 2, query: { filter: { ids: [8] } }, confirm: "q1" } }),
  );
  expect(posts[0]!.body).not.toHaveProperty("kind");
});

test("export offers PNG only when a codec instance writes it", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  // Push's target list waits for plugins and instances, so both have loaded.
  await user.click(screen.getByRole("button", { name: "Push" }));
  await screen.findByRole("group", { name: "Targets" });
  await user.click(screen.getByRole("button", { name: "Export" }));
  const format = screen.getByRole("combobox", { name: "Format" });
  expect(Array.from((format as HTMLSelectElement).options).map((o) => o.text)).toEqual(["JSON"]);
});

// answer replaces the fake response for one path.
function answer(path: string, f: (req: Request, rest: (req: Request) => Promise<Response>) => Promise<Response>) {
  const rest = fetch;
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => (new URL(req.url).pathname === path ? f(req, rest) : rest(req))));
}
const never = () => new Promise<Response>(() => {});

test("a section with no values has no header", async () => {
  answer("/api/cards/search", async (req, rest) => {
    const body = await req.clone().json();
    return body.facets?.includes("sources") ? json({ hits: [], total: 0, facets: {} }) : rest(req);
  });
  renderCards("/cards");
  await screen.findByRole("button", { name: /^pirate/ });
  expect(screen.queryByRole("button", { name: "Sources" })).not.toBeInTheDocument();
});

test("no collections sends no collections count", async () => {
  answer("/api/collections", async () => json([]));
  renderCards("/cards");
  await screen.findByRole("button", { name: /^pirate/ });
  expect(searches).not.toContainEqual(expect.objectContaining({ facets: ["collections"] }));
});

test("an excluded state keeps the target rows", async () => {
  answer("/api/cards/search", async (req, rest) => {
    const body = await req.clone().json();
    if (!body.facets?.includes("targets")) return rest(req);
    return json({ hits: [], total: 5, facets: { targets: [{ instance: 2, is: "in_sync", cards: 5 }] } });
  });
  renderCards("/cards?f=-state:ahead");
  expect(await screen.findByRole("button", { name: "Lumiverse 5" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Exclude not pushed" })).toHaveAttribute("aria-pressed", "true");
});

test("a target row sums its states, over the included states only", async () => {
  answer("/api/cards/search", async (req, rest) => {
    const body = await req.clone().json();
    if (!body.facets?.includes("targets")) return rest(req);
    const targets = [{ instance: 2, is: "ahead", cards: 3 }, { instance: 2, is: "in_sync", cards: 5 }];
    return json({ hits: [], total: 8, facets: { targets } });
  });
  renderCards("/cards");
  expect(await screen.findByRole("button", { name: "Lumiverse 8" })).toBeInTheDocument();
  cleanup();
  renderCards("/cards?f=state:in_sync");
  expect(await screen.findByRole("button", { name: "Lumiverse 5" })).toBeInTheDocument();
});

test("a second click while the job starts sends nothing more", async () => {
  answer("/api/bulk", async (req) => {
    posts.push({ path: "/api/bulk", body: await req.json() });
    return never();
  });
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Archive" }));
  await user.click(screen.getByRole("button", { name: "Archive 1 card" }));
  await waitFor(() => expect(posts).toHaveLength(1));
  await user.click(screen.getByRole("button", { name: "Archive 1 card" }));
  await new Promise((r) => setTimeout(r, 20));
  expect(posts).toHaveLength(1);
});

test("a push refused for a stale preview shows the new one", async () => {
  answer("/api/pushes", async () => new Response(JSON.stringify({ detail: "the push would differ from the preview" }), { status: 409 }));
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Push" }));
  await screen.findByText("1 not pushed");
  await user.click(screen.getByRole("button", { name: "Push 1 card" }));
  expect(await screen.findByText(/differ from the preview/)).toBeInTheDocument();
  await waitFor(() => expect(previews).toHaveLength(2));
});

test("a facet click clears the selection, and paging keeps it", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "2" }));
  expect(router.state.location.search).toBe("?page=2");
  expect(screen.getByText("1 selected")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /^pirate/ }));
  expect(screen.getByRole("searchbox", { name: "Search cards" })).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Select Carrow" })).not.toBeChecked();
});

test("collection actions wait for a collection", async () => {
  answer("/api/collections", async () => json([]));
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Collection" }));
  expect(screen.getByRole("button", { name: "Add to collection" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Remove from collection" })).toBeDisabled();
});

test("push waits for targets before saying there are none", async () => {
  answer("/api/plugins", never);
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Push" }));
  expect(screen.getByText("Loading…")).toBeInTheDocument();
  expect(screen.queryByText(/No target can take pushes/)).not.toBeInTheDocument();
});

test("going back to a search doesn't bring its selection back", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: /^pirate/ }));
  await user.click(screen.getByRole("button", { name: "Remove tag: pirate" }));
  expect(screen.getByRole("checkbox", { name: "Select Carrow" })).not.toBeChecked();
  expect(screen.queryByText("1 selected")).not.toBeInTheDocument();
});

const manyTags = Array.from({ length: 12 }, (_, i) => ({ name: `t${i + 1}`, cards: 20 - i }));

test("tags show the ten most used, then all, and filter as you type", async () => {
  answer("/api/cards/search", async () => json({ ...results, facets: { ...results.facets, tags: manyTags } }));
  renderCards("/cards?f=tag:t12");
  const user = userEvent.setup();
  expect(await screen.findByRole("button", { name: /^t10 / })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^t11 / })).not.toBeInTheDocument();
  // Selected tags show even outside the top ten.
  expect(screen.getByRole("button", { name: /^t12 / })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Show all 12" }));
  expect(screen.getByRole("button", { name: /^t11 / })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Show fewer" }));
  await user.type(screen.getByRole("searchbox", { name: "Find a tag" }), "t1");
  expect(screen.getByRole("button", { name: /^t11 / })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /^t2 / })).not.toBeInTheDocument();
});

test("a collapsed section stays collapsed and shows its selection count", async () => {
  const first = renderCards("/cards?f=tag:fantasy&f=tag:pirate");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Tags · 2" }));
  expect(screen.getByRole("button", { name: "Tags · 2" })).toHaveAttribute("aria-expanded", "false");
  expect(screen.queryByRole("button", { name: /^fantasy/ })).not.toBeInTheDocument();
  first.unmount();
  renderCards("/cards");
  expect(await screen.findByRole("button", { name: "Tags" })).toHaveAttribute("aria-expanded", "false");
});

test("a tag in the URL matches its facet row in any case", async () => {
  renderCards("/cards?f=tag:Fantasy");
  expect(await screen.findByRole("button", { name: /^fantasy / })).toHaveAttribute("aria-pressed", "true");
  expect(screen.getAllByRole("button", { name: /^fantasy /i })).toHaveLength(1);
});

test("an excluded row's pick button is not pressed", async () => {
  renderCards("/cards?f=-tag:fantasy");
  expect(await screen.findByRole("button", { name: /^not fantasy / })).toHaveAttribute("aria-pressed", "false");
  expect(screen.getByRole("button", { name: "Exclude fantasy" })).toHaveAttribute("aria-pressed", "true");
});

test("Import cards opens the importer and imports a page URL", async () => {
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
  renderCards("/cards");
  const open = await screen.findByRole("button", { name: "Import cards" });
  expect(screen.getByLabelText("Import from a page URL")).not.toBeVisible();
  await userEvent.click(open);
  await userEvent.type(screen.getByLabelText("Import from a page URL"), "https://chub.ai/characters/u/brakka");
  await userEvent.click(screen.getByRole("button", { name: "Import" }));
  await waitFor(() =>
    expect(posts).toEqual([{ path: "/api/imports", body: { instance_id: 6, params: { url: "https://chub.ai/characters/u/brakka" } } }]),
  );
  expect(await screen.findByRole("link", { name: /brakka/ })).toHaveAttribute("href", "/settings/jobs/11");
});

test("push sends one push per ticked target", async () => {
  answer("/api/instances", async () =>
    json([
      { id: 2, name: "Lumiverse", plugin: "lumiverse" },
      { id: 5, name: "SillyTavern", plugin: "sillytavern" },
    ]),
  );
  answer("/api/plugins", async () =>
    json([
      { name: "lumiverse", capabilities: ["push", "pull"] },
      { name: "sillytavern", capabilities: ["push"] },
    ]),
  );
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Push" }));
  await user.click(await screen.findByRole("checkbox", { name: "SillyTavern" }));
  expect(await screen.findByText(/SillyTavern: 1 not pushed/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Push 1 card" }));
  await waitFor(() =>
    expect(posts).toEqual([
      { path: "/api/pushes", body: { instance_id: 2, query: { filter: { ids: [8] } }, confirm: "q1" } },
      { path: "/api/pushes", body: { instance_id: 5, query: { filter: { ids: [8] } }, confirm: "q1" } },
    ]),
  );
  await waitFor(() => expect(screen.queryByText("1 selected")).not.toBeInTheDocument());
});

test("New card links to the new-card page", async () => {
  renderCards("/cards");
  expect(await screen.findByRole("link", { name: "New card" })).toHaveAttribute("href", "/cards/new");
});

const scored = {
  instance_id: 5,
  keys: [
    { key: "quality", label: "Quality", type: "score", range: [0, 100], grid: true },
    { key: "summary", label: "Summary", type: "text" },
  ],
};

test("tiles show the grid score, dimmed when stale", async () => {
  keys = scored;
  renderCards("/cards");
  const fresh = await screen.findByRole("img", { name: "Quality 78" });
  expect(fresh).not.toHaveClass("stale");
  const stale = screen.getByRole("img", { name: "Quality 40, stale: from an older revision" });
  expect(stale).toHaveClass("stale");
  expect(searches).toContainEqual(expect.objectContaining({ include: ["tags", "lorebooks", "targets", "enrichments"] }));
});

test("without a score instance the grid asks for no scores", async () => {
  renderCards("/cards");
  await screen.findByText("Brakka");
  expect(screen.queryByRole("img", { name: /^Quality/ })).not.toBeInTheDocument();
  expect(searches).not.toContainEqual(expect.objectContaining({ include: expect.arrayContaining(["enrichments"]) }));
});

test("a score range filters, and Not evaluated replaces it", async () => {
  keys = scored;
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("spinbutton", { name: "Quality at least" }), "70");
  await user.click(screen.getByRole("button", { name: "Apply" }));
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["score:quality:70.."]);
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ filter: { and: [{ enrichment: "quality", gte: 70 }] } })));
  expect(screen.getByRole("button", { name: "Remove score: quality 70–…" })).toBeInTheDocument();
  await user.click(screen.getByRole("checkbox", { name: "Quality not evaluated" }));
  expect(new URLSearchParams(router.state.location.search).getAll("f")).toEqual(["score:quality:none"]);
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ filter: { and: [{ enrichment: "quality", missing: true }] } })));
  // Only score keys get a range.
  expect(screen.queryByRole("spinbutton", { name: "Summary at least" })).not.toBeInTheDocument();
});

test("each score key is a sort", async () => {
  keys = scored;
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await screen.findByRole("option", { name: "Quality" });
  await user.selectOptions(screen.getByRole("combobox", { name: "Sort" }), "Quality");
  expect(router.state.location.search).toBe("?sort=enrichment%3Aquality");
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ sort: "enrichment:quality" })));
});

test("Evaluate starts a run on the selection", async () => {
  keys = scored;
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Evaluate" }));
  await user.click(screen.getByRole("button", { name: "Evaluate 1 card" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/enrichments/runs", body: { instance_id: 5, query: { filter: { ids: [8] } }, rescore: false } }]));
});

test("Evaluate re-scores cards already scored when asked", async () => {
  keys = scored;
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Evaluate" }));
  await user.click(screen.getByRole("checkbox", { name: "Re-score cards already scored" }));
  await user.click(screen.getByRole("button", { name: "Evaluate 1 card" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/enrichments/runs", body: { instance_id: 5, query: { filter: { ids: [8] } }, rescore: true } }]));
});

test("without a score instance, score filters and sort in the URL are dropped with a note", async () => {
  renderCards("/cards?f=score:quality:70..&f=tag:fantasy&sort=enrichment:quality");
  expect(await screen.findByText(/Score filters and sort are off/)).toBeInTheDocument();
  await waitFor(() => expect(searches).toContainEqual(expect.objectContaining({ filter: { and: [{ tag: "fantasy" }] }, sort: "updated" })));
  expect(searches).not.toContainEqual(expect.objectContaining({ sort: "enrichment:quality" }));
});

const mainSearches = () => searches.filter((s) => (s as { page?: { size?: number } }).page?.size === 48);

test("variants group by default, and a family row expands to its members", async () => {
  family = { root_id: 7, size: 3, matched: [7, 9] };
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "+2 variants" }));
  expect(mainSearches()[0]).toMatchObject({ group: "family" });
  const list = await screen.findByRole("list", { name: "Variants of Brakka" });
  expect(searches).toContainEqual({ filter: { variant_of_root: 7 }, fields: ["name"], page: { size: 500 } });
  expect(await within(list).findByRole("link", { name: "Carrow" })).toHaveAttribute("href", "/cards/8");
  expect(within(list).getAllByText("matched")).toHaveLength(2);
});

test("Ungroup variants drops the grouping and is remembered", async () => {
  const { unmount } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Ungroup variants" }));
  await waitFor(() => expect(mainSearches().at(-1)).not.toHaveProperty("group"));
  expect(localStorage.getItem("innkeeper.library.ungrouped")).toBe("true");
  expect(screen.getByText("49 cards")).toBeInTheDocument();
  unmount();
  renderCards("/cards");
  expect(await screen.findByRole("button", { name: "Ungroup variants" })).toHaveAttribute("aria-pressed", "true");
});

test("grouped, select all matching names no count, since total counts families", async () => {
  renderCards("/cards?f=tag:fantasy");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Select all matching" }));
  expect(screen.getByText("All matching cards")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Archive" }));
  await user.click(screen.getByRole("button", { name: "Archive all matching cards" }));
  await waitFor(() =>
    expect(posts[0]).toEqual({ path: "/api/bulk", body: { query: { filter: { and: [{ tag: "fantasy" }] } }, action: "archive", unpublish: false } }),
  );
});

test("Shares a source filters the list", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Shares a source" }));
  expect(router.state.location.search).toBe("?shared=1");
  await waitFor(() => expect(mainSearches().at(-1)).toMatchObject({ filter: { and: [{ shares_source: true }] } }));
});

test("Make variants of… links each picked card to the chosen one", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("checkbox", { name: "Select Dellin" }));
  await user.click(screen.getByRole("button", { name: "Make variants of…" }));
  await user.type(screen.getByRole("textbox", { name: "Parent" }), "Brakka");
  await user.click(screen.getByRole("button", { name: "Search" }));
  await user.click(await screen.findByRole("button", { name: "Brakka" }));
  await user.click(screen.getByRole("button", { name: "Make variants" }));
  await waitFor(() =>
    expect(puts).toEqual([
      { path: "/api/cards/8/variant_of", body: { id: 7 } },
      { path: "/api/cards/9/variant_of", body: { id: 7 } },
    ]),
  );
});
