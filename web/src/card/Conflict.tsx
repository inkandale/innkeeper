import type { Kind } from "../api/client";
import { clashes, show, type Data, type Draft } from "./draft";
import type { Editor } from "./useEditor";

/** Conflict shows the fields both you and the latest revision changed. */
export function Conflict(props: { latest: Data; draft: Draft; onUse: (key: string) => void; onKeep: () => void }) {
  const { latest, draft, onUse, onKeep } = props;
  const keys = clashes(draft, latest);
  return (
    <section className="conflict" aria-label="Changed elsewhere">
      {keys.length === 0 && <p>No field you changed was changed elsewhere.</p>}
      {keys.map((k) => (
        <div key={k} className="conflict-field">
          <h3>{k}</h3>
          <div>
            <h4>Latest</h4>
            <pre>{show(latest[k])}</pre>
          </div>
          <div>
            <h4>Yours</h4>
            <pre>{show(draft.data[k])}</pre>
          </div>
          <button className="button" aria-label={`Use latest ${k}`} onClick={() => onUse(k)}>
            Use latest
          </button>
        </div>
      ))}
      <button className="button primary" onClick={onKeep}>
        Keep my version
      </button>
    </section>
  );
}

/** Stale is the "changed elsewhere" banner and, once asked, the comparison. */
export function Stale({ kind, ed }: { kind: Kind; ed: Editor }) {
  if (!ed.stale) return null;
  return (
    <>
      <div className="banner" role="alert">
        <span>This {kind} changed elsewhere since you started editing.</span>
        <button className="button" onClick={ed.reload}>
          Reload and lose my edits
        </button>
        <button className="button" onClick={() => void ed.compare()}>
          Compare with the latest
        </button>
      </div>
      {ed.comparing && ed.draft && <Conflict latest={ed.headData} draft={ed.draft} onUse={ed.use} onKeep={ed.keep} />}
    </>
  );
}
