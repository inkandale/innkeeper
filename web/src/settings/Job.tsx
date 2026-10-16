import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { age, finished, jobStates, kindLabel, progressText, useJob, type Job as JobT } from "./jobUtil";

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
    </section>
  );
}
