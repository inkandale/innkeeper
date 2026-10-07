import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Review } from "./Review";
import type { Snapshot } from "./sync";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const snap: Snapshot = {
  params: { instance_id: 3 },
  items: [
    {
      remote_id: "a", name: "Ann", app_hash: "ha", verdict: "likely",
      options: [{ action: "bind", card_id: 1 }, { action: "import" }, { action: "skip" }],
      suggested: { action: "bind", card_id: 1, why: "one core field differs" },
      candidates: [{ card_id: 1, name: "Ann", reasons: { name: 0 }, differing_fields: 2, conflicts: 1 }],
      lorebooks: [
        {
          remote_id: "w2", name: "Ann's world", app_hash: "hw", verdict: "likely",
          options: [{ action: "bind", card_id: 9 }, { action: "import" }, { action: "skip" }], suggested: { action: "bind", card_id: 9 },
          candidates: [{ card_id: 9, name: "Ann's world", reasons: { name: 0 }, differing_fields: 1, conflicts: 1 }],
        },
      ],
    },
  ],
  lorebooks: null, missing: null, unpushed: null, totals: { likely: 1 },
};
const job = { id: 5, kind: "target.reconcile", status: "succeeded", created_at: now, payload: {}, progress: snap, attempts: 1, max_attempts: 3, run_after: now };
const cardCmp = {
  instance_id: 3, remote_id: "a", card_id: 1, head_revision_id: 118, accepted_hash: "", base: "none", app_hash: "h2",
  fields: [
    { path: "name", kind: "scalar", hunks: [{ id: "name#0", status: "same", library: "Ann" }] },
    {
      path: "description", kind: "prose",
      hunks: [
        { id: "description#0", status: "same", library: "Ann is " },
        { id: "description#1", status: "conflict", library: "tall.", app: "short." },
      ],
    },
    { path: "tags", kind: "list", hunks: [{ id: "tags#0", status: "app_only", app: "elf" }] },
  ],
};
const bookCmp = {
  instance_id: 3, remote_id: "w2", card_id: 9, head_revision_id: 50, accepted_hash: "", base: "none", app_hash: "hw2",
  fields: [{ path: "description", kind: "prose", hunks: [{ id: "description#0", status: "conflict", library: "x", app: "y" }] }],
};

let progress: Snapshot;
let compares: string[];
let resolved: unknown[];
let stale: boolean;
beforeEach(() => {
  progress = snap;
  compares = [];
  resolved = [];
  stale = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/jobs/5") return json({ ...job, progress });
      if (u.pathname === "/api/instances/3/items/a/compare") {
        compares.push(u.searchParams.get("card_id")!);
        return json({ ...cardCmp, card_id: Number(u.searchParams.get("card_id")) });
      }
      if (u.pathname === "/api/instances/3/lorebook-items/w2/compare") return json(bookCmp);
      if (u.pathname === "/api/instances/3/items/a/resolve") {
        resolved.push(await req.json());
        if (stale) {
          stale = false;
          return json({ detail: "the app's copy, the library, or the binding changed since the comparison; compare again" }, 409);
        }
        return json({ remote_id: "a", action: "bind", card_id: 1 });
      }
      if (u.pathname === "/api/search") return json({ total: 1, hits: [{ id: 77, slug: "belle", revision_id: 1, fields: { name: "Belle" } }] });
      return json({}, 404);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderReview(entry = "/sync/3/items/a?job=5") {
  const router = createMemoryRouter(
    [
      { path: "/sync/:instance/:kind/:remoteId", element: <Toasts><Review /></Toasts> },
      { path: "/sync/reconciles/:job", element: <p>Queue page</p> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("binds with the chosen side of each hunk, and the nested world book's", async () => {
  renderReview();
  const user = userEvent.setup();
  const card = await screen.findByRole("region", { name: "Ann" });
  await user.click(within(within(card).getByRole("radiogroup", { name: "Keep for description#1" })).getByLabelText("App"));
  await user.click(within(within(card).getByRole("radiogroup", { name: "Keep for tags#0" })).getByLabelText("Both"));
  const book = screen.getByRole("region", { name: "World book Ann's world" });
  await user.click(within(await within(book).findByRole("radiogroup", { name: "Keep for description#0" })).getByLabelText("App"));
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  expect(await screen.findByText("Queue page")).toBeInTheDocument();
  expect(resolved).toEqual([
    {
      remote_id: "a", action: "bind", card_id: 1, app_hash: "h2", base_revision_id: 118, accepted_hash: "", default: "library",
      choices: { "description#1": "app", "tags#0": "both" },
      lorebooks: [
        { remote_id: "w2", action: "bind", card_id: 9, app_hash: "hw2", base_revision_id: 50, accepted_hash: "", default: "library", choices: { "description#0": "app" } },
      ],
    },
  ]);
});

test("fields with no differences start collapsed, and list hunks alone offer Both", async () => {
  renderReview();
  const card = await screen.findByRole("region", { name: "Ann" });
  expect(within(card).getByText("name", { exact: false, selector: "summary" }).closest("details")).not.toHaveAttribute("open");
  expect(within(within(card).getByRole("radiogroup", { name: "Keep for description#1" })).queryByLabelText("Both")).not.toBeInTheDocument();
});

test("a stale resolve reloads the comparison", async () => {
  stale = true;
  renderReview();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Resolve" }));
  await waitFor(() => expect(compares).toHaveLength(2));
  expect(await screen.findByText(/compare again/)).toBeInTheDocument();
});

test("Other match compares with any library card", async () => {
  renderReview();
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Other match" }), "Bel");
  await user.click(screen.getByRole("button", { name: "Search" }));
  await user.click(await screen.findByRole("button", { name: "Belle" }));
  await waitFor(() => expect(compares).toContain("77"));
});

test("a drift review compares with the named card", async () => {
  renderReview("/sync/3/items/a?card=1");
  expect(await screen.findByRole("region", { name: "a" })).toBeInTheDocument();
  await waitFor(() => expect(compares).toEqual(["1"]));
});

test("an error item can only be skipped", async () => {
  progress = { ...snap, items: [{ remote_id: "a", name: "Ann", app_hash: "ha", verdict: "error", error: "boom", options: [{ action: "skip" }] }] };
  renderReview();
  const actions = await screen.findByRole("radiogroup", { name: "Action" });
  expect(within(actions).getAllByRole("radio")).toHaveLength(1);
  expect(within(actions).getByLabelText("Skip")).toBeChecked();
});

test("a bound item with no suggestion defaults to Bind", async () => {
  const { suggested: _, lorebooks: __, ...ann } = snap.items![0]!;
  progress = { ...snap, items: [{ ...ann, verdict: "bound" }] };
  renderReview();
  expect(within(await screen.findByRole("radiogroup", { name: "Action" })).getByLabelText("Bind")).toBeChecked();
});

test("Skip sends no choices and no world books", async () => {
  renderReview();
  const user = userEvent.setup();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  await user.click(within(screen.getByRole("radiogroup", { name: "Action" })).getByLabelText("Skip"));
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await screen.findByText("Queue page");
  expect(resolved).toEqual([{ remote_id: "a", app_hash: "h2", action: "skip" }]);
});

test("Other match on a new item offers Bind and binds to the found card", async () => {
  progress = { ...snap, items: [{ remote_id: "a", name: "Ann", app_hash: "ha", verdict: "new", options: [{ action: "import" }, { action: "skip" }] }] };
  renderReview();
  const user = userEvent.setup();
  await user.type(await screen.findByRole("textbox", { name: "Other match" }), "Bel");
  await user.click(screen.getByRole("button", { name: "Search" }));
  await user.click(await screen.findByRole("button", { name: "Belle" }));
  expect(within(screen.getByRole("radiogroup", { name: "Action" })).getByLabelText("Bind")).toBeChecked();
  const resolve = screen.getByRole("button", { name: "Resolve" });
  await waitFor(() => expect(resolve).toBeEnabled());
  await user.click(resolve);
  await screen.findByText("Queue page");
  expect(resolved).toEqual([expect.objectContaining({ action: "bind", card_id: 77 })]);
});
