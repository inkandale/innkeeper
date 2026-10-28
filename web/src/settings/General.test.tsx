import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { General } from "./General";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/settings") return json({ version: "1.2.3", theme: "", onboarding_done: true });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

test("shows Innkeeper's version beside the theme", async () => {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <General />
    </QueryClientProvider>,
  );
  expect(await screen.findByText("Innkeeper 1.2.3")).toBeInTheDocument();
  expect(screen.getByRole("group", { name: "Theme" })).toBeInTheDocument();
});
