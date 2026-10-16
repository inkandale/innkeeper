import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, withCapability } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { ImportForm, importers, JobLine } from "../cards/ImportForm";
import type { Job } from "../settings/jobUtil";
import { useFinish } from "./Welcome";

/** BringInCards syncs the apps that were set up and imports uploaded files and page URLs. */
export function BringInCards() {
  const qc = useQueryClient();
  const finish = useFinish();
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery(instancesQuery);
  const [started, setStarted] = useState<Record<number, number>>({});

  // Seeds the cache so the job line has a status before its first fetch.
  const track = (job: Job) => {
    qc.setQueryData(["job", job.id], job);
    return job.id;
  };
  const pull = useMutation({
    mutationFn: async (instance_id: number) => track(must(await api.POST("/reconciles", { body: { instance_id } }))),
    onSuccess: (job, id) => setStarted((s) => ({ ...s, [id]: job })),
    meta: { toast: false },
  });
  const failed = plugins.error ?? instances.error;
  if (failed) return <p className="error">{failed.message}</p>;
  if (plugins.isPending || instances.isPending) return null;
  // An app is read and pushed to; a plugin that only pulls follows site sources instead.
  const apps = withCapability(withCapability(instances.data, plugins.data, "pull"), plugins.data, "push");
  const imp = importers(instances.data, plugins.data);

  return (
    <div className="panel">
      <h2>Bring in cards</h2>
      {!apps.length && !imp.file && !imp.url && (
        <p>
          Nothing to import from yet. <Link to="/welcome/plugins">Set up an app or the importer first.</Link>
        </p>
      )}
      {apps.map((i) => (
        <div key={i.id}>
          {started[i.id] ? (
            <JobLine label={i.name} id={started[i.id]!} to={`/sync/reconciles/${started[i.id]}`} />
          ) : (
            <button className="button" disabled={pull.isPending} onClick={() => pull.mutate(i.id)}>
              Sync {i.name}
            </button>
          )}
        </div>
      ))}
      {pull.error && <p className="error">{pull.error.message}</p>}
      <ImportForm {...imp} />
      <p className="muted">Imports keep running after you leave; Settings › Jobs shows them.</p>
      <button className="button primary" disabled={finish.isPending} onClick={() => finish.mutate()}>
        Finish
      </button>
      {finish.error && <p className="error">{finish.error.message}</p>}
    </div>
  );
}
