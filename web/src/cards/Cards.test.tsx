import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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

let searches: unknown[];
let posts: { path: string; body: unknown }[];
beforeEach(() => {
  searches = [];
  posts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/search") {
        const body = await req.json();
        searches.push(body);
        if (body.page?.size === 1 && body.facets?.includes("targets"))
          return json({ hits: [], total: 1, facets: { targets: [{ instance: 2, is: "ahead", cards: 1 }] } });
        return json(results);
      }
      if (path === "/api/bulk" || path === "/api/pushes") {
        posts.push({ path, body: await req.json() });
        return json({ id: 5, kind: "bulk", status: "waiting" });
      }
      if (path === "/api/instances")
        return json([
          { id: 2, name: "Lumiverse", plugin: "lumiverse" },
          { id: 3, name: "Cards folder", plugin: "folder" },
        ]);
      if (path === "/api/plugins")
        return json([
          { name: "lumiverse", capabilities: ["push", "pull"] },
          { name: "folder", capabilities: ["import"] },
        ]);
      if (path === "/api/collections") return json([{ id: 4, name: "Elves", cards: 0 }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderCards(entry: string) {
  const router = createMemoryRouter([{ path: "/cards", element: <Toasts><Cards /></Toasts> }], { initialEntries: [entry] });
  const { container } = render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return { router, container };
}

test("the URL becomes the search, and hits become tiles", async () => {
  const { container } = renderCards("/cards?f=tag:fantasy&page=2");
  expect(await screen.findByText("Brakka")).toBeInTheDocument();
  expect(searches[0]).toMatchObject({ filter: { and: [{ tag: "fantasy" }] }, page: { size: 48, number: 2 } });
  expect(screen.getByText("+1")).toBeInTheDocument();
  expect(screen.getByTitle("Lumiverse: ahead")).toBeInTheDocument();
  expect(screen.getByText("49 cards")).toBeInTheDocument();
  expect(container.querySelector("img")).toHaveAttribute("src", `/api/blobs/${"ab".repeat(32)}?w=320`);
});

test("a facet click filters and shift-click excludes", async () => {
  const { router } = renderCards("/cards?page=3");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /pirate/ }));
  expect(router.state.location.search).toBe("?f=tag%3Apirate");
  await user.keyboard("{Shift>}");
  await user.click(screen.getByRole("button", { name: /fantasy/ }));
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
  await user.type(await screen.findByRole("searchbox"), "dwarf{Enter}");
  expect(router.state.location.search).toBe("?q=dwarf");
});

test("a new search keeps a chosen sort", async () => {
  const { router } = renderCards("/cards?sort=name");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("searchbox"), "x{Enter}");
  expect(router.state.location.search).toBe("?q=x&sort=name");
});

test("a new search from the default sort stays default", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.type(await screen.findByRole("searchbox"), "x{Enter}");
  expect(router.state.location.search).toBe("?q=x");
});

test("an active facet is pressed", async () => {
  renderCards("/cards?f=tag:pirate");
  expect(await screen.findByRole("button", { name: /^pirate/ })).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("button", { name: /^fantasy/ })).toHaveAttribute("aria-pressed", "false");
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
  expect(screen.queryByRole("searchbox")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: "Tag" }));
  await user.type(screen.getByRole("textbox", { name: "Tags" }), "elf, sage");
  await user.click(screen.getByRole("button", { name: "Add tags" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/bulk", body: { query: { filter: { ids: [7, 8, 9] } }, action: "tag", tags: ["elf", "sage"] } }]));
  expect(await screen.findByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/5");
  expect(screen.getByRole("searchbox")).toBeInTheDocument();
});

test("select all matching archives the search", async () => {
  renderCards("/cards?f=tag:fantasy");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Select all 49 matching" }));
  expect(screen.getByText("All 49 matching cards")).toBeInTheDocument();
  expect(screen.getByRole("checkbox", { name: "Select Brakka" })).toBeChecked();
  await user.click(screen.getByRole("button", { name: "Archive" }));
  await user.click(screen.getByRole("button", { name: "Archive 49 cards" }));
  await waitFor(() => expect(posts[0]).toEqual({ path: "/api/bulk", body: { query: { filter: { and: [{ tag: "fantasy" }] } }, action: "archive" } }));
  expect(posts[0]!.body).not.toHaveProperty("kind");
});

test("push offers only targets that push", async () => {
  renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "Push" }));
  const target = await screen.findByRole("combobox", { name: "Target" });
  expect(Array.from((target as HTMLSelectElement).options).map((o) => o.text)).toEqual(["Lumiverse"]);
  expect(await screen.findByText("1 ahead")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Push 1 card" }));
  await waitFor(() => expect(posts[0]).toEqual({ path: "/api/pushes", body: { instance_id: 2, query: { filter: { ids: [8] } } } }));
  expect(posts[0]!.body).not.toHaveProperty("kind");
});

// answer replaces the fake response for one path.
function answer(path: string, f: (req: Request) => Promise<Response>) {
  const rest = fetch;
  vi.stubGlobal("fetch", vi.fn(async (req: Request) => (new URL(req.url).pathname === path ? f(req) : rest(req))));
}
const never = () => new Promise<Response>(() => {});

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

test("a facet click clears the selection, and paging keeps it", async () => {
  const { router } = renderCards("/cards");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("checkbox", { name: "Select Carrow" }));
  await user.click(screen.getByRole("button", { name: "2" }));
  expect(router.state.location.search).toBe("?page=2");
  expect(screen.getByText("1 selected")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /pirate/ }));
  expect(screen.getByRole("searchbox")).toBeInTheDocument();
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
  await user.click(screen.getByRole("button", { name: /pirate/ }));
  await user.click(screen.getByRole("button", { name: "Remove tag: pirate" }));
  expect(screen.getByRole("checkbox", { name: "Select Carrow" })).not.toBeChecked();
  expect(screen.queryByText("1 selected")).not.toBeInTheDocument();
});
