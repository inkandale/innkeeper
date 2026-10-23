import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Related, Similar } from "./Discover";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const now = "2026-10-23T00:00:00Z";
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

function renderIt(element: React.ReactNode) {
  const router = createMemoryRouter([{ path: "*", element }]);
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("Similar links each card and shows more on demand", async () => {
  routes["/api/cards/7/similar?scope=library"] = {
    basis: "embedding",
    items: [{ card_id: 8, library_id: 1, name: "Dorna", score: 0.9 }],
    next_cursor: "1",
  };
  routes["/api/cards/7/similar?scope=library&cursor=1"] = {
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
  expect(await screen.findByText(/Ranked by shared tags/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Pick an embed instance" })).toHaveAttribute("href", "/settings/plugins");
});

test("Similar across all libraries names the other library", async () => {
  routes["/api/cards/7/similar?scope=library"] = { basis: "embedding", items: [] };
  routes["/api/cards/7/similar?scope=all"] = {
    basis: "embedding",
    items: [{ card_id: 21, library_id: 2, name: "Dorna", score: 0.9 }],
  };
  renderIt(<Similar id={7} library={1} />);
  expect(await screen.findByText("None in this library.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("checkbox", { name: "All libraries" }));
  expect(await screen.findByRole("link", { name: "Dorna" })).toBeInTheDocument();
  expect(await screen.findByText("in Archive")).toBeInTheDocument();
  expect(requested).toContain("/api/cards/7/similar?scope=all");
});

test("Related shows each group and pages through it", async () => {
  routes["/api/cards/7/related"] = {
    groups: [{ group: "creator", items: [{ card_id: 8, library_id: 1, name: "Dorna" }], next_cursor: "1" }],
  };
  routes["/api/cards/7/related?group=creator&cursor=1"] = {
    groups: [{ group: "creator", items: [{ card_id: 9, library_id: 1, name: "Brakka" }] }],
  };
  renderIt(<Related id={7} library={1} />);
  expect(await screen.findByRole("heading", { name: "Same creator" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Dorna" })).toHaveAttribute("href", "/cards/8");
  await userEvent.click(screen.getByRole("button", { name: "Show more" }));
  expect(await screen.findByRole("link", { name: "Brakka" })).toBeInTheDocument();
  expect(requested).toContain("/api/cards/7/related?group=creator&cursor=1");
  expect(screen.queryByRole("button", { name: "Show more" })).not.toBeInTheDocument();
});

test("Related with no groups renders nothing", async () => {
  routes["/api/cards/7/related"] = { groups: [] };
  const { container } = renderIt(<Related id={7} library={1} />);
  await waitFor(() => expect(requested).toContain("/api/cards/7/related"));
  await new Promise((r) => setTimeout(r, 50));
  expect(container).toBeEmptyDOMElement();
});
