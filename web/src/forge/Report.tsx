import { useState, type CSSProperties, type ReactNode } from "react";
import Markdown from "react-markdown";
import { fieldLabel } from "../card/Fields";
import { refineInstruction, type Finding, type Report as ReportT } from "./actions";

const tone = (n: number) => (n >= 80 ? "var(--ok)" : n >= 60 ? "var(--drift)" : "var(--danger)");
const pct = (n: number) => Math.max(0, Math.min(100, n));

/** reportAnchor is the id of the findings group that lists key's findings: the group of a finding's first field. */
export function reportAnchor(report: ReportT, key: string): string | undefined {
  const f = report.findings?.find((f) => f.fields?.includes(key));
  return f && `report-${f.fields![0]}`;
}

/**
 * Report shows an evaluation: score, summary, dimensions, findings by field, and suggestions. Refine runs onRefine with
 * the ticked items as an instruction; without refine, there is no action to run it.
 */
export function Report({ report, busy, onRefine }: { report: ReportT; busy: boolean; onRefine?: (instruction: string) => void }) {
  const [ticked, setTicked] = useState<Set<string>>(new Set());
  const tick = (id: string) =>
    setTicked((t) => {
      const next = new Set(t);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  const findings = (report.findings ?? []).map((f, i) => ({ f, id: `f${i}` }));
  const suggestions = (report.suggestions ?? []).map((s, i) => ({ s, id: `s${i}` }));
  const groups = new Map<string, typeof findings>();
  for (const x of findings) {
    const k = x.f.fields?.[0] ?? "";
    groups.set(k, [...(groups.get(k) ?? []), x]);
  }
  const check = (id: string) => <input type="checkbox" checked={ticked.has(id)} onChange={() => tick(id)} />;
  const refine = () =>
    onRefine?.(
      refineInstruction(
        findings.filter((x) => ticked.has(x.id)).map((x) => x.f),
        suggestions.filter((x) => ticked.has(x.id)).map((x) => x.s),
        fieldLabel,
      ),
    );

  return (
    <div className="forge-report">
      <div className="forge-rhead">
        {typeof report.score === "number" && (
          <div className="forge-ring" style={{ "--p": pct(report.score), "--tone": tone(report.score) } as CSSProperties} aria-label={`Score ${report.score} of 100`}>
            <span>
              {Math.round(report.score)}
              <small>/100</small>
            </span>
          </div>
        )}
        <div className="forge-summary">
          <Markdown>{report.summary ?? ""}</Markdown>
        </div>
      </div>
      {!!report.dimensions?.length && (
        <table className="forge-dims">
          <tbody>
            {report.dimensions.map((d) => (
              <tr key={d.key}>
                <th>{d.label}</th>
                <td className="forge-bar">
                  <span style={{ width: `${pct(d.score)}%`, background: tone(d.score) }} />
                </td>
                <td>{d.score}</td>
                <td className="muted">{d.comment}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {groups.size > 0 && <h3>Findings</h3>}
      {[...groups].map(([k, list]) => (
        <section key={k} id={`report-${k}`} className="field forge-section">
          <h4>{k ? fieldLabel(k) : "The whole card"}</h4>
          {list.map(({ f, id }) => (
            <FindingRow key={id} f={f} check={check(id)} />
          ))}
        </section>
      ))}
      {suggestions.length > 0 && <h3>Suggestions</h3>}
      {suggestions.map(({ s, id }) => (
        <label key={id} className="forge-check">
          {check(id)}
          <span>{s}</span>
        </label>
      ))}
      {onRefine && (findings.length > 0 || suggestions.length > 0) && (
        <div className="forge-footer">
          <button className="button primary" disabled={!ticked.size || busy} onClick={refine}>
            Refine with {ticked.size} selected
          </button>
        </div>
      )}
    </div>
  );
}

function FindingRow({ f, check }: { f: Finding; check: ReactNode }) {
  return (
    <label className="forge-check">
      {check}
      <span className={`forge-sev ${f.severity}`}>{f.severity}</span>
      <span>
        {f.text}
        {(f.fields?.length ?? 0) > 1 && <span className="muted"> Also: {f.fields!.slice(1).map(fieldLabel).join(", ")}.</span>}
        {f.move && (
          <span className="forge-move">
            Move “{f.move.excerpt}” to {fieldLabel(f.move.to)}
          </span>
        )}
      </span>
    </label>
  );
}
