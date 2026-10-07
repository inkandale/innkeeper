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
let conflict: boolean;
let current: typeof preview;
beforeEach(() => {
  gets = 0;
  pushes = [];
  conflict = false;
  current = preview;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/cards/7/push-preview" && u.searchParams.get("instance_id") === "2") {
        gets++;
        return json(current);
      }
      if (u.pathname === "/api/cards/7/push") {
        pushes.push(await req.json());
        if (conflict) {
          conflict = false;
          return json({ detail: "the push would differ from the preview" }, 409);
        }
        return json({ id: 9, kind: "target.push", status: "queued" }, 202);
      }
      return json({}, 404);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPreview(onClose = vi.fn()) {
  const router = createMemoryRouter(
    [{ path: "/", element: <Toasts><PushPreview kind="card" id={7} instance={2} name="Lumiverse" onClose={onClose} /></Toasts> }],
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
  conflict = true;
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
