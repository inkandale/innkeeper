import { useState, type ReactNode } from "react";
import { same, show, text, tokens, type Data } from "./draft";
import { MacroTextarea } from "./MacroTextarea";

type Edit = (key: string, value: unknown) => void;
/** Lock returns the label of the run that locks a field, if one does. */
type Lock = (key: string) => string | undefined;

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

/** fieldLabel names a card data key, as the Fields tab does. */
export const fieldLabel = (key: string) =>
  [...before, ...after, ["name", "Name"], ["tags", "Tags"]].find(([k]) => k === key)?.[1] ?? key;

type SectionProps = {
  id?: string;
  label: string;
  dirty?: boolean;
  count?: number;
  lock?: string;
  extra?: ReactNode;
  open?: boolean;
  onToggle?: (open: boolean) => void;
  children: ReactNode;
};

/** Section is one collapsible field: a header with its state and extra, then the field, disabled while locked. It starts open unless open says otherwise. */
export function Section(props: SectionProps) {
  return (
    <details
      id={props.id}
      className={props.dirty ? "field dirty" : "field"}
      open={props.open ?? true}
      onToggle={(e) => props.onToggle?.(e.currentTarget.open)}
    >
      <summary>
        {props.label}
        {props.dirty && <span className="unsaved">unsaved</span>}
        {props.lock && <span className="locked">locked while {props.lock} runs</span>}
        {props.count !== undefined && <span className="tokens">~{props.count} tokens</span>}
        {props.extra}
      </summary>
      <fieldset className="contents" disabled={!!props.lock}>
        {props.children}
      </fieldset>
    </details>
  );
}

const greetingList = (v: unknown) => (Array.isArray(v) ? v.map(text) : []);

/** Greetings edits a list of greetings. noun names one, as in "Add greeting". */
function Greetings({ field, noun, data, edit }: { field: string; noun: string; data: Data; edit: Edit }) {
  const list = greetingList(data[field]);
  const Noun = noun[0]!.toUpperCase() + noun.slice(1);
  return (
    <>
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
    </>
  );
}

type Row = [code: string, notes: string];

const notesByCode = (v: unknown): Record<string, string> =>
  Object.fromEntries(Object.entries(v && typeof v === "object" ? v : {}).map(([c, n]) => [c, text(n)]));

/** Translations edits creator_notes_multilingual, an object from language code to notes, as rows. */
function Translations({ data, edit }: { data: Data; edit: Edit }) {
  const value = notesByCode(data.creator_notes_multilingual);
  // Rows can hold what the object can't: an empty code, or a code two rows share while one is typed.
  const [state, setState] = useState({ rows: Object.entries(value), from: value });
  let rows = state.rows;
  if (!same(state.from, value)) {
    rows = Object.entries(value);
    setState({ rows, from: value });
  }
  const codes = rows.map(([c]) => c).filter((c) => c);
  const clashes = new Set(codes.filter((c, i) => codes.indexOf(c) !== i));
  const set = (next: Row[]) => {
    const filled = next.filter(([c]) => c);
    const obj = Object.fromEntries(filled);
    // Two rows with one code would become one key, losing a row's notes, so nothing is written until they differ.
    const write = new Set(filled.map(([c]) => c)).size === filled.length && !same(obj, value);
    setState({ rows: next, from: write ? obj : value });
    if (write) edit("creator_notes_multilingual", obj);
  };
  const put = (i: number, row: Row) => set(rows.map((r, j) => (j === i ? row : r)));
  return (
    <>
      {rows.map(([code, notes], i) => (
        <div key={i} className="greeting">
          <input
            className="input lang"
            aria-label={`Language ${i + 1}`}
            aria-invalid={clashes.has(code) || undefined}
            placeholder="Code"
            value={code}
            onChange={(e) => put(i, [e.target.value, notes])}
          />
          <MacroTextarea label={`Notes ${i + 1}`} value={notes} onChange={(v) => put(i, [code, v])} />
          <button className="button" aria-label={`Remove language ${i + 1}`} onClick={() => set(rows.filter((_, j) => j !== i))}>
            ×
          </button>
        </div>
      ))}
      {[...clashes].map((c) => (
        <p key={c} className="error lang-clash">
          Two rows have the code {c}. Their changes save once the codes differ.
        </p>
      ))}
      <button className="button" disabled={rows.some(([c]) => c === "")} onClick={() => set([...rows, ["", ""]])}>
        Add language
      </button>
    </>
  );
}

type Field = { key: string; label: string; count?: number; extra?: ReactNode; body: ReactNode };

/**
 * Fields is the card's text, one collapsible section per field, under an outline that jumps to each and opens or
 * closes them all. extra adds to a text field's header.
 */
export function Fields(props: { data: Data; dirty: Set<string>; edit: Edit; lock?: Lock; extra?: (key: string) => ReactNode }) {
  const { data, dirty, edit } = props;
  const lock = props.lock ?? (() => undefined);
  const [closed, setClosed] = useState(new Set<string>());
  const area = ([key, label]: [string, string]): Field => ({
    key,
    label,
    count: tokens(text(data[key])),
    extra: props.extra?.(key),
    body: <MacroTextarea label={label} value={text(data[key])} onChange={(v) => edit(key, v)} start={key === "mes_example"} />,
  });
  const greetings = (key: string, label: string, noun: string): Field => ({
    key,
    label,
    count: tokens(greetingList(data[key]).join("")),
    body: <Greetings field={key} noun={noun} data={data} edit={edit} />,
  });
  // The outline and the sections both read this list, so they can't disagree.
  const fields: Field[] = [
    ...before.map(area),
    greetings("alternate_greetings", "Alternate greetings", "greeting"),
    greetings("group_only_greetings", "Group-only greetings", "group greeting"),
    ...after.map(area),
    {
      key: "creator_notes_multilingual",
      label: "Creator notes in other languages",
      count: tokens(Object.values(notesByCode(data.creator_notes_multilingual)).join("")),
      body: <Translations data={data} edit={edit} />,
    },
    { key: "extensions", label: "Extensions", body: <pre className="extensions">{show(data.extensions ?? {})}</pre> },
  ];
  const total = fields.reduce((sum, f) => sum + (f.count ?? 0), 0);
  const toggle = (key: string, open: boolean) =>
    setClosed((c) => {
      if (c.has(key) !== open) return c;
      const next = new Set(c);
      if (open) next.delete(key);
      else next.add(key);
      return next;
    });
  const id = (key: string) => `field-${key}`;
  const jump = (key: string) => {
    toggle(key, true);
    document.getElementById(id(key))?.scrollIntoView({ behavior: "smooth", block: "start" });
  };
  return (
    <>
      <div className="row field-outline-head">
        <span className="muted">~{total} tokens total</span>
        <button className="button" onClick={() => setClosed(new Set())}>
          Expand all
        </button>
        <button className="button" onClick={() => setClosed(new Set(fields.map((f) => f.key)))}>
          Collapse all
        </button>
      </div>
      <nav className="row field-outline" aria-label="Fields">
        {fields.map((f) => (
          <button key={f.key} className="chip" onClick={() => jump(f.key)}>
            {dirty.has(f.key) && <span className="dot" aria-hidden="true" />}
            {f.label}
            {dirty.has(f.key) && <span className="visually-hidden">, unsaved</span>}
            {f.count !== undefined && (
              <>
                <b aria-hidden="true">{f.count}</b>
                <span className="visually-hidden">, ~{f.count} tokens</span>
              </>
            )}
          </button>
        ))}
      </nav>
      {fields.map((f) => (
        <Section
          key={f.key}
          id={id(f.key)}
          label={f.label}
          dirty={dirty.has(f.key)}
          count={f.count}
          lock={lock(f.key)}
          extra={f.extra}
          open={!closed.has(f.key)}
          onToggle={(open) => toggle(f.key, open)}
        >
          {f.body}
        </Section>
      ))}
    </>
  );
}
