import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { NewCard } from "./NewCard";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
let posts: { path: string; body: unknown }[];
// POST /cards waits on gate when it's set, and fails when failCreate is set.
let gate: Promise<void> | undefined;
let failCreate: boolean;
beforeEach(() => {
  localStorage.clear();
  posts = [];
  gate = undefined;
  failCreate = false;
  // jsdom has no object URLs.
  vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:avatar");
  vi.spyOn(URL, "revokeObjectURL").mockReturnValue();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/blobs") {
        posts.push({ path, body: await req.text() });
        return json({ sha256: "cd".repeat(32), mime: "image/png", size: 3, store: "local", created_at: now }, 201);
      }
      if (path === "/api/cards" && req.method === "POST") {
        const body = await req.json();
        posts.push({ path, body });
        await gate;
        if (failCreate) return json({ detail: "the database is down" }, 500);
        return json(
          { id: 9, slug: "brakka", provenance: "api", created_at: now, revision: { id: 1, content_hash: "x", message: "", source: "ui", created_at: now, data: body.data } },
          201,
        );
      }
      return json([]);
    }),
  );
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

function renderNew() {
  const router = createMemoryRouter(
    [
      { path: "/cards/new", element: <Toasts><NewCard /></Toasts> },
      { path: "/cards/:id", element: <p>Card page</p> },
    ],
    { initialEntries: ["/cards/new"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("a new card's draft survives a reload", async () => {
  renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.type(screen.getByLabelText("Description"), "A grumpy dwarf.");
  cleanup();
  renderNew();
  expect(screen.getByLabelText("Name")).toHaveValue("Brakka");
  expect(screen.getByLabelText("Description")).toHaveValue("A grumpy dwarf.");
});

test("the first Save uploads the avatar, creates the card, clears the draft, and opens it", async () => {
  const router = renderNew();
  expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.upload(screen.getByLabelText("Choose image"), new File(["png"], "a.png", { type: "image/png" }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/cards/9"));
  expect(posts).toEqual([
    { path: "/api/blobs", body: "png" },
    { path: "/api/cards", body: { data: { name: "Brakka" }, avatar: "cd".repeat(32), source: "ui" } },
  ]);
  expect(localStorage.getItem("innkeeper.newCard")).toBeNull();
});

test("a crop replaces the new card's image before Save", async () => {
  // jsdom can't draw, so the canvas hands back a stand-in PNG.
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation((done) => done(new Blob(["cropped"])));
  renderNew();
  await userEvent.upload(screen.getByLabelText("Choose image"), new File(["png"], "a.png", { type: "image/png" }));
  await userEvent.click(screen.getByRole("button", { name: "Crop" }));
  // jsdom lacks showModal, so the dialog stays closed, with no accessible name.
  const dialog = document.querySelector<HTMLElement>('dialog[aria-label="Crop image"]')!;
  fireEvent.load(dialog.querySelector("img")!);
  await userEvent.click(within(dialog).getByRole("button", { name: "Save crop", hidden: true }));
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(posts[0]).toEqual({ path: "/api/blobs", body: "cropped" }));
});

test("the page works when storage is blocked", async () => {
  const blocked = () => {
    throw new Error("blocked");
  };
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(blocked);
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(blocked);
  vi.spyOn(Storage.prototype, "removeItem").mockImplementation(blocked);
  const router = renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(router.state.location.pathname).toBe("/cards/9"));
});

test("the form is read-only while the first Save runs", async () => {
  let release!: () => void;
  gate = new Promise((r) => (release = r));
  const router = renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  await waitFor(() => expect(screen.getByLabelText("Description")).toBeDisabled());
  expect(screen.getByLabelText("Name")).toBeDisabled();
  release();
  await waitFor(() => expect(router.state.location.pathname).toBe("/cards/9"));
});

test("a failed save keeps the draft and stays on the new card", async () => {
  failCreate = true;
  const router = renderNew();
  await userEvent.type(screen.getByLabelText("Name"), "Brakka");
  await userEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText("the database is down")).toBeInTheDocument();
  expect(router.state.location.pathname).toBe("/cards/new");
  expect(JSON.parse(localStorage.getItem("innkeeper.newCard")!)).toEqual({ name: "Brakka" });
  expect(screen.getByLabelText("Name")).toBeEnabled();
});
