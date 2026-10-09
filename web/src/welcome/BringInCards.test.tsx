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
beforeEach(() => {
  sent = [];
  blobGate = Promise.resolve();
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
        return json(job(sent.length), 202);
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

test("imports from an app that can be read", async () => {
  plugins = [plugin("lumiverse", ["push", "pull"])];
  instances = [instance(5, "lumiverse", "Lumiverse")];
  renderStep();
  await userEvent.click(await screen.findByRole("button", { name: "Import from Lumiverse" }));
  await vi.waitFor(() => expect(sent).toEqual([{ method: "POST", path: "/api/imports", body: { instance_id: 5 } }]));
  expect(await screen.findByRole("link", { name: /queued/ })).toHaveAttribute("href", "/settings/jobs/1");
});

test("uploads files to the card importer", async () => {
  plugins = [plugin("card-import", ["import"])];
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
      { method: "POST", path: "/api/imports", body: { instance_id: 6, params: { blob: "abc", name: "brakka.png" } } },
    ]),
  );
  expect(await screen.findByText(/brakka\.png/)).toBeInTheDocument();
  await vi.waitFor(() => expect(input).toBeEnabled());
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
