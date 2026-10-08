import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Setup } from "./Setup";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const form = {
  type: "object",
  properties: {
    password: { type: "string", format: "password", title: "Password" },
    confirm: { type: "string", format: "password", title: "Confirm password" },
  },
  required: ["password", "confirm"],
};

function renderSetup() {
  const router = createMemoryRouter(
    [
      { path: "/setup", element: <Setup /> },
      { path: "/", element: <p>home page</p> },
      { path: "/login", element: <p>login page</p> },
    ],
    { initialEntries: ["/setup"] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

afterEach(() => vi.unstubAllGlobals());

test("claiming sends the code and form, then goes home", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.method === "GET") return json([{ plugin: "password-login", form }]);
      expect(await req.json()).toEqual({
        code: "ABCD-EFGH",
        plugin: "password-login",
        params: { password: "correct horse", confirm: "correct horse" },
      });
      return new Response(null, { status: 204 });
    }),
  );
  renderSetup();
  await userEvent.type(await screen.findByLabelText("Setup code"), "ABCD-EFGH");
  await userEvent.type(screen.getByLabelText("Password"), "correct horse");
  await userEvent.type(screen.getByLabelText("Confirm password"), "correct horse");
  await userEvent.click(screen.getByRole("button", { name: "Claim this server" }));
  expect(await screen.findByText("home page")).toBeInTheDocument();
});

test("a refused claim shows the error", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) =>
      req.method === "GET" ? json([{ plugin: "password-login", form }]) : json({ detail: "setup failed" }, 403),
    ),
  );
  renderSetup();
  await userEvent.type(await screen.findByLabelText("Setup code"), "nope");
  await userEvent.type(screen.getByLabelText("Password"), "a");
  await userEvent.type(screen.getByLabelText("Confirm password"), "a");
  await userEvent.click(screen.getByRole("button", { name: "Claim this server" }));
  expect(await screen.findByText("setup failed")).toBeInTheDocument();
});

test("a claimed server links to login", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json({ detail: "the server is already set up" }, 404)));
  renderSetup();
  expect(await screen.findByText(/already set up/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Log in" })).toHaveAttribute("href", "/login");
});
