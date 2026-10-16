import { useEffect, useState } from "react";
import { useJobEvents } from "../settings/jobUtil";
import type { Progress } from "./actions";
import { Hammer } from "./Hammer";
import type { Run, Waiting } from "./useForge";

// title names a run, with its field named by label.
const title = (r: { label: string; field?: string }, label: (key: string) => string) => (r.field ? `${r.label} · ${label(r.field)}` : r.label);

/** useNow is the time, updated every second while on. */
function useNow(on: boolean) {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!on) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [on]);
  return now;
}

/** RunHeader shows a run: while it runs, its progress and Stop; once it ends, how it ended, its raw response, and dismiss. */
export function RunHeader({ run, label, onStop, onDismiss }: { run: Run; label: (key: string) => string; onStop: () => void; onDismiss: () => void }) {
  useJobEvents(run.id, run.live);
  const now = useNow(run.live);
  const { job } = run;
  const end = job.finished_at ? Date.parse(job.finished_at) : now;
  const seconds = Math.max(0, Math.round((end - Date.parse(job.created_at)) / 1000));
  if (!run.live) {
    const outcome =
      job.status === "succeeded" ? `finished in ${seconds}s on ${run.instance}` : job.error === "cancelled" ? "stopped. Nothing changed." : `failed: ${job.error}`;
    return (
      <div className={job.status === "failed" ? "forge-last error" : "forge-last"}>
        <Hammer />
        <span>
          {title(run, label)} {outcome}
        </span>
        <button className="forge-x" aria-label={`Dismiss ${run.label}`} onClick={onDismiss}>
          ×
        </button>
        {run.result?.raw && (
          <details>
            <summary>View raw response</summary>
            <pre className="forge-raw">{run.result.raw}</pre>
          </details>
        )}
      </div>
    );
  }
  const p = (job.progress ?? {}) as Progress;
  return (
    <div className="forge-progress running" role="status">
      <div className="row">
        <Hammer />
        <strong>{title(run, label)}</strong>
        <span className="muted">on {run.instance}</span>
        <span className="forge-elapsed muted">{seconds}s</span>
        <button className="button" onClick={onStop}>
          Stop
        </button>
      </div>
      <p>{p.message ?? (job.status === "queued" ? "Waiting to start…" : "Working…")}</p>
      {!!p.total && <progress max={p.total} value={p.done ?? 0} />}
    </div>
  );
}

/** WaitingHeader shows a run held until the runs it overlaps finish. */
export function WaitingHeader({ w, label, onCancel }: { w: Waiting; label: (key: string) => string; onCancel: () => void }) {
  return (
    <div className="forge-progress" role="status">
      <div className="row">
        <Hammer />
        <strong>{title(w, label)}</strong>
        <span className="muted">on {w.instance}, waiting for the run before it</span>
        <button className="button forge-elapsed" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}
