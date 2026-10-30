import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Lorebook } from "./Lorebook";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const anvil = { keys: ["anvil"], content: "Heavy iron.\nRings when struck.", enabled: true, insertion_order: 100, extensions: {} };
const bellows = { keys: ["bellows"], content: "Pumps air.", enabled: true, insertion_order: 100, extensions: { depth: 4 } };
const data = { name: "Forge", description: "Smithing lore.", entries: [anvil, bellows] };
const entity = {
  id: 3, slug: "forge", provenance: "api", created_at: now, cards: [7], library_id: 2,
  revision: { id: 21, content_hash: "x", message: "", source: "ui", created_at: now, data },
};

const decl = { applies: "lorebook", instance_id: 1, instance_name: "Local model" };
const forgeActions = [
  { ...decl, id: "reforge", label: "Reforge", applies: "card", instruction: true },
  { ...decl, id: "tighten", label: "Tighten card", applies: "card", fields: ["description"] },
  { ...decl, id: "lore", label: "Rework lore", instruction: true },
  { ...decl, id: "tighten-lore", label: "Tighten", fields: ["description"] },
];

type JobT = { id: number; status: string; payload: { action: string; field?: string }; progress?: unknown };
const job = (j: JobT) => ({
  kind: "action",
  instance_id: 1,
  attempts: 1,
  max_attempts: 1,
  run_after: now,
  created_at: now,
  progress: null,
  ...j,
  payload: { ...j.payload, lorebook_id: 3, entity: { kind: "lorebook", data } },
});

let head: typeof entity;
let bindings: unknown[];
let puts: { path: string; body: { base_revision_id: number; data: typeof data } }[];
let actions: unknown[];
let jobs: ReturnType<typeof job>[];
let runs: { action: string; entity: { kind: string; id?: number } }[];
beforeEach(() => {
  head = entity;
  bindings = [];
  puts = [];
  actions = [];
  jobs = [];
  runs = [];
  localStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      const path = url.pathname;
      if (path === "/api/actions") return json(actions);
      if (path === "/api/actions/skills") return json([{ name: "noir", instance_id: 1, instance_name: "Local model" }]);
      if (path === "/api/jobs") return json(url.searchParams.get("lorebook_id") === "3" ? jobs : []);
      const one = /^\/api\/jobs\/(\d+)$/.exec(path);
      if (one) return json(jobs.find((j) => j.id === Number(one[1])));
      if (path === "/api/actions/runs") {
        const body = await req.json();
        runs.push(body);
        return json(job({ id: 90, status: "queued", payload: body }), 202);
      }
      if (req.method === "PUT" && path === "/api/lorebooks/3") {
        const body = await req.json();
        puts.push({ path, body });
        head = { ...head, revision: { ...head.revision, id: head.revision.id + 1, data: body.data } };
        return json(head);
      }
      if (path === "/api/libraries") return json([{ id: 1, name: "Main", cards: 0, lorebooks: 0, created_at: now }, { id: 2, name: "Chub archive", cards: 0, lorebooks: 1, created_at: now }]);
      if (path === "/api/lorebooks/3") return json(head);
      if (path === "/api/lorebooks/3/revisions") return json([head.revision]);
      if (path === "/api/cards/search") return json({ total: 1, hits: [{ id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" } }] });
      if (path === "/api/lorebooks/3/bindings") return json(bindings);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/lorebooks/:id", element: <Toasts><Lorebook /></Toasts> },
      { path: "/lorebooks/:id/history", element: <p>History</p> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("rows show keys and the first line, and the filter narrows them", async () => {
  renderAt("/lorebooks/3");
  expect(await screen.findByText("anvil", { selector: "summary strong" })).toBeInTheDocument();
  expect(screen.getByText("Heavy iron.")).toBeInTheDocument();
  expect(screen.getByText("bellows", { selector: "summary strong" })).toBeInTheDocument();
  expect(await screen.findByRole("link", { name: "Brakka" })).toHaveAttribute("href", "/cards/7");
  await userEvent.type(screen.getByLabelText("Filter entries"), "air");
  expect(screen.queryByText("Heavy iron.")).not.toBeInTheDocument();
  expect(screen.getByText("Pumps air.", { selector: "summary .muted" })).toBeInTheDocument();
});

test("editing an entry marks it and saves every entry", async () => {
  renderAt("/lorebooks/3");
  const content = await screen.findByLabelText("Entry 1 content");
  await userEvent.type(content, " Hot.");
  expect(screen.getByText("unsaved")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.base_revision_id).toBe(21);
  expect(puts[0]?.body.data.entries).toEqual([{ ...anvil, content: "Heavy iron.\nRings when struck. Hot." }, bellows]);
  expect(puts[0]?.body.data.name).toBe("Forge");
});

test("⌘S saves the lorebook from its bar", async () => {
  renderAt("/lorebooks/3");
  await userEvent.type(await screen.findByLabelText("Name"), " Lore");
  expect(screen.getByText("1 unsaved field")).toBeInTheDocument();
  fireEvent.keyDown(window, { key: "s", metaKey: true });
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.data.name).toBe("Forge Lore");
});

test("keys, flags, depth, add, move, and delete", async () => {
  renderAt("/lorebooks/3");
  await userEvent.type(await screen.findByLabelText("Add to Entry 1 keys"), "forge{Enter}");
  await userEvent.click(screen.getByLabelText("Entry 2 Always on"));
  await userEvent.clear(screen.getByLabelText("Entry 2 depth"));
  await userEvent.type(screen.getByLabelText("Entry 2 depth"), "2");
  await userEvent.click(screen.getByRole("button", { name: "Move entry 2 up" }));
  await userEvent.click(screen.getByRole("button", { name: "Add entry" }));
  await userEvent.type(screen.getByLabelText("Entry 3 content"), "New lore.");
  await userEvent.click(screen.getByRole("button", { name: "Delete entry 2" }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.data.entries).toEqual([
    { ...bellows, constant: true, extensions: { depth: 2 } },
    { keys: [], content: "New lore.", enabled: true, insertion_order: 100, position: "before_char", extensions: {} },
  ]);
});

test("entry flags have readable labels with hints, and rows show a drag handle", async () => {
  renderAt("/lorebooks/3");
  expect(await screen.findByLabelText("Entry 1 Always on")).toHaveAccessibleDescription("Inserted even without a key match");
  expect(screen.getByLabelText("Entry 1 Needs a secondary key")).toHaveAccessibleDescription("A key from each key list must match");
  expect(screen.getByLabelText("Entry 1 Enabled")).toBeChecked();
  expect(screen.getAllByTitle("Drag to reorder")).toHaveLength(2);
});

test("the Metadata tab names the lorebook's library", async () => {
  renderAt("/lorebooks/3");
  await userEvent.click(await screen.findByRole("tab", { name: "Metadata" }));
  expect(await screen.findByText("Chub archive")).toBeInTheDocument();
});

test("the Metadata tab shows the core facts and no enrichments", async () => {
  renderAt("/lorebooks/3");
  await userEvent.setup().click(await screen.findByRole("tab", { name: "Metadata" }));
  expect(await screen.findByRole("heading", { name: "Details" })).toBeInTheDocument();
  expect(screen.queryByRole("heading", { name: "Enrichments" })).not.toBeInTheDocument();
});

test("the panel links to history", async () => {
  renderAt("/lorebooks/3");
  expect(await screen.findByRole("link", { name: /revision/ })).toHaveAttribute("href", "/lorebooks/3/history");
});

test("saving keeps entries that aren't objects, and a cleared depth removes the key", async () => {
  head = { ...entity, revision: { ...entity.revision, data: { ...data, entries: [anvil, bellows, "stray" as never] } } };
  renderAt("/lorebooks/3");
  await userEvent.clear(await screen.findByLabelText("Entry 2 depth"));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.data.entries).toEqual([anvil, { ...bellows, extensions: {} }, "stray"]);
});

test("only the row's summary starts a drag", async () => {
  renderAt("/lorebooks/3");
  const content = await screen.findByLabelText("Entry 1 content");
  expect(content.closest("[draggable='true']")).toBeNull();
  expect(document.querySelectorAll("summary[draggable='true']")).toHaveLength(2);
});

test("a lorebook's followed source update shows a banner", async () => {
  bindings = [
    { id: 5, instance_id: 4, remote_id: "chub:alice/forge", status: "remote_changed", ahead: false, remote_state: "changed",
      autosync: false, push: false, paused: false, check_failures: 0, app_hash: "h" },
  ];
  renderAt("/lorebooks/3");
  expect(await screen.findByText(/Update on chub/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Review" })).toHaveAttribute("href", "/sync/4/lorebook/chub%3Aalice%2Fforge?card=3");
});

test("a lorebook field run locks only its field", async () => {
  actions = forgeActions;
  jobs = [job({ id: 5, status: "running", payload: { action: "tighten-lore", field: "description" }, progress: { message: "Tightening" } })];
  renderAt("/lorebooks/3");
  expect(await screen.findByText("locked while Tighten runs")).toBeInTheDocument();
  expect(screen.getByLabelText("Description")).toBeDisabled();
  expect(screen.getByLabelText("Entry 1 content")).toBeEnabled();
  expect(screen.getByLabelText("Name")).toBeEnabled();
  expect(screen.getByText("Tightening")).toBeInTheDocument();
});

test("a whole-lorebook run locks the name, the description, and the entries", async () => {
  actions = forgeActions;
  jobs = [job({ id: 5, status: "running", payload: { action: "lore" } })];
  renderAt("/lorebooks/3");
  await waitFor(() => expect(screen.getByLabelText("Entry 1 content")).toBeDisabled());
  expect(document.querySelectorAll("summary[draggable='true']")).toHaveLength(0);
  expect(screen.getByLabelText("Description")).toBeDisabled();
  expect(screen.getByLabelText("Name")).toBeDisabled();
  expect(screen.getByRole("button", { name: "Add entry" })).toBeDisabled();
});

test("Reforge runs the lorebook's instruction action on the lorebook", async () => {
  actions = forgeActions;
  renderAt("/lorebooks/3");
  await screen.findByRole("button", { name: /^Reforge ⌘K/ });
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "Forge actions for Description" }));
  expect(screen.getAllByRole("menuitem").map((m) => m.textContent)).toEqual(["Tighten"]);
  await user.keyboard("{Escape}");
  await user.keyboard("{Control>}k{/Control}");
  const box = await screen.findByRole("combobox", { name: "Instruction" });
  expect(box).toHaveAttribute("placeholder", expect.stringContaining("Describe a change to this lorebook"));
  expect(screen.queryByText("Quick actions")).not.toBeInTheDocument();
  await user.type(box, "Add a hammer @skills/noir please");
  await waitFor(() => expect(screen.getByRole("button", { name: "Run" })).toBeEnabled());
  await user.click(screen.getByRole("button", { name: "Run" }));
  await waitFor(() => expect(runs).toHaveLength(1));
  expect(runs[0]).toMatchObject({
    action: "lore",
    instance_id: 1,
    instruction: "Add a hammer @skills/noir please",
    context: { skills: ["noir"] },
    entity: { kind: "lorebook", id: 3, data },
  });
});

test("Review offers each changed entry, and one click saves only the taken ones", async () => {
  actions = forgeActions;
  const proposal = { ...data, entries: [{ ...anvil, content: "Heavier iron." }, { keys: ["hammer"], content: "Rings." }] };
  jobs = [job({ id: 6, status: "succeeded", payload: { action: "lore" }, progress: { result: { proposal, warnings: [] } } })];
  renderAt("/lorebooks/3");
  expect(await screen.findByRole("tab", { name: "Review (3)" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("radiogroup", { name: "Choice for Entry 1 · anvil (changed)" })).toBeInTheDocument();
  expect(screen.getByText("removed")).toBeInTheDocument();
  expect(screen.getByText("added")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(within(screen.getByRole("radiogroup", { name: "Choice for Entry 2 · bellows (removed)" })).getByRole("radio", { name: "Keep mine" }));
  await user.click(screen.getByRole("button", { name: "Save 2 of 3 changes" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]?.body.data.entries).toEqual([{ ...anvil, content: "Heavier iron." }, bellows, { keys: ["hammer"], content: "Rings." }]);
});

test("a plugin with only card actions shows no Forge on the lorebook page", async () => {
  actions = forgeActions.filter((a) => a.applies === "card");
  renderAt("/lorebooks/3");
  await screen.findByLabelText("Entry 1 content");
  await waitFor(() => expect(fetch).toHaveBeenCalledWith(expect.objectContaining({ url: expect.stringContaining("/api/actions") })));
  expect(screen.queryByRole("button", { name: /^Reforge ⌘K/ })).not.toBeInTheDocument();
  expect(screen.getByRole("tab", { name: "Fields" })).toBeInTheDocument();
  expect(screen.queryByRole("tab", { name: /^Review/ })).not.toBeInTheDocument();
  expect(screen.queryByRole("tab", { name: "Report" })).not.toBeInTheDocument();
});

test("a proposal that only reorders entries opens no Review", async () => {
  actions = forgeActions;
  const proposal = { ...data, entries: [bellows, anvil] };
  jobs = [job({ id: 6, status: "succeeded", payload: { action: "lore" }, progress: { result: { proposal, warnings: [] } } })];
  renderAt("/lorebooks/3");
  expect(await screen.findByRole("tab", { name: "Review" })).toBeDisabled();
  expect(screen.getByRole("tab", { name: "Fields" })).toHaveAttribute("aria-selected", "true");
});
