import { useState } from "react";
import type { Data } from "../card/draft";
import { Words } from "../sync/Diff";
import { wordDiff } from "../sync/sync";
import type { Proposed } from "./subject";

const absent = <em className="muted">(empty)</em>;

/**
 * Review puts a proposal against the draft, one row per change, each word diff taken against the data the run began
 * from. onApply gets the draft changes the taken rows make; saves says whether applying them also saves.
 */
export function Review(props: {
  title: string;
  proposed: Proposed;
  warnings?: string[] | null;
  saves?: boolean;
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
  const [showDraft, setShowDraft] = useState(false);
  return (
    <div className="forge-review">
      <div className="forge-review-bar">
        <span className="forge-review-title">
          {props.title} proposes {rows.length} change{rows.length === 1 ? "" : "s"}.{!props.saves && " Nothing saves until you press Save."}
        </span>
        <label className="check">
          <input type="checkbox" checked={showDraft} onChange={(e) => setShowDraft(e.target.checked)} /> Show your draft
        </label>
        <button className="button primary" disabled={!taken.size} onClick={() => props.onApply(apply(taken))}>
          {props.saves ? `Save ${taken.size} of ${rows.length} changes` : `Apply ${taken.size} of ${rows.length} changes to draft`}
        </button>
        <button className="button" onClick={props.onDiscard}>
          Discard
        </button>
      </div>
      {props.warnings?.map((w) => (
        <p key={w} className="banner">
          {w}
        </p>
      ))}
      {rows.map((r) => (
        <section key={r.key} className={taken.has(r.key) ? "forge-row take" : "forge-row keep"} aria-label={r.label}>
          <div className="forge-row-head">
            <strong>{r.label}</strong>
            {r.change && <small className={`forge-change ${r.change}`}>{r.change}</small>}
            {r.edited && <small className="muted">You've edited this since the run started.</small>}
            <div className="forge-toggle" role="radiogroup" aria-label={`Choice for ${r.label}${r.change ? ` (${r.change})` : ""}`}>
              <label className="check">
                <input type="radio" name={`take-${r.key}`} checked={taken.has(r.key)} onChange={() => choose(r.key, true)} /> Take proposal
              </label>
              <label className="check">
                <input type="radio" name={`take-${r.key}`} checked={!taken.has(r.key)} onChange={() => choose(r.key, false)} /> Keep mine
              </label>
            </div>
          </div>
          {/* The diff runs from where the run began, so an edit made since shows only in the draft. */}
          <div className={showDraft || r.edited ? "forge-row-body both" : "forge-row-body"}>
            {(showDraft || r.edited) && (
              <div className="forge-text mine" data-label="Your draft">
                {r.mine || absent}
              </div>
            )}
            <div className="forge-text hunks" data-label="Proposal">
              {r.after === undefined ? absent : <Words runs={wordDiff(r.before, r.after)} />}
            </div>
          </div>
        </section>
      ))}
    </div>
  );
}
