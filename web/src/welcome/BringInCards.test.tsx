import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { BringInCards } from "./BringInCards";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const plugin = (name: string, capabilities: string[]) => ({ name, source: "official", version: "0.1.0", capabilities, secrets: [] });
const instance = (id: number, plugin: string, name: string) => ({ id, plugin, name, config: {}, secrets: [] });
const job = (id: number) => ({ id, kind: "import", status: "queued", attempts: 0, max_attempts: 3, payload: {}, progress: null, created_at: "", run_after: "" });

let plugins: unknown[];
let instances: unknown[];
let sent: { method: string; path: string; body?: unknown }[];
let blobGate: Promise<void>;
let importGate: Promise<void>;
beforeEach(() => {
  sent = [];
  blobGate = Promise.resolve();
  importGate = Promise.resolve();
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugins") return json(plugins);
      if (u.pathname === "/api/instances") return json(instances);
      if (req.method === "POST" && u.pathname === "/api/blobs") {
        sent.push({ method: "POST", path: u.pathname });
        await blobGate;
        return json({ sha256: "abc" });
      }
      if (req.method === "POST" && u.pathname === "/api/imports") {
        sent.push({ method: "POST", path: u.pathname, body: await req.json() });
        await importGate;
        return json(job(sent.length), 202);
      }
      if (req.method === "POST" && u.pathname === "/api/reconciles") {
        sent.push({ method: "POST", path: u.pathname, body: await req.json() });
        return json({ ...job(sent.length), kind: "target.reconcile" }, 202);
      }
      if (req.method === "PATCH") {
        const body = await req.json();
        sent.push({ method: "PATCH", path: u.pathname, body });
        return json({ theme: "", ...(body as object) });
      }
      if (u.pathname.startsWith("/api/jobs/")) return json(job(Number(u.pathname.split("/").pop())));
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderStep() {
  const router = createMemoryRouter(
    [
      { path: "/welcome/cards", element: <BringInCards /> },
      { path: "/cards", element: <p>cards page</p> },
    ],
    { initialEntries: ["/welcome/cards"] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("syncs an app that can be read", async () => {
  plugins = [plugin("lumiverse", ["push", "pull"])];
  instances = [instance(5, "lumiverse", "Lumiverse")];
  renderStep();
  await userEvent.click(await screen.findByRole("button", { name: "Sync Lumiverse" }));
  await vi.waitFor(() => expect(sent).toEqual([{ method: "POST", path: "/api/reconciles", body: { instance_id: 5 } }]));
  expect(await screen.findByRole("link", { name: /queued/ })).toHaveAttribute("href", "/sync/reconciles/1");
});

test("uploads files to the card importer", async () => {
  plugins = [{ ...plugin("card-import", ["import"]), inputs: ["file"] }];
  instances = [instance(6, "card-import", "card-import")];
  renderStep();
  let release!: () => void;
  blobGate = new Promise((r) => (release = r));
  const file = new File(["png"], "brakka.png", { type: "image/png" });
  const input = await screen.findByLabelText("Upload files");
  await userEvent.upload(input, file);
  expect(input).toBeDisabled();
  release();
  await vi.waitFor(() =>
    expect(sent).toEqual([
      { method: "POST", path: "/api/blobs" },
      { method: "POST", path: "/api/imports", body: { instance_id: 6, library: 1, params: { blob: "abc", name: "brakka.png" } } },
    ]),
  );
  expect(await screen.findByText(/brakka\.png/)).toBeInTheDocument();
  await vi.waitFor(() => expect(input).toBeEnabled());
  expect(await screen.findByRole("link", { name: /brakka\.png/ })).toHaveAttribute("href", "/settings/jobs/2");
});

test("imports a page URL with the URL importer", async () => {
  plugins = [{ ...plugin("url-import", ["import"]), inputs: ["url"] }];
  instances = [instance(7, "url-import", "url-import")];
  renderStep();
  await userEvent.type(await screen.findByLabelText("Import from a page URL"), "https://chub.ai/characters/u/brakka");
  await userEvent.click(screen.getByRole("button", { name: "Import" }));
  await vi.waitFor(() =>
    expect(sent).toEqual([
      { method: "POST", path: "/api/imports", body: { instance_id: 7, library: 1, params: { url: "https://chub.ai/characters/u/brakka" } } },
    ]),
  );
  expect(await screen.findByRole("link", { name: /brakka/ })).toHaveAttribute("href", "/settings/jobs/1");
  expect(screen.getByLabelText("Import from a page URL")).toHaveValue("");
  expect(screen.queryByLabelText("Upload files")).not.toBeInTheDocument();
  expect(screen.queryByText(/Nothing to import from yet/)).not.toBeInTheDocument();
});

test("any importer offers the inputs its manifest declares", async () => {
  plugins = [{ ...plugin("site-grabber", ["import"]), inputs: ["file", "url"] }, plugin("mystery", ["import"])];
  instances = [instance(9, "mystery", "mystery"), instance(8, "site-grabber", "Site grabber")];
  renderStep();
  expect(await screen.findByLabelText("Upload files")).toBeInTheDocument();
  await userEvent.type(screen.getByLabelText("Import from a page URL"), "https://x.test/a");
  await userEvent.click(screen.getByRole("button", { name: "Import" }));
  await vi.waitFor(() =>
    expect(sent).toEqual([{ method: "POST", path: "/api/imports", body: { instance_id: 8, library: 1, params: { url: "https://x.test/a" } } }]),
  );
});

test("shows a page URL import while it starts and blocks a second", async () => {
  let release!: () => void;
  importGate = new Promise((r) => (release = r));
  plugins = [{ ...plugin("url-import", ["import"]), inputs: ["url"] }];
  instances = [instance(7, "url-import", "url-import")];
  renderStep();
  await userEvent.type(await screen.findByLabelText("Import from a page URL"), "https://chub.ai/characters/u/brakka");
  await userEvent.click(screen.getByRole("button", { name: "Import" }));
  expect(await screen.findByText(/brakka: uploading…/)).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Import" })).toBeDisabled();
  release();
  expect(await screen.findByRole("link", { name: /brakka/ })).toHaveAttribute("href", "/settings/jobs/1");
  await userEvent.type(screen.getByLabelText("Import from a page URL"), "https://x.test/a");
  expect(screen.getByRole("button", { name: "Import" })).toBeEnabled();
});

test("with nothing to import from, points back to choosing plugins", async () => {
  plugins = [];
  instances = [];
  renderStep();
  expect(await screen.findByText(/Nothing to import from yet/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: /Set up an app/ })).toHaveAttribute("href", "/welcome/plugins");
});

test("Finish ends the wizard", async () => {
  plugins = [];
  instances = [];
  renderStep();
  await userEvent.click(await screen.findByRole("button", { name: "Finish" }));
  expect(await screen.findByText("cards page")).toBeInTheDocument();
  expect(sent).toEqual([{ method: "PATCH", path: "/api/settings", body: { onboarding_done: true } }]);
});

test("a plugin that only follows site sources isn't offered as an app to sync", async () => {
  plugins = [{ ...plugin("url-import", ["import", "sites", "pull"]), inputs: ["url"] }];
  instances = [instance(7, "url-import", "url-import")];
  renderStep();
  expect(await screen.findByLabelText("Import from a page URL")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Sync url-import" })).not.toBeInTheDocument();
});
