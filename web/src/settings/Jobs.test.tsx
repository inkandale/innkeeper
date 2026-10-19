import { act, render, screen, within } from "@testing-library/react";
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
  attempts: 1, max_attempts: 3, run_after: now, created_at: now, started_at: new Date(Date.parse(now) + 30_000).toISOString(), finished_at: now, duration_ms: 90_000,
  cards_changed: 0,
};
const failedImport = {
  id: 8, kind: "import", instance_id: 3, status: "failed", payload: {}, progress: { done: 10, total: 100 }, attempts: 8, max_attempts: 8,
  run_after: now, created_at: now, finished_at: now, error: "worker stopped renewing its lease", cards_changed: 12,
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
      if (u.pathname === "/api/jobs" && u.searchParams.get("plugin") === "chub-archive") return json([failedImport]);
      if (u.pathname === "/api/jobs") return json([bulkJob, exportJob, failedImport]);
      if (u.pathname === "/api/plugins") return json([{ name: "chub-archive" }, { name: "lumiverse" }]);
      if (u.pathname === "/api/instances") return json([{ id: 3, plugin: "chub-archive", label: "Chub archive" }]);
      if (u.pathname === "/api/jobs/5/cancel") return json({ ...bulkJob, status: "failed" });
      if (u.pathname === "/api/jobs/5") return json(bulkJob);
      if (u.pathname === "/api/jobs/6") return json(exportJob);
      if (u.pathname === "/api/jobs/8/retry")
        return json({ ...failedImport, status: "queued", attempts: 0, error: undefined, finished_at: undefined });
      if (u.pathname === "/api/jobs/8/logs")
        return json([
          { id: 1, at: now, level: "info", message: "Attempt 8 of 8 started" },
          { id: 2, at: now, level: "error", message: "The worker stopped renewing its lease after attempt 8 of 8; out of attempts" },
        ]);
      if (u.pathname === "/api/jobs/8") return json(failedImport);
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
  expect(screen.getByText("1 of 2")).toBeInTheDocument();
  expect(screen.getByText("card.export")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.selectOptions(screen.getByRole("combobox", { name: "State" }), "failed");
  expect(router.state.location.search).toBe("?status=failed");
  expect(requests).toContain("GET /api/jobs?status=failed");
});

test("the list filters by plugin and names each job's instance", async () => {
  const router = renderAt("/settings/jobs?status=failed");
  expect(await screen.findByText("Chub archive")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByRole("combobox", { name: "Plugin" }), "chub-archive");
  expect(router.state.location.search).toBe("?status=failed&plugin=chub-archive");
  expect(requests).toContain("GET /api/jobs?status=failed&plugin=chub-archive");
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
  expect(FakeEventSource.last!.closed).toBe(true);
});

test("a finished event closes the stream", async () => {
  renderAt("/settings/jobs/5");
  await screen.findByText("1 of 2");
  const es = FakeEventSource.last!;
  act(() => es.listener!({ data: JSON.stringify({ ...bulkJob, status: "succeeded", children: { succeeded: 2 } }) }));
  expect(es.closed).toBe(true);
});

test("a finished export links to its zip and opens no stream", async () => {
  renderAt("/settings/jobs/6");
  const link = await screen.findByRole("link", { name: "Download zip" });
  expect(link).toHaveAttribute("href", `/api/blobs/${"cd".repeat(32)}`);
  expect(FakeEventSource.last).toBeNull();
  expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument();
  expect(screen.getByText("Waited").nextElementSibling).toHaveTextContent("30s");
  expect(screen.getByText("Ran for").nextElementSibling).toHaveTextContent("1m");
  expect(screen.getByText("Cards changed").nextElementSibling).toHaveTextContent("0");
});

test("the list shows how long each job ran and the cards it changed", async () => {
  renderAt("/settings/jobs");
  await screen.findByText("card.export");
  const cells = (text: string) =>
    within(screen.getByText(text).closest("tr")!).getAllByRole("cell").map((c) => c.textContent);
  expect(cells("card.export").slice(5, 7)).toEqual(["1m", ""]);
  expect(cells("Chub archive").slice(5, 7)).toEqual(["", "12"]);
});

test("a job that never started shows no wait or run time", async () => {
  renderAt("/settings/jobs/8");
  expect((await screen.findByText("Cards changed")).nextElementSibling).toHaveTextContent("12");
  expect(screen.queryByText("Waited")).not.toBeInTheDocument();
  expect(screen.queryByText("Ran for")).not.toBeInTheDocument();
});

test("a job's page shows its log", async () => {
  renderAt("/settings/jobs/8");
  const log = await screen.findByRole("region", { name: "Log" });
  expect(log).toHaveTextContent("Attempt 8 of 8 started");
  expect(screen.getByText(/out of attempts/).closest("li")).toHaveClass("error");
});

test("a failed job retries from its page", async () => {
  const user = userEvent.setup();
  renderAt("/settings/jobs/8");
  await user.click(await screen.findByRole("button", { name: "Retry" }));
  expect(requests).toContain("POST /api/jobs/8/retry");
  expect(await screen.findByText("queued")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Retry" })).not.toBeInTheDocument();
});

test("a bulk job filters its children by any state, waiting included", async () => {
  renderAt("/settings/jobs/5");
  await screen.findByText("card.tag");
  await userEvent.setup().selectOptions(screen.getByRole("combobox", { name: "Child state" }), "waiting");
  expect(requests).toContain("GET /api/jobs?parent=5&status=waiting");
});
