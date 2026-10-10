import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Instance, InstanceForm } from "./Instance";
import type { Plugin } from "./Plugins";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const st = {
  name: "sillytavern", version: "0.1.0", capabilities: ["push", "pull"], secrets: ["password"],
  config: {
    type: "object", required: ["url"],
    properties: {
      url: { type: "string", title: "Server URL" },
      auth: { type: "string", title: "Login", enum: ["none", "basic"] },
    },
  },
};
const login = { name: "password-login", version: "0.1.0", capabilities: ["auth"], secrets: ["password_hash"] };
const broken = {
  id: 3, plugin: "sillytavern", name: "home", config: { url: "http://st", auth: "none" }, secrets: ["password"],
  broken_error: "connection refused", broken_at: now, created_at: now, updated_at: now,
};
const owned = { ...broken, id: 4, plugin: "password-login", name: "pw", config: {}, secrets: [], owner_subject: "me" };

let sent: { method: string; path: string; body: unknown }[];
beforeEach(() => {
  sent = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugins") return json([login, st]);
      if (req.method === "GET" && u.pathname === "/api/instances/3") return json(broken);
      if (req.method === "GET" && u.pathname === "/api/instances/4") return json(owned);
      const body = await req.json();
      sent.push({ method: req.method, path: u.pathname, body });
      return json({ ...broken, ...(body as object), id: 9 });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/settings/plugins", element: <p>list</p> },
      { path: "/settings/plugins/:plugin/new", element: <Instance /> },
      { path: "/settings/instances/:id", element: <Instance /> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Toasts>
        <RouterProvider router={router} />
      </Toasts>
    </QueryClientProvider>,
  );
  return router;
}

test("creates an instance from the schema form", async () => {
  const user = userEvent.setup();
  const router = renderAt("/settings/plugins/sillytavern/new");
  await user.type(await screen.findByLabelText("Name"), "home");
  await user.type(screen.getByLabelText("Server URL"), "http://st");
  await user.selectOptions(screen.getByLabelText("Login"), "basic");
  expect(screen.getByText("Not set.")).toBeInTheDocument();
  await user.type(screen.getByLabelText("password"), "pw");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(router.state.location.pathname).toBe("/settings/plugins"));
  expect(sent).toEqual([
    {
      method: "POST",
      path: "/api/instances",
      body: { plugin: "sillytavern", name: "home", config: { url: "http://st", auth: "basic" }, secrets: { password: "pw" } },
    },
  ]);
});

test("edits a broken instance and sends only what changed", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/3");
  expect(await screen.findByText(/connection refused/)).toBeInTheDocument();
  expect(screen.getByText("Set. Type to replace it.")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Remove password" }));
  const name = screen.getByLabelText("Name");
  await user.clear(name);
  await user.type(name, "home2");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0]).toEqual({
    method: "PATCH",
    path: "/api/instances/3",
    body: { name: "home2", secrets: { password: null } },
  });
});

test("a login provider edits its owner and warns about sessions", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/4");
  expect(await screen.findByText(/ends every login through this provider/)).toBeInTheDocument();
  const owner = screen.getByLabelText("Owner subject");
  expect(owner).toHaveValue("me");
  await user.clear(owner);
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0]?.body).toEqual({ name: "pw", owner_subject: "" });
});

test("a config edited back to its old value isn't sent", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/3");
  const url = await screen.findByLabelText("Server URL");
  await user.clear(url);
  await user.type(url, "http://st");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0]?.body).toEqual({ name: "home" });
});

test("InstanceForm calls onSaved instead of going to Settings", async () => {
  const user = userEvent.setup();
  const onSaved = vi.fn();
  const router = createMemoryRouter(
    [{ path: "/", element: <InstanceForm plugin={{ ...st, source: "official", builtin: false } satisfies Plugin} onSaved={onSaved} cancelTo="/welcome" /> }],
    { initialEntries: ["/"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <Toasts>
        <RouterProvider router={router} />
      </Toasts>
    </QueryClientProvider>,
  );
  await user.type(await screen.findByLabelText("Name"), "home");
  await user.type(screen.getByLabelText("Server URL"), "http://st");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(onSaved).toHaveBeenCalledWith(expect.objectContaining({ id: 9 })));
  expect(router.state.location.pathname).toBe("/");
  expect(screen.getByRole("link", { name: "Cancel" })).toHaveAttribute("href", "/welcome");
});
