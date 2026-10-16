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
const ai = {
  name: "forge-ai", version: "0.1.0", capabilities: ["action", "records"], secrets: [],
  records: { skills: { label: "Skills", schema: { type: "object", properties: { name: { type: "string" } } } } },
};
const broken = {
  id: 3, plugin: "sillytavern", name: "home", config: { url: "http://st", auth: "none" }, secrets: ["password"],
  broken_error: "connection refused", broken_at: now, created_at: now, updated_at: now,
};
const owned = { ...broken, id: 4, plugin: "password-login", name: "pw", config: {}, secrets: [], owner_subject: "me" };
const forge = { ...broken, id: 5, plugin: "forge-ai", name: "ai", config: {}, secrets: [], broken_error: undefined, library_access: false };
const conf = {
  name: "conf", version: "0.1.0", capabilities: ["settings"], secrets: ["api_key"],
  settings_actions: { test: "Test connection" },
  config: {
    type: "object",
    properties: {
      url: { type: "string", title: "URL" },
      prompts: { type: "object", title: "Prompts", properties: { greet: { type: "string", title: "Greet", contentMediaType: "text/plain" } } },
    },
  },
};
const required = {
  name: "required", version: "0.1.0", capabilities: [], secrets: [],
  config: {
    type: "object", required: ["url"],
    properties: {
      url: { type: "string", title: "URL" },
      prompts: { type: "object", title: "Prompts", required: ["greet"], properties: { greet: { type: "string", title: "Greet" } } },
    },
  },
};
const confInst = { ...broken, id: 6, plugin: "conf", name: "c", config: { url: "http://x" }, secrets: ["api_key"], broken_error: undefined };

let sent: { method: string; path: string; body: unknown }[];
// Set to hold the conf plugin's defaults until it resolves.
let defaultsHeld: Promise<void> | undefined;
beforeEach(() => {
  sent = [];
  defaultsHeld = undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugins") return json([login, st, ai, conf, required]);
      if (req.method === "GET" && u.pathname === "/api/instances/3") return json(broken);
      if (req.method === "GET" && u.pathname === "/api/instances/4") return json(owned);
      if (req.method === "GET" && u.pathname === "/api/instances/5") return json(forge);
      if (req.method === "GET" && u.pathname === "/api/instances/5/records/skills") return json([{ id: "noir", name: "noir" }]);
      if (u.pathname === "/api/instances/3/check") {
        sent.push({ method: req.method, path: u.pathname, body: null });
        return json({ ok: false, error: "login refused" });
      }
      if (req.method === "GET" && u.pathname === "/api/instances/6") return json(confInst);
      if (u.pathname === "/api/plugins/conf/settings/defaults") {
        await defaultsHeld;
        return json({ defaults: { "/prompts/greet": "Hello" }, updated: [] });
      }
      if (u.pathname.endsWith("/settings/defaults")) return json({ defaults: {}, updated: [] });
      if (u.pathname === "/api/plugins/conf/settings/run") {
        const body = await req.json();
        sent.push({ method: req.method, path: u.pathname, body });
        return json({ ok: false, message: "The key was refused.", checks: [{ name: "chat", ok: false, latency_ms: 12 }] });
      }
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

test("checks an instance and shows why it doesn't work", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/3");
  await user.click(await screen.findByRole("button", { name: "Check" }));
  expect(await screen.findByText("Doesn't work: login refused")).toBeInTheDocument();
  expect(sent).toEqual([{ method: "POST", path: "/api/instances/3/check", body: null }]);
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

test("an action plugin's instance turns on Library access", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/5");
  const access = await screen.findByLabelText("Library access");
  expect(access).not.toBeChecked();
  await user.click(access);
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(sent).toHaveLength(1));
  expect(sent[0]?.body).toEqual({ name: "ai", library_access: true });
});

test("other plugins have no Library access setting", async () => {
  renderAt("/settings/instances/3");
  expect(await screen.findByLabelText("Name")).toBeInTheDocument();
  expect(screen.queryByLabelText("Library access")).toBeNull();
});

test("a saved instance of a records plugin lists each record type", async () => {
  renderAt("/settings/instances/5");
  expect(await screen.findByRole("heading", { name: "Skills" })).toBeInTheDocument();
  expect(await screen.findByText("noir")).toBeInTheDocument();
});

test("a new instance shows no records until it's saved", async () => {
  renderAt("/settings/plugins/forge-ai/new");
  await screen.findByLabelText("Name");
  expect(screen.queryByRole("heading", { name: "Skills" })).not.toBeInTheDocument();
});

test("an object in the config gets its own tab, and the default shows there", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  expect(await screen.findByRole("tab", { name: "General" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByLabelText("URL")).toBeVisible();
  await user.click(screen.getByRole("tab", { name: "Prompts" }));
  expect(screen.getByLabelText("Greet")).toHaveValue("Hello");
  expect(screen.getByLabelText("URL")).not.toBeVisible();
});

test("arrow keys move between the instance form's tabs", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  await user.click(await screen.findByRole("tab", { name: "General" }));
  await user.keyboard("{ArrowRight}");
  expect(screen.getByRole("tab", { name: "Prompts" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("tab", { name: "Prompts" })).toHaveFocus();
  await user.keyboard("{ArrowRight}");
  expect(screen.getByRole("tab", { name: "General" })).toHaveAttribute("aria-selected", "true");
});

test("saving an override sends the default it replaced", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  await user.click(await screen.findByRole("tab", { name: "Prompts" }));
  await user.type(screen.getByLabelText("Greet"), "!");
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(sent.some((s) => s.method === "PATCH")).toBe(true));
  const patch = sent.find((s) => s.method === "PATCH");
  expect(patch?.body).toMatchObject({
    config: { url: "http://x", prompts: { greet: "Hello!" } },
    default_bases: { "/prompts/greet": "Hello" },
  });
});

test("a settings button runs against the draft and shows each check", async () => {
  const user = userEvent.setup();
  renderAt("/settings/instances/6");
  await user.type(await screen.findByLabelText("api_key"), "nope");
  await user.click(screen.getByRole("button", { name: "Test connection" }));
  expect((await screen.findByText("The key was refused.")).closest('[role="status"]')).toHaveTextContent("12 ms");
  expect(screen.getByText(/chat/)).toHaveTextContent("12 ms");
  expect(screen.queryByText(/nope/)).not.toBeInTheDocument();
  const run = sent.find((s) => s.path === "/api/plugins/conf/settings/run");
  expect(run?.body).toEqual({ action: "test", instance: 6, config: { url: "http://x" }, secrets: { api_key: "nope" } });
});

test("a required field on a hidden tab opens that tab on save", async () => {
  const user = userEvent.setup();
  renderAt("/settings/plugins/required/new");
  await user.type(await screen.findByLabelText("Name"), "r");
  await user.type(screen.getByLabelText("URL"), "http://x");
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(screen.getByRole("tab", { name: "Prompts" })).toHaveAttribute("aria-selected", "true");
  expect(sent).toEqual([]);
});

test("with invalid fields on two tabs, save opens the first", async () => {
  const user = userEvent.setup();
  renderAt("/settings/plugins/required/new");
  await user.type(await screen.findByLabelText("Name"), "r");
  await user.click(screen.getByRole("tab", { name: "Prompts" }));
  await user.click(screen.getByRole("button", { name: "Save" }));
  expect(screen.getByRole("tab", { name: "General" })).toHaveAttribute("aria-selected", "true");
});

test("save waits for the defaults, so the bases are never skipped", async () => {
  let release!: () => void;
  defaultsHeld = new Promise((r) => (release = r));
  renderAt("/settings/instances/6");
  expect(await screen.findByRole("button", { name: "Save" })).toBeDisabled();
  release();
  await vi.waitFor(() => expect(screen.getByRole("button", { name: "Save" })).toBeEnabled());
});
