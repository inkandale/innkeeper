import { Fragment, useState } from "react";
import { Link, useParams } from "react-router";
import { finished, progressText, useJob } from "../settings/jobUtil";
import { count } from "../shell/count";
import { optionLabel, reviewPath, rowKey, rows, snapshotOf, type Candidate, type Item } from "./sync";
import "./Sync.css";

const chipLabel: Record<string, string> = { auto_bound: "linked (auto)", auto_imported: "imported (auto)" };
const verdictLabel = (i: Item) => (i.auto_bound ? "linked (auto)" : i.auto_imported ? "imported (auto)" : i.verdict);

/** reasons lists the signals a candidate matched on, with their distances. */
const reasons = (c: Candidate) =>
  Object.entries(c.reasons ?? {})
    .map(([k, d]) => (d ? `${k} ${d}` : k))
    .join(", ");

/** Queue is a sync run's record: every app item it found, with its verdict, match and suggestion. */
export function Queue() {
  const id = Number(useParams().job);
  const job = useJob(id);
  const j = job.data;
  const snap = snapshotOf(j);
  const [verdict, setVerdict] = useState<string>();

  const all = snap ? rows(snap) : [];
  const ownMatch = (i: Item) =>
    !verdict || i.verdict === verdict || (verdict === "auto_bound" && !!i.auto_bound) || (verdict === "auto_imported" && !!i.auto_imported);
  // A character shown only for a matching world book is context.
  const shown = all.filter((r) => ownMatch(r.item) || r.item.lorebooks?.some((lb) => lb.verdict === verdict));
  const instance = snap?.params.instance_id ?? 0;

  // Matches the CLI: bound items without a suggestion or error are in sync, and auto-applied rows are done.
  const left = (i: Item) => !i.auto_bound && !i.auto_imported && (i.verdict !== "bound" || !!i.suggested || !!i.error);
  const nothingLeft = !all.some((r) => left(r.item) || r.item.lorebooks?.some(left));
  const summary = [
    [snap?.totals?.auto_imported, "imported"],
    [snap?.totals?.auto_bound, "linked"],
  ]
    .filter(([n]) => n)
    .map(([n, w]) => `${n} ${w}`)
    .join(", ");

  const note = (!!snap?.missing?.length || !!snap?.unpushed?.length) && (
    <p className="muted">
      {!!snap.missing?.length && `${count(snap.missing.length, "bound card")} the app no longer has. `}
      {!!snap.unpushed?.length && `${count(snap.unpushed.length, "card")} never pushed here.`}
    </p>
  );

  if (job.isError) return <p className="error page">{job.error.message}</p>;
  if (!j) return null;
  return (
    <section className="page sync">
      <p>
        <Link to="/sync">← Sync</Link>
      </p>
      <h1>Sync #{j.id}</h1>
      {snap && (
        <p className="muted">
          What this sync found. Work through what needs you in <Link to={`/sync/changes?instance_id=${instance}`}>Changes</Link>.
        </p>
      )}
      {!finished(j) ? (
        <p>Reading the app… {progressText(j)}</p>
      ) : j.status === "failed" ? (
        <p className="error">{j.error}</p>
      ) : !snap ? (
        <p>Loading…</p>
      ) : nothingLeft ? (
        <>
          <p>Nothing to review. {summary}</p>
          {note}
        </>
      ) : (
        <>
          <p className="chips" role="group" aria-label="Verdicts">
            {Object.entries(snap.totals ?? {}).map(([v, n]) => (
              <button key={v} className="chip" aria-pressed={verdict === v} onClick={() => setVerdict(verdict === v ? undefined : v)}>
                {chipLabel[v] ?? v} {n}
              </button>
            ))}
          </p>
          {note}
          <table>
            <thead>
              <tr>
                <th>App item</th>
                <th>Library match</th>
                <th>Verdict</th>
                <th>Suggested</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((r) => {
                const it = r.item;
                const name = it.name || it.remote_id;
                const top = it.candidates?.[0];
                return (
                  <Fragment key={rowKey(r)}>
                    <tr>
                      <td>
                        <Link to={reviewPath(instance, r.kind, it.remote_id, { job: id })}>{name}</Link>
                        {r.kind === "lorebook" && <span className="muted"> world book</span>}
                      </td>
                      <td>
                        {top && (
                          <>
                            {top.name} <span className="muted">{reasons(top)}</span>
                            {top.differing_fields > 0 && <span className="muted"> · {count(top.differing_fields, "field")} differ</span>}
                          </>
                        )}
                      </td>
                      <td>
                        {verdictLabel(it)}
                        {it.error && <span className="error"> {it.error}</span>}
                      </td>
                      <td>
                        {it.suggested && optionLabel(it.suggested, it.candidates)}
                        {it.suggested?.why && <div className="muted">{it.suggested.why}</div>}
                      </td>
                    </tr>
                    {(it.lorebooks ?? []).map((lb) => (
                      <tr key={lb.remote_id} className="nested">
                        <td>↳ {lb.name || lb.remote_id}</td>
                        <td>{lb.candidates?.[0]?.name}</td>
                        <td>{verdictLabel(lb)}</td>
                        <td className="muted">{lb.suggested && optionLabel(lb.suggested, lb.candidates)}</td>
                      </tr>
                    ))}
                  </Fragment>
                );
              })}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
