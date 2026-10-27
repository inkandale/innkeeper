import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Welcome } from "./Welcome";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

// The API names a plugin with no manifest title by its name.
const titled = <P extends { name: string }>(plugins: P[]) => plugins.map((p) => ({ title: p.name, ...p }));

let sent: { method: string; path: string; body: unknown }[];
beforeEach(() => {
  sent = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugins")
        return json(titled([
          { name: "lumiverse", source: "official", builtin: false, version: "1", capabilities: ["push", "pull"], secrets: [] },
          { name: "card-codec", source: "builtin", builtin: true, version: "1", capabilities: ["codec"], secrets: [] },
        ]));
      const body = await req.json();
      sent.push({ method: req.method, path: u.pathname, body });
      return json({ theme: "", ...(body as object) });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

test("the rail lists chosen plugins and Finish later ends the wizard", async () => {
  const user = userEvent.setup();
  const router = createMemoryRouter(
    [
      { path: "/welcome/*", element: <Welcome /> },
      { path: "/cards", element: <p>cards page</p> },
    ],
    { initialEntries: ["/welcome/plugins"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  const rail = screen.getByRole("navigation", { name: "Setup steps" });
  expect(await screen.findByRole("link", { name: "lumiverse" })).toHaveAttribute("href", "/welcome/setup/lumiverse");
  expect(rail).not.toHaveTextContent("card-codec");
  await user.click(screen.getByRole("button", { name: "Finish later" }));
  expect(await screen.findByText("cards page")).toBeInTheDocument();
  expect(sent).toEqual([{ method: "PATCH", path: "/api/settings", body: { onboarding_done: true } }]);
});
