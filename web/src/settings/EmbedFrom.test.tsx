import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { EmbedFrom } from "./EmbedFrom";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

let patches: unknown[];
let instances: unknown[];
beforeEach(() => {
  patches = [];
  instances = [{ id: 5, label: "Embedder", plugin: "brain" }];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (url.pathname === "/api/settings" && req.method === "PATCH") {
        const body = await req.json();
        patches.push(body);
        return json({ theme: "", onboarding_done: true, ...body });
      }
      if (url.pathname === "/api/settings") return json({ theme: "", onboarding_done: true, embed_instance: null });
      if (url.pathname === "/api/instances" && url.searchParams.get("offers") === "ai.embed") return json(instances);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const renderIt = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <EmbedFrom />
    </QueryClientProvider>,
  );

test("picking an instance saves it", async () => {
  renderIt();
  const user = userEvent.setup();
  const select = await screen.findByRole("combobox", { name: "Embeddings from" });
  await waitFor(() => expect(select).toBeEnabled());
  await user.selectOptions(select, "Embedder");
  await waitFor(() => expect(patches).toEqual([{ embed_instance: 5 }]));
});

test("renders nothing when no instance offers embeddings", async () => {
  instances = [];
  const { container } = renderIt();
  await waitFor(() => expect(fetch).toHaveBeenCalled());
  await new Promise((r) => setTimeout(r, 50));
  expect(container).toBeEmptyDOMElement();
});
