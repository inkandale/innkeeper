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
const entity = { id: 7, slug: "brakka", provenance: "api", created_at: now, library_id: 2, revision: { id: 11, content_hash: "x", message: "", source: "ui", created_at: now, data } };

const decl = { applies: "card", instance_id: 1, instance_name: "Local model" };
const allActions = [
  { ...decl, id: "tighten", label: "Tighten", fields: ["description", "personality"] },
  { ...decl, id: "rewrite", label: "Rewrite…", fields: ["description"], instruction: true },
  { ...decl, id: "reforge", label: "Reforge", instruction: true },
  { ...decl, id: "evaluate", label: "Evaluate", report_only: true },
  { ...decl, id: "critique", label: "Critique", fields: ["description"], report_only: true },
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
const skills = [
  { name: "college", description: "Campus life.", instance_id: 1, instance_name: "Local model" },
  { name: "noir", instance_id: 1, instance_name: "Local model" },
  { name: "space", description: "Ships and stations.", instance_id: 2, instance_name: "Cloud model" },
];
let jobs: ReturnType<typeof job>[];
let runs: unknown[];
// Jobs the tab started; the card's job list holds only jobs.
let created: ReturnType<typeof job>[];
let runFails: boolean;
let mentionSearches: unknown[];
beforeEach(() => {
  mentionSearches = [];
  actions = allActions;
  jobs = [];
  runs = [];
  created = [];
  runFails = false;
  localStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      const path = url.pathname;
      if (path === "/api/cards/7") return json(entity);
      if (path === "/api/actions") return json(actions);
      if (path === "/api/actions/skills") return json(skills);
      if (path === "/api/jobs") return json(url.searchParams.get("card_id") === "7" ? jobs : []);
      const one = /^\/api\/jobs\/(\d+)$/.exec(path);
      if (one) return json([...jobs, ...created].find((j) => j.id === Number(one[1])));
      if (path === "/api/actions/runs") {
        if (runFails) return json({ detail: "the plugin is busy" }, 503);
        const body = await req.json();
        runs.push(body);
        created.push(job({ id: 90 + runs.length, status: "running", payload: body }));
        return json(created.at(-1), 202);
      }
      if (path === "/api/cards/search") {
        const body = await req.json();
        mentionSearches.push(body);
        const names = [{ id: 1, name: "Billy" }, { id: 2, name: "Bill" }, { id: 4, name: "Billy Bob" }].filter((n) => n.name.toLowerCase().includes((body.filter?.name ?? "").toLowerCase()));
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
  await user.type(box, "@cards/Nobody ");
  expect(await screen.findByText(/no card has this name/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();

  await user.clear(box);
  await user.type(box, "@cards/Bil");
  await user.click(await screen.findByRole("option", { name: /^Billy card/ }));
  await user.type(box, "is");
  expect(screen.getByRole("button", { name: "Remove Billy" })).toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Run" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ action: "reforge", instruction: "@cards/Billy is", context: { cards: [1] } });
  // Mentions name items in the card's own library.
  expect(mentionSearches.length).toBeGreaterThan(0);
  expect(mentionSearches.every((b) => (b as { library?: number }).library === 2)).toBe(true);
});

test("a new card's Forge runs the new action from the concept", async () => {
  renderAt("/cards/new");
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Concept"), "A dwarf smith");
  await user.selectOptions(screen.getByRole("combobox", { name: "point_of_view" }), "third");
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

test("after a pick the list closes, so Enter runs instead of picking again", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  const box = screen.getByRole("combobox", { name: "Instruction" });
  await user.type(box, "@cards/Bil");
  expect(await screen.findByRole("option", { name: /^Billy card/ })).toHaveAttribute("aria-selected", "true");
  expect(box.getAttribute("aria-activedescendant")).toBe(screen.getByRole("option", { name: /^Billy card/ }).id);
  await user.keyboard("{Enter}");
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeEnabled());
  await user.keyboard("{Enter}");
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ instruction: "@cards/Billy ", context: { cards: [1] } });
});

test("the Reforge panel keeps the instruction open until the run starts", async () => {
  runFails = true;
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  await user.type(screen.getByLabelText("Instruction"), "Robots{Enter}");
  expect(await screen.findByText("the plugin is busy")).toBeInTheDocument();
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  expect(screen.getByLabelText("Instruction")).toHaveValue("Robots");
});

test("a field menu takes focus, moves with arrows, and Escape returns to its button", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  const button = await screen.findByRole("button", { name: "Forge actions for Description" });
  await user.click(button);
  expect(screen.getByRole("menuitem", { name: "Tighten" })).toHaveFocus();
  await user.keyboard("{ArrowDown}");
  expect(screen.getByRole("menuitem", { name: "Rewrite…" })).toHaveFocus();
  await user.keyboard("{Escape}");
  expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  expect(button).toHaveFocus();
});

test("arrow keys move between tabs", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  const fields = await screen.findByRole("tab", { name: "Fields" });
  expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", fields.id);
  await user.click(fields);
  // Review is disabled with nothing to review, so the arrow skips it.
  await user.keyboard("{ArrowRight}");
  expect(screen.getByRole("tab", { name: "Report" })).toHaveFocus();
  expect(screen.getByRole("tab", { name: "Report" })).toHaveAttribute("aria-selected", "true");
});

test("a new card offers no report-only actions and can't be saved while a run is out", async () => {
  renderAt("/cards/new");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("tab", { name: "Fields" }));
  await user.type(screen.getByLabelText("Description"), "A smith");
  await user.click(screen.getByRole("button", { name: "Forge actions for Description" }));
  expect(screen.queryByRole("menuitem", { name: "Critique" })).not.toBeInTheDocument();
  await user.click(screen.getByRole("menuitem", { name: "Tighten" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(await screen.findByText(/Save waits for the Forge/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});

test("skill autocomplete offers the chosen model setup's skills, and a picked skill's name goes in context", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  const box = screen.getByLabelText("Instruction");
  await user.type(box, "@skills/");
  expect(await screen.findByRole("option", { name: "college Campus life." })).toBeInTheDocument();
  expect(screen.getByRole("option", { name: "noir skill" })).toBeInTheDocument();
  expect(screen.queryByRole("option", { name: /space/ })).not.toBeInTheDocument();
  await user.click(screen.getByRole("option", { name: "college Campus life." }));
  await user.type(box, "tone");
  expect(await screen.findByRole("button", { name: "Remove college" })).toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Run" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ instruction: "@skills/college tone", context: { skills: ["college"] } });
});

test("a typed skill name resolves, and one the model setup lacks blocks Run", async () => {
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  const box = screen.getByLabelText("Instruction");
  await user.type(box, "@skills/space ");
  expect(await screen.findByText(/this model setup has no skill by this name/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Run" })).toBeDisabled();

  await user.clear(box);
  await user.type(box, "@skills/noir please");
  await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Run" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ context: { skills: ["noir"] } });
});

test("switching the model setup turns a skill it lacks unknown on the Start concept", async () => {
  actions = [...allActions, { ...allActions.at(-1)!, instance_id: 2, instance_name: "Cloud model" }];
  renderAt("/cards/new");
  const user = userEvent.setup();
  const box = await screen.findByLabelText("Concept");
  await user.type(box, "@skills/col");
  await user.click(await screen.findByRole("option", { name: "college Campus life." }));
  await user.type(box, "a dean");
  await waitFor(() => expect(screen.getByRole("button", { name: "Forge" })).toBeEnabled());

  await user.selectOptions(screen.getByRole("combobox", { name: "Model setup" }), "2");
  expect(await screen.findByText(/this model setup has no skill by this name/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Forge" })).toBeDisabled();

  await user.selectOptions(screen.getByRole("combobox", { name: "Model setup" }), "1");
  await waitFor(() => expect(screen.getByRole("button", { name: "Forge" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Forge" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ instance_id: 1, action: "generate", context: { skills: ["college"] } });
});

test("the Reforge panel offers a whole-card field action, run without a field", async () => {
  actions = [...allActions, { ...decl, id: "expand", label: "Expand", description: "Add detail.", fields: ["description", "personality"], whole_card: true }];
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: /Reforge/ }));
  await user.click(screen.getByRole("button", { name: /^Expand(?! all)/ }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({ action: "expand" });
  expect(runs[0]).not.toHaveProperty("field");
});

test("the Report tab runs its report action again once a report is in", async () => {
  jobs = [job({ id: 5, status: "succeeded", payload: { action: "evaluate" }, progress: { result: { report: { summary: "Solid.", score: 70 } } } })];
  renderAt("/cards/7");
  const user = userEvent.setup();
  await user.click(await screen.findByRole("tab", { name: "Report" }));
  expect(await screen.findByText("Solid.")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /Evaluate again/ }));
  await waitFor(() => expect(runs).toEqual([expect.objectContaining({ action: "evaluate" })]));
});
