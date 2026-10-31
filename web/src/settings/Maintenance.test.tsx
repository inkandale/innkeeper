import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Maintenance } from "./Maintenance";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
let posts: string[];
let gets: string[];
let vectors: boolean;
beforeEach(() => {
  posts = [];
  gets = [];
  vectors = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (req.method === "POST") {
        posts.push(await req.text());
        return json({ id: 5, kind: "maintenance", label: "Maintenance", status: "running", created_at: now, payload: {}, progress: {} }, 202);
      }
      if (url.pathname === "/api/jobs/5") return json({ id: 5, kind: "maintenance", label: "Maintenance", status: "running", created_at: now, payload: {}, progress: {} });
      gets.push(url.search);
      return json({
        database_bytes: 512 * 1048576,
        largest: [{ name: "enrich_value", bytes: 300 * 1048576 }],
        history: { rows: 12000, trimmable: 7000 },
        vectors: { available: vectors, rows: 480 },
        failures: 4,
        purge: { cards: 9, lorebooks: 2 },
        last_backup: now,
      });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderTab() {
  const router = createMemoryRouter([{ path: "/", element: <Toasts><Maintenance /></Toasts> }]);
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("shows the preview", async () => {
  renderTab();
  expect(await screen.findByText(/512 MB/)).toBeInTheDocument();
  expect(screen.getByText(/enrich_value 300 MB/)).toBeInTheDocument();
  expect(screen.getByText(/Deletes 7,000 of 12,000 values/)).toBeInTheDocument();
  expect(screen.getByText(/4 cards a plugin failed on/)).toBeInTheDocument();
  expect(screen.getByText(/Deletes 9 cards and 2 lorebooks/)).toBeInTheDocument();
  expect(screen.getByText(/Last backup/)).toBeInTheDocument();
});

test("Trim posts the keep count", async () => {
  renderTab();
  await userEvent.click(await screen.findByRole("button", { name: "Trim" }));
  await waitFor(() => expect(posts).toEqual(['{"action":"trim-history","keep":3}']));
});

test("Purge stays disabled until purge is typed, then posts the days", async () => {
  renderTab();
  const purge = await screen.findByRole("button", { name: "Purge" });
  expect(purge).toBeDisabled();
  await userEvent.type(screen.getByLabelText(/Type purge to confirm/), "purg");
  expect(purge).toBeDisabled();
  await userEvent.type(screen.getByLabelText(/Type purge to confirm/), "e");
  expect(purge).toBeEnabled();
  await userEvent.click(purge);
  await waitFor(() => expect(posts).toEqual(['{"action":"purge","days":30}']));
});

test("without a vector source the stale-vectors section is hidden", async () => {
  vectors = false;
  renderTab();
  await screen.findByRole("button", { name: "Trim" });
  expect(screen.queryByText("Remove stale vectors")).not.toBeInTheDocument();
});

test("changing keep and days refetches the preview with them", async () => {
  renderTab();
  const keep = await screen.findByLabelText(/Keep the newest/);
  await userEvent.clear(keep);
  await userEvent.type(keep, "5");
  const days = screen.getByLabelText(/Archived at least/);
  await userEvent.clear(days);
  await userEvent.type(days, "90");
  await waitFor(() => expect(gets).toContain("?keep=5&days=90"));
});
