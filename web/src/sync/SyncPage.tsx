import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, must } from "../api/client";
import { age } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { stateCounts } from "./sync";
import "./Sync.css";

/** Sync lists each target app with how its cards stand, and runs reconciles and pushes. */
export function Sync() {
  const navigate = useNavigate();
  const toast = useToast();
  // The instance whose Push all ahead waits for a second click.
  const [arming, setArming] = useState<number>();
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
  const caps = new Map((plugins.data ?? []).map((p) => [p.name, p.capabilities ?? []]));
  const targets = (instances.data ?? []).filter((i) => caps.get(i.plugin)?.some((c) => c === "push" || c === "pull"));

  const reconcile = useMutation({
    mutationFn: async (instance: number) => must(await api.POST("/reconciles", { body: { instance_id: instance } })),
    onSuccess: (j) => navigate(`/sync/reconciles/${j.id}`),
    onError: (e) => toast({ text: e.message, error: true }),
  });
  const pushAhead = useMutation({
    mutationFn: async (instance: number) =>
      must(await api.POST("/pushes", { body: { instance_id: instance, query: { filter: { target_state: { instance, is: "ahead" } } } } })),
    onSuccess: (j) => {
      setArming(undefined);
      toast({ text: `Job ${j.id} started`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } });
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });

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
        <table>
          <thead>
            <tr>
              <th>Target</th>
              <th>Plugin</th>
              <th>In sync</th>
              <th>Ahead</th>
              <th>Drifted</th>
              <th>Can't check</th>
              <th>Last reconcile</th>
              <th>
                <span className="visually-hidden">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {targets.map((i) => {
              const c = stateCounts(facets.data?.facets?.targets, i.id);
              const last = runs.data?.find((j) => j.instance_id === i.id);
              const can = caps.get(i.plugin) ?? [];
              return (
                <tr key={i.id}>
                  <td>{i.name}</td>
                  <td>{i.plugin}</td>
                  <td>{c.in_sync}</td>
                  <td>{c.ahead}</td>
                  <td>{c.drifted + c.missing}</td>
                  <td>{c.cant_check}</td>
                  <td>
                    {last ? (
                      <Link to={`/sync/reconciles/${last.id}`}>
                        {age(last.created_at)} ago{last.status !== "succeeded" && ` (${last.status})`}
                      </Link>
                    ) : (
                      "Never"
                    )}
                  </td>
                  <td className="row">
                    {can.includes("pull") && (
                      <button className="button" disabled={reconcile.isPending} onClick={() => reconcile.mutate(i.id)}>
                        Reconcile
                      </button>
                    )}
                    {can.includes("push") && c.ahead > 0 &&
                      (arming === i.id ? (
                        <>
                          <button className="button primary" disabled={pushAhead.isPending} onClick={() => pushAhead.mutate(i.id)}>
                            Push {c.ahead} ahead card{c.ahead === 1 ? "" : "s"}
                          </button>
                          <button className="button" onClick={() => setArming(undefined)}>
                            Cancel
                          </button>
                        </>
                      ) : (
                        <button className="button" onClick={() => setArming(i.id)}>
                          Push all ahead
                        </button>
                      ))}
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
