import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
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
beforeEach(() => {
  searches = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/search") {
        searches.push(await req.json());
        return json(results);
      }
      if (path === "/api/instances") return json([{ id: 2, name: "Lumiverse", plugin: "lumiverse" }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderCards(entry: string) {
  const router = createMemoryRouter([{ path: "/cards", element: <Cards /> }], { initialEntries: [entry] });
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
