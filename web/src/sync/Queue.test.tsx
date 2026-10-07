import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
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
const cmp = { instance_id: 3, remote_id: "a", card_id: 1, head_revision_id: 118, accepted_hash: "", base: "none", app_hash: "ha", fields: [] };

let resolutions: unknown;
beforeEach(() => {
  resolutions = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/jobs/5") return json(job);
      if (u.pathname === "/api/instances/3/items/a/compare" && u.searchParams.get("card_id") === "1") return json(cmp);
      if (u.pathname === "/api/instances/3/lorebook-items/w/compare" && u.searchParams.get("lorebook_id") === "9")
        return json({ ...cmp, remote_id: "w", card_id: 9, head_revision_id: 50 });
      if (u.pathname === "/api/resolutions") {
        resolutions = (await req.json()).resolutions;
        return json({ id: 9, kind: "bulk", status: "waiting" }, 202);
      }
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
  expect(await screen.findByRole("link", { name: "Ann" })).toHaveAttribute("href", "/sync/3/items/a?job=5");
  expect(screen.getByRole("link", { name: "World" })).toHaveAttribute("href", "/sync/3/lorebook-items/w?job=5");
  expect(screen.getByRole("combobox", { name: "Action for Ann" })).toHaveDisplayValue("Bind to Ann (suggested)");
  expect(screen.getByText("bad JSON")).toBeInTheDocument();
  expect(screen.getByText(/Ann's world/)).toBeInTheDocument();
  expect(screen.getByText(/1 bound card the app no longer has/)).toBeInTheDocument();
});

test("verdict chips filter the rows", async () => {
  renderQueue();
  await userEvent.click(await screen.findByRole("button", { name: "new 2" }));
  expect(screen.queryByRole("link", { name: "World" })).not.toBeInTheDocument();
  // Ann stays: her nested world book is new.
  expect(screen.getByRole("link", { name: "Ann" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Bo" })).toBeInTheDocument();
});

test("Apply sends the checked rows as picked, comparing each bind", async () => {
  renderQueue();
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByRole("combobox", { name: "Action for Bo" }), "skip");
  await user.click(screen.getByRole("checkbox", { name: "Apply World" }));
  await user.click(screen.getByRole("button", { name: "Apply 2 suggested" }));
  await waitFor(() =>
    expect(resolutions).toEqual([
      { instance_id: 3, remote_id: "a", action: "bind", card_id: 1, app_hash: "ha", base_revision_id: 118, accepted_hash: "", default: "library" },
      { instance_id: 3, remote_id: "b", action: "skip", app_hash: "hb" },
    ]),
  );
  expect(await screen.findByText(/Job 9 started/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Applied" })).toBeDisabled();
});

test("a running reconcile shows its progress", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ ...job, status: "running", progress: { done: 2, total: 9 } })));
  renderQueue();
  expect(await screen.findByText(/2 of 9/)).toBeInTheDocument();
  expect(within(document.body).queryByRole("table")).not.toBeInTheDocument();
});

test("bound and error rows start unchecked; picking an action checks the row", async () => {
  renderQueue();
  const user = userEvent.setup();
  expect(await screen.findByRole("checkbox", { name: "Apply Di" })).not.toBeChecked();
  const bad = screen.getByRole("checkbox", { name: "Apply c" });
  expect(bad).toBeDisabled();
  await user.selectOptions(screen.getByRole("combobox", { name: "Action for c" }), "skip");
  expect(bad).toBeChecked();
});
