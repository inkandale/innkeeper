import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Queue } from "./Queue";
import type { Snapshot } from "./sync";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const snap: Snapshot = {
  params: { instance_id: 3 },
  items: [
    {
      remote_id: "a", name: "Ann", app_hash: "ha", verdict: "exact",
      options: [{ action: "bind", card_id: 1 }, { action: "import" }, { action: "skip" }],
      suggested: { action: "bind", card_id: 1, why: "same content" },
      candidates: [{ card_id: 1, name: "Ann", reasons: { content: 0 }, differing_fields: 0, conflicts: 0 }],
      lorebooks: [{ remote_id: "w1", name: "Ann's world", app_hash: "h1", verdict: "new", options: [{ action: "import" }, { action: "skip" }], suggested: { action: "import" } }],
    },
    { remote_id: "b", name: "Bo", app_hash: "hb", verdict: "new", options: [{ action: "import" }, { action: "skip" }], suggested: { action: "import" } },
    { remote_id: "c", name: "", app_hash: "", verdict: "error", error: "bad JSON", options: [{ action: "skip" }] },
    { remote_id: "d", name: "Di", app_hash: "hd", verdict: "bound", options: [{ action: "skip" }], suggested: { action: "skip" } },
  ],
  lorebooks: [{ remote_id: "w", name: "World", app_hash: "hw", verdict: "likely", options: [{ action: "bind", card_id: 9 }, { action: "skip" }], suggested: { action: "bind", card_id: 9 } }],
  missing: [4],
  unpushed: [5, 6],
  totals: { exact: 1, new: 2, error: 1, likely: 1, bound: 1 },
};
const job = { id: 5, kind: "target.reconcile", status: "succeeded", instance_id: 3, created_at: now, payload: {}, progress: snap, attempts: 1, max_attempts: 3, run_after: now };

let progress: Snapshot = snap;
beforeEach(() => {
  progress = snap;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/jobs/5") return json({ ...job, progress });
      return json({}, 404);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderQueue() {
  const router = createMemoryRouter([{ path: "/sync/reconciles/:job", element: <Toasts><Queue /></Toasts> }], {
    initialEntries: ["/sync/reconciles/5"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("lists each app item with its match and suggestion", async () => {
  renderQueue();
  expect(await screen.findByRole("link", { name: "Ann" })).toHaveAttribute("href", "/sync/3/card/a?job=5");
  expect(screen.getByRole("link", { name: "World" })).toHaveAttribute("href", "/sync/3/lorebook/w?job=5");
  expect(screen.getByRole("row", { name: /^Bo / })).toHaveTextContent("Import as new");
  expect(screen.getByText("bad JSON")).toBeInTheDocument();
  expect(screen.getByText(/Ann's world/)).toBeInTheDocument();
  expect(screen.getByText(/1 bound card the app no longer has/)).toBeInTheDocument();
});

test("is a record: no checkboxes or Apply, and a note pointing at Changes", async () => {
  renderQueue();
  await screen.findByRole("table");
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
  expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: /Apply/ })).not.toBeInTheDocument();
  expect(screen.getByText(/What this sync found\. Work through what needs you in/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Changes" })).toHaveAttribute("href", "/sync/changes?instance_id=3");
});

test("verdict chips filter the rows", async () => {
  renderQueue();
  await userEvent.click(await screen.findByRole("button", { name: "Not in the library 2" }));
  expect(screen.queryByRole("link", { name: "World" })).not.toBeInTheDocument();
  // Ann stays: her nested world book is new.
  expect(screen.getByRole("link", { name: "Ann" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Bo" })).toBeInTheDocument();
});

test("a running sync shows its progress", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ ...job, status: "running", progress: { done: 2, total: 9 } })));
  renderQueue();
  expect(await screen.findByText(/2 of 9/)).toBeInTheDocument();
  expect(within(document.body).queryByRole("table")).not.toBeInTheDocument();
});

const done = (over: Partial<Snapshot>): Snapshot => ({ ...snap, lorebooks: [], missing: null, unpushed: null, ...over });
const auto = { options: null, app_hash: "h" } as const;

test("with nothing left to review, shows a summary instead of the table", async () => {
  progress = done({
    items: [
      { ...auto, remote_id: "a", name: "Ann", verdict: "new", auto_imported: true },
      { ...auto, remote_id: "b", name: "Bo", verdict: "new", auto_imported: true },
      { ...auto, remote_id: "c", name: "Cy", verdict: "exact", auto_bound: true },
      { ...auto, remote_id: "d", name: "Di", verdict: "bound" },
    ],
    totals: { new: 2, exact: 1, bound: 1, auto_imported: 2, auto_bound: 1 },
  });
  renderQueue();
  expect(await screen.findByText("Nothing to review. 2 imported, 1 bound")).toBeInTheDocument();
  expect(screen.queryByRole("table")).not.toBeInTheDocument();
  expect(screen.queryByRole("group", { name: "Filter by status" })).not.toBeInTheDocument();
});

test("an app edit the sync brought in is done, labelled in words, with no suggestion left", async () => {
  const pulled = { action: "bind" as const, card_id: 4, why: "changed only in the app" };
  progress = done({
    items: [
      { ...auto, remote_id: "h", name: "Helka", verdict: "bound", auto_pulled: true, options: [pulled], suggested: pulled },
      { ...auto, remote_id: "z", name: "Zed", verdict: "new", options: [{ action: "import" }], suggested: { action: "import" } },
    ],
    totals: { bound: 1, new: 1, auto_pulled: 1 },
  });
  renderQueue();
  const row = await screen.findByRole("row", { name: /^Helka / });
  expect(row).toHaveTextContent("App edits brought in");
  expect(row).not.toHaveTextContent("Bind to");
  expect(screen.getByRole("button", { name: "App edits brought in 1" })).toBeInTheDocument();
});

test("a drifted bound item, or one with an error, is still left to review", async () => {
  progress = done({
    items: [{ ...auto, remote_id: "d", name: "Di", verdict: "bound", options: [{ action: "skip" }], suggested: { action: "skip" } }],
    totals: { bound: 1 },
  });
  renderQueue();
  expect(await screen.findByRole("table")).toBeInTheDocument();
  expect(screen.queryByText(/Nothing to review/)).not.toBeInTheDocument();
});

test("a bound item with an error is still left to review", async () => {
  progress = done({ items: [{ ...auto, remote_id: "e", name: "Ed", verdict: "bound", error: "bad JSON" }], totals: { bound: 1 } });
  renderQueue();
  expect(await screen.findByRole("table")).toBeInTheDocument();
  expect(screen.getByText("bad JSON")).toBeInTheDocument();
});

test("the missing and unpushed note shows even with nothing to review", async () => {
  progress = done({ items: [], missing: [4], unpushed: [5, 6], totals: {} });
  renderQueue();
  expect(await screen.findByText(/Nothing to review/)).toBeInTheDocument();
  expect(screen.getByText(/1 bound card the app no longer has/)).toBeInTheDocument();
});

test("a pending nested world book keeps the table", async () => {
  progress = done({
    items: [{ ...auto, remote_id: "d", name: "Di", verdict: "bound", lorebooks: [{ ...auto, remote_id: "w", name: "W", verdict: "new", options: [{ action: "import" }], suggested: { action: "import" } }] }],
    totals: { bound: 1 },
  });
  renderQueue();
  expect(await screen.findByRole("table")).toBeInTheDocument();
});

test("auto rows are labelled", async () => {
  progress = done({
    items: [
      { remote_id: "a", name: "Ann", app_hash: "ha", verdict: "new", auto_imported: true, options: [{ action: "import" }, { action: "skip" }], suggested: { action: "import" } },
      { remote_id: "b", name: "Bo", app_hash: "hb", verdict: "likely", options: [{ action: "import" }], suggested: { action: "import" } },
    ],
    totals: { new: 1, likely: 1, auto_imported: 1 },
  });
  renderQueue();
  expect(await screen.findByText("Imported automatically")).toBeInTheDocument();
});

test("the auto_imported chip shows the auto-imported rows, with the same label on world books", async () => {
  progress = done({
    items: [
      { remote_id: "a", name: "Ann", app_hash: "ha", verdict: "new", auto_imported: true, options: null, lorebooks: [{ remote_id: "w", name: "AW", app_hash: "h", verdict: "new", auto_imported: true, options: null }] },
      { remote_id: "b", name: "Bo", app_hash: "hb", verdict: "likely", options: [{ action: "import" }], suggested: { action: "import" } },
    ],
    totals: { new: 1, likely: 1, auto_imported: 1 },
  });
  renderQueue();
  await userEvent.click(await screen.findByRole("button", { name: "Imported automatically 1" }));
  expect(screen.getByRole("link", { name: "Ann" })).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: "Bo" })).not.toBeInTheDocument();
  expect(screen.getAllByText("Imported automatically")).toHaveLength(2);
});
