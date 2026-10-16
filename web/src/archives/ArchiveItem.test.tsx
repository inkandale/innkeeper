import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts } from "../shell/Toasts";
import { ArchiveItem } from "./ArchiveItem";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = "2026-10-16T00:00:00Z";
const item = { id: "sub/brakka.png", kind: "card", name: "Brakka", author: "smith", latest_version: "2", hashes: { content_hash: "0".repeat(64) } };
const versions = [{ version: "2", created: now }, { version: "1" }];
const card = (v: string) => ({ spec: "chara_card_v3", data: { name: "Brakka", description: `Forge master v${v}.`, first_mes: "Hello." } });

let gets: URL[];
let adopts: URL[];
beforeEach(() => {
  gets = [];
  adopts = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (url.pathname === "/api/catalogs/1/items/forge.json") {
        const lorebook = { ...item, id: "forge.json", kind: "lorebook", name: "Forge" };
        return json({ item: lorebook, version: "1", versions: [{ version: "1" }], data: { name: "Forge", entries: [{ keys: ["anvil"], content: "Heavy iron." }] } });
      }
      if (req.method === "POST" && url.pathname === "/api/catalogs/1/items/forge.json/adopt") {
        return json({ id: 12, slug: "forge", provenance: "catalog:1:forge.json@1", created_at: now, revision: { id: 1, content_hash: "x", message: "", source: "plugin:folder", created_at: now, data: {} } }, 201);
      }
      if (url.pathname === "/api/catalogs/1/items/sub%2Fbrakka.png") {
        gets.push(url);
        const v = url.searchParams.get("version") ?? "2";
        return json({ item, version: v, versions, data: card(v) });
      }
      if (req.method === "POST" && url.pathname === "/api/catalogs/1/items/sub%2Fbrakka.png/adopt") {
        adopts.push(url);
        return json({ id: 12, slug: "brakka", provenance: "catalog:1:sub/brakka.png@1", created_at: now, revision: { id: 1, content_hash: "x", message: "", source: "plugin:folder", created_at: now, data: {} } }, 201);
      }
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAt(entry: string) {
  const router = createMemoryRouter([{ path: "/archives/:catalog/items/:item", element: <Toasts><ArchiveItem /></Toasts> }], { initialEntries: [entry] });
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("the item view reads an escaped item ID and shows the card", async () => {
  renderAt("/archives/1/items/sub%2Fbrakka.png");
  expect(await screen.findByRole("heading", { name: "Brakka" })).toBeInTheDocument();
  expect(screen.getByText("Forge master v2.")).toBeInTheDocument();
  expect(gets[0]!.searchParams.has("version")).toBe(false);
});

test("Adopt copies the picked version and then links to the library card", async () => {
  renderAt("/archives/1/items/sub%2Fbrakka.png");
  await screen.findByText("Forge master v2.");
  await userEvent.selectOptions(screen.getByLabelText("Version"), "1");
  expect(await screen.findByText("Forge master v1.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Adopt" }));
  expect(await screen.findByRole("link", { name: "Open in library" })).toHaveAttribute("href", "/cards/12");
  expect(adopts).toHaveLength(1);
  expect(adopts[0]!.searchParams.get("version")).toBe("1");
});

test("picking another version after adopting offers Adopt again", async () => {
  renderAt("/archives/1/items/sub%2Fbrakka.png");
  await screen.findByText("Forge master v2.");
  await userEvent.click(screen.getByRole("button", { name: "Adopt" }));
  await screen.findByRole("link", { name: "Open in library" });
  await userEvent.selectOptions(screen.getByLabelText("Version"), "1");
  await waitFor(() => expect(screen.getByRole("button", { name: "Adopt" })).toBeInTheDocument());
});

test("a lorebook item lists its entries and adopts into the library's lorebooks", async () => {
  renderAt("/archives/1/items/forge.json");
  expect(await screen.findByRole("heading", { name: "1 entry" })).toBeInTheDocument();
  expect(screen.getByText("anvil")).toBeInTheDocument();
  expect(screen.getByText("Heavy iron.")).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Adopt" }));
  expect(await screen.findByRole("link", { name: "Open in library" })).toHaveAttribute("href", "/lorebooks/12");
});
