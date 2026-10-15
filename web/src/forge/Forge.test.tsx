import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Card } from "../card/Card";
import { NewCard } from "../card/NewCard";
import { Toasts } from "../shell/Toasts";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const data = { name: "Brakka", description: "A grumpy dwarf.", personality: "Gruff." };
const entity = { id: 7, slug: "brakka", provenance: "api", created_at: now, revision: { id: 11, content_hash: "x", message: "", source: "ui", created_at: now, data } };

const decl = { applies: "card", instance_id: 1, instance_name: "Local model" };
const allActions = [
  { ...decl, id: "tighten", label: "Tighten", fields: ["description", "personality"] },
  { ...decl, id: "rewrite", label: "Rewrite…", fields: ["description"], instruction: true },
  { ...decl, id: "reforge", label: "Reforge", instruction: true },
  { ...decl, id: "evaluate", label: "Evaluate", report_only: true },
  { ...decl, id: "generate", label: "Forge", applies: "new", instruction: true, inputs: { type: "object", properties: { point_of_view: { type: "string", enum: ["first", "third"] } } } },
];

type JobT = { id: number; status: string; payload: { action: string; field?: string }; progress?: unknown; error?: string };
const job = (j: JobT) => ({
  kind: "action",
  instance_id: 1,
  attempts: 1,
  max_attempts: 1,
  run_after: now,
  created_at: now,
  progress: null,
  ...j,
  payload: { ...j.payload, card_id: 7, entity: { kind: "card", data } },
});

let actions: unknown[];
let jobs: ReturnType<typeof job>[];
let runs: unknown[];
beforeEach(() => {
  actions = allActions;
  jobs = [];
  runs = [];
  localStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      const path = url.pathname;
      if (path === "/api/cards/7") return json(entity);
      if (path === "/api/actions") return json(actions);
      if (path === "/api/jobs") return json(url.searchParams.get("card_id") === "7" ? jobs : []);
      const one = /^\/api\/jobs\/(\d+)$/.exec(path);
      if (one) return json(jobs.find((j) => j.id === Number(one[1])));
      if (path === "/api/actions/runs") {
        const body = await req.json();
        runs.push(body);
        return json(job({ id: 90 + runs.length, status: "queued", payload: body }), 202);
      }
      if (path === "/api/cards/search") {
        const body = await req.json();
        const names = [{ id: 1, name: "Billy" }, { id: 2, name: "Bill" }].filter((n) => n.name.toLowerCase().includes((body.filter?.name ?? "").toLowerCase()));
        return json({ hits: names.map((n) => ({ id: n.id, slug: "", revision_id: 1, fields: { name: n.name } })), total: names.length });
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/cards/new", element: <Toasts><NewCard /></Toasts> },
      { path: "/cards/:id", element: <Toasts><Card /></Toasts> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("without an action plugin the card page is the plain editor", async () => {
  actions = [];
  renderAt("/cards/7");
  await screen.findByLabelText("Description");
  expect(screen.queryByRole("button", { name: /Reforge/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Forge actions for Description" })).not.toBeInTheDocument();
});

test("field locks follow the scope of the runs picked up from the job list", async () => {
  jobs = [job({ id: 5, status: "running", payload: { action: "tighten", field: "description" }, progress: { message: "Tightening", done: 1, total: 2 } })];
  renderAt("/cards/7");
  expect(await screen.findByText("locked while Tighten runs")).toBeInTheDocument();
  expect(screen.getByLabelText("Description")).toBeDisabled();
  expect(screen.getByLabelText("Personality")).toBeEnabled();
  expect(screen.getByLabelText("Name")).toBeEnabled();
  expect(screen.getByText("Tightening")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Stop" })).toBeInTheDocument();
});

test("a whole-card run locks every field, and a report-only one none", async () => {
  jobs = [job({ id: 5, status: "running", payload: { action: "reforge" } })];
  renderAt("/cards/7");
  await waitFor(() => expect(screen.getByLabelText("Personality")).toBeDisabled());
  expect(screen.getByLabelText("Description")).toBeDisabled();
  expect(screen.getByLabelText("Name")).toBeDisabled();
});

test("a report-only run locks nothing", async () => {
  jobs = [job({ id: 5, status: "running", payload: { action: "evaluate" } })];
  renderAt("/cards/7");
  expect(await screen.findByText("Evaluate")).toBeInTheDocument();
  expect(screen.getByLabelText("Description")).toBeEnabled();
  expect(screen.queryByText(/locked while/)).not.toBeInTheDocument();
});

const tightened = (id: number) =>
  job({ id, status: "succeeded", payload: { action: "tighten", field: "description" }, progress: { result: { proposal: { ...data, description: "A dwarf." }, raw: "{}", warnings: [] } } });

test("an unreviewed run from before a reload opens Review", async () => {
  jobs = [tightened(6)];
  renderAt("/cards/7");
  const apply = await screen.findByRole("button", { name: "Apply 1 of 1 changes to draft" });
  expect(screen.getByRole("tab", { name: "Review (1)" })).toHaveAttribute("aria-selected", "true");
  await userEvent.click(apply);
  expect(screen.getByLabelText("Description")).toHaveValue("A dwarf.");
  expect(screen.getByText("unsaved")).toBeInTheDocument();
  expect(JSON.parse(localStorage.getItem("innkeeper.forge.settled")!)).toEqual([6]);
});

test("a settled run stays settled after a reload", async () => {
  localStorage.setItem("innkeeper.forge.settled", "[6]");
  jobs = [tightened(6)];
  renderAt("/cards/7");
  await screen.findByLabelText("Description");
  expect(await screen.findByRole("tab", { name: "Review" })).toBeDisabled();
  expect(screen.queryByText(/finished in/)).not.toBeInTheDocument();
});

test("a field action runs from the field's hammer menu with the draft", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Forge actions for Description" }));
  await user.click(screen.getByRole("menuitem", { name: "Tighten" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ instance_id: 1, action: "tighten", field: "description", entity: { kind: "card", id: 7, data } });
});

test("a field action with an instruction opens the Reforge panel set to it", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: "Forge actions for Description" }));
  await user.click(screen.getByRole("menuitem", { name: "Rewrite…" }));
  expect(screen.getByText("on Description", { exact: false })).toBeInTheDocument();
  await user.type(screen.getByLabelText("Instruction"), "Shorter{Enter}");
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ action: "rewrite", field: "description", instruction: "Shorter" });
});

test("an unknown mention blocks Run; a picked one sends its ID and the text as typed", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  const box = screen.getByLabelText("Instruction");
  await user.type(box, "ask @cards/Nobody now");
  expect(await screen.findByText(/no card has this name/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();

  await user.clear(box);
  await user.type(box, "@cards/Bil");
  await user.click(await screen.findByRole("button", { name: /^Billy/ }));
  await user.type(box, "is the brother");
  expect(screen.getByRole("button", { name: "Remove Billy" })).toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Run" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ action: "reforge", instruction: "@cards/Billy is the brother", context: { cards: [1] } });
});

test("a new card's Forge runs the new action from the concept", async () => {
  renderAt("/cards/new");
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Concept"), "A dwarf smith");
  await user.selectOptions(screen.getByRole("combobox"), "third");
  await user.click(screen.getByRole("button", { name: "Forge" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ action: "generate", instruction: "A dwarf smith", inputs: { point_of_view: "third" }, entity: { kind: "card" } });
  expect((runs[0] as { entity: object }).entity).not.toHaveProperty("data");
});

test("a whole-card run waits while another runs", async () => {
  jobs = [job({ id: 5, status: "running", payload: { action: "reforge" } })];
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  await user.type(screen.getByLabelText("Instruction"), "Robots{Enter}");
  expect(await screen.findByText(/waiting for the run before it/)).toBeInTheDocument();
  expect(runs).toHaveLength(0);
});
