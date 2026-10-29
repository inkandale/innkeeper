import { useQueryClient } from "@tanstack/react-query";
import { useEffect, type ReactNode } from "react";
import { Link } from "react-router";
import { toast, Toaster } from "sonner";
import { useJob, type Job } from "../settings/jobUtil";

declare module "@tanstack/react-query" {
  interface Register {
    // toast: false is for a mutation that shows its own error.
    mutationMeta: { toast?: false };
  }
}

// Successes close themselves after this long; errors stay until dismissed.
const lifetime = 8000;

/** toastError shows an error toast, which stays until dismissed. */
export const toastError = (text: string, id?: string | number) => toast.error(text, { id, duration: Infinity });

/** JobText is a running job's toast text; when the job ends it turns the toast into done or the job's error. */
function JobText({ toastId, job, text, done }: { toastId: string; job: number; text: string; done: string }) {
  const j = useJob(job).data;
  useEffect(() => {
    if (j?.status === "succeeded") toast.success(done, { id: toastId, duration: lifetime });
    if (j?.status === "failed") toastError(`${j.label} failed: ${j.error ?? "unknown error"}`, toastId);
  }, [j?.status, j?.label, j?.error, toastId, done]);
  return text;
}

/** toastJob shows a toast for a job that just started: text while it runs, then done or its error, linking to its page. */
export function toastJob(j: Job, text = `${j.label}…`, done = `${j.label}: done`) {
  const id = `job-${j.id}`;
  toast(<JobText toastId={id} job={j.id} text={text} done={done} />, {
    id,
    duration: Infinity,
    description: <Link to={`/settings/jobs/${j.id}`}>View job</Link>,
  });
}

/** Toasts shows toasts for its children, and every failed mutation's error. It must sit inside the router and query client. */
export function Toasts({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  useEffect(
    () =>
      qc.getMutationCache().subscribe((e) => {
        if (e.type === "updated" && e.action.type === "error" && e.mutation.meta?.toast !== false) toastError(e.action.error.message);
      }),
    [qc],
  );
  return (
    <>
      {children}
      <Toaster className="toaster" theme="dark" duration={lifetime} expand closeButton toastOptions={{ closeButtonAriaLabel: "Dismiss" }} />
    </>
  );
}
