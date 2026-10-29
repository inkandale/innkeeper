import { QueryClient, QueryClientProvider, useMutation } from "@tanstack/react-query";
import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { toast } from "sonner";
import type { Job } from "../settings/jobUtil";
import { toastError, toastJob, Toasts } from "./Toasts";

const undo = vi.fn();
const job = { id: 7, kind: "target.push", label: "Push to app", status: "running", payload: {}, progress: {} } as unknown as Job;

function Buttons() {
  const fail = useMutation({ mutationFn: () => Promise.reject(new Error("Refused")) });
  const quiet = useMutation({ mutationFn: () => Promise.reject(new Error("Shown inline")), meta: { toast: false } });
  return (
    <>
      <button onClick={() => fail.mutate()}>fail</button>
      <button onClick={() => quiet.mutate()}>quiet</button>
      <button onClick={() => toast.success("Saved Brakka")}>info</button>
      <button onClick={() => toastError("Boom")}>error</button>
      <button onClick={() => toast.success("Archived Brakka", { action: { label: "Undo", onClick: undo } })}>archive</button>
      <button onClick={() => toastJob(job, "Pushing to Lumiverse…", "Pushed to Lumiverse")}>push</button>
    </>
  );
}

function renderToasts(qc = new QueryClient()) {
  const router = createMemoryRouter([{ path: "/", element: <Toasts><Buttons /></Toasts> }]);
  render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
}

test("a toast shows its text, and dismisses", async () => {
  renderToasts();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "info" }));
  expect(await screen.findByText("Saved Brakka")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Dismiss" }));
  await waitFor(() => expect(screen.queryByText("Saved Brakka")).not.toBeInTheDocument());
});

test("info toasts close themselves and errors stay", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  try {
    renderToasts();
    act(() => screen.getByRole("button", { name: "info" }).click());
    act(() => screen.getByRole("button", { name: "error" }).click());
    await act(() => vi.advanceTimersByTimeAsync(9000));
    expect(screen.queryByText("Saved Brakka")).not.toBeInTheDocument();
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

test("a job's toast links to the job, says it's under way, then how it ended", async () => {
  const qc = new QueryClient();
  qc.setQueryData(["job", 7], job);
  renderToasts(qc);
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "push" }));
  expect(await screen.findByText("Pushing to Lumiverse…")).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "View job" })).toHaveAttribute("href", "/settings/jobs/7");
  act(() => qc.setQueryData(["job", 7], { ...job, status: "succeeded" }));
  expect(await screen.findByText("Pushed to Lumiverse")).toBeInTheDocument();

  act(() => qc.setQueryData(["job", 7], { ...job, status: "running" }));
  await user.click(screen.getByRole("button", { name: "push" }));
  act(() => qc.setQueryData(["job", 7], { ...job, status: "failed", error: "Lumiverse refused the login" }));
  expect(await screen.findAllByText("Push to app failed: Lumiverse refused the login")).toHaveLength(1);
});

test("a toast's action runs once and dismisses it", async () => {
  renderToasts();
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: "archive" }));
  await user.click(await screen.findByRole("button", { name: "Undo" }));
  expect(undo).toHaveBeenCalledOnce();
  await waitFor(() => expect(screen.queryByText("Archived Brakka")).not.toBeInTheDocument());
});
