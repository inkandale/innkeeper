import { useState } from "react";
import Markdown from "react-markdown";
import type { Data } from "../card/draft";
import { useModal } from "../shell/useModal";
import { Words } from "../sync/Diff";
import { lineCounts, wordDiff } from "../sync/sync";
import type { Proposed, Shown } from "./subject";

const absent = <em className="muted">(empty)</em>;

/**
 * Review puts a proposal against the draft, one row per change, each word diff taken against the data the run began
 * from. onApply gets the draft changes the taken rows make; saves says whether applying them also saves. preview reads
 * the draft with a set of changes made, for Show draft.
 */
export function Review(props: {
  title: string;
  proposed: Proposed;
  warnings?: string[] | null;
  /** instruction is what the owner asked the run for. */
  instruction?: string;
  /** stale names the entity when it has changed since the run began. */
  stale?: string;
  saves?: boolean;
  preview: (changes: Data) => Shown[];
  onApply: (changes: Data) => void;
  onDiscard: () => void;
}) {
  const { rows, apply } = props.proposed;
  // A row the owner edited during the run keeps the edit unless they take the proposal.
  const [taken, setTaken] = useState(
    () => new Set(rows.filter((r) => !r.edited).map((r) => r.key)),
  );
  const choose = (k: string, take: boolean) =>
    setTaken((s) => {
      const next = new Set(s);
      if (take) next.add(k);
      else next.delete(k);
      return next;
    });
  const [showOriginal, setShowOriginal] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const lines = rows
    .map((r) => lineCounts(r.before, r.after ?? ""))
    .reduce((sum, c) => ({ removed: sum.removed + c.removed, added: sum.added + c.added }), { removed: 0, added: 0 });
  return (
    <div className="forge-review">
      <div className="forge-review-bar">
        <span className="forge-review-title">
          {props.title} proposes {rows.length} change
          {rows.length === 1 ? "" : "s"}.
          {!props.saves && " Nothing saves until you press Save."}
        </span>
        <span className="forge-line-counts" aria-label={`${lines.removed} lines removed, ${lines.added} added`}>
          <span className="removed">−{lines.removed}</span> <span className="added">+{lines.added}</span>
        </span>
        <label className="check">
          <input
            type="checkbox"
            checked={showOriginal}
            onChange={(e) => setShowOriginal(e.target.checked)}
          />{" "}
          Show original
        </label>
        <button className="button" onClick={() => setPreviewing(true)}>
          Show draft
        </button>
        <button
          className="button primary"
          disabled={!taken.size}
          onClick={() => props.onApply(apply(taken))}
        >
          {props.saves
            ? `Save ${taken.size} of ${rows.length} changes`
            : `Apply ${taken.size} of ${rows.length} changes to draft`}
        </button>
        <button className="button" onClick={props.onDiscard}>
          Discard
        </button>
      </div>
      {props.stale && <p className="banner">This {props.stale} has changed since the run started.</p>}
      {props.instruction && (
        <details className="forge-instruction">
          <summary>Instruction</summary>
          <p>{props.instruction}</p>
        </details>
      )}
      {props.warnings?.map((w) => (
        <p key={w} className="banner">
          {w}
        </p>
      ))}
      {rows.map((r) => (
        <section
          key={r.key}
          className={taken.has(r.key) ? "forge-row take" : "forge-row keep"}
          aria-label={r.label}
        >
          <div className="forge-row-head">
            <strong>{r.label}</strong>
            {r.change && (
              <small className={`forge-change ${r.change}`}>{r.change}</small>
            )}
            {r.edited && (
              <small className="muted">
                You've edited this since the run started.
              </small>
            )}
            <div
              className="forge-toggle"
              role="radiogroup"
              aria-label={`Choice for ${r.label}${r.change ? ` (${r.change})` : ""}`}
            >
              <label className="check">
                <input
                  type="radio"
                  name={`take-${r.key}`}
                  checked={taken.has(r.key)}
                  onChange={() => choose(r.key, true)}
                />{" "}
                Take proposal
              </label>
              <label className="check">
                <input
                  type="radio"
                  name={`take-${r.key}`}
                  checked={!taken.has(r.key)}
                  onChange={() => choose(r.key, false)}
                />{" "}
                Keep mine
              </label>
            </div>
          </div>
          {/* The diff runs from where the run began, so an edit made since shows only in the original. */}
          <div
            className={
              showOriginal || r.edited
                ? "forge-row-body both"
                : "forge-row-body"
            }
          >
            {(showOriginal || r.edited) && (
              <div className="forge-text mine" data-label="Original">
                {r.mine || absent}
              </div>
            )}
            <div className="forge-text hunks" data-label="Proposal">
              {r.after === undefined ? (
                absent
              ) : (
                <Words runs={wordDiff(r.before, r.after)} />
              )}
            </div>
          </div>
        </section>
      ))}
      {previewing && (
        <Preview
          title={`Draft with ${taken.size} of ${rows.length} changes`}
          shown={props.preview(apply(taken))}
          onClose={() => setPreviewing(false)}
        />
      )}
    </div>
  );
}

// Preview lays the draft out as it reads, over the review; Escape, the backdrop or Close shuts it.
function Preview(props: {
  title: string;
  shown: Shown[];
  onClose: () => void;
}) {
  const modal = useModal(props.onClose);
  return (
    <dialog
      {...modal}
      className="dialog forge-preview"
      aria-label={props.title}
      onClick={(e) => e.target === e.currentTarget && props.onClose()}
    >
      <div className="forge-preview-body">
        <div className="forge-preview-head">
          <h2>{props.title}</h2>
          <button className="button" onClick={props.onClose}>
            Close
          </button>
        </div>
        {props.shown.map((s, i) => (
          <section key={i} aria-label={s.title}>
            <h3>{s.title}</h3>
            <Markdown>{s.text}</Markdown>
          </section>
        ))}
      </div>
    </dialog>
  );
}
