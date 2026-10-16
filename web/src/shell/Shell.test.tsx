import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Shell } from "./Shell";

test("the rail links every section and marks the current one", () => {
  const router = createMemoryRouter(
    [{ path: "/", element: <Shell />, children: [{ path: "cards", element: <p>cards page</p> }] }],
    { initialEntries: ["/cards"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  for (const name of ["Cards", "Lorebooks", "Collections", "Sync", "Settings"]) {
    expect(screen.getByRole("link", { name })).toBeInTheDocument();
  }
  expect(screen.getByRole("link", { name: "Cards" })).toHaveAttribute("aria-current", "page");
  expect(screen.getByText("cards page")).toBeInTheDocument();
});

test("logout goes to /login even when the request fails", async () => {
  const assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, origin: window.location.origin, assign });
  vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("offline"))));
  const router = createMemoryRouter([{ path: "/", element: <Shell /> }]);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await userEvent.click(screen.getByRole("button", { name: "Collapse menu" }));
  await userEvent.click(screen.getByRole("button", { name: "Log out" }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("/login"));
  vi.unstubAllGlobals();
});
