import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
// The next avatar PUT gets a 409.
let avatarConflict: boolean;
let cardGets: number;
let bindingStatus: string;
let pushPosts: number;
// A save waits on gate when it's set.
let gate: Promise<void> | undefined;
let puts: { path: string; body: unknown }[];
let patches: { path: string; body: unknown }[];
// Whether a card-codec instance, which writes PNG, exists.
let pngCodec: boolean;
beforeEach(() => {
  pngCodec = false;
  head = entity;
  conflict = false;
  avatarConflict = false;
  cardGets = 0;
  bindingStatus = "ahead";
  pushPosts = 0;
  gate = undefined;
  puts = [];
  patches = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method === "PATCH") {
        const body = await req.json();
        patches.push({ path, body });
        return json({ id: 1, instance_id: 2, remote_id: "r", status: bindingStatus, ahead: true, remote_state: "unchanged", ...body });
      }
      if (req.method === "PUT") {
        // Collection PUTs have no body.
        const body = await req.json().catch(() => null);
        puts.push({ path, body });
        if (path === "/api/cards/7") {
          await gate;
          if (avatarConflict && body.avatar) return json({ detail: "the card changed" }, 409);
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
      if (path === "/api/cards/7") {
        cardGets++;
        return json(head);
      }
      if (path === "/api/cards/7/revisions") return json([head.revision]);
      if (path === "/api/cards/7/bindings")
        return json([{ id: 1, instance_id: 2, remote_id: "r", status: bindingStatus, ahead: true, remote_state: "unchanged", autosync: false }]);
      if (path === "/api/cards/7/push") {
        pushPosts++;
        return json({ id: 9, kind: "target.push", status: "queued" }, 202);
      }
      if (path === "/api/cards/7/push-preview")
        return json({ replaces: true, new_copy: false, dropped_fields: [], confirm: "c", changes: [] });
      if (path === "/api/cards/7/sources")
        return json([{ id: 4, kind: "chub", ref: "dg/brakka", url: "https://chub.ai/characters/dg/brakka", linked_by: "user", created_at: now },
          { id: 5, kind: "chub", ref: "dg/evil", url: "javascript:alert(1)", linked_by: "user", created_at: now }]);
      if (path === "/api/instances")
        return json([{ id: 2, name: "Lumiverse", plugin: "lumiverse" }, ...(pngCodec ? [{ id: 5, name: "Codec", plugin: "card-codec" }] : [])]);
      if (path === "/api/plugins")
        return json([{ name: "lumiverse", capabilities: ["push"] }, { name: "card-codec", capabilities: ["codec"], formats: ["png"] }]);
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
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
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
  expect(screen.getByText("chub: dg/evil")).not.toHaveAttribute("href");
  expect(screen.queryByRole("link", { name: "chub: dg/evil" })).not.toBeInTheDocument();
});

test("tags and collections save as they change", async () => {
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Add tag"), "smith{Enter}");
  await waitFor(() => expect(puts).toContainEqual({ path: "/api/cards/7/tags", body: { tags: ["dwarf", "smith"] } }));
  await userEvent.click(await screen.findByRole("checkbox", { name: "Pirates" }));
  await waitFor(() => expect(puts.map((p) => p.path)).toContain("/api/collections/2/cards/7"));
});

test("edits typed while a save is in flight stay unsaved", async () => {
  let release = () => {};
  gate = new Promise((r) => (release = r));
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), " Old.");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await userEvent.type(screen.getByLabelText("Personality"), "Gruff.");
  release();
  expect(await screen.findByText("Saved")).toBeInTheDocument();
  expect(screen.getByLabelText("Personality")).toHaveValue("Gruff.");
  expect(screen.getByLabelText("Description")).toHaveValue("A grumpy dwarf. Old.");
  expect(screen.getAllByText("unsaved")).toHaveLength(1);
});

test("using the latest value for a clashing field drops my edit to it", async () => {
  conflict = true;
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), " Old.");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await userEvent.click(await screen.findByRole("button", { name: "Compare with the latest" }));
  const changes = await screen.findByRole("region", { name: "Changed elsewhere" });
  await userEvent.click(within(changes).getByRole("button", { name: "Use latest description" }));
  await userEvent.click(within(changes).getByRole("button", { name: "Keep my version" }));
  expect(screen.getByLabelText("Description")).toHaveValue("A cheerful dwarf.");
  expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
});

test("replacing the image saves a revision with the new avatar", async () => {
  renderAt("/cards/7");
  const file = new File(["png"], "new.png", { type: "image/png" });
  await userEvent.upload(await screen.findByLabelText("Replace image"), file);
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ path: "/api/cards/7", body: { base_revision_id: 11, avatar: "cd".repeat(32), data } });
});

test("a crop saves a revision named Crop image", async () => {
  // jsdom can't draw, so the canvas hands back a stand-in PNG.
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((done) => done(new Blob(["png"])));
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "Crop" }));
  // jsdom lacks showModal, so the dialog stays closed, with no accessible name.
  const dialog = document.querySelector<HTMLElement>('dialog[aria-label="Crop image"]')!;
  fireEvent.load(dialog.querySelector("img")!);
  await userEvent.click(within(dialog).getByRole("button", { name: "Save crop", hidden: true }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ path: "/api/cards/7", body: { avatar: "cd".repeat(32), message: "Crop image" } });
});

test("an upload saves a revision named Replace image", async () => {
  renderAt("/cards/7");
  await userEvent.upload(await screen.findByLabelText("Replace image"), new File(["png"], "new.png", { type: "image/png" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  expect(puts[0]).toMatchObject({ body: { message: "Replace image" } });
});

test("Export PNG hides when no codec instance writes PNG", async () => {
  renderAt("/cards/7");
  // The Target select needs plugins and instances, so both have loaded.
  await screen.findByRole("combobox", { name: "Target" });
  expect(screen.queryByRole("link", { name: "Export PNG" })).not.toBeInTheDocument();
});

test("Export PNG links to the PNG export when a codec instance writes PNG", async () => {
  pngCodec = true;
  renderAt("/cards/7");
  expect(await screen.findByRole("link", { name: "Export PNG" })).toHaveAttribute("href", "/api/cards/7/export?format=png");
});

test("saving after an image change goes on the avatar revision", async () => {
  renderAt("/cards/7");
  await userEvent.type(await screen.findByLabelText("Description"), " Old.");
  await userEvent.upload(screen.getByLabelText("Replace image"), new File(["png"], "new.png", { type: "image/png" }));
  await waitFor(() => expect(puts).toHaveLength(1));
  await waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeEnabled());
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(puts).toHaveLength(2));
  expect(puts[1]).toMatchObject({ body: { base_revision_id: 12, data: { description: "A grumpy dwarf. Old." } } });
});

test("an image change that hits a conflict says so and refetches the card", async () => {
  avatarConflict = true;
  renderAt("/cards/7");
  await userEvent.upload(await screen.findByLabelText("Replace image"), new File(["png"], "new.png", { type: "image/png" }));
  expect(await screen.findByText("This card changed elsewhere. Try again.")).toBeInTheDocument();
  await waitFor(() => expect(cardGets).toBe(2));
});

test.each(["ahead", "missing"])("a %s target links to the changes queue", async (status) => {
  bindingStatus = status;
  renderAt("/cards/7");
  const link = await screen.findByRole("link", { name: "Changes" });
  expect(link).toHaveAttribute("href", "/sync/changes?instance_id=2");
});

// The queue lists drift only after a sync reads the app, so a drifted target opens its review.
test.each(["diverged", "remote_changed"])("a %s target links to its review", async (status) => {
  bindingStatus = status;
  renderAt("/cards/7");
  const link = await screen.findByRole("link", { name: "Review" });
  expect(link).toHaveAttribute("href", "/sync/2/items/r?card=7");
  expect(screen.queryByRole("link", { name: "Changes" })).not.toBeInTheDocument();
});

test("an in-sync target has no changes link", async () => {
  bindingStatus = "in_sync";
  renderAt("/cards/7");
  await screen.findByRole("checkbox", { name: "Autosync to Lumiverse" });
  expect(screen.queryByRole("link", { name: "Changes" })).not.toBeInTheDocument();
});

test("the autosync box turns autosync on for that app", async () => {
  renderAt("/cards/7");
  const box = await screen.findByRole("checkbox", { name: "Autosync to Lumiverse" });
  expect(box).not.toBeChecked();
  await userEvent.click(box);
  await waitFor(() => expect(patches).toEqual([{ path: "/api/bindings/1", body: { autosync: true } }]));
});

test("Push previews before pushing", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "Push" }));
  expect(await screen.findByText("The app's copy already matches.")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Confirm push" })).toBeInTheDocument();
  expect(pushPosts).toBe(0);
});
