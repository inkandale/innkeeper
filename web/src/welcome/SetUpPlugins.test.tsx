import { StrictMode } from "react";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { SetUpPlugins } from "./SetUpPlugins";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

// The API names a plugin with no manifest title by its name.
const titled = <P extends { name: string }>(plugins: P[]) => plugins.map((p) => ({ title: p.name, ...p }));

const plugin = (name: string, capabilities: string[], config?: unknown) => ({
  name, source: "official", version: "0.1.0", capabilities, secrets: [], ...(config ? { config } : {}),
});
const st = plugin("sillytavern", ["push", "pull"], {
  type: "object", required: ["url"], properties: { url: { type: "string", title: "Server URL" } },
});
const cardImport = plugin("card-import", ["import"], { type: "object", properties: { folder: { type: "string", title: "Folder" } } });
const bare = plugin("hello", ["push"]);

let installed: unknown[];
let instances: unknown[];
let itemsStatus: number;
let sent: { method: string; path: string; body?: unknown }[];
beforeEach(() => {
  sent = [];
  instances = [];
  itemsStatus = 200;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugins") return json(titled(installed as { name: string }[]));
      if (req.method === "POST" && u.pathname === "/api/instances") {
        const body = (await req.json()) as { plugin: string };
        sent.push({ method: "POST", path: u.pathname, body });
        return json({ id: 9, config: {}, secrets: [], label: body.plugin, ...body });
      }
      if (req.method === "PATCH" && u.pathname === "/api/instances/9") {
        const body = (await req.json()) as { config?: unknown };
        sent.push({ method: "PATCH", path: u.pathname, body });
        return json({ id: 9, plugin: "sillytavern", config: body.config, secrets: [], label: "sillytavern" });
      }
      if (u.pathname === "/api/instances") return json(instances);
      if (u.pathname === "/api/instances/9/items") {
        sent.push({ method: "GET", path: u.pathname });
        return itemsStatus === 200
          ? json([])
          : json({ title: "Bad Gateway", status: itemsStatus, detail: "connection refused" }, itemsStatus);
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter(
    [
      { path: "/welcome/setup", element: <SetUpPlugins /> },
      { path: "/welcome/setup/:plugin", element: <SetUpPlugins /> },
      { path: "/welcome/cards", element: <p>cards step</p> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <StrictMode>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <Toasts>
          <RouterProvider router={router} />
        </Toasts>
      </QueryClientProvider>
    </StrictMode>,
  );
  return router;
}

async function saveSillyTavern() {
  const user = userEvent.setup();
  await user.type(await screen.findByLabelText("Server URL"), "http://st");
  await user.click(screen.getByRole("button", { name: "Save" }));
  return user;
}

test("saves a pull plugin, checks the connection, and moves on", async () => {
  installed = [st];
  renderAt("/welcome/setup");
  const user = await saveSillyTavern();
  expect(await screen.findByText("✓ Connected")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Next" }));
  expect(await screen.findByText("cards step")).toBeInTheDocument();
});

test("a failed check shows its error and can be retried", async () => {
  installed = [st];
  itemsStatus = 502;
  renderAt("/welcome/setup/sillytavern");
  const user = await saveSillyTavern();
  expect(await screen.findByText("connection refused")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Skip" })).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Retry" }));
  await vi.waitFor(() => expect(sent.filter((s) => s.path === "/api/instances/9/items")).toHaveLength(2));
});

test("a failed check can fix the saved instance's settings", async () => {
  installed = [st];
  itemsStatus = 502;
  renderAt("/welcome/setup/sillytavern");
  const user = await saveSillyTavern();
  await user.click(await screen.findByRole("button", { name: "Edit" }));
  const url = await screen.findByLabelText("Server URL");
  expect(url).toHaveValue("http://st");
  await user.clear(url);
  await user.type(url, "http://st.lan");
  itemsStatus = 200;
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByText("✓ Connected")).toBeInTheDocument();
  expect(sent.find((s) => s.method === "PATCH")?.body).toMatchObject({ config: { url: "http://st.lan" } });
});

test("a plugin with config properties gets the form", async () => {
  installed = [cardImport];
  renderAt("/welcome/setup/card-import");
  expect(await screen.findByLabelText("Folder")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Skip this plugin" })).toHaveAttribute("href", "/welcome/cards");
  expect(sent).toEqual([]);
});

test("a plugin with nothing to configure is created without a click", async () => {
  installed = [bare];
  renderAt("/welcome/setup/hello");
  expect(await screen.findByText("cards step")).toBeInTheDocument();
  expect(sent).toEqual([{ method: "POST", path: "/api/instances", body: { plugin: "hello" } }]);
});

test("with nothing to set up, moves on to bringing in cards", async () => {
  installed = [{ ...plugin("card-codec", ["codec"]), source: "builtin" }];
  renderAt("/welcome/setup");
  expect(await screen.findByText("cards step")).toBeInTheDocument();
});

test("a plugin that pulls without pushing moves on without checking an app", async () => {
  installed = [plugin("site-follower", ["pull"])];
  renderAt("/welcome/setup/site-follower");
  expect(await screen.findByText("cards step")).toBeInTheDocument();
  expect(sent).toEqual([{ method: "POST", path: "/api/instances", body: { plugin: "site-follower" } }]);
});

test("a setup step says what the plugin does", async () => {
  installed = [{ ...cardImport, description: "Bring in card files you already have." }];
  renderAt("/welcome/setup/card-import");
  expect(await screen.findByText("Bring in card files you already have.")).toBeInTheDocument();
});

test("Skip this plugin goes to the next plugin in the list, even one already set up", async () => {
  installed = [cardImport, st];
  instances = [{ id: 3, plugin: "sillytavern", label: "SillyTavern", config: {}, secrets: [] }];
  renderAt("/welcome/setup/card-import");
  expect(await screen.findByRole("link", { name: "Skip this plugin" })).toHaveAttribute("href", "/welcome/setup/sillytavern");
});
