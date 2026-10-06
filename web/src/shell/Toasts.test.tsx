import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts, useToast } from "./Toasts";

function Buttons() {
  const toast = useToast();
  return (
    <>
      <button onClick={() => toast({ text: "Job 5 started", link: { to: "/settings/jobs/5", label: "View job" } })}>info</button>
      <button onClick={() => toast({ text: "Boom", error: true })}>error</button>
    </>
  );
}

function renderToasts() {
  const router = createMemoryRouter([{ path: "/", element: <Toasts><Buttons /></Toasts> }]);
  render(<RouterProvider router={router} />);
}

test("a toast shows its text and link, and dismisses", async () => {
  renderToasts();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "info" }));
  expect(screen.getByText("Job 5 started")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/5");
  await user.click(screen.getByRole("button", { name: "Dismiss" }));
  expect(screen.queryByText("Job 5 started")).not.toBeInTheDocument();
});

test("info toasts close themselves and errors stay", () => {
  vi.useFakeTimers();
  try {
    renderToasts();
    act(() => screen.getByRole("button", { name: "info" }).click());
    act(() => screen.getByRole("button", { name: "error" }).click());
    act(() => vi.advanceTimersByTime(9000));
    expect(screen.queryByText("Job 5 started")).not.toBeInTheDocument();
    expect(screen.getByText("Boom")).toBeInTheDocument();
  } finally {
    vi.useRealTimers();
  }
});
