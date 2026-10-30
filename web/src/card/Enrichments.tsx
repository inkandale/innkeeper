import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, must } from "../api/client";
import { useInstancesWith } from "../api/queries";
import type { components } from "../api/schema";
import { enrichmentText } from "../cards/Tile";
import { finished, useJob } from "../settings/jobUtil";

type Value = components["schemas"]["EnrichmentValue"];

// Past this length a value reads better at full width than in the score grid.
const gridLength = 40;
const isLong = (v: Value) => Array.isArray(v.value) || String(v.value).length > gridLength;

/** Enrichments shows each enrich instance's values for the card, with a Rescore per instance that still exists. */
export function Enrichments({ id }: { id: number }) {
  const enrichers = useInstancesWith("enrich");
  const values = useQuery({
    queryKey: ["card", id, "enrichments"],
    queryFn: async () => must(await api.GET("/cards/{id}/enrichments", { params: { path: { id } } })),
  });
  const list = values.data ?? [];
  if (!enrichers.length && !list.length) return null;
  const instances = [...new Set([...list.map((v) => v.instance_id), ...enrichers.map((e) => e.id)])];
  const name = (inst: number) =>
    enrichers.find((e) => e.id === inst)?.label ?? (list.find((v) => v.instance_id === inst)?.instance_name || `Deleted instance #${inst}`);
  return (
    <section>
      <h2>Enrichments</h2>
      {values.data && !list.length && <p className="muted">Not scored yet.</p>}
      {instances.map((inst) => (
        <InstanceValues
          key={inst}
          card={id}
          instance={inst}
          name={name(inst)}
          values={list.filter((v) => v.instance_id === inst)}
          rescore={enrichers.some((e) => e.id === inst)}
        />
      ))}
    </section>
  );
}

type InstanceValuesProps = { card: number; instance: number; name: string; values: Value[]; rescore: boolean };

/** InstanceValues is one instance's values: short ones in a grid, lists and long text at full width. */
function InstanceValues({ card, instance, name, values, rescore }: InstanceValuesProps) {
  const qc = useQueryClient();
  const [jobId, setJobId] = useState<number>();
  const job = useJob(jobId ?? 0, { enabled: jobId !== undefined });
  const done = !!job.data && finished(job.data);
  useEffect(() => {
    if (done) void qc.invalidateQueries({ queryKey: ["card", card, "enrichments"] });
  }, [done, qc, card]);
  const start = useMutation({
    meta: { toast: false },
    mutationFn: async () => must(await api.POST("/enrichments/runs", { body: { instance_id: instance, card_ids: [card], rescore: true, thorough: true } })),
    onSuccess: (j) => setJobId(j.id),
  });
  const running = start.isPending || (jobId !== undefined && !done);
  const failed = job.data?.status === "failed" ? job.data.error : undefined;
  const short = values.filter((v) => !isLong(v));
  const long = values.filter(isLong);
  const staleTitle = (v: Value) => (v.stale ? "Stale: from an older revision" : undefined);
  return (
    <div className="enrichment-instance">
      <div className="row">
        <h3>{name}</h3>
        {rescore && (
          <button className="button" disabled={running} onClick={() => start.mutate()}>
            {running ? "Rescoring…" : "Rescore"}
          </button>
        )}
      </div>
      {short.length > 0 && (
        <dl className="enrichment-grid">
          {short.map((v) => (
            <div key={v.key} className={v.stale ? "stale" : undefined} title={staleTitle(v)}>
              <dt>{v.label}</dt>
              <dd>
                {enrichmentText(v.value, v.range)}
                {v.stale && <span className="muted"> (stale)</span>}
              </dd>
            </div>
          ))}
        </dl>
      )}
      {long.map((v) => (
        <div key={v.key} className={v.stale ? "enrichment-long stale" : "enrichment-long"} title={staleTitle(v)}>
          <h4>
            {v.label}
            {v.stale && <span className="muted"> (stale)</span>}
          </h4>
          {Array.isArray(v.value) ? (
            <ul>
              {v.value.map((item, i) => (
                <li key={i}>{String(item)}</li>
              ))}
            </ul>
          ) : (
            <p>{String(v.value)}</p>
          )}
        </div>
      ))}
      {(start.error || failed) && <p className="error">{start.error?.message ?? failed}</p>}
    </div>
  );
}
