import { useQuery, useQueryClient, type UseQueryOptions } from "@tanstack/react-query";
import { useEffect } from "react";
import { api, must } from "../api/client";
import type { components } from "../api/schema";

export type Job = components["schemas"]["JobOut"];

export const jobStates: Job["status"][] = ["queued", "running", "waiting", "succeeded", "failed"];

export const finished = (j: Job) => j.status === "succeeded" || j.status === "failed";

/** importText is a finished import's outcome in words, such as "3 imported, 1 skipped", or "". */
export function importText(j: Job): string {
  const p = j.progress as Record<string, unknown> | null;
  if (!finished(j) || typeof p?.imported !== "number") return "";
  const n = (k: string) => (typeof p[k] === "number" ? (p[k] as number) : 0);
  const parts = [
    [n("imported"), "imported"],
    [n("revised"), "updated"],
    [n("pending"), "waiting for review"],
    [n("skipped"), "skipped"],
    [n("failed"), "failed"],
  ].filter(([c]) => (c as number) > 0);
  return parts.length ? parts.map(([c, w]) => `${c} ${w}`).join(", ") : "Nothing found";
}

/** progressText is "done of total" from a bulk job's children or a job's progress, plus any failed and skipped, or "". */
export function progressText(j: Job): string {
  if (j.children) {
    const counts = Object.values(j.children);
    const total = counts.reduce((a, b) => a + b, 0);
    return `${(j.children.succeeded ?? 0) + (j.children.failed ?? 0)} of ${total}`;
  }
  const p = j.progress as { done?: unknown; total?: unknown; failed?: unknown; skipped?: unknown } | null;
  if (typeof p?.done !== "number" || typeof p.total !== "number") return "";
  const extra = (n: unknown, label: string) => (typeof n === "number" && n > 0 ? `, ${n} ${label}` : "");
  return `${p.done} of ${p.total}${extra(p.failed, "failed")}${extra(p.skipped, "skipped")}`;
}

/** span is a length of time, short: 30s, 45m, 10h, 4d. */
export function span(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

/** age is how long ago t was, as span shows it. */
export const age = (t: string, now = Date.now()) => span(now - Date.parse(t));

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
        // A finished job, like a bulk action or a library delete, can change any search or library count.
        void qc.invalidateQueries({ queryKey: ["search"] });
        void qc.invalidateQueries({ queryKey: ["libraries"] });
      }
    });
    return () => es.close();
  }, [id, live, qc]);
}

/** useJob reads a job; with follow, the default, it tracks the job's event stream until the job finishes. */
export function useJob(id: number, { follow = true, ...opts }: { follow?: boolean } & Pick<UseQueryOptions<Job>, "enabled" | "refetchInterval"> = {}) {
  const job = useQuery({
    queryKey: ["job", id],
    queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id } } })),
    ...opts,
  });
  useJobEvents(id, follow && !!job.data && !finished(job.data));
  return job;
}
