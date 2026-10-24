import { useState } from "react";
import type { Data } from "../card/draft";
import { Words } from "../sync/Diff";
import { wordDiff } from "../sync/sync";
import type { Proposed } from "./subject";

const absent = <em className="muted">(empty)</em>;

/**
 * Review puts a proposal against the draft, one row per change, each word diff taken against the data the run began
 * from. onApply gets the draft changes the taken rows make.
 */
export function Review(props: {
  title: string;
  proposed: Proposed;
  warnings?: string[] | null;
  onApply: (changes: Data) => void;
  onDiscard: () => void;
}) {
  const { rows, apply } = props.proposed;
  // A row the owner edited during the run keeps the edit unless they take the proposal.
  const [taken, setTaken] = useState(() => new Set(rows.filter((r) => !r.edited).map((r) => r.key)));
  const choose = (k: string, take: boolean) =>
    setTaken((s) => {
      const next = new Set(s);
      if (take) next.add(k);
      else next.delete(k);
      return next;
    });
  return (
    <div className="forge-review">
      <p className="muted">
        {props.title} proposes {rows.length} change{rows.length === 1 ? "" : "s"}. Nothing saves until you press Save.
      </p>
      {props.warnings?.map((w) => (
        <p key={w} className="banner">
          {w}
        </p>
      ))}
      <table>
        <thead>
          <tr>
            <th>Field</th>
            <th>Your draft</th>
            <th>Proposal</th>
            <th>Choice</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.key} className={taken.has(r.key) ? "take" : "keep"}>
              <th>
                {r.label}
                {r.change && <small className={`forge-change ${r.change}`}>{r.change}</small>}
                {r.edited && <small className="muted forge-edited">You've edited this since the run started.</small>}
              </th>
              <td data-label="Your draft">
                <div className="forge-text">{r.mine || absent}</div>
              </td>
              <td data-label="Proposal">
                <div className="forge-text hunks">{r.after === undefined ? absent : <Words runs={wordDiff(r.before, r.after)} />}</div>
              </td>
              <td>
                <div className="forge-toggle" role="radiogroup" aria-label={`Choice for ${r.label}${r.change ? ` (${r.change})` : ""}`}>
                  <label className="check">
                    <input type="radio" name={`take-${r.key}`} checked={taken.has(r.key)} onChange={() => choose(r.key, true)} /> Take proposal
                  </label>
                  <label className="check">
                    <input type="radio" name={`take-${r.key}`} checked={!taken.has(r.key)} onChange={() => choose(r.key, false)} /> Keep mine
                  </label>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="forge-footer">
        <button className="button primary" disabled={!taken.size} onClick={() => props.onApply(apply(taken))}>
          Apply {taken.size} of {rows.length} changes to draft
        </button>
        <button className="button" onClick={props.onDiscard}>
          Discard
        </button>
      </div>
    </div>
  );
}
