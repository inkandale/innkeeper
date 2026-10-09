import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Home } from "./App";
import { nextKey } from "./api/client";

let onboardingDone: boolean;
beforeEach(() => {
  onboardingDone = true;
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify({ theme: "", onboarding_done: onboardingDone }), { headers: { "Content-Type": "application/json" } })),
  );
});
afterEach(() => {
  sessionStorage.clear();
  vi.unstubAllGlobals();
});

function renderHome() {
  const router = createMemoryRouter(
    [
      { path: "/", element: <Home /> },
      { path: "/cards", element: <p>cards page</p> },
      { path: "/sync", element: <p>sync page</p> },
      { path: "/welcome", element: <p>welcome page</p> },
    ],
    { initialEntries: ["/"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("Home goes to the cards by default", async () => {
  renderHome();
  expect(await screen.findByText("cards page")).toBeInTheDocument();
});

test("Home goes to the wizard until the owner finishes it", async () => {
  onboardingDone = false;
  sessionStorage.setItem(nextKey, "/sync");
  renderHome();
  expect(await screen.findByText("welcome page")).toBeInTheDocument();
  expect(sessionStorage.getItem(nextKey)).toBeNull();
});

test("Home goes to the saved next once", async () => {
  sessionStorage.setItem(nextKey, "/sync");
  renderHome();
  expect(await screen.findByText("sync page")).toBeInTheDocument();
  expect(sessionStorage.getItem(nextKey)).toBeNull();
});

test("Home ignores an unsafe saved next", async () => {
  sessionStorage.setItem(nextKey, "//evil.example");
  renderHome();
  expect(await screen.findByText("cards page")).toBeInTheDocument();
});
