import { useState } from "react";
import { text, tokens, type Data } from "../card/draft";
import { ChipInput } from "./ChipInput";
import { asEntries, blankEntry, firstLine, matches, moved, strings, type Entry } from "./entries";

let lastUid = 0;
const uid = () => ++lastUid;
const num = (v: unknown) => (typeof v === "number" ? v : "");
const ext = (e: Entry) => (typeof e.extensions === "object" && e.extensions !== null ? (e.extensions as Record<string, unknown>) : {});

/** Entries edits a lorebook's entries: a filter, then one collapsible row per entry. */
export function Entries({ data, base, edit }: { data: Data; base: Data; edit: (key: string, value: unknown) => void }) {
  const list = asEntries(data.entries);
  const saved = new Set(asEntries(base.entries).map((e) => JSON.stringify(e)));
  // Client-side IDs follow each entry through moves, so keys and open rows stay with it.
  const [uids, setUids] = useState<number[]>(() => list.map(uid));
  // A reload or rebase can change the count; start the IDs over.
  if (uids.length !== list.length) setUids(list.map(uid));
  const [filter, setFilter] = useState("");
  const [opened, setOpened] = useState<number>();
  const [dragging, setDragging] = useState<number>();

  const set = (next: Entry[], nextUids: number[]) => {
    setUids(nextUids);
    edit("entries", next);
  };
  const update = (i: number, patch: Entry) => set(list.map((e, j) => (j === i ? { ...e, ...patch } : e)), uids);
  const move = (from: number, to: number) => set(moved(list, from, to), moved(uids, from, to));
  const remove = (i: number) => set(list.filter((_, j) => j !== i), uids.filter((_, j) => j !== i));
  const add = () => {
    const id = uid();
    setOpened(id);
    set([...list, blankEntry()], [...uids, id]);
  };

  return (
    <>
      <div className="row entries-toolbar">
        <input className="input" type="search" aria-label="Filter entries" placeholder="Filter entries" value={filter} onChange={(e) => setFilter(e.target.value)} />
        <button className="button" onClick={add}>
          Add entry
        </button>
      </div>
      {list.map((e, i) => {
        if (!matches(e, filter)) return null;
        const n = i + 1;
        const unsaved = !saved.has(JSON.stringify(e));
        return (
          <details
            key={uids[i]}
            className={unsaved ? "field entry dirty" : "field entry"}
            open={uids[i] === opened || undefined}
            draggable
            onDragStart={() => setDragging(i)}
            onDragOver={(ev) => ev.preventDefault()}
            onDrop={() => {
              if (dragging !== undefined && dragging !== i) move(dragging, i);
              setDragging(undefined);
            }}
          >
            <summary>
              <strong>{strings(e.keys).join(", ") || "(no keys)"}</strong>
              <span className="muted">{firstLine(e)}</span>
              {unsaved && <span className="unsaved">unsaved</span>}
              <span className="tokens">~{tokens(text(e.content))} tokens</span>
            </summary>
            <div className="entry-body">
              <ChipInput label={`Entry ${n} keys`} values={strings(e.keys)} onChange={(v) => update(i, { keys: v })} />
              <ChipInput label={`Entry ${n} secondary keys`} values={strings(e.secondary_keys)} onChange={(v) => update(i, { secondary_keys: v })} />
              <textarea className="input grow" aria-label={`Entry ${n} content`} value={text(e.content)} onChange={(ev) => update(i, { content: ev.target.value })} />
              <div className="row">
                {(["constant", "selective", "enabled"] as const).map((k) => (
                  <label key={k} className="check">
                    <input type="checkbox" aria-label={`Entry ${n} ${k}`} checked={e[k] === true} onChange={(ev) => update(i, { [k]: ev.target.checked })} /> {k}
                  </label>
                ))}
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
                    onChange={(ev) => update(i, { extensions: { ...ext(e), depth: Number(ev.target.value) } })} />
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
    </>
  );
}
