import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Plugins } from "./Plugins";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const inst = (id: number, plugin: string, name: string, broken?: string) => ({
  id, plugin, name, config: {}, secrets: [], auto_bind_exact: false, created_at: now, updated_at: now,
  ...(broken ? { broken_error: broken, broken_at: now } : {}),
});

let requests: string[];
beforeEach(() => {
  requests = [];
  vi.stubGlobal("confirm", () => true);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      requests.push(`${req.method} ${u.pathname}`);
      if (u.pathname === "/api/plugins")
        return json([
          { name: "dusk", version: "1.0.0", capabilities: ["theme"], secrets: [] },
          { name: "sillytavern", version: "0.1.0", capabilities: ["push", "pull"], secrets: ["password"] },
        ]);
      if (u.pathname === "/api/instances")
        return json([inst(1, "sillytavern", "home"), inst(2, "sillytavern", "work", "connection refused"), inst(3, "gone", "old")]);
      if (req.method === "DELETE" && u.pathname === "/api/instances/1")
        return json({ title: "Conflict", status: 409, detail: "instance in use" }, 409);
      if (req.method === "DELETE") return new Response(null, { status: 204 });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPlugins() {
  const router = createMemoryRouter([{ path: "/settings/plugins", element: <Plugins /> }], {
    initialEntries: ["/settings/plugins"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("lists plugins with their instances and status", async () => {
  renderPlugins();
  const st = (await screen.findByRole("heading", { name: "sillytavern" })).closest("article")!;
  expect(within(st).getByText("push")).toBeInTheDocument();
  expect(within(st).getByRole("link", { name: "home" })).toHaveAttribute("href", "/settings/instances/1");
  expect(within(st).getByText("connection refused")).toBeInTheDocument();
  expect(within(st).getByRole("link", { name: "Add instance" })).toHaveAttribute("href", "/settings/plugins/sillytavern/new");
  const dusk = screen.getByRole("heading", { name: "dusk" }).closest("article")!;
  expect(within(dusk).queryByRole("link", { name: "Add instance" })).not.toBeInTheDocument();
  expect(within(dusk).getByRole("link", { name: "Select in Appearance" })).toBeInTheDocument();
  // An instance whose plugin was removed stays visible so it can be deleted.
  expect(screen.getByRole("link", { name: "old" })).toBeInTheDocument();
});

test("delete asks, calls the API, and shows a refusal", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByRole("button", { name: "Delete work" }));
  expect(requests).toContain("DELETE /api/instances/2");
  await user.click(screen.getByRole("button", { name: "Delete home" }));
  expect(await screen.findByText("instance in use")).toBeInTheDocument();
});
