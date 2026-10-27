import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";
import { useJob } from "../settings/jobUtil";

declare module "@tanstack/react-query" {
  interface Register {
    // toast: false is for a mutation that shows its own error.
    mutationMeta: { toast?: false };
  }
}

export type ToastSpec = {
  text: string;
  link?: { to: string; label: string };
  /** action is a button, like Undo, that runs once and dismisses the toast. */
  action?: { label: string; run: () => void };
  error?: boolean;
  /** job is a job the toast follows: it shows text while the job runs, then done or the job's error. */
  job?: { id: number; done: string };
};
type Toast = ToastSpec & { id: number };

/** JobText is a job toast's text as the job goes; it dismisses the toast a while after the job succeeds. */
function JobText({ id, text, job, dismiss }: { id: number; text: string; job: { id: number; done: string }; dismiss: (id: number) => void }) {
  const j = useJob(job.id).data;
  const ok = j?.status === "succeeded";
  useEffect(() => {
    if (!ok) return;
    const t = setTimeout(() => dismiss(id), 8000);
    return () => clearTimeout(t);
  }, [ok, id, dismiss]);
  if (ok) return <span>{job.done}</span>;
  if (j?.status === "failed") return <span className="error">{`${j.label} failed: ${j.error ?? "unknown error"}`}</span>;
  return <span>{text}</span>;
}

const ToastContext = createContext<(t: ToastSpec) => void>(() => {});

/** useToast returns a function that shows a toast. */
export const useToast = () => useContext(ToastContext);

let nextId = 0;

/** Toasts shows toasts for its children, and every failed mutation's error. Errors stay until dismissed. */
export function Toasts({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const dismiss = useCallback((id: number) => setList((l) => l.filter((t) => t.id !== id)), []);
  const show = useCallback(
    (t: ToastSpec) => {
      const id = ++nextId;
      setList((l) => [...l, { ...t, id }]);
      if (!t.error && !t.job) setTimeout(() => dismiss(id), 8000);
    },
    [dismiss],
  );
  const qc = useQueryClient();
  useEffect(
    () =>
      qc.getMutationCache().subscribe((e) => {
        if (e.type === "updated" && e.action.type === "error" && e.mutation.meta?.toast !== false) show({ text: e.action.error.message, error: true });
      }),
    [qc, show],
  );
  return (
    <ToastContext.Provider value={show}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className={t.error ? "toast error" : "toast"}>
            {t.job ? <JobText id={t.id} text={t.text} job={t.job} dismiss={dismiss} /> : <span>{t.text}</span>}
            {t.link && <Link to={t.link.to}>{t.link.label}</Link>}
            {t.action && (
              <button
                className="button"
                onClick={() => {
                  t.action?.run();
                  dismiss(t.id);
                }}
              >
                {t.action.label}
              </button>
            )}
            <button className="button" aria-label="Dismiss" onClick={() => dismiss(t.id)}>
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
