import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, must } from "../api/client";
import { useInstancesWith } from "../api/queries";
import type { components } from "../api/schema";
import { enrichmentText } from "../cards/Tile";
import { finished, useJob } from "../settings/jobUtil";

type Value = components["schemas"]["EnrichmentValue"];

/** Enrichments lists every instance's values for the card, and reruns one on the head revision. */
export function Enrichments({ id }: { id: number }) {
  const qc = useQueryClient();
  const enrichers = useInstancesWith("enrich");
  const key = ["card", id, "enrichments"];
  const values = useQuery({
    queryKey: key,
    queryFn: async () => must(await api.GET("/cards/{id}/enrichments", { params: { path: { id } } })),
  });
  const [picked, setPicked] = useState<number>();
  const [jobId, setJobId] = useState<number>();
  const job = useJob(jobId ?? 0, { enabled: jobId !== undefined });
  const done = !!job.data && finished(job.data);
  useEffect(() => {
    if (done) void qc.invalidateQueries({ queryKey: ["card", id, "enrichments"] });
  }, [done, qc, id]);
  const start = useMutation({
    mutationFn: async (instance_id: number) => must(await api.POST("/enrichments/runs", { body: { instance_id, card_ids: [id] } })),
    onSuccess: (j) => setJobId(j.id),
  });

  const list = values.data ?? [];
  if (!enrichers.length && !list.length) return null;
  const instanceId = picked ?? enrichers[0]?.id;
  const running = start.isPending || (jobId !== undefined && !done);
  const groups = [...new Set(list.map((v) => v.instance_id))];
  const failed = job.data?.status === "failed" ? job.data.error : undefined;
  return (
    <section className="enrichments">
      <h2>Enrichments</h2>
      {groups.map((inst) => {
        const vals = list.filter((v) => v.instance_id === inst);
        return (
          <div key={inst}>
            {groups.length > 1 && <h3>{vals[0]?.instance_name || `Deleted instance #${inst}`}</h3>}
            <dl className="enrichment-list">
              {vals.map((v) => (
                <div key={v.key} className={v.stale ? "stale" : undefined} title={v.stale ? "Stale: from an older revision" : undefined}>
                  <dt>{v.label}</dt>
                  <dd>
                    {enrichmentText(v.value, v.range)}
                    {v.stale && <span className="muted"> (stale)</span>}
                  </dd>
                </div>
              ))}
            </dl>
          </div>
        );
      })}
      {values.data && !list.length && <p className="muted">Not evaluated yet.</p>}
      {enrichers.length > 0 && (
        <div className="row">
          {enrichers.length > 1 && (
            <select className="input" aria-label="Evaluate with" value={instanceId} onChange={(e) => setPicked(Number(e.target.value))}>
              {enrichers.map((i) => (
                <option key={i.id} value={i.id}>
                  {i.name}
                </option>
              ))}
            </select>
          )}
          <button className="button" disabled={running || instanceId === undefined} onClick={() => start.mutate(instanceId!)}>
            {running ? "Evaluating…" : "Evaluate"}
          </button>
        </div>
      )}
      {(start.error || failed) && <p className="error">{start.error?.message ?? failed}</p>}
    </section>
  );
}
