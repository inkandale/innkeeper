import { QueryClient, QueryClientProvider, useMutation } from "@tanstack/react-query";
import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { Toasts, useToast } from "./Toasts";

const undo = vi.fn();

function Buttons() {
  const toast = useToast();
  const fail = useMutation({ mutationFn: () => Promise.reject(new Error("Refused")) });
  const quiet = useMutation({ mutationFn: () => Promise.reject(new Error("Shown inline")), meta: { toast: false } });
  return (
    <>
      <button onClick={() => fail.mutate()}>fail</button>
      <button onClick={() => quiet.mutate()}>quiet</button>
      <button onClick={() => toast({ text: "Job 5 started", link: { to: "/settings/jobs/5", label: "View job" } })}>info</button>
      <button onClick={() => toast({ text: "Boom", error: true })}>error</button>
      <button onClick={() => toast({ text: "Archived Brakka", action: { label: "Undo", run: undo } })}>archive</button>
    </>
  );
}

function renderToasts() {
  const router = createMemoryRouter([{ path: "/", element: <Toasts><Buttons /></Toasts> }]);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
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

test("a failed mutation toasts its error once, unless it opts out", async () => {
  renderToasts();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "quiet" }));
  await user.click(screen.getByRole("button", { name: "fail" }));
  expect(await screen.findAllByText("Refused")).toHaveLength(1);
  expect(screen.queryByText("Shown inline")).not.toBeInTheDocument();
});

test("a toast's action runs once and dismisses it", async () => {
  renderToasts();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "archive" }));
  await user.click(screen.getByRole("button", { name: "Undo" }));
  expect(undo).toHaveBeenCalledOnce();
  expect(screen.queryByText("Archived Brakka")).not.toBeInTheDocument();
});
