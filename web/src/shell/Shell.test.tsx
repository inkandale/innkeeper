import { render, screen } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Shell } from "./Shell";

test("the rail links every section and marks the current one", () => {
  const router = createMemoryRouter(
    [{ path: "/", element: <Shell />, children: [{ path: "cards", element: <p>cards page</p> }] }],
    { initialEntries: ["/cards"] },
  );
  render(<RouterProvider router={router} />);
  for (const name of ["Cards", "Lorebooks", "Collections", "Sync", "Settings"]) {
    expect(screen.getByRole("link", { name })).toBeInTheDocument();
  }
  expect(screen.getByRole("link", { name: "Cards" })).toHaveAttribute("aria-current", "page");
  expect(screen.getByText("cards page")).toBeInTheDocument();
});
