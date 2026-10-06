import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Job } from "./Job";
import { Jobs } from "./Jobs";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const bulkJob = {
  id: 5, kind: "bulk", status: "waiting", payload: { action: "card.tag" }, progress: {}, attempts: 0, max_attempts: 3,
  run_after: now, created_at: now, children: { succeeded: 1, queued: 1 },
};
const exportJob = {
  id: 6, kind: "card.export", status: "succeeded", payload: {}, progress: { done: 2, total: 2, blob: "cd".repeat(32) },
  attempts: 1, max_attempts: 3, run_after: now, created_at: now,
};

class FakeEventSource {
  static last: FakeEventSource | null = null;
  listener: ((e: { data: string }) => void) | null = null;
  closed = false;
  constructor(readonly url: string) {
    FakeEventSource.last = this;
  }
  addEventListener(_: string, f: (e: { data: string }) => void) {
    this.listener = f;
  }
  close() {
    this.closed = true;
  }
}

let requests: string[];
beforeEach(() => {
  requests = [];
  FakeEventSource.last = null;
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      requests.push(`${req.method} ${u.pathname}${u.search}`);
      if (u.pathname === "/api/jobs" && u.searchParams.get("parent") === "5")
        return json([{ ...bulkJob, id: 7, kind: "card.tag", status: "succeeded", children: undefined }]);
      if (u.pathname === "/api/jobs") return json([bulkJob, exportJob]);
      if (u.pathname === "/api/jobs/5/cancel") return json({ ...bulkJob, status: "failed" });
      if (u.pathname === "/api/jobs/5") return json(bulkJob);
      if (u.pathname === "/api/jobs/6") return json(exportJob);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/settings/jobs", element: <Jobs /> },
      { path: "/settings/jobs/:id", element: <Toasts><Job /></Toasts> },
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

test("the list shows jobs and filters by state", async () => {
  const router = renderAt("/settings/jobs");
  expect(await screen.findByText("bulk card.tag")).toBeInTheDocument();
  expect(screen.getByText("card.export")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.selectOptions(screen.getByRole("combobox", { name: "State" }), "failed");
  expect(router.state.location.search).toBe("?status=failed");
  expect(requests).toContain("GET /api/jobs?status=failed");
});

test("a bulk job shows its children, follows events, and cancels", async () => {
  renderAt("/settings/jobs/5");
  expect(await screen.findByText("1 of 2")).toBeInTheDocument();
  expect(await screen.findByText("card.tag")).toBeInTheDocument();
  expect(FakeEventSource.last?.url).toBe("/api/jobs/5/events");

  act(() => FakeEventSource.last!.listener!({ data: JSON.stringify({ ...bulkJob, status: "running", children: { succeeded: 2 } }) }));
  expect(await screen.findByText("2 of 2")).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Cancel" }));
  expect(requests).toContain("POST /api/jobs/5/cancel");
  expect(await screen.findByText("failed", { selector: "dd" })).toBeInTheDocument();
});

test("a finished export links to its zip and opens no stream", async () => {
  renderAt("/settings/jobs/6");
  const link = await screen.findByRole("link", { name: "Download zip" });
  expect(link).toHaveAttribute("href", `/api/blobs/${"cd".repeat(32)}`);
  expect(FakeEventSource.last).toBeNull();
  expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
});
