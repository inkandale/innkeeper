import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import type { components } from "../api/schema";

export type Job = components["schemas"]["JobOut"];

export const finished = (j: Job) => j.status === "succeeded" || j.status === "failed";

/** kindLabel is a job's kind, with the action for a bulk job. */
export function kindLabel(j: Job): string {
  const action = (j.payload as { action?: unknown } | null)?.action;
  return j.kind === "bulk" && typeof action === "string" ? `bulk ${action}` : j.kind;
}

/** progressText is "done of total" from a bulk job's children or a job's progress, or "". */
export function progressText(j: Job): string {
  if (j.children) {
    const counts = Object.values(j.children);
    const total = counts.reduce((a, b) => a + b, 0);
    return `${(j.children.succeeded ?? 0) + (j.children.failed ?? 0)} of ${total}`;
  }
  const p = j.progress as { done?: unknown; total?: unknown } | null;
  return typeof p?.done === "number" && typeof p.total === "number" ? `${p.done} of ${p.total}` : "";
}

/** age is how long ago t was: 30s, 45m, 10h, 4d. */
export function age(t: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - Date.parse(t)) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** useJobEvents keeps the ["job", id] query current from the job's event stream while live. */
export function useJobEvents(id: number, live: boolean) {
  const qc = useQueryClient();
  useEffect(() => {
    if (!live) return;
    const es = new EventSource(`/api/jobs/${id}/events`);
    es.addEventListener("job", (e) => {
      const j = JSON.parse((e as MessageEvent<string>).data) as Job;
      qc.setQueryData(["job", id], j);
      if (finished(j)) {
        es.close();
        // Streams cut a reconcile's snapshot, so fetch the whole job once it's done.
        void qc.invalidateQueries({ queryKey: ["job", id] });
        void qc.invalidateQueries({ queryKey: ["jobs"] });
        // A finished bulk job can change what any search shows.
        void qc.invalidateQueries({ queryKey: ["search"] });
      }
    });
    return () => es.close();
  }, [id, live, qc]);
}
