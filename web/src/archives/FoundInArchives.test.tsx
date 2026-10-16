import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { FoundInArchives } from "./FoundInArchives";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const hash = { content_hash: "0".repeat(64) };
let body: unknown;
let paths: string[];
beforeEach(() => {
  paths = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      paths.push(new URL(req.url).pathname);
      return json(body);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderPanel(kind: "card" | "lorebook") {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <FoundInArchives kind={kind} id={7} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("the panel lists matches by archive, links each to its item, and names archives it couldn't reach", async () => {
  body = {
    matches: [{ catalog: "chub", catalog_id: 1, items: [{ id: "chub:1", kind: "card", name: "Brakka", latest_version: "2", hashes: hash }] }],
    unreachable: [{ id: 2, name: "folder", error: "timeout" }],
  };
  renderPanel("card");
  expect(await screen.findByRole("heading", { name: "Found in archives" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "chub" })).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Brakka" })).toHaveAttribute("href", "/archives/1/items/chub%3A1");
  expect(screen.getByText("Couldn't reach folder.")).toBeInTheDocument();
  expect(paths).toEqual(["/api/cards/7/catalog-matches"]);
});

test("the panel is hidden when no archive has the entry", async () => {
  body = { matches: [], unreachable: [] };
  const { container } = renderPanel("lorebook");
  await vi.waitFor(() => expect(paths).toEqual(["/api/lorebooks/7/catalog-matches"]));
  expect(container).toBeEmptyDOMElement();
});
