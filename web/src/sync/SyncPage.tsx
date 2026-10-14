import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link } from "react-router";
import { api, must, withCapability } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { age, finished, type Job } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { needsYou, stateCounts } from "./sync";
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
    // A job that can't be read, such as one pruned since, stops the polling.
    refetchInterval: (q) => (q.state.status === "error" || (q.state.data && finished(q.state.data)) ? false : 2000),
  });
  const done = job.isError || (!!job.data && finished(job.data));
  useEffect(() => {
    if (!done) return;
    setId(undefined);
    for (const queryKey of [["search", "card", "sync-counts"], ["jobs", "target.reconcile"], ["sync", "changes"]])
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

/** Sync lists each target app with how its cards stand, and refreshes or syncs every app at once. */
export function Sync() {
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const facets = useQuery({
    queryKey: ["search", "card", "sync-counts"],
    queryFn: async () => must(await api.POST("/cards/search", { body: { facets: ["targets"], page: { size: 1 } } })),
  });
  const runs = useQuery({
    queryKey: ["jobs", "target.reconcile"],
    // ponytail: the newest 200 runs across all instances; an instance not run in that window shows Never.
    queryFn: async () => must(await api.GET("/jobs", { params: { query: { kind: "target.reconcile", limit: 200 } } })),
  });
  const changes = useQuery({
    queryKey: ["sync", "changes", "counts"],
    queryFn: async () => must(await api.GET("/sync/changes")),
  });
  const targets = withCapability(instances.data, plugins.data, "push", "pull");
  const cantCheck = targets.some((i) => stateCounts(facets.data?.facets?.targets, i.id).cant_check > 0);

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
      {[facets, runs, changes].map((q, n) => q.isError && <p key={n} className="error">{q.error.message}</p>)}
      {!!targets.length && (
        <>
          <p>
            <Link to="/sync/changes">Changes</Link>
          </p>
          <table>
            <thead>
              <tr>
                <th>Target</th>
                <th>In sync (cards)</th>
                <th>Needs you</th>
                <th>Not pushed</th>
                <th>Missing</th>
                {cantCheck && <th>Can't check</th>}
                <th>Last sync</th>
              </tr>
            </thead>
            <tbody>
              {targets.map((i) => {
                const c = stateCounts(facets.data?.facets?.targets, i.id);
                const last = runs.data?.find((j) => j.instance_id === i.id);
                const wait = facets.isPending;
                const n = changes.data?.counts[String(i.id)] ?? {};
                const queue = (state?: string) => `/sync/changes?instance_id=${i.id}${state ? `&state=${state}` : ""}`;
                return (
                  <tr key={i.id}>
                    <td>{i.name}</td>
                    <td>{wait ? "…" : c.in_sync}</td>
                    {changes.data ? (
                      <>
                        <td>
                          <Link to={queue()}>{needsYou(n)}</Link>
                        </td>
                        <td>
                          <Link to={queue("ahead")}>{n.ahead ?? 0}</Link>
                        </td>
                        <td>
                          <Link to={queue("missing")}>{n.missing ?? 0}</Link>
                        </td>
                      </>
                    ) : (
                      <>
                        <td>…</td>
                        <td>…</td>
                        <td>…</td>
                      </>
                    )}
                    {cantCheck && <td>{c.cant_check}</td>}
                    <td>
                      {last ? (
                        <Link to={`/sync/reconciles/${last.id}`}>
                          {age(last.created_at)} ago{last.status !== "succeeded" && ` (${last.status})`}
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
        </>
      )}
    </section>
  );
}
