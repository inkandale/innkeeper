import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Home } from "./App";
import { nextKey } from "./api/client";

function renderHome() {
  const router = createMemoryRouter(
    [
      { path: "/", element: <Home /> },
      { path: "/cards", element: <p>cards page</p> },
      { path: "/sync", element: <p>sync page</p> },
    ],
    { initialEntries: ["/"] },
  );
  render(<RouterProvider router={router} />);
}

afterEach(() => sessionStorage.clear());

test("Home goes to the cards by default", async () => {
  renderHome();
  expect(await screen.findByText("cards page")).toBeInTheDocument();
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
