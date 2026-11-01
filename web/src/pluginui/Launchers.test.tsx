import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Launchers } from "./Launchers";

const instance = { grants: [], disabled: false };
const instances = [
  { ...instance, id: 1, plugin: "advisor" },
  { ...instance, id: 2, plugin: "off", disabled: true },
  { ...instance, id: 3, plugin: "bad", broken_error: "won't start" },
  { ...instance, id: 4, plugin: "plain" },
];
const panel = (name: string) => ({ name, title: name, ui_url: `/api/plugin-ui/${name}/index.html` });
const plugins = [panel("advisor"), panel("off"), panel("bad"), { name: "plain", title: "plain" }];

beforeEach(() => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const { pathname } = new URL(req.url);
      const body = pathname === "/api/instances" ? instances : pathname === "/api/plugins" ? plugins : [];
      return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderLaunchers() {
  const router = createMemoryRouter([{ path: "*", element: <Launchers /> }], { initialEntries: ["/cards"] });
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

test("only an enabled, working ui instance gets a bubble", async () => {
  renderLaunchers();
  expect(await screen.findByRole("button", { name: "Open advisor" })).toBeInTheDocument();
  expect(screen.getAllByRole("button")).toHaveLength(1);
});

test("a bubble opens its panel, which closes back to the bubble", async () => {
  renderLaunchers();
  await userEvent.click(await screen.findByRole("button", { name: "Open advisor" }));
  expect(screen.getByTitle("advisor")).toHaveAttribute("src", "/api/plugin-ui/advisor/index.html");
  await userEvent.click(screen.getByRole("button", { name: "Close advisor" }));
  expect(screen.queryByTitle("advisor")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Open advisor" })).toBeInTheDocument();
});

test("an open panel keeps its frame across routes", async () => {
  const router = renderLaunchers();
  await userEvent.click(await screen.findByRole("button", { name: "Open advisor" }));
  const frame = screen.getByTitle("advisor");
  await act(() => router.navigate("/sync"));
  expect(screen.getByTitle("advisor")).toBe(frame);
});
