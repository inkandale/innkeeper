import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Access } from "./Access";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const now = new Date().toISOString();
let sent: string[];
beforeEach(() => {
  sent = [];
  vi.stubGlobal("confirm", () => true);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (req.method !== "GET") sent.push(`${req.method} ${u.pathname} ${req.method === "POST" ? await req.text() : ""}`.trim());
      if (u.pathname === "/api/auth/me") return json({ tier: "write" });
      if (req.method === "POST" && u.pathname === "/api/tokens")
        return json({ id: 2, name: "ci", tier: "write", created_at: now, secret: "ik_secret" }, 201);
      if (u.pathname === "/api/tokens") return json([{ id: 1, name: "laptop", tier: "read", created_at: now }]);
      if (u.pathname === "/api/grants")
        return json([{ id: 7, client_name: "Claude", tier: "write", created_at: now, last_used_at: now }]);
      return new Response(null, { status: 204 });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderAccess() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Access />
    </QueryClientProvider>,
  );
}

test("lists tokens and grants, and revokes them", async () => {
  const user = userEvent.setup();
  renderAccess();
  expect(await screen.findByText("laptop")).toBeInTheDocument();
  expect(await screen.findByText("Claude")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Revoke laptop" }));
  await user.click(screen.getByRole("button", { name: "Revoke Claude" }));
  expect(sent).toEqual(["DELETE /api/tokens/1", "DELETE /api/grants/7"]);
});

test("creates a token up to the caller's tier and shows its secret once", async () => {
  const user = userEvent.setup();
  renderAccess();
  const tier = await screen.findByLabelText("Tier");
  await vi.waitFor(() => expect([...(tier as HTMLSelectElement).options].map((o) => o.value)).toEqual(["read", "write"]));
  await user.type(screen.getByLabelText("Token name"), "ci");
  await user.selectOptions(tier, "write");
  await user.click(screen.getByRole("button", { name: "Create" }));
  expect(await screen.findByText("ik_secret")).toBeInTheDocument();
  expect(sent).toEqual(['POST /api/tokens {"name":"ci","tier":"write"}']);
  await user.click(screen.getByRole("button", { name: "Done" }));
  expect(screen.queryByText("ik_secret")).not.toBeInTheDocument();
});
