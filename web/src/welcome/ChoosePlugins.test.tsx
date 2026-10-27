import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { ChoosePlugins } from "./ChoosePlugins";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const offer = (name: string, category: string, recommended = false) => ({
  name, version: "0.1.0", versions: ["0.1.0"], protocol: 1, capabilities: [] as string[], description: `${name} plugin`, category,
  released: "2026-10-06", recommended,
});

let installs: { path: string; body: unknown }[];
let installStatus: number;
let offers: (ReturnType<typeof offer> & { title?: string })[];
let pluginsHang: boolean;
let pluginsBody: unknown[];
beforeEach(() => {
  installs = [];
  installStatus = 204;
  offers = [offer("lumiverse", "Apps"), offer("sillytavern", "Apps"), offer("card-import", "Import")];
  pluginsHang = false;
  pluginsBody = [{ name: "card-import", source: "official", builtin: false, version: "0.1.0", capabilities: ["import"], secrets: [] }];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugin-catalog")
        return json([
          {
            source: { id: "official", url: "https://example.com/index.toml" },
            plugins: offers,
          },
        ]);
      if (u.pathname === "/api/plugins" && pluginsHang) return new Promise<Response>(() => {});
      if (u.pathname === "/api/plugins")
        return json(pluginsBody);
      if (req.method === "POST") {
        installs.push({ path: u.pathname, body: await req.json() });
        return installStatus === 204
          ? new Response(null, { status: 204 })
          : json({ title: "Bad Gateway", status: installStatus, detail: "download failed" }, installStatus);
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderStep() {
  const router = createMemoryRouter(
    [
      { path: "/welcome/plugins", element: <ChoosePlugins /> },
      { path: "/welcome/setup", element: <p>setup step</p> },
    ],
    { initialEntries: ["/welcome/plugins"] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("groups the catalog and installs the chosen plugins", async () => {
  const user = userEvent.setup();
  renderStep();
  expect(await screen.findByText("Apps")).toBeInTheDocument();
  expect(screen.getByText("Import")).toBeInTheDocument();
  expect(screen.queryByText(/v0\.1\.0|2026-10-06/)).not.toBeInTheDocument();
  const installed = await screen.findByRole("checkbox", { name: "card-import" });
  await vi.waitFor(() => expect(installed).toBeChecked());
  expect(installed).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: "lumiverse" }));
  await user.click(screen.getByRole("button", { name: "Install and continue" }));
  expect(await screen.findByText("setup step")).toBeInTheDocument();
  expect(installs).toEqual([{ path: "/api/plugins/lumiverse/install", body: { source: "official", version: "0.1.0" } }]);
});

test("recommended plugins start chosen, and the rest wait under More plugins", async () => {
  offers = [
    { ...offer("lumiverse", "Apps", true), capabilities: ["push", "pull"] },
    { ...offer("sillytavern", "Apps", true), capabilities: ["push", "pull"] },
    { ...offer("card-import", "Import", true), capabilities: ["import"] },
    offer("card-lint", "Quality"),
  ];
  const user = userEvent.setup();
  renderStep();
  const lumiverse = await screen.findByRole("checkbox", { name: "lumiverse" });
  await vi.waitFor(() => expect(lumiverse).toBeEnabled());
  // Two apps that do the same job: the owner picks the one they use.
  expect(lumiverse).not.toBeChecked();
  expect(screen.getByRole("checkbox", { name: "sillytavern" })).not.toBeChecked();
  expect(screen.getByText(/Pick the ones you use/)).toBeInTheDocument();
  const lint = screen.getByRole("checkbox", { name: "card-lint" });
  expect(lint).not.toBeChecked();
  expect(lint).not.toBeVisible();
  await user.click(screen.getByText("More plugins"));
  expect(lint).toBeVisible();
  await user.click(screen.getByRole("checkbox", { name: "sillytavern" }));
  await user.click(screen.getByRole("button", { name: "Install and continue" }));
  expect(await screen.findByText("setup step")).toBeInTheDocument();
  expect(installs.map((i) => i.path)).toEqual(["/api/plugins/sillytavern/install"]);
});

test("opens by saying what Innkeeper does", async () => {
  renderStep();
  expect(await screen.findByRole("heading", { name: "Welcome to Innkeeper" })).toBeInTheDocument();
  expect(screen.getByText(/in step with the apps you chat in/)).toBeInTheDocument();
});

test("a built-in plugin shows as included, not as a choice", async () => {
  offers = [offer("url-import", "Import"), offer("card-import", "Import")];
  pluginsBody = [{ name: "url-import", title: "URL import", source: "builtin", builtin: true, version: "0.1.0", capabilities: ["import"], secrets: [] }];
  renderStep();
  expect(await screen.findByText("Included")).toBeInTheDocument();
  expect(screen.queryByRole("checkbox", { name: "url-import" })).not.toBeInTheDocument();
});

test("the Advanced category starts folded", async () => {
  offers = [offer("card-import", "Import", true), offer("chub-archive", "Advanced")];
  const user = userEvent.setup();
  renderStep();
  await user.click(await screen.findByText("More plugins"));
  const chub = screen.getByRole("checkbox", { name: "chub-archive" });
  expect(chub).not.toBeVisible();
  await user.click(screen.getByText("Advanced"));
  expect(chub).toBeVisible();
});

test("offers show the plugin's title", async () => {
  offers = [{ ...offer("sillytavern", "Apps"), title: "SillyTavern" }];
  renderStep();
  expect(await screen.findByRole("checkbox", { name: "SillyTavern" })).toBeInTheDocument();
});

test("a failed install shows its error and stays on the step", async () => {
  installStatus = 502;
  const user = userEvent.setup();
  renderStep();
  await user.click(await screen.findByRole("checkbox", { name: "lumiverse" }));
  await user.click(screen.getByRole("button", { name: "Install and continue" }));
  const error = await screen.findByRole("alert");
  expect(error).toHaveTextContent("download failed");
  expect(screen.getByRole("checkbox", { name: "lumiverse" })).toHaveAccessibleDescription("download failed");
  expect(screen.queryByText("setup step")).not.toBeInTheDocument();
});

test("nothing can be chosen until the installed plugins load", async () => {
  pluginsHang = true;
  renderStep();
  expect(await screen.findByRole("checkbox", { name: "lumiverse" })).toBeDisabled();
  expect(screen.getByRole("button", { name: "Install and continue" })).toBeDisabled();
});

test("an empty catalog says so and offers only Skip", async () => {
  offers = [];
  renderStep();
  expect(await screen.findByText("Nothing in the catalog to install. Skip to continue.")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Install and continue" })).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Skip" })).toBeEnabled();
});

test("choosing a plugin that needs another chooses that one too", async () => {
  offers = [
    { ...offer("forge-ai", "AI"), title: "Forge", needs: ["ai"] } as (typeof offers)[number],
    { ...offer("ai", "AI"), title: "AI", capabilities: ["ai"] } as (typeof offers)[number],
  ];
  const user = userEvent.setup();
  renderStep();
  expect(await screen.findByText("Needs AI")).toBeInTheDocument();
  const forge = screen.getByRole("checkbox", { name: "Forge" });
  await vi.waitFor(() => expect(forge).toBeEnabled());
  await user.click(forge);
  expect(screen.getByRole("checkbox", { name: "AI" })).toBeChecked();
  await user.click(screen.getByRole("button", { name: "Install and continue" }));
  expect(await screen.findByText("setup step")).toBeInTheDocument();
  expect(installs.map((i) => i.path).sort()).toEqual(["/api/plugins/ai/install", "/api/plugins/forge-ai/install"]);
});
