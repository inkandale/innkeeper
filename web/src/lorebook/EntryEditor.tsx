import { useId, useState } from "react";
import { text, tokens, type Data } from "../card/draft";
import { ChipInput } from "../card/ChipInput";
import { asEntries, blankEntry, firstLine, isEntry, matches, moved, type Entry } from "./entries";
import { strings } from "../card/draft";

let lastUid = 0;
const uid = () => ++lastUid;
const num = (v: unknown) => (typeof v === "number" ? v : "");
// Hints follow the character card V2 spec's meaning for each flag.
const flags = [
  ["constant", "Always on", "Inserted even without a key match"],
  ["selective", "Needs a secondary key", "A key from each key list must match"],
  ["enabled", "Enabled", "Off skips this entry"],
] as const;

/** Grip is the drag handle's icon: six dots. */
const Grip = () => (
  <svg className="grip" width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
    {[6, 12, 18].map((y) => [9, 15].map((x) => <circle key={`${x}-${y}`} cx={x} cy={y} r="1.6" />))}
  </svg>
);

const ext = (e: Entry) => (typeof e.extensions === "object" && e.extensions !== null ? (e.extensions as Record<string, unknown>) : {});

/** Entries edits a lorebook's entries: a filter, then one collapsible row per entry. lock names the run that locks them. */
export function Entries({ data, base, edit, lock }: { data: Data; base: Data; edit: (key: string, value: unknown) => void; lock?: string }) {
  // Non-object items in imported data aren't shown, but stay in place on save.
  const list: unknown[] = Array.isArray(data.entries) ? data.entries : [];
  const saved = new Set(asEntries(base.entries).map((e) => JSON.stringify(e)));
  // Client-side IDs follow each entry through moves, so keys and open rows stay with it.
  const [uids, setUids] = useState<number[]>(() => list.map(uid));
  // A reload or rebase can change the count; start the IDs over.
  if (uids.length !== list.length) setUids(list.map(uid));
  const [filter, setFilter] = useState("");
  const [opened, setOpened] = useState<number>();
  const [dragging, setDragging] = useState<number>();
  const hintId = useId();

  const set = (next: unknown[], nextUids: number[]) => {
    setUids(nextUids);
    edit("entries", next);
  };
  const update = (i: number, patch: Entry) => set(list.map((e, j) => (j === i && isEntry(e) ? { ...e, ...patch } : e)), uids);
  const move = (from: number, to: number) => set(moved(list, from, to), moved(uids, from, to));
  const remove = (i: number) => set(list.filter((_, j) => j !== i), uids.filter((_, j) => j !== i));
  const add = () => {
    const id = uid();
    setOpened(id);
    set([...list, blankEntry()], [...uids, id]);
  };

  return (
    <fieldset className="contents" disabled={!!lock}>
      <div className="row entries-toolbar">
        <input className="input" type="search" aria-label="Filter entries" placeholder="Filter entries" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button className="button" onClick={add}>
          Add entry
        </button>
        {lock && <span className="locked">locked while {lock} runs</span>}
      </div>
      {list.map((e, i) => {
        if (!isEntry(e) || !matches(e, filter)) return null;
        const n = i + 1;
        const unsaved = !saved.has(JSON.stringify(e));
        return (
          <details
            key={uids[i]}
            className={unsaved ? "field entry dirty" : "field entry"}
            open={uids[i] === opened || undefined}
            // Locked entries can't be reordered.
            onDragOver={lock ? undefined : (ev) => ev.preventDefault()}
            onDrop={lock ? undefined : (ev) => {
              ev.preventDefault();
              if (dragging !== undefined && dragging !== i) move(dragging, i);
              setDragging(undefined);
            }}
          >
            {/* Only the summary drags, so selecting or dragging text in the fields doesn't move the entry. */}
            <summary draggable={!lock} title={lock ? undefined : "Drag to reorder"} onDragStart={() => setDragging(i)} onDragEnd={() => setDragging(undefined)}>
              {!lock && <Grip />}
              <strong>{strings(e.keys).join(", ") || "(no keys)"}</strong>
              <span className="muted">{firstLine(e)}</span>
              {unsaved && <span className="unsaved">unsaved</span>}
              <span className="tokens">~{tokens(text(e.content))} tokens</span>
            </summary>
            <div className="entry-body">
              <ChipInput label={`Entry ${n} keys`} values={strings(e.keys)} onChange={(v) => update(i, { keys: v })} />
              <ChipInput label={`Entry ${n} secondary keys`} values={strings(e.secondary_keys)} onChange={(v) => update(i, { secondary_keys: v })} />
              <textarea className="input grow" aria-label={`Entry ${n} content`} value={text(e.content)} onChange={(ev) => update(i, { content: ev.target.value })} />
              <div className="row entry-flags">
                {flags.map(([k, label, hint]) => (
                  <label key={k} className="check">
                    <input
                      type="checkbox"
                      aria-label={`Entry ${n} ${label}`}
                      aria-describedby={`${hintId}-${i}-${k}`}
                      checked={e[k] === true}
                      onChange={(ev) => update(i, { [k]: ev.target.checked })}
                    />{" "}
                    {label}
                    <span id={`${hintId}-${i}-${k}`} className="muted hint">
                      {hint}
                    </span>
                  </label>
                ))}
              </div>
              <div className="row">
                <label>
                  Order{" "}
                  <input className="input num" type="number" aria-label={`Entry ${n} order`} value={num(e.insertion_order)}
                    onChange={(ev) => update(i, { insertion_order: Number(ev.target.value) })} />
                </label>
                <label>
                  Position{" "}
                  <select className="input" aria-label={`Entry ${n} position`} value={text(e.position) || "before_char"}
                    onChange={(ev) => update(i, { position: ev.target.value })}>
                    <option value="before_char">Before character</option>
                    <option value="after_char">After character</option>
                  </select>
                </label>
                <label>
                  Depth{" "}
                  <input className="input num" type="number" min={0} aria-label={`Entry ${n} depth`} value={num(ext(e).depth)}
                    onChange={(ev) => {
                      const { depth: _, ...rest } = ext(e);
                      update(i, { extensions: ev.target.value === "" ? rest : { ...rest, depth: Number(ev.target.value) } });
                    }} />
                </label>
              </div>
              <div className="row">
                <button className="button" aria-label={`Move entry ${n} up`} disabled={i === 0} onClick={() => move(i, i - 1)}>
                  ↑
                </button>
                <button className="button" aria-label={`Move entry ${n} down`} disabled={i === list.length - 1} onClick={() => move(i, i + 1)}>
                  ↓
                </button>
                <button className="button danger" aria-label={`Delete entry ${n}`} onClick={() => remove(i)}>
                  Delete
                </button>
              </div>
            </div>
          </details>
        );
      })}
    </fieldset>
  );
}
