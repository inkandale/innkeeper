import type { ReactNode } from "react";
import { show, text, tokens, type Data } from "./draft";
import { MacroTextarea } from "./MacroTextarea";

type Edit = (key: string, value: unknown) => void;

const before: [string, string][] = [
  ["description", "Description"],
  ["personality", "Personality"],
  ["scenario", "Scenario"],
  ["first_mes", "First message"],
];
const after: [string, string][] = [
  ["mes_example", "Example dialogue"],
  ["system_prompt", "System prompt"],
  ["post_history_instructions", "Post-history instructions"],
  ["creator_notes", "Creator notes"],
];

function Section(props: { label: string; dirty?: boolean; count?: number; children: ReactNode }) {
  return (
    <details className={props.dirty ? "field dirty" : "field"} open>
      <summary>
        {props.label}
        {props.dirty && <span className="unsaved">unsaved</span>}
        {props.count !== undefined && <span className="tokens">~{props.count} tokens</span>}
      </summary>
      {props.children}
    </details>
  );
}

/** Greetings edits a list of greetings. noun names one, as in "Add greeting". */
function Greetings({ field, label, noun, data, dirty, edit }: { field: string; label: string; noun: string; data: Data; dirty: Set<string>; edit: Edit }) {
  const v = data[field];
  const list = Array.isArray(v) ? v.map(text) : [];
  const Noun = noun[0]!.toUpperCase() + noun.slice(1);
  return (
    <Section label={label} dirty={dirty.has(field)} count={tokens(list.join(""))}>
      {list.map((g, i) => (
        <div key={i} className="greeting">
          <MacroTextarea label={`${Noun} ${i + 1}`} value={g} onChange={(v) => edit(field, list.map((x, j) => (j === i ? v : x)))} />
          <button className="button" aria-label={`Remove ${noun} ${i + 1}`} onClick={() => edit(field, list.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button className="button" onClick={() => edit(field, [...list, ""])}>
        Add {noun}
      </button>
    </Section>
  );
}

/** Translations edits creator_notes_multilingual, an object from language code to notes, as rows. */
function Translations({ data, dirty, edit }: { data: Data; dirty: Set<string>; edit: Edit }) {
  const value = data.creator_notes_multilingual;
  const rows = Object.entries(value && typeof value === "object" ? value : {}).map(([c, n]): [string, string] => [c, text(n)]);
  const set = (next: [string, string][]) => edit("creator_notes_multilingual", Object.fromEntries(next));
  const put = (i: number, row: [string, string]) => set(rows.map((r, j) => (j === i ? row : r)));
  return (
    <Section label="Creator notes in other languages" dirty={dirty.has("creator_notes_multilingual")} count={tokens(rows.map(([, n]) => n).join(""))}>
      {rows.map(([code, notes], i) => (
        <div key={i} className="greeting">
          <input
            className="input lang"
            aria-label={`Language ${i + 1}`}
            placeholder="Code"
            value={code}
            // Two rows with one code would become one key, losing a row's notes.
            onChange={(e) => rows.every(([c], j) => j === i || c !== e.target.value) && put(i, [e.target.value, notes])}
          />
          <MacroTextarea label={`Notes ${i + 1}`} value={notes} onChange={(v) => put(i, [code, v])} />
          <button className="button" aria-label={`Remove language ${i + 1}`} onClick={() => set(rows.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      <button className="button" disabled={rows.some(([c]) => c === "")} onClick={() => set([...rows, ["", ""]])}>
        Add language
      </button>
    </Section>
  );
}

/** Fields is the card's text, one collapsible section per field. */
export function Fields({ data, dirty, edit }: { data: Data; dirty: Set<string>; edit: Edit }) {
  const area = ([key, label]: [string, string]) => (
    <Section key={key} label={label} dirty={dirty.has(key)} count={tokens(text(data[key]))}>
      <MacroTextarea label={label} value={text(data[key])} onChange={(v) => edit(key, v)} start={key === "mes_example"} />
    </Section>
  );
  return (
    <>
      {before.map(area)}
      <Greetings field="alternate_greetings" label="Alternate greetings" noun="greeting" data={data} dirty={dirty} edit={edit} />
      <Greetings field="group_only_greetings" label="Group-only greetings" noun="group greeting" data={data} dirty={dirty} edit={edit} />
      {after.map(area)}
      <Translations data={data} dirty={dirty} edit={edit} />
      <Section label="Extensions">
        <pre className="extensions">{show(data.extensions ?? {})}</pre>
      </Section>
    </>
  );
}
