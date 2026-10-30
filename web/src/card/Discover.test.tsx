import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import type { Entity } from "./Card";
import { Related, Similar } from "./Discover";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const now = "2026-10-23T00:00:00Z";
const card = {
  id: 7,
  slug: "brakka",
  provenance: "ui",
  created_at: now,
  library_id: 1,
  variants: null,
  lorebooks: [3, 4],
  collections: [9],
  revision: { id: 1, content_hash: "x", message: "", source: "ui", created_at: now, data: { name: "Brakka", creator: "OnlyYa" } },
} as Entity;
let routes: Record<string, unknown>;
let requested: string[];
beforeEach(() => {
  requested = [];
  routes = {
    "/api/libraries": [
      { id: 1, name: "Main", cards: 1, lorebooks: 0, created_at: now },
      { id: 2, name: "Archive", cards: 1, lorebooks: 0, created_at: now },
    ],
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      requested.push(url.pathname + url.search);
      return json(routes[url.pathname + url.search] ?? routes[url.pathname] ?? []);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderIt(element: React.ReactNode, client = new QueryClient({ defaultOptions: { queries: { retry: false } } })) {
  const router = createMemoryRouter([{ path: "*", element }]);
  return render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("Similar links each card and shows more on demand", async () => {
  routes["/api/cards/7/similar?scope=library&limit=8"] = {
    basis: "embedding",
    items: [{ card_id: 8, library_id: 1, name: "Dorna", score: 0.9 }],
    next_cursor: "1",
  };
  routes["/api/cards/7/similar?scope=library&cursor=1&limit=8"] = {
    basis: "embedding",
    items: [{ card_id: 9, library_id: 1, name: "Brakka", score: 0.8 }],
  };
  renderIt(<Similar id={7} library={1} />);
  expect(await screen.findByRole("link", { name: "Dorna" })).toHaveAttribute("href", "/cards/8");
  await userEvent.click(screen.getByRole("button", { name: "Show more" }));
  expect(await screen.findByRole("link", { name: "Brakka" })).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
});

test("Similar ranked by tags points to the embed setting", async () => {
  routes["/api/cards/7/similar"] = { basis: "tags", items: [{ card_id: 8, library_id: 1, name: "Dorna" }] };
  renderIt(<Similar id={7} library={1} />);
  expect(await screen.findByText(/Ranked by shared tags until this card has an embedding\./)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Embed instance" })).toHaveAttribute("href", "/settings/plugins");
});

test("Similar across all libraries names the other library", async () => {
  routes["/api/cards/7/similar?scope=library&limit=8"] = { basis: "embedding", items: [] };
  routes["/api/cards/7/similar?scope=all&limit=8"] = {
    basis: "embedding",
    items: [{ card_id: 21, library_id: 2, name: "Dorna", score: 0.9 }],
  };
  renderIt(<Similar id={7} library={1} />);
  expect(await screen.findByText("None in this library.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("checkbox", { name: "All libraries" }));
  expect(await screen.findByRole("link", { name: "Dorna" })).toBeInTheDocument();
  expect(await screen.findByText("in Archive")).toBeInTheDocument();
  expect(requested).toContain("/api/cards/7/similar?scope=all&limit=8");
});

test("Similar keeps its toggle while the other scope loads", async () => {
  routes["/api/cards/7/similar?scope=library&limit=8"] = { basis: "embedding", items: [] };
  let release = () => {};
  let asked = false;
  const held = new Promise<void>((r) => (release = r));
  const plain = vi.mocked(fetch).getMockImplementation()!;
  vi.mocked(fetch).mockImplementation(async (req) => {
    if (new URL((req as Request).url).search === "?scope=all&limit=8") {
      asked = true;
      await held;
    }
    return plain(req);
  });
  renderIt(<Similar id={7} library={1} />);
  expect(await screen.findByText("None in this library.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("checkbox", { name: "All libraries" }));
  await waitFor(() => expect(asked).toBe(true));
  expect(screen.getByRole("checkbox", { name: "All libraries" })).toBeChecked();
  expect(screen.queryByText("None in any library.")).not.toBeInTheDocument();
  release();
  expect(await screen.findByText("None in any library.")).toBeInTheDocument();
});

test("Related asks for 8 cards a group and links each group's rest to the gallery", async () => {
  routes["/api/cards/7/related?limit=8"] = {
    groups: [
      { group: "creator", items: [{ card_id: 8, library_id: 1, name: "Dorna" }], next_cursor: "8" },
      { group: "lorebook", items: [{ card_id: 10, library_id: 1, name: "Ilse" }], next_cursor: "8" },
      { group: "collection", items: [{ card_id: 11, library_id: 1, name: "Pell" }], next_cursor: "8" },
    ],
  };
  renderIt(<Related card={card} />);
  expect(await screen.findByRole("heading", { name: "Same creator" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Dorna" })).toHaveAttribute("href", "/cards/8");
  const all = screen.getAllByRole("link", { name: "See all" }).map((a) => new URL(a.getAttribute("href")!, "http://x"));
  expect(all.map((u) => u.pathname)).toEqual(["/cards", "/cards", "/cards"]);
  expect(all[0]!.searchParams.getAll("f")).toEqual(["creator:OnlyYa"]);
  expect(all[1]!.searchParams.getAll("f")).toEqual(["lorebook:3", "lorebook:4"]);
  expect(all[2]!.searchParams.getAll("f")).toEqual(["collection:9"]);
  expect(all[0]!.searchParams.get("library")).toBe("1");
});

test("Related with no groups renders nothing", async () => {
  routes["/api/cards/7/related?limit=8"] = { groups: [] };
  const { container } = renderIt(<Related card={card} />);
  await waitFor(() => expect(requested).toContain("/api/cards/7/related?limit=8"));
  await new Promise((r) => setTimeout(r, 50));
  expect(container).toBeEmptyDOMElement();
});

test("a found card tagged NSFW marks its image for blurring", async () => {
  routes["/api/cards/7/similar?scope=library&limit=8"] = {
    basis: "embedding",
    items: [
      { card_id: 8, library_id: 1, name: "Dorna", avatar: "ab".repeat(32), tags: ["nsfw"] },
      { card_id: 9, library_id: 1, name: "Brakka", avatar: "cd".repeat(32), tags: ["dwarf"] },
    ],
  };
  renderIt(<Similar id={7} library={1} />);
  const dorna = await screen.findByRole("link", { name: "Dorna" });
  expect(dorna.querySelector("img")).toHaveClass("nsfw");
  expect(screen.getByRole("link", { name: "Brakka" }).querySelector("img")).not.toHaveClass("nsfw");
});
