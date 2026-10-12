import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, must } from "../api/client";
import { age, finished, type Job } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { stateCounts } from "./sync";
import "./Sync.css";

/** useRun starts a job for every app and watches it until it finishes, then refreshes the page's counts. */
function useRun(start: () => Promise<Job>, text: string) {
  const qc = useQueryClient();
  const toast = useToast();
  const [id, setId] = useState<number>();
  const job = useQuery({
    queryKey: ["job", id],
    queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id: id! } } })),
    enabled: id !== undefined,
    // ponytail: polls every 2s; the job's event stream would also work but holds a connection open.
    refetchInterval: (q) => (q.state.data && finished(q.state.data) ? false : 2000),
  });
  const done = !!job.data && finished(job.data);
  useEffect(() => {
    if (!done) return;
    setId(undefined);
    for (const queryKey of [["search", "card", "sync-counts"], ["jobs", "target.reconcile"], ["jobs", "target.scan"]])
      void qc.invalidateQueries({ queryKey });
  }, [done, qc]);
  const run = useMutation({
    mutationFn: start,
    onSuccess: (j) => {
      // The response is the job, so a joined run doesn't read as finished from an older fetch.
      qc.setQueryData(["job", j.id], j);
      setId(j.id);
      toast({ text, link: { to: `/settings/jobs/${j.id}`, label: "View job" } });
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });
  return { start: () => run.mutate(), busy: run.isPending || id !== undefined };
}

/** applied is how many items the run linked, imported, or pulled on its own. */
function applied(j: Job): number | undefined {
  const t = (j.progress as { totals?: Record<string, number> } | null)?.totals;
  return t && (t.auto_bound ?? 0) + (t.auto_imported ?? 0) + (t.auto_pulled ?? 0);
}

/** Sync lists each target app with how its cards stand, and refreshes or syncs every app at once. */
export function Sync() {
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const plugins = useQuery({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });
  const facets = useQuery({
    queryKey: ["search", "card", "sync-counts"],
    queryFn: async () => must(await api.POST("/search", { body: { facets: ["targets"], page: { size: 1 } } })),
  });
  const runs = useQuery({
    queryKey: ["jobs", "target.reconcile"],
    // ponytail: the newest 200 runs across all instances; an instance not run in that window shows Never.
    queryFn: async () => must(await api.GET("/jobs", { params: { query: { kind: "target.reconcile", limit: 200 } } })),
  });
  const scans = useQuery({
    queryKey: ["jobs", "target.scan"],
    queryFn: async () => must(await api.GET("/jobs", { params: { query: { kind: "target.scan", limit: 1 } } })),
  });
  const newInApp = (scans.data?.[0]?.progress as { new_in_app?: Record<string, number> } | null)?.new_in_app ?? {};
  const caps = new Map((plugins.data ?? []).map((p) => [p.name, p.capabilities ?? []]));
  const targets = (instances.data ?? []).filter((i) => caps.get(i.plugin)?.some((c) => c === "push" || c === "pull"));

  const refresh = useRun(async () => must(await api.POST("/scans")), "Refreshing every app…");
  const sync = useRun(async () => must(await api.POST("/syncs", { body: {} })), "Syncing every app…");

  if (instances.isError) return <p className="error page">{instances.error.message}</p>;
  return (
    <section className="page sync">
      <h1>Sync</h1>
      {instances.data && plugins.data && !targets.length && (
        <p>
          No target apps yet. Add one in <Link to="/settings/plugins">Settings</Link>.
        </p>
      )}
      {!!targets.length && (
        <div className="toolbar">
          <button className="button" disabled={refresh.busy} onClick={refresh.start}>
            Refresh
          </button>
          <button className="button primary" disabled={sync.busy} onClick={sync.start}>
            Sync
          </button>
        </div>
      )}
      {[facets, runs, scans].map((q, n) => q.isError && <p key={n} className="error">{q.error.message}</p>)}
      {!!targets.length && (
        <table>
          <thead>
            <tr>
              <th>Target</th>
              <th>Plugin</th>
              <th>In sync</th>
              <th>Ahead</th>
              <th>Drifted</th>
              <th>Can't check</th>
              <th>New in app</th>
              <th>Last sync</th>
            </tr>
          </thead>
          <tbody>
            {targets.map((i) => {
              const c = stateCounts(facets.data?.facets?.targets, i.id);
              const last = runs.data?.find((j) => j.instance_id === i.id);
              const wait = facets.isPending;
              const n = last && applied(last);
              return (
                <tr key={i.id}>
                  <td>{i.name}</td>
                  <td>{i.plugin}</td>
                  <td>{wait ? "…" : c.in_sync}</td>
                  <td>{wait ? "…" : c.ahead}</td>
                  <td>{wait ? "…" : c.drifted + c.missing}</td>
                  <td>{wait ? "…" : c.cant_check}</td>
                  <td>{newInApp[String(i.id)] ?? "–"}</td>
                  <td>
                    {last ? (
                      <Link to={`/sync/reconciles/${last.id}`}>
                        {age(last.created_at)} ago{last.status !== "succeeded" && ` (${last.status})`}
                        {n !== undefined && `, applied: ${n}`}
                      </Link>
                    ) : runs.isPending ? (
                      "…"
                    ) : (
                      "Never"
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </section>
  );
}
