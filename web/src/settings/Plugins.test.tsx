import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { Plugins } from "./Plugins";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
const inst = (id: number, plugin: string, name: string, broken?: string, status?: string) => ({
  id, plugin, name, label: name, config: {}, secrets: [], created_at: now, updated_at: now,
  disabled: status === "disabled", status: status ?? (broken ? "broken" : "ready"),
  ...(broken ? { broken_error: broken, broken_at: now } : {}),
});

let requests: string[];
let bodies: string[];
beforeEach(() => {
  requests = [];
  bodies = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      requests.push(`${req.method} ${u.pathname}`);
      bodies.push(await req.clone().text());
      if (u.pathname === "/api/plugins")
        return json([
          { name: "dusk", version: "1.0.0", capabilities: ["theme"], secrets: [], source: "builtin", builtin: true,
            multiple_instances: false, status: "ready" },
          {
            name: "sillytavern", version: "0.1.0", capabilities: ["push", "pull"], secrets: ["password"],
            source: "official", builtin: false, previous_source: "official", previous_version: "0.0.9",
            category: "Apps", description: "Pushes cards to SillyTavern.", multiple_instances: true, status: "broken",
          },
          { name: "card-codec", version: "0.2.0", capabilities: ["codec"], secrets: [], source: "builtin", builtin: true,
            category: "Formats", multiple_instances: false, status: "needs_setup" },
          { name: "solo-app", version: "0.1.0", capabilities: ["push"], secrets: [], source: "official", builtin: false,
            category: "Apps", multiple_instances: true, status: "ready" },
          { name: "fresh-app", version: "0.1.0", capabilities: ["push"], secrets: [], source: "official", builtin: false,
            category: "Apps", multiple_instances: true, status: "needs_setup" },
          { name: "paused", version: "0.1.0", capabilities: ["push"], secrets: [], source: "official", builtin: false,
            category: "Apps", multiple_instances: true, status: "disabled" },
          { name: "url-import", version: "0.3.0", capabilities: ["import"], secrets: [], source: "builtin", builtin: true,
            config: { type: "object", properties: { delay: { type: "integer" } } },
            category: "Import", multiple_instances: false, status: "ready" },
          { name: "pnglike", version: "0.4.0", capabilities: ["codec"], secrets: [], source: "builtin", builtin: true,
            category: "Formats", multiple_instances: false, status: "ready" },
        ]);
      if (u.pathname === "/api/plugin-sources") return json([{ id: "official", url: "https://example.com/index.toml" }]);
      if (u.pathname === "/api/plugin-catalog")
        return json([
          {
            source: { id: "official", url: "https://example.com/index.toml" },
            plugins: [
              { name: "sillytavern", version: "0.2.0", versions: ["0.2.0", "0.1.0"], protocol: 1, capabilities: [], description: "ST" },
              { name: "card-import", version: "0.1.0", versions: ["0.1.0"], protocol: 1, capabilities: [], description: "Import cards.", recommended: true },
            ],
          },
          { source: { id: "broken", url: "https://example.org/index.toml" }, plugins: [], error: "plugin source: 404" },
        ]);
      if (req.method === "POST") return new Response(null, { status: req.url.endsWith("/plugin-sources") ? 201 : 204 });
      if (u.pathname === "/api/instances")
        return json([
          inst(1, "sillytavern", "home"), inst(2, "sillytavern", "work", "connection refused"),
          inst(3, "gone", "old"), inst(4, "url-import", "url-import"), inst(5, "pnglike", "pnglike"),
          inst(6, "solo-app", "office"), inst(7, "paused", "napping", undefined, "disabled"),
        ]);
      if (req.method === "DELETE" && u.pathname === "/api/instances/1")
        return json({ title: "Conflict", status: 409, detail: "instance in use" }, 409);
      if (req.method === "DELETE") return new Response(null, { status: 204 });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPlugins() {
  const router = createMemoryRouter([
    { path: "/settings/plugins", element: <Toasts><Plugins /></Toasts> },
    { path: "/settings/plugins/:plugin/new", element: <p>new instance form</p> },
  ], {
    initialEntries: ["/settings/plugins"],
  });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

const confirmWith = (label: string) => userEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: label }));

const menuOf = (article: HTMLElement, name: string) =>
  document.getElementById(within(article).getByRole("button", { name: `More for ${name}` }).getAttribute("popovertarget")!)!;

test("lists plugins with their instances and status", async () => {
  const user = userEvent.setup();
  renderPlugins();
  const st = (await screen.findByRole("heading", { name: "sillytavern" })).closest("article")!;
  expect(within(st).getByRole("link", { name: "home" })).toHaveAttribute("href", "/settings/instances/1");
  expect(within(st).getByText("connection refused")).toBeInTheDocument();
  expect(within(st).queryByRole("link", { name: "Add instance" })).not.toBeInTheDocument();
  const dusk = screen.getByRole("heading", { name: "dusk" }).closest("article")!;
  expect(within(dusk).queryByRole("link", { name: "Add instance" })).not.toBeInTheDocument();
  expect(within(dusk).getByRole("link", { name: "Select in Appearance" })).toBeInTheDocument();
  // An instance whose plugin was removed stays visible so it can be deleted.
  expect(screen.getByRole("link", { name: "old" })).toBeInTheDocument();
  await user.click(within(menuOf(st, "sillytavern")).getByRole("button", { name: "Add instance" }));
  expect(await screen.findByText("new instance form")).toBeInTheDocument();
});

test("delete asks, calls the API, and shows a refusal", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByRole("button", { name: "Delete instance work" }));
  await confirmWith("Delete instance");
  expect(requests).toContain("DELETE /api/instances/2");
  await user.click(screen.getByRole("button", { name: "Delete instance home" }));
  await confirmWith("Delete instance");
  expect(await screen.findByText("instance in use")).toBeInTheDocument();
});

test("checks sources only when asked, then offers updates and installs", async () => {
  const user = userEvent.setup();
  renderPlugins();
  const st = (await screen.findByRole("heading", { name: "sillytavern" })).closest("article")!;
  expect(within(st).getByText("official/0.1.0")).toBeInTheDocument();
  expect(requests).not.toContain("GET /api/plugin-catalog");

  await user.click(screen.getByRole("button", { name: "Check for updates" }));
  await user.click(await within(st).findByRole("button", { name: "Update to 0.2.0" }));
  expect(requests).toContain("POST /api/plugins/sillytavern/install");
  expect(screen.getByText("broken: plugin source: 404")).toBeInTheDocument();
  expect(screen.getByText(/official\/card-import/)).toHaveTextContent("official/card-import (recommended)");
  await user.click(screen.getByRole("button", { name: "Install official/card-import" }));
  expect(requests).toContain("POST /api/plugins/card-import/install");
});

test("rolls back, uninstalls, and removes data only after typing the name", async () => {
  const user = userEvent.setup();
  renderPlugins();
  const st = (await screen.findByRole("heading", { name: "sillytavern" })).closest("article")!;
  const menu = document.getElementById(within(st).getByRole("button", { name: "More for sillytavern" }).getAttribute("popovertarget")!)!;
  expect(within(menu).getByRole("button", { name: "Roll back to 0.0.9" })).toBeInTheDocument();
  expect(within(menu).getByRole("button", { name: "Uninstall" })).toBeInTheDocument();
  await user.click(within(st).getByRole("button", { name: "Roll back to 0.0.9" }));
  expect(requests).toContain("POST /api/plugins/sillytavern/install");
  await user.click(within(st).getByRole("button", { name: "Uninstall" }));
  await confirmWith("Uninstall");
  expect(requests).toContain("DELETE /api/plugins/sillytavern");
  // A built-in plugin has nothing to uninstall.
  const dusk = screen.getByRole("heading", { name: "dusk" }).closest("article")!;
  expect(within(dusk).queryByRole("button", { name: "Uninstall" })).not.toBeInTheDocument();

  const gone = screen.getByRole("heading", { name: "gone" }).closest("article")!;
  await user.click(within(gone).getByRole("button", { name: "Remove data" }));
  const dialog = screen.getByRole("dialog", { name: "Remove gone's data?" });
  const typed = within(dialog).getByLabelText("Type gone to confirm");
  await user.type(typed, "nope");
  expect(within(dialog).getByRole("button", { name: "Remove data" })).toBeDisabled();
  await user.clear(typed);
  await user.type(typed, "gone");
  await user.click(within(dialog).getByRole("button", { name: "Remove data" }));
  expect(requests).toContain("DELETE /api/plugins/gone/data");
});

test("adds a source", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByText("Plugin sources"));
  await user.type(screen.getByRole("textbox", { name: "Source id" }), "mine");
  await user.type(screen.getByRole("textbox", { name: "Source URL" }), "https://example.net/index.toml");
  await user.click(screen.getByRole("button", { name: "Add source" }));
  expect(requests).toContain("POST /api/plugin-sources");
});

test("recommends plugins until one is installed", async () => {
  renderPlugins();
  await screen.findByRole("heading", { name: "sillytavern" });
  expect(screen.queryByText(/comes with login/)).not.toBeInTheDocument();
});

test("recommends plugins on a fresh install", async () => {
  const fetch = globalThis.fetch;
  vi.stubGlobal("fetch", async (req: Request) =>
    new URL(req.url).pathname === "/api/plugins" ? json([]) : fetch(req),
  );
  renderPlugins();
  expect(await screen.findByText(/comes with login/)).toBeInTheDocument();
});

test("groups plugins by category, alphabetically, with Other last", async () => {
  renderPlugins();
  await screen.findByRole("heading", { name: "sillytavern" });
  const groups = screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent);
  expect(groups.filter((g) => ["Apps", "Formats", "Import", "Other"].includes(g!))).toEqual(["Apps", "Formats", "Import", "Other"]);
  expect(groups).toContain("Not installed");
});

test("a many-instance plugin with no instance offers Set up, not Add instance", async () => {
  renderPlugins();
  const fresh = (await screen.findByRole("heading", { name: "fresh-app" })).closest("article")!;
  expect(within(fresh).getByRole("link", { name: "Set up" })).toHaveAttribute("href", "/settings/plugins/fresh-app/new");
  expect(within(fresh).queryByRole("button", { name: "Add instance" })).not.toBeInTheDocument();
});

test("a singleton shows its status and one action, never Add instance", async () => {
  renderPlugins();
  const codec = (await screen.findByRole("heading", { name: "card-codec" })).closest("article")!;
  expect(within(codec).getByText("Needs setup")).toBeInTheDocument();
  expect(within(codec).getByRole("link", { name: "Set up" })).toHaveAttribute("href", "/settings/plugins/card-codec/new");
  expect(within(codec).queryByRole("link", { name: "Add instance" })).not.toBeInTheDocument();
  const url = screen.getByRole("heading", { name: "url-import" }).closest("article")!;
  expect(within(url).getByText("Ready")).toBeInTheDocument();
  expect(within(url).getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings/instances/4");
});

test("a singleton with no settings still links to its instance and can delete it", async () => {
  const user = userEvent.setup();
  renderPlugins();
  const card = (await screen.findByRole("heading", { name: "pnglike" })).closest("article")!;
  expect(within(card).getByText("0.4.0 · built in")).toBeInTheDocument();
  expect(within(card).getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings/instances/5");
  await user.click(within(card).getByRole("button", { name: "Delete instance pnglike" }));
  await confirmWith("Delete instance");
  expect(requests).toContain("DELETE /api/instances/5");
});

test("a many-instance plugin with one instance is one line", async () => {
  renderPlugins();
  const solo = (await screen.findByRole("heading", { name: "solo-app" })).closest("article")!;
  expect(solo.querySelector(".instances")).toBeNull();
  expect(within(solo).getByText("Ready")).toBeInTheDocument();
  expect(within(solo).getByRole("link", { name: "Settings" })).toHaveAttribute("href", "/settings/instances/6");
  expect(within(solo).queryByRole("link", { name: "Add instance" })).not.toBeInTheDocument();
  const menu = menuOf(solo, "solo-app");
  expect(within(menu).getByRole("button", { name: "Add instance" })).toBeInTheDocument();
  expect(within(menu).getByRole("button", { name: "Disable office" })).toBeInTheDocument();
  expect(within(menu).getByRole("button", { name: "Delete instance office" })).toBeInTheDocument();
});

test("two instances are listed, each with its own menu, and no one is Working", async () => {
  renderPlugins();
  const st = (await screen.findByRole("heading", { name: "sillytavern" })).closest("article")!;
  expect(st.querySelectorAll(".instances li")).toHaveLength(2);
  expect(within(st).getByRole("button", { name: "More for home" })).toBeInTheDocument();
  expect(within(st).getByRole("button", { name: "More for work" })).toBeInTheDocument();
  expect(screen.queryByText("Working")).toBeNull();
});

test("Disable sends the flag with Undo, and a disabled instance offers Enable", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByRole("button", { name: "Disable office" }));
  expect(requests).toContain("PATCH /api/instances/6");
  expect(bodies[requests.indexOf("PATCH /api/instances/6")]).toBe('{"disabled":true}');
  await user.click(await screen.findByRole("button", { name: "Undo" }));
  await waitFor(() =>
    expect(bodies.filter((_, i) => requests[i] === "PATCH /api/instances/6")).toEqual(['{"disabled":true}', '{"disabled":false}']),
  );
  const paused = screen.getByRole("heading", { name: "paused" }).closest("article")!;
  expect(within(paused).getByText("Disabled")).toBeInTheDocument();
  expect(within(paused).getByRole("button", { name: "Enable napping" })).toBeInTheDocument();
});

test("Delete instance asks, then calls the API", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByRole("button", { name: "Delete instance office" }));
  expect(requests.filter((r) => r.startsWith("DELETE"))).toEqual([]);
  await confirmWith("Delete instance");
  expect(requests).toContain("DELETE /api/instances/6");
});

test("Enable sends disabled false", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByRole("button", { name: "Enable napping" }));
  expect(requests).toContain("PATCH /api/instances/7");
  expect(bodies[requests.indexOf("PATCH /api/instances/7")]).toBe('{"disabled":false}');
});

test("cancelling the Delete instance confirm sends no DELETE", async () => {
  const user = userEvent.setup();
  renderPlugins();
  await user.click(await screen.findByRole("button", { name: "Delete instance office" }));
  await user.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(requests.filter((r) => r.startsWith("DELETE"))).toEqual([]);
});
