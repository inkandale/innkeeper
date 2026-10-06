import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Card } from "./Card";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const data = { name: "Brakka", creator: "dg", description: "A grumpy dwarf.", alternate_greetings: ["Hail."], extensions: { depth: 4 } };
const entity = {
  id: 7, slug: "brakka", provenance: "api", created_at: now, tags: ["dwarf"], lorebooks: [3], collections: [1],
  revision: { id: 11, content_hash: "x", message: "", source: "ui", created_at: now, avatar: "ab".repeat(32), data },
};

let head: typeof entity;
let conflict: boolean;
let puts: { path: string; body: unknown }[];
beforeEach(() => {
  head = entity;
  conflict = false;
  puts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method === "PUT") {
        // Collection PUTs have no body.
        const body = await req.json().catch(() => null);
        puts.push({ path, body });
        if (path === "/api/cards/7") {
          if (conflict) {
            // Someone else saved revision 12 first.
            conflict = false;
            head = { ...entity, revision: { ...entity.revision, id: 12, data: { ...data, description: "A cheerful dwarf." } } };
            return json({ detail: "the card changed" }, 409);
          }
          const avatar = body.avatar ?? head.revision.avatar;
          head = { ...head, revision: { ...head.revision, id: head.revision.id + 1, data: body.data, avatar } };
          return json(head);
        }
        if (path === "/api/cards/7/tags") return json(body.tags);
        return new Response(null, { status: 204 });
      }
      if (path === "/api/blobs") return json({ sha256: "cd".repeat(32), mime: "image/png", size: 3, store: "local", created_at: now }, 201);
      if (path === "/api/cards/7") return json(head);
      if (path === "/api/cards/7/revisions") return json([head.revision]);
      if (path === "/api/cards/7/bindings")
        return json([{ id: 1, instance_id: 2, remote_id: "r", status: "ahead", ahead: true, remote_state: "unchanged" }]);
      if (path === "/api/cards/7/sources")
        return json([{ id: 4, kind: "chub", ref: "dg/brakka", url: "https://chub.ai/characters/dg/brakka", linked_by: "user", created_at: now }]);
      if (path === "/api/instances") return json([{ id: 2, name: "Lumiverse", plugin: "lumiverse" }]);
      if (path === "/api/plugins") return json([{ name: "lumiverse", capabilities: ["push"] }]);
      if (path === "/api/collections")
        return json([
          { id: 1, name: "Favorites" },
          { id: 2, name: "Pirates" },
        ]);
      if (path === "/api/lorebooks/3") return json({ id: 3, revision: { id: 1, data: { name: "Forge" } } });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/cards/:id", element: <Toasts><Card /></Toasts> },
      { path: "/cards/:id/history", element: <p>History</p> },
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

test("editing a field marks it unsaved and saves on the base revision", async () => {
  renderAt("/cards/7");
  const box = await screen.findByLabelText("Description");
  expect(screen.getByText("~4 tokens")).toBeInTheDocument();
  const save = screen.getByRole("button", { name: "Save" });
  expect(save).toBeDisabled();
  await userEvent.type(box, " Old.");
  expect(screen.getByText("unsaved")).toBeInTheDocument();
  await userEvent.click(save);
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({
    path: "/api/cards/7",
    body: { base_revision_id: 11, data: { name: "Brakka", description: "A grumpy dwarf. Old.", extensions: { depth: 4 } } },
  });
  await waitFor(() => expect(screen.queryByText("unsaved")).not.toBeInTheDocument());
  expect(screen.getByLabelText("Description")).toHaveValue("A grumpy dwarf. Old.");
});

test("alternate greetings can be added and removed", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "Add greeting" }));
  await userEvent.type(screen.getByLabelText("Greeting 2"), "Well met.");
  await userEvent.click(screen.getByRole("button", { name: "Remove greeting 1" }));
  expect(screen.getByLabelText("Greeting 1")).toHaveValue("Well met.");
  expect(screen.queryByLabelText("Greeting 2")).not.toBeInTheDocument();
});

test("leaving with unsaved edits asks first", async () => {
  const ask = vi.spyOn(window, "confirm").mockReturnValue(false);
  const router = renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), "!");
  await userEvent.click(screen.getByRole("link", { name: "All history" }));
  await waitFor(() => expect(ask).toHaveBeenCalled());
  expect(router.state.location.pathname).toBe("/cards/7");
  ask.mockRestore();
});

test("after a stale save, compare and keep my version", async () => {
  conflict = true;
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), " Old.");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("changed elsewhere");
  await userEvent.click(screen.getByRole("button", { name: "Compare with the latest" }));
  const changes = await screen.findByRole("region", { name: "Changed elsewhere" });
  expect(within(changes).getByText("A cheerful dwarf.")).toBeInTheDocument();
  expect(within(changes).getByText("A grumpy dwarf. Old.")).toBeInTheDocument();
  await userEvent.click(within(changes).getByRole("button", { name: "Keep my version" }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(2));
  expect(puts[1]).toMatchObject({ body: { base_revision_id: 12, data: { description: "A grumpy dwarf. Old." } } });
});

test("after a stale save, reload drops my edits", async () => {
  conflict = true;
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), " Old.");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await userEvent.click(await screen.findByRole("button", { name: "Reload and lose my edits" }));
  await waitFor(() => expect(screen.getByLabelText("Description")).toHaveValue("A cheerful dwarf."));
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
});

test("the panel shows sync, lorebooks, collections, and sources", async () => {
  renderAt("/cards/7");
  expect(await screen.findByText("Lumiverse: ahead")).toBeInTheDocument();
  expect(await screen.findByRole("link", { name: "Forge" })).toHaveAttribute("href", "/lorebooks/3");
  expect(await screen.findByRole("checkbox", { name: "Favorites" })).toBeChecked();
  expect(screen.getByRole("checkbox", { name: "Pirates" })).not.toBeChecked();
  expect(await screen.findByRole("link", { name: "chub: dg/brakka" })).toHaveAttribute("href", "https://chub.ai/characters/dg/brakka");
});

test("tags and collections save as they change", async () => {
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Add tag"), "smith{Enter}");
  await waitFor(() => expect(puts).toContainEqual({ path: "/api/cards/7/tags", body: { tags: ["dwarf", "smith"] } }));
  await userEvent.click(await screen.findByRole("checkbox", { name: "Pirates" }));
  await waitFor(() => expect(puts.map((p) => p.path)).toContain("/api/collections/2/cards/7"));
});
