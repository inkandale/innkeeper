import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { History } from "./History";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const revisions = [
  { id: 12, message: "grumpier", source: "ui", content_hash: "b", created_at: now },
  { id: 11, message: "", source: "ui", content_hash: "a", created_at: now },
];
const diff = {
  fields: [
    {
      path: "description",
      kind: "prose",
      hunks: [
        { id: "description#0", status: "same", library: "A ", app: "A " },
        { id: "description#1", status: "conflict", library: "dwarf.", app: "grumpy dwarf." },
      ],
    },
    { path: "alternate_greetings", kind: "list", hunks: [{ id: "alternate_greetings#0", status: "app_only", app: ["Hail."] }] },
    { path: "name", kind: "scalar", hunks: [{ id: "name#0", status: "same", library: "Brakka", app: "Brakka" }] },
  ],
};

let requests: string[];
let posts: unknown[];
let staleHead: boolean;
beforeEach(() => {
  requests = [];
  posts = [];
  staleHead = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      requests.push(path);
      if (path === "/api/cards/7/revisions") return json(revisions);
      if (path.startsWith("/api/cards/7/revisions/")) return json(diff);
      if (path === "/api/cards/7/restore") {
        posts.push(await req.json());
        if (staleHead) return json({ detail: "the card changed" }, 409);
        return json({ id: 7, revision: { id: 13 } });
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter([{ path: "/cards/:id/history", element: <Toasts><History /></Toasts> }], {
    initialEntries: [entry],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("the head shows what it changed from the revision before", async () => {
  renderAt("/cards/7/history");
  expect(await screen.findByText("grumpy dwarf.")).toBeInTheDocument();
  expect(requests).toContain("/api/cards/7/revisions/11/compare/12");
  expect(screen.getByText("dwarf.").tagName).toBe("DEL");
  expect(screen.getByText("grumpy dwarf.").tagName).toBe("INS");
  expect(screen.queryByRole("heading", { name: "name" })).not.toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Restore" })).not.toBeInTheDocument();
});

test("an older revision compares with the head and can be restored", async () => {
  const router = renderAt("/cards/7/history?rev=11");
  expect(await screen.findByText("This is the first revision.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("radio", { name: "Against head" }));
  await waitFor(() => expect(requests).toContain("/api/cards/7/revisions/11/compare/12"));
  expect(router.state.location.search).toBe("?rev=11&against=head");
  await userEvent.click(screen.getByRole("button", { name: "Restore" }));
  await waitFor(() => expect(posts).toEqual([{ revision_id: 11, base_revision_id: 12, source: "ui" }]));
});

test("a restore on a stale head says so and refetches the revisions", async () => {
  staleHead = true;
  renderAt("/cards/7/history?rev=11");
  await userEvent.click(await screen.findByRole("button", { name: "Restore" }));
  expect(await screen.findByText(/changed since you opened this page/)).toBeInTheDocument();
  await waitFor(() => expect(requests.filter((r) => r === "/api/cards/7/revisions")).toHaveLength(2));
});

test("list hunks render as blocks", async () => {
  renderAt("/cards/7/history");
  await screen.findByText("grumpy dwarf.");
  expect(screen.getByRole("heading", { name: "alternate_greetings" }).nextElementSibling).toHaveClass("blocks");
  expect(screen.getByRole("heading", { name: "description" }).nextElementSibling).not.toHaveClass("blocks");
});
