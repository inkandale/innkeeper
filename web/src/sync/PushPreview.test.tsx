import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { PushPreview } from "./PushPreview";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const preview = {
  replaces: true, new_copy: false, dropped_fields: ["creator_notes"], confirm: "c1",
  changes: [{ field: "description", before: "Old words.", after: "New words." }],
};

let gets: number;
let pushes: unknown[];
let conflictFor: number | undefined;
let current: typeof preview;
beforeEach(() => {
  gets = 0;
  pushes = [];
  conflictFor = undefined;
  current = preview;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/cards/7/push-preview") {
        gets++;
        const id = u.searchParams.get("instance_id");
        return json(id === "2" ? current : { ...current, confirm: "c" + id });
      }
      if (u.pathname === "/api/cards/7/push") {
        const body = (await req.json()) as { instance_id: number };
        pushes.push(body);
        if (conflictFor === body.instance_id) {
          conflictFor = undefined;
          return json({ detail: "the push would differ from the preview" }, 409);
        }
        return json({ id: 9, kind: "target.push", status: "queued" }, 202);
      }
      return json({}, 404);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const lumiverse = { id: 2, label: "Lumiverse" };
const tavern = { id: 3, label: "SillyTavern" };

function renderPreview(onClose = vi.fn(), targets = [lumiverse]) {
  const router = createMemoryRouter(
    [{ path: "/", element: <Toasts><PushPreview kind="card" id={7} targets={targets} onClose={onClose} /></Toasts> }],
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return onClose;
}

test("shows what changes in the app and pushes with the preview's confirm", async () => {
  const onClose = renderPreview();
  expect(await screen.findByText("New words.")).toBeInTheDocument();
  expect(screen.getByText("Old words.")).toBeInTheDocument();
  expect(screen.getByText(/doesn't store creator_notes/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Confirm push" }));
  await waitFor(() => expect(onClose).toHaveBeenCalled());
  expect(pushes).toEqual([{ instance_id: 2, confirm: "c1" }]);
});

test("a push that would differ reloads the preview", async () => {
  conflictFor = 2;
  const onClose = renderPreview();
  await userEvent.click(await screen.findByRole("button", { name: "Confirm push" }));
  await waitFor(() => expect(gets).toBe(2));
  expect(onClose).not.toHaveBeenCalled();
  expect(await screen.findByText(/Check the new preview/)).toBeInTheDocument();
});

test("a copy the app can't update is pushed as new", async () => {
  current = { ...preview, new_copy: true };
  const onClose = renderPreview();
  await userEvent.click(await screen.findByRole("button", { name: "Confirm push" }));
  await waitFor(() => expect(onClose).toHaveBeenCalled());
  expect(pushes).toEqual([{ instance_id: 2, confirm: "c1", new_copy: true }]);
});

test("pushes to every target with each one's confirm", async () => {
  const onClose = renderPreview(vi.fn(), [lumiverse, tavern]);
  expect(await screen.findByRole("heading", { name: "Push to 2 apps" })).toBeInTheDocument();
  expect(screen.getByRole("region", { name: "SillyTavern" })).toBeInTheDocument();
  await waitFor(() => expect(screen.getByRole("button", { name: "Confirm push" })).toBeEnabled());
  await userEvent.click(screen.getByRole("button", { name: "Confirm push" }));
  await waitFor(() => expect(onClose).toHaveBeenCalled());
  expect(pushes).toEqual([{ instance_id: 2, confirm: "c1" }, { instance_id: 3, confirm: "c3" }]);
});

test("a target that fails keeps the dialog open, and confirming again pushes only it", async () => {
  conflictFor = 3;
  const onClose = renderPreview(vi.fn(), [lumiverse, tavern]);
  await waitFor(() => expect(screen.getByRole("button", { name: "Confirm push" })).toBeEnabled());
  await userEvent.click(screen.getByRole("button", { name: "Confirm push" }));
  expect(await screen.findByText(/Check the new preview/)).toBeInTheDocument();
  expect(screen.getByText("Pushed.")).toBeInTheDocument();
  expect(onClose).not.toHaveBeenCalled();
  await waitFor(() => expect(screen.getByRole("button", { name: "Confirm push" })).toBeEnabled());
  await userEvent.click(screen.getByRole("button", { name: "Confirm push" }));
  await waitFor(() => expect(onClose).toHaveBeenCalled());
  expect(pushes).toEqual([
    { instance_id: 2, confirm: "c1" },
    { instance_id: 3, confirm: "c3" },
    { instance_id: 3, confirm: "c3" },
  ]);
});
