import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { ChoosePlugins } from "./ChoosePlugins";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const offer = (name: string, category: string, released?: string) => ({
  name, version: "0.1.0", versions: ["0.1.0"], protocol: 1, capabilities: [], description: `${name} plugin`, category,
  ...(released ? { released } : {}),
});

let installs: { path: string; body: unknown }[];
let installStatus: number;
beforeEach(() => {
  installs = [];
  installStatus = 204;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugin-catalog")
        return json([
          {
            source: { id: "official", url: "https://example.com/index.toml" },
            plugins: [offer("lumiverse", "Apps", "2026-10-06"), offer("sillytavern", "Apps"), offer("card-import", "Import")],
          },
        ]);
      if (u.pathname === "/api/plugins")
        return json([{ name: "card-import", source: "official", builtin: false, version: "0.1.0", capabilities: ["import"], secrets: [] }]);
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
  expect(screen.getByText("v0.1.0 · 2026-10-06")).toBeInTheDocument();
  const installed = await screen.findByRole("checkbox", { name: "card-import" });
  await vi.waitFor(() => expect(installed).toBeChecked());
  expect(installed).toBeDisabled();
  await user.click(screen.getByRole("checkbox", { name: "lumiverse" }));
  await user.click(screen.getByRole("button", { name: "Install and continue" }));
  expect(await screen.findByText("setup step")).toBeInTheDocument();
  expect(installs).toEqual([{ path: "/api/plugins/lumiverse/install", body: { source: "official", version: "0.1.0" } }]);
});

test("a failed install shows its error and stays on the step", async () => {
  installStatus = 502;
  const user = userEvent.setup();
  renderStep();
  await user.click(await screen.findByRole("checkbox", { name: "lumiverse" }));
  await user.click(screen.getByRole("button", { name: "Install and continue" }));
  expect(await screen.findByText("download failed")).toBeInTheDocument();
  expect(screen.queryByText("setup step")).not.toBeInTheDocument();
});
