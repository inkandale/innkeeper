import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { age, finished, jobStates, kindLabel, progressText, span, useJob, type Job as JobT } from "./jobUtil";

function Counts({ label, counts }: { label: string; counts?: Record<string, number> }) {
  if (!counts || !Object.keys(counts).length) return null;
  return (
    <p className="counts">
      {label}:{" "}
      {Object.entries(counts).map(([k, n]) => (
        <span key={k} className="chip">
          {k} {n}
        </span>
      ))}
    </p>
  );
}

/** Logs is what the worker and the job's plugin logged, refreshed while the job runs. */
function Logs({ job }: { job: JobT }) {
  const running = !finished(job);
  const logs = useQuery({
    queryKey: ["job-logs", job.id],
    queryFn: async () =>
      must(await api.GET("/jobs/{id}/logs", { params: { path: { id: job.id }, query: { limit: 1000 } } })),
    // ponytail: refetches the whole log while the job runs; fetch after the last line if 1000 lines gets slow.
    refetchInterval: running ? 2000 : false,
  });
  if (logs.isError) return <p className="error">{logs.error.message}</p>;
  if (!logs.data?.length) return null;
  return (
    <section aria-label="Log">
      <h2>Log</h2>
      <ol className="job-log">
        {logs.data.map((l) => (
          <li key={l.id} className={l.level}>
            <time dateTime={l.at}>{new Date(l.at).toLocaleTimeString()}</time> {l.message}
          </li>
        ))}
      </ol>
    </section>
  );
}

export function Job() {
  const id = Number(useParams().id);
  const qc = useQueryClient();
  const job = useJob(id);
  const j = job.data;

  const [childStatus, setChildStatus] = useState<JobT["status"] | "">("");
  const children = useQuery({
    enabled: j?.kind === "bulk",
    // Keyed on the counts, so the list refetches as children finish.
    queryKey: ["jobs", "children", id, childStatus, j?.children],
    queryFn: async () =>
      must(await api.GET("/jobs", { params: { query: { parent: id, ...(childStatus ? { status: childStatus } : {}) } } })),
  });
  const cancel = useMutation({
    mutationFn: async () => must(await api.POST("/jobs/{id}/cancel", { params: { path: { id } } })),
    onSuccess: (data) => qc.setQueryData(["job", id], data),
  });
  const retry = useMutation({
    mutationFn: async () => must(await api.POST("/jobs/{id}/retry", { params: { path: { id } } })),
    onSuccess: (data) => qc.setQueryData(["job", id], data),
  });

  if (job.isError) return <p className="error">{job.error.message}</p>;
  if (!j) return null;
  const blob = j.kind === "card.export" ? (j.progress as { blob?: string } | null)?.blob : undefined;
  return (
    <section className="job">
      <p>
        <Link to="/settings/jobs">All jobs</Link>
      </p>
      <h1>Job #{j.id}</h1>
      <dl>
        <dt>Kind</dt>
        <dd>{kindLabel(j)}</dd>
        <dt>State</dt>
        <dd className={"state " + j.status}>{j.status}</dd>
        <dt>Progress</dt>
        <dd>{progressText(j)}</dd>
        <dt>Age</dt>
        <dd>{age(j.created_at)}</dd>
        {j.started_at && (
          <>
            <dt>Waited</dt>
            <dd>{span(Date.parse(j.started_at) - Date.parse(j.created_at))}</dd>
          </>
        )}
        {j.duration_ms != null && (
          <>
            <dt>Ran for</dt>
            <dd>{span(j.duration_ms)}</dd>
          </>
        )}
        <dt>Cards changed</dt>
        <dd>{j.cards_changed}</dd>
        {j.error && (
          <>
            <dt>Error</dt>
            <dd className="error">{j.error}</dd>
          </>
        )}
      </dl>
      {!finished(j) && (
        <button className="button danger" onClick={() => cancel.mutate()} disabled={cancel.isPending}>
          Cancel
        </button>
      )}
      {/* A bulk job's children finish their parent, so only a job of its own retries. */}
      {j.status === "failed" && !j.parent_id && j.kind !== "bulk" && (
        <button className="button" onClick={() => retry.mutate()} disabled={retry.isPending}>
          Retry
        </button>
      )}
      {retry.isError && <p className="error">{retry.error.message}</p>}
      {blob && (
        <a className="button primary" href={`/api/blobs/${blob}`} download="innkeeper-cards.zip">
          Download zip
        </a>
      )}
      <Counts label="Children" counts={j.children} />
      <Counts label="Outcomes" counts={j.outcomes} />
      {j.kind === "bulk" && (
        <>
          <select
            className="input"
            aria-label="Child state"
            value={childStatus}
            onChange={(e) => setChildStatus(e.target.value as JobT["status"] | "")}
          >
            <option value="">All children</option>
            {jobStates.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          <table className="jobs">
            <tbody>
              {(children.data ?? []).map((c) => (
                <tr key={c.id}>
                  <td>#{c.id}</td>
                  <td>{c.kind}</td>
                  <td className={"state " + c.status}>{c.status}</td>
                  <td className="error">{c.error}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      <Logs job={j} />
    </section>
  );
}
