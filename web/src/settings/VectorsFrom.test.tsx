import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { VectorsFrom } from "./VectorsFrom";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

let patches: unknown[];
let instances: unknown[];
let vectorsAvailable: boolean;
beforeEach(() => {
  patches = [];
  vectorsAvailable = true;
  instances = [
    { id: 5, label: "Embedder", plugin: "brain" },
    { id: 6, label: "Other", plugin: "brain" },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (url.pathname === "/api/settings" && req.method === "PATCH") {
        const body = await req.json();
        patches.push(body);
        return json({ theme: "", onboarding_done: true, vectors_available: vectorsAvailable, ...body });
      }
      if (url.pathname === "/api/settings")
        return json({ theme: "", onboarding_done: true, vector_instance: null, vectors_available: vectorsAvailable });
      if (url.pathname === "/api/instances" && url.searchParams.get("offers") === "vector.query") return json(instances);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const renderIt = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <VectorsFrom />
    </QueryClientProvider>,
  );

test("picking an instance saves it", async () => {
  renderIt();
  const user = userEvent.setup();
  const select = await screen.findByRole("combobox", { name: "Vectors from" });
  await waitFor(() => expect(select).toBeEnabled());
  await user.selectOptions(select, "Embedder");
  await waitFor(() => expect(patches).toEqual([{ vector_instance: 5 }]));
});

test("renders nothing with a single vector source, which core picks itself", async () => {
  instances = [{ id: 5, label: "Embedder", plugin: "brain" }];
  const { container } = renderIt();
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  await new Promise((r) => setTimeout(r, 50));
  expect(container).toBeEmptyDOMElement();
});

test("says pgvector is needed, with no picker, when vectors are unavailable", async () => {
  vectorsAvailable = false;
  renderIt();
  expect(await screen.findByText(/Similar cards by meaning and semantic search need pgvector/)).toBeInTheDocument();
  expect(screen.queryByRole("combobox")).toBeNull();
});
