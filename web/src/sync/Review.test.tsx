import { focusManager, QueryClient, QueryClientProvider } from "@tanstack/react-query";
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
let cardBody: typeof cardCmp;
let compares: string[];
let resolved: unknown[];
let stale: boolean;
let changesGets: number;
let changes: { rows: { card_id?: number; name: string; apps: { instance_id: number; state: string; remote_id: string; job_id?: number; binding_id?: number }[] }[]; counts: object };
const baseChanges = {
  rows: [
    { card_id: 1, name: "Ann", apps: [{ instance_id: 3, state: "differs", remote_id: "a", job_id: 5 }] },
    { card_id: 4, name: "Bo", apps: [{ instance_id: 3, state: "ahead", remote_id: "x", binding_id: 11 }] },
    { name: "Cy", apps: [{ instance_id: 3, state: "new", remote_id: "c", job_id: 5 }] },
  ],
  counts: {},
};
beforeEach(() => {
  changes = baseChanges;
  changesGets = 0;
  progress = snap;
  cardBody = cardCmp;
  compares = [];
  resolved = [];
  stale = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/jobs/5") return json({ ...job, progress });
      if (u.pathname === "/api/sync/changes") {
        changesGets++;
        // The API filters entries by app and state.
        const inst = Number(u.searchParams.get("instance_id")) || 0;
        const state = u.searchParams.get("state") ?? "";
        const rows = changes.rows
          .map((r) => ({ ...r, apps: r.apps.filter((c) => (!inst || c.instance_id === inst) && (!state || c.state === state)) }))
          .filter((r) => r.apps.length);
        return json({ ...changes, rows });
      }
      if (u.pathname === "/api/instances/3/cards/a/compare") {
        compares.push(u.searchParams.get("card_id")!);
        return json({ ...cardBody, card_id: Number(u.searchParams.get("card_id")) });
      }
      if (u.pathname === "/api/instances/3/cards/x/compare") {
        compares.push(u.search);
        return json({ ...cardCmp, remote_id: "x", card_id: 4, accepted_hash: "acc", base: u.searchParams.get("base") ?? "accepted" });
      }
      if (u.pathname === "/api/instances/3/cards/x/resolve") {
        resolved.push(await req.json());
        return json({ remote_id: "x", action: "bind", card_id: 4 });
      }
      if (u.pathname === "/api/instances/3/lorebooks/w2/compare") return json(bookCmp);
      if (u.pathname === "/api/instances/3/cards/a/resolve") {
        resolved.push(await req.json());
        if (stale) {
          stale = false;
          return json({ detail: "the app's copy, the library, or the binding changed since the comparison; compare again" }, 409);
        }
        return json({ remote_id: "a", action: "bind", card_id: 1 });
      }
      if (u.pathname === "/api/cards/search") return json({ total: 1, hits: [{ id: 77, slug: "belle", revision_id: 1, fields: { name: "Belle" } }] });
      return json({}, 404);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderReview(entry = "/sync/3/card/a?job=5") {
  const router = createMemoryRouter(
    [
      { path: "/sync/:instance/:kind/:remoteId", element: <Toasts><Review /></Toasts> },
      { path: "/sync/reconciles/:job", element: <p>Queue page</p> },
      { path: "/sync/changes", element: <p>Changes page</p> },
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

function importWithNewerComparison() {
  const [ann] = snap.items!;
  progress = { ...snap, items: [{ ...ann!, binding_id: 3, lorebooks: [{ ...ann!.lorebooks![0]!, binding_id: 4 }] }] };
  // Reopened after an import, the fresh comparison names the binding that import made.
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/instances/3/cards/a/compare" ? json({ ...cardCmp, binding_id: 12 }) : fetch(req),
  );
}

test("an import sends the snapshot's binding, not a reopened comparison's", async () => {
  importWithNewerComparison();
  renderReview();
  const user = userEvent.setup();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  await user.click(within(screen.getByRole("radiogroup", { name: "Action" })).getByLabelText("Import as new"));
  await user.selectOptions(screen.getByRole("combobox", { name: "Action for Ann's world" }), "import");
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await screen.findByText("Queue page");
  expect(resolved).toEqual([
    {
      remote_id: "a", action: "import", app_hash: "h2", binding_id: 3,
      lorebooks: [{ remote_id: "w2", action: "import", app_hash: "hw", binding_id: 4 }],
    },
  ]);
});

test("after a 409, an import sends the reloaded comparison's binding", async () => {
  importWithNewerComparison();
  stale = true;
  renderReview();
  const user = userEvent.setup();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  await user.click(within(screen.getByRole("radiogroup", { name: "Action" })).getByLabelText("Import as new"));
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  expect(await screen.findByText(/compare again/)).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await screen.findByText("Queue page");
  expect(resolved.map((r) => (r as { binding_id: number }).binding_id)).toEqual([3, 12]);
});

test("a nested world book with no suggestion is left to the server", async () => {
  const [ann] = snap.items!;
  const { suggested: _, ...book } = ann!.lorebooks![0]!;
  progress = { ...snap, items: [{ ...ann!, lorebooks: [book] }] };
  renderReview();
  const user = userEvent.setup();
  expect(await screen.findByRole("combobox", { name: "Action for Ann's world" })).toHaveDisplayValue("Server's suggestion");
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await screen.findByText("Queue page");
  expect(resolved).toEqual([expect.not.objectContaining({ lorebooks: expect.anything() })]);
});

test("refocusing the window keeps the comparison", async () => {
  renderReview();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  focusManager.setFocused(false);
  focusManager.setFocused(true);
  await new Promise((r) => setTimeout(r, 50));
  expect(compares).toEqual(["1"]);
  focusManager.setFocused(undefined);
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
  renderReview("/sync/3/card/a?card=1");
  expect(await screen.findByRole("region", { name: "Ann" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { level: 1 })).toHaveTextContent("Ann");
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

const at = (router: ReturnType<typeof renderReview>) => router.state.location.pathname + router.state.location.search;

test("from the queue, shows its place among the reviews and resolves on to the next", async () => {
  const router = renderReview("/sync/3/card/a?job=5&queue=1");
  const user = userEvent.setup();
  expect(await screen.findByText("1 of 3")).toBeInTheDocument();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await waitFor(() => expect(at(router)).toBe("/sync/3/card/x?card=4&base=none&queue=1"));
  expect(resolved).toHaveLength(1);
});

test("Later moves on without resolving", async () => {
  const router = renderReview("/sync/3/card/a?job=5&queue=1");
  const user = userEvent.setup();
  await screen.findByText("1 of 3");
  await user.click(screen.getByRole("button", { name: "Later" }));
  await waitFor(() => expect(at(router)).toBe("/sync/3/card/x?card=4&base=none&queue=1"));
  expect(resolved).toEqual([]);
});

test("with no review left, the queue opens", async () => {
  const router = renderReview("/sync/3/card/c?job=5&queue=1");
  const user = userEvent.setup();
  await screen.findByText("3 of 3");
  await user.click(screen.getByRole("button", { name: "Later" }));
  await waitFor(() => expect(at(router)).toBe("/sync/changes"));
});

test("filtered to one app, steps through that app's reviews only and returns to the filtered queue", async () => {
  changes = {
    ...baseChanges,
    rows: [
      baseChanges.rows[0]!,
      { card_id: 6, name: "Dee", apps: [{ instance_id: 8, state: "differs", remote_id: "d", job_id: 9 }] },
      ...baseChanges.rows.slice(1),
    ],
  };
  const router = renderReview("/sync/3/card/a?job=5&queue=1&instance_id=3");
  const user = userEvent.setup();
  expect(await screen.findByText("1 of 3")).toBeInTheDocument();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await waitFor(() => expect(at(router)).toBe("/sync/3/card/x?card=4&base=none&queue=1&instance_id=3"));
  await screen.findByText("2 of 3");
  await user.click(screen.getByRole("button", { name: "Later" }));
  await waitFor(() => expect(at(router)).toBe("/sync/3/card/c?job=5&queue=1&instance_id=3"));
  await screen.findByText("3 of 3");
  await user.click(screen.getByRole("button", { name: "Later" }));
  await waitFor(() => expect(at(router)).toBe("/sync/changes?instance_id=3"));
});

test("a change not pushed compares without the accepted copy and resolves the same way", async () => {
  const router = renderReview("/sync/3/card/x?card=4&base=none&queue=1");
  const user = userEvent.setup();
  await screen.findByText("2 of 3");
  expect(compares).toEqual(["?card_id=4&base=none"]);
  await user.click(await screen.findByLabelText("App", { selector: "[name='card:x/description#1']" }));
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await waitFor(() => expect(at(router)).toBe("/sync/3/card/c?job=5&queue=1"));
  expect(resolved).toEqual([
    expect.objectContaining({ card_id: 4, accepted_hash: "acc", base: "none", choices: { "description#1": "app" } }),
  ]);
});

test("without the queue, Review neither counts nor offers Later", async () => {
  renderReview();
  await screen.findByRole("radiogroup", { name: "Keep for description#1" });
  expect(screen.queryByRole("button", { name: "Later" })).not.toBeInTheDocument();
  expect(changesGets).toBe(0);
});

test("a suggested import compares with the first unbound match and shows the diff without choices", async () => {
  progress = {
    ...snap,
    items: [{
      remote_id: "a", name: "Ann", app_hash: "ha", verdict: "likely",
      options: [{ action: "import" }, { action: "skip" }],
      suggested: { action: "import", why: "4 of 4 core fields differ from #1 Ann" },
      candidates: [
        { card_id: 2, name: "Ann", reasons: { name: 0 }, differing_fields: 4, conflicts: 0, bound_to: "Ann_1.png" },
        { card_id: 1, name: "Ann", reasons: { name: 0 }, differing_fields: 4, conflicts: 0 },
      ],
    }],
  };
  renderReview();
  expect(await screen.findByRole("combobox", { name: "Library match" })).toHaveValue("1");
  expect(screen.getByRole("option", { name: "#2 Ann (already bound to another app item)" })).toBeInTheDocument();
  expect(await screen.findByText("short.")).toBeInTheDocument();
  expect(screen.queryByRole("radiogroup", { name: "Keep for description#1" })).not.toBeInTheDocument();
});

test("a prose change reads in one column, with removed words struck and added ones marked", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{ path: "first_mes", kind: "prose", hunks: [{ id: "first_mes#0", status: "conflict", library: "The storm is “crazy” here.", app: "The storm is “wild” here." }] }],
  };
  renderReview();
  const block = (await screen.findByRole("radiogroup", { name: "Keep for first_mes#0" })).parentElement!;
  expect(within(block).getByText("“crazy”", { selector: "del" })).toBeInTheDocument();
  expect(within(block).getByText("“wild”", { selector: "ins" })).toBeInTheDocument();
  // One column: the shared words appear once, not once per side.
  expect(within(block).getAllByText(/The storm is/)).toHaveLength(1);
});

test("a JSON change names its key and diffs its words side by side", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{ path: "extensions", kind: "json", hunks: [{ id: "extensions.talkativeness#0", status: "conflict", library: "0.5", app: "0.7" }] }],
  };
  renderReview();
  const block = (await screen.findByRole("radiogroup", { name: "Keep for extensions.talkativeness#0" })).parentElement!;
  expect(within(block).getByText("talkativeness")).toBeInTheDocument();
  expect(within(block).getByText("0.5", { selector: "del" })).toBeInTheDocument();
  expect(within(block).getByText("0.7", { selector: "ins" })).toBeInTheDocument();
});

test("a JSON field folds its unchanged keys, each still labelled", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{
      path: "extensions", kind: "json",
      hunks: [
        { id: "extensions.depth_prompt.depth#0", status: "same", library: 4 },
        { id: "extensions.depth_prompt.role#0", status: "same", library: "system" },
        { id: "extensions.talkativeness#0", status: "conflict", library: "0.5", app: "0.7" },
      ],
    }],
  } as typeof cardCmp;
  renderReview();
  await screen.findByRole("radiogroup", { name: "Keep for extensions.talkativeness#0" });
  const folded = screen.getByText("2 unchanged keys").closest("details")!;
  expect(folded).not.toHaveAttribute("open");
  expect(within(folded).getByText("depth_prompt.depth")).toBeInTheDocument();
  expect(within(folded).getByText("depth_prompt.role")).toBeInTheDocument();
});

test("prose folds an unchanged run longer than two sentences and keeps a shorter one", async () => {
  cardBody = {
    ...cardCmp,
    fields: [{
      path: "first_mes", kind: "prose",
      hunks: [
        { id: "first_mes#0", status: "same", library: "One. Two. Three. " },
        { id: "first_mes#1", status: "conflict", library: "Old.", app: "New." },
        { id: "first_mes#2", status: "same", library: " Four. Five." },
      ],
    }],
  };
  renderReview();
  await screen.findByRole("radiogroup", { name: "Keep for first_mes#1" });
  const folded = screen.getByText("⋯ 3 unchanged sentences").closest("details")!;
  expect(within(folded).getByText("One. Two. Three.")).toBeInTheDocument();
  expect(screen.getByText("Four. Five.").closest("details.unchanged")).toBeNull();
});
