import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Login } from "./Login";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function renderLogin(entry = "/login?next=%2Fcards") {
  const router = createMemoryRouter(
    [
      { path: "/login", element: <Login /> },
      { path: "/cards", element: <p>cards page</p> },
    ],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

let assign: ReturnType<typeof vi.fn>;
beforeEach(() => {
  assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, origin: window.location.origin, assign });
});
afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

test("a form login completes and goes to next", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      calls.push(`${req.method} ${new URL(req.url).pathname}`);
      if (req.url.endsWith("/auth/providers")) return json([{ id: 3, name: "Password", plugin: "fake" }]);
      if (req.url.endsWith("/auth/3/begin"))
        return json({ form: { type: "object", properties: { password: { type: "string" } }, required: ["password"] } });
      if (req.url.endsWith("/auth/3/complete")) {
        expect(await req.json()).toEqual({ params: { password: "hunter2" } });
        // Like the API: an empty 200 says Content-Length: 0.
        return new Response(null, { status: 200, headers: { "Content-Length": "0" } });
      }
      return json({ detail: "unexpected" }, 500);
    }),
  );
  renderLogin();
  await userEvent.type(await screen.findByLabelText("password"), "hunter2");
  await userEvent.click(screen.getByRole("button", { name: "Log in" }));
  expect(await screen.findByText("cards page")).toBeInTheDocument();
  expect(calls).toEqual(["GET /api/auth/providers", "POST /api/auth/3/begin", "POST /api/auth/3/complete"]);
});

test("a failed login shows the error", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.url.endsWith("/auth/providers")) return json([{ id: 3, name: "Password", plugin: "fake" }]);
      if (req.url.endsWith("/begin")) return json({ form: { type: "object", properties: { password: { type: "string" } } } });
      return json({ status: 401, detail: "login failed" }, 401);
    }),
  );
  renderLogin();
  await userEvent.type(await screen.findByLabelText("password"), "wrong");
  await userEvent.click(screen.getByRole("button", { name: "Log in" }));
  expect(await screen.findByText("login failed")).toBeInTheDocument();
  expect(assign).not.toHaveBeenCalled();
});

test("no providers explains what to do", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => json([])));
  renderLogin();
  expect(await screen.findByText(/No login provider is set up/)).toBeInTheDocument();
});

test("a redirect provider saves next and sends the browser away", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.url.endsWith("/auth/providers")) return json([{ id: 4, name: "SSO", plugin: "oidc" }]);
      return json({ redirect: "https://idp.example/auth?x=1" });
    }),
  );
  renderLogin();
  await vi.waitFor(() => expect(assign).toHaveBeenCalledWith("https://idp.example/auth?x=1"));
  expect(sessionStorage.getItem("innkeeper.next")).toBe("/cards");
});

test("a non-http redirect is refused", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.url.endsWith("/auth/providers")) return json([{ id: 4, name: "SSO", plugin: "oidc" }]);
      return json({ redirect: "javascript:alert(1)" });
    }),
  );
  renderLogin();
  await new Promise((r) => setTimeout(r, 50));
  expect(assign).not.toHaveBeenCalled();
});

test("a single redirect provider waits for a click without next", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      if (req.url.endsWith("/auth/providers")) return json([{ id: 4, name: "SSO", plugin: "oidc" }]);
      return json({ redirect: "https://idp.example/auth?x=1" });
    }),
  );
  renderLogin("/login");
  const button = await screen.findByRole("button", { name: "SSO" });
  await new Promise((r) => setTimeout(r, 50));
  expect(assign).not.toHaveBeenCalled();
  await userEvent.click(button);
  await vi.waitFor(() => expect(assign).toHaveBeenCalledWith("https://idp.example/auth?x=1"));
});
