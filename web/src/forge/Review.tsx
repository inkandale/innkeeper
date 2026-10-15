import { useState } from "react";
import { fieldLabel } from "../card/Fields";
import { show, type Data } from "../card/draft";
import { Words } from "../sync/Diff";
import { wordDiff } from "../sync/sync";
import { changedKeys } from "./actions";

const absent = <em className="muted">(empty)</em>;

/**
 * Review puts a proposal against the draft, one row per field it changes, its word diff taken against start, the data
 * the run began from. onApply gets the taken fields.
 */
export function Review(props: {
  title: string;
  start: Data;
  draft: Data;
  proposal: Data;
  warnings?: string[] | null;
  onApply: (changes: Data) => void;
  onDiscard: () => void;
}) {
  const { start, draft, proposal } = props;
  const rows = changedKeys(start, proposal);
  const [taken, setTaken] = useState(() => new Set(rows));
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
          {rows.map((k) => (
            <tr key={k} className={taken.has(k) ? "take" : "keep"}>
              <th>{fieldLabel(k)}</th>
              <td>
                <div className="forge-text">{show(draft[k]) || absent}</div>
              </td>
              <td>
                <div className="forge-text hunks">
                  {proposal[k] === undefined ? absent : <Words runs={wordDiff(show(start[k]), show(proposal[k]))} />}
                </div>
              </td>
              <td>
                <div className="forge-toggle" role="radiogroup" aria-label={`Choice for ${fieldLabel(k)}`}>
                  <label className="check">
                    <input type="radio" name={`take-${k}`} checked={taken.has(k)} onChange={() => choose(k, true)} /> Take proposal
                  </label>
                  <label className="check">
                    <input type="radio" name={`take-${k}`} checked={!taken.has(k)} onChange={() => choose(k, false)} /> Keep mine
                  </label>
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="forge-footer">
        <button
          className="button primary"
          disabled={!taken.size}
          onClick={() => props.onApply(Object.fromEntries(rows.filter((k) => taken.has(k)).map((k) => [k, proposal[k]])))}
        >
          Apply {taken.size} of {rows.length} changes to draft
        </button>
        <button className="button" onClick={props.onDiscard}>
          Discard
        </button>
      </div>
    </div>
  );
}
