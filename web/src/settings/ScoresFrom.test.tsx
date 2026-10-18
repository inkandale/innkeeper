import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ScoresFrom } from "./ScoresFrom";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

let patches: unknown[];
let instances: unknown[];
beforeEach(() => {
  patches = [];
  instances = [
    { id: 5, label: "Judge", name: "Judge", plugin: "judge" },
    { id: 6, label: "Second judge", name: "Second judge", plugin: "judge" },
  ];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/settings" && req.method === "PATCH") {
        const body = await req.json();
        patches.push(body);
        return json({ theme: "", onboarding_done: true, score_instance: body.score_instance });
      }
      if (path === "/api/settings") return json({ theme: "", onboarding_done: true, score_instance: null });
      if (path === "/api/instances") return json(instances);
      if (path === "/api/plugins") return json([{ name: "judge", capabilities: ["enrich"] }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const renderIt = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ScoresFrom />
    </QueryClientProvider>,
  );

test("picking an instance saves it, and None clears it", async () => {
  renderIt();
  const user = userEvent.setup();
  const select = await screen.findByRole("combobox", { name: "Scores from" });
  await waitFor(() => expect(select).toBeEnabled());
  await user.selectOptions(select, "Second judge");
  await waitFor(() => expect(patches).toEqual([{ score_instance: 6 }]));
  await waitFor(() => expect(select).toHaveValue("6"));
  await user.selectOptions(select, "None");
  await waitFor(() => expect(patches).toEqual([{ score_instance: 6 }, { score_instance: null }]));
});

test("with one instance, unset reads as automatic", async () => {
  instances = [{ id: 5, label: "Judge", name: "Judge", plugin: "judge" }];
  renderIt();
  expect(await screen.findByRole("option", { name: "Automatic (Judge)" })).toBeInTheDocument();
});
