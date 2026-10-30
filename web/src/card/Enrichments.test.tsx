import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Toasts } from "../shell/Toasts";
import { Enrichments } from "./Enrichments";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

const value = (key: string, label: string, v: unknown, stale = false) => ({
  instance_id: 5, instance_name: "Judge", revision_id: 1, key, label, type: "score", value: v, meta: {}, created_at: "2026-10-15T00:00:00Z", current: true, stale,
});

let runs: unknown[];
let refuse: boolean;
beforeEach(() => {
  runs = [];
  refuse = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/cards/7/enrichments")
        return json([
          value("quality", "Quality", 78),
          value("voice", "Voice", 40, true),
          value("contradictions", "Contradictions", ["description vs first_mes", "scenario vs mes_example"]),
          value("summary", "Summary", "A grumpy dwarf blacksmith who owes everyone in the harbor money and a favor."),
          { ...value("lint", "Lint", 100), instance_id: 6, instance_name: "Card lint" },
        ]);
      if (path === "/api/enrichments/runs") {
        if (refuse) return new Response(JSON.stringify({ detail: "Judge is offline" }), { status: 502, headers: { "Content-Type": "application/problem+json" } });
        runs.push(await req.json());
        return json({ id: 3, kind: "enrich", status: "queued" });
      }
      if (path === "/api/jobs/3") return json({ id: 3, kind: "enrich", status: "queued" });
      if (path === "/api/instances") return json([{ id: 5, label: "Judge", name: "Judge", plugin: "judge" }, { id: 6, label: "Card lint", name: "Card lint", plugin: "card-lint" }]);
      if (path === "/api/plugins") return json([{ name: "judge", capabilities: ["enrich"] }, { name: "card-lint", capabilities: ["enrich"] }]);
      return json([]);
    }),
  );
  vi.stubGlobal("EventSource", class { addEventListener() {} close() {} });
});
afterEach(() => vi.unstubAllGlobals());

test("values show with stale ones marked, and Rescore reruns the full evaluation on the card", async () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Enrichments id={7} />
    </QueryClientProvider>,
  );
  expect(await screen.findByText("78")).toBeInTheDocument();
  expect(screen.getByText("Voice").parentElement).toHaveClass("stale");
  expect(screen.getByText("(stale)")).toBeInTheDocument();
  const judge = (await screen.findByRole("heading", { name: "Judge" })).closest(".enrichment-instance") as HTMLElement;
  await userEvent.setup().click(within(judge).getByRole("button", { name: "Rescore" }));
  await waitFor(() => expect(runs).toEqual([{ instance_id: 5, card_ids: [7], rescore: true, thorough: true }]));
  expect(await screen.findByRole("button", { name: "Rescoring…" })).toBeDisabled();
});

test("a Rescore that fails to start shows its error once, beside the button", async () => {
  refuse = true;
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Toasts>
        <Enrichments id={7} />
      </Toasts>
    </QueryClientProvider>,
  );
  const judge = (await screen.findByRole("heading", { name: "Judge" })).closest(".enrichment-instance") as HTMLElement;
  await userEvent.setup().click(within(judge).getByRole("button", { name: "Rescore" }));
  expect(await screen.findByText("Judge is offline")).toBeInTheDocument();
  await new Promise((r) => setTimeout(r, 50));
  expect(screen.getAllByText("Judge is offline")).toHaveLength(1);
});

test("a list value shows as bullets and long text at full width, each instance with its own Rescore", async () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Enrichments id={7} />
    </QueryClientProvider>,
  );
  const judge = (await screen.findByRole("heading", { name: "Judge" })).closest(".enrichment-instance") as HTMLElement;
  expect(within(judge).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["description vs first_mes", "scenario vs mes_example"]);
  expect(within(judge).getByText(/grumpy dwarf/).closest(".enrichment-long")).not.toBeNull();
  expect(within(judge).getByText("78").closest(".enrichment-grid")).not.toBeNull();
  const lint = screen.getByRole("heading", { name: "Card lint" }).closest(".enrichment-instance") as HTMLElement;
  expect(within(lint).getByText("100")).toBeInTheDocument();
  expect(within(lint).getByRole("button", { name: "Rescore" })).toBeInTheDocument();
});
