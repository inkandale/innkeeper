import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
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
  id: 3, slug: "forge", provenance: "api", created_at: now, cards: [7],
  revision: { id: 21, content_hash: "x", message: "", source: "ui", created_at: now, data },
};

let head: typeof entity;
let bindings: unknown[];
let puts: { path: string; body: { base_revision_id: number; data: typeof data } }[];
beforeEach(() => {
  head = entity;
  bindings = [];
  puts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method === "PUT" && path === "/api/lorebooks/3") {
        const body = await req.json();
        puts.push({ path, body });
        head = { ...head, revision: { ...head.revision, id: head.revision.id + 1, data: body.data } };
        return json(head);
      }
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

test("keys, flags, depth, add, move, and delete", async () => {
  renderAt("/lorebooks/3");
  await userEvent.type(await screen.findByLabelText("Add to Entry 1 keys"), "forge{Enter}");
  await userEvent.click(screen.getByLabelText("Entry 2 constant"));
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

test("the panel links to history", async () => {
  renderAt("/lorebooks/3");
  expect(await screen.findByRole("link", { name: "All history" })).toHaveAttribute("href", "/lorebooks/3/history");
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
