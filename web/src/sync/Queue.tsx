import { useMutation, useQuery } from "@tanstack/react-query";
import { Fragment, useState } from "react";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { finished, progressText, useJobEvents } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import {
  bulkResolutions,
  compareItem,
  optionKey,
  optionLabel,
  reviewPath,
  rowKey,
  rows,
  snapshotOf,
  type Candidate,
  type Item,
  type Option,
  type Row,
} from "./sync";
import "./Sync.css";

const plural = (n: number, s: string) => `${n} ${s}${n === 1 ? "" : "s"}`;

const verdictLabel = (i: Item) => (i.auto_bound ? "linked (auto)" : i.auto_imported ? "imported (auto)" : i.verdict);

/** reasons lists the signals a candidate matched on, with their distances. */
const reasons = (c: Candidate) =>
  Object.entries(c.reasons ?? {})
    .map(([k, d]) => (d ? `${k} ${d}` : k))
    .join(", ");

function OptionSelect({ item, value, onChange, label }: { item: Item; value?: Option; onChange: (key: string) => void; label: string }) {
  const s = item.suggested && optionKey(item.suggested);
  return (
    <select className="input" aria-label={label} value={value ? optionKey(value) : ""} title={item.suggested?.why} onChange={(e) => onChange(e.target.value)}>
      {!value && <option value="">Choose…</option>}
      {(item.options ?? []).map((o) => (
        <option key={optionKey(o)} value={optionKey(o)}>
          {optionLabel(o, item.candidates)}
          {optionKey(o) === s ? " (suggested)" : ""}
        </option>
      ))}
    </select>
  );
}

/** Queue shows a sync run's snapshot: every app item, its verdict and match, and the action to apply. */
export function Queue() {
  const id = Number(useParams().job);
  const toast = useToast();
  const job = useQuery({ queryKey: ["job", id], queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id } } })) });
  const j = job.data;
  useJobEvents(id, !!j && !finished(j));
  const snap = snapshotOf(j);
  const [verdict, setVerdict] = useState<string>();
  // The owner's changes from the defaults: the suggestion, checked unless the run applied it itself.
  const [picks, setPicks] = useState<Record<string, string>>({});
  const [checked, setChecked] = useState<Record<string, boolean>>({});

  const all = snap ? rows(snap) : [];
  const pickOf = (r: Row) => {
    const key = picks[rowKey(r)] ?? (r.item.suggested && optionKey(r.item.suggested));
    return (r.item.options ?? []).find((o) => optionKey(o) === key);
  };
  const isChecked = (r: Row) => !!pickOf(r) && (checked[rowKey(r)] ?? (!!r.item.suggested && !r.item.auto_bound && !r.item.auto_imported && r.item.verdict !== "bound"));
  const ownMatch = (r: Row) => !verdict || r.item.verdict === verdict || (verdict === "auto_bound" && !!r.item.auto_bound) ||
    (verdict === "auto_imported" && !!r.item.auto_imported);
  // A character shown only for a matching world book is context; Apply leaves it out.
  const shown = all.filter((r) => ownMatch(r) || r.item.lorebooks?.some((lb) => lb.verdict === verdict));
  const chosen = shown.filter((r) => ownMatch(r) && isChecked(r));
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
      {!!snap.missing?.length && `${plural(snap.missing.length, "bound card")} the app no longer has. `}
      {!!snap.unpushed?.length && `${plural(snap.unpushed.length, "card")} never pushed here.`}
    </p>
  );

  const apply = useMutation({
    mutationFn: async () => {
      const resolutions = await bulkResolutions(instance, chosen, (r) => pickOf(r)!, (r, card) =>
        compareItem(instance, r.kind, r.item.remote_id, card).catch((e: Error) => {
          throw new Error(`${r.item.name || r.item.remote_id}: ${e.message}`);
        }),
      );
      return must(await api.POST("/resolutions", { body: { resolutions } }));
    },
    onSuccess: (j) =>
      toast({ text: `Job ${j.id} started. Sync again to see what's left.`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } }),
    onError: (e) => toast({ text: e.message, error: true }),
  });

  if (job.isError) return <p className="error page">{job.error.message}</p>;
  if (!j) return null;
  return (
    <section className="page sync">
      <p>
        <Link to="/sync">← Sync</Link>
      </p>
      <h1>Sync #{j.id}</h1>
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
                {v} {n}
              </button>
            ))}
          </p>
          {note}
          <table>
            <thead>
              <tr>
                <th>
                  <span className="visually-hidden">Apply</span>
                </th>
                <th>App item</th>
                <th>Library match</th>
                <th>Verdict</th>
                <th>Action</th>
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
                        <input
                          type="checkbox"
                          aria-label={`Apply ${name}`}
                          disabled={!pickOf(r) || !ownMatch(r)}
                          checked={ownMatch(r) && isChecked(r)}
                          onChange={(e) => setChecked({ ...checked, [rowKey(r)]: e.target.checked })}
                        />
                      </td>
                      <td>
                        <Link to={reviewPath(instance, r.kind, it.remote_id, { job: id })}>{name}</Link>
                        {r.kind === "lorebook-items" && <span className="muted"> world book</span>}
                      </td>
                      <td>
                        {top && (
                          <>
                            {top.name} <span className="muted">{reasons(top)}</span>
                            {top.differing_fields > 0 && <span className="muted"> · {plural(top.differing_fields, "field")} differ</span>}
                          </>
                        )}
                      </td>
                      <td>
                        {verdictLabel(it)}
                        {it.error && <span className="error"> {it.error}</span>}
                      </td>
                      <td>
                        {!!it.options?.length && (
                          <>
                          <OptionSelect
                            item={it}
                            value={pickOf(r)}
                            label={`Action for ${name}`}
                            onChange={(key) => {
                              setPicks({ ...picks, [rowKey(r)]: key });
                              setChecked({ ...checked, [rowKey(r)]: true });
                            }}
                          />
                          {it.suggested?.why && <div className="muted">{it.suggested.why}</div>}
                          </>
                        )}
                      </td>
                    </tr>
                    {(it.lorebooks ?? []).map((lb) => (
                      <tr key={lb.remote_id} className="nested">
                        <td />
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
          <button className="button primary" disabled={!chosen.length || apply.isPending || apply.isSuccess} onClick={() => apply.mutate()}>
            {apply.isSuccess ? "Applied" : `Apply ${chosen.length} suggested`}
          </button>
          <p className="muted">World books under a character take their suggestion. Open a character to choose for them.</p>
        </>
      )}
    </section>
  );
}
