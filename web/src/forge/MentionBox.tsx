import { useQueries, useQuery } from "@tanstack/react-query";
import { useId, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { Mirrored } from "../card/MacroTextarea";
import { mentionsIn, mentionTypes, suggestAt, type Mention, type Picked } from "./mentions";

/** Instruction is the text of an instruction or concept box and the names picked into it. */
export type Instruction = { text: string; picked: Picked[] };

export const emptyInstruction: Instruction = { text: "", picked: [] };

const typeOf = (key: string) => mentionTypes.find((t) => t.key === key)!;

// ponytail: a typed name resolves among the first 100 names containing its first word; pick it from the list past that.
const lookupSize = 100;

/**
 * useMentions resolves the mentions in an instruction, looking typed names up by their first word. instance is the
 * chosen model setup, whose skills a skill mention can name.
 */
export function useMentions({ text, picked }: Instruction, instance: number | undefined): Mention[] {
  const words = [...new Map(mentionsIn(text, picked, () => undefined).filter((m) => m.state === "pending").map((m) => [`${m.type}/${m.word}`, m])).values()];
  const found = useQueries({
    queries: words.map((m) => ({
      queryKey: ["mention", m.type, m.word, typeOf(m.type).ofInstance ? instance : null],
      queryFn: () => typeOf(m.type).find(m.word, lookupSize, instance),
      staleTime: 30_000,
    })),
  });
  return mentionsIn(text, picked, (type, word) => found[words.findIndex((m) => m.type === type && m.word === word)]?.data);
}

/** blocking is the mentions Run must wait for: unresolved ones. */
export const blocking = (mentions: Mention[]) => mentions.filter((m) => m.state !== "ok");

type Option = { key: string; label: string; hint: string; insert: string; pick?: Picked };

/**
 * MentionBox is an instruction box where @ starts a mention of a card, lorebook, or skill. Picking from the list
 * records the item's ID; unknown and ambiguous mentions are underlined in red. Enter calls onEnter; Shift+Enter adds a
 * line.
 */
export function MentionBox(props: {
  label: string;
  placeholder: string;
  value: Instruction;
  onChange: (v: Instruction) => void;
  mentions: Mention[];
  instance: number | undefined;
  onEnter?: () => void;
  autoFocus?: boolean;
}) {
  const { value, onChange, mentions } = props;
  const area = useRef<HTMLTextAreaElement>(null);
  const [caret, setCaret] = useState(0);
  const [sel, setSel] = useState(0);
  const [dismissed, setDismissed] = useState(-1);
  const found = suggestAt(value.text, caret);
  // Past a resolved mention, as after a pick, the owner has moved on from it.
  const here = mentions.find((m) => m.start === found?.at);
  const past = here?.state === "ok" && caret > here.end;
  const active = found && found.at !== dismissed && !past ? found : null;
  const listId = useId();
  const optionId = (i: number) => `${listId}-${i}`;
  const names = useQuery({
    queryKey: ["mention", "suggest", active?.type?.key, active?.query, active?.type?.ofInstance ? props.instance : null],
    enabled: !!active?.type,
    queryFn: () => active!.type!.find(active!.query, 8, props.instance),
    staleTime: 30_000,
  });
  const options: Option[] = !active
    ? []
    : !active.type
      ? mentionTypes
          .filter((t) => t.key.startsWith(active.query))
          .map((t) => ({ key: t.key, label: `${t.key}/`, hint: `${t.noun}s`, insert: `@${t.key}/` }))
      : (names.data ?? []).map((n) => ({
          key: String(n.id),
          label: n.name,
          hint: n.description ?? (typeof n.id === "number" ? `${active.type!.noun} #${n.id}` : active.type!.noun),
          insert: `@${active.type!.key}/${n.name} `,
          // A skill isn't picked by ID: its name resolves against whichever model setup is chosen when Run is pressed.
          pick: active.type!.ofInstance ? undefined : { ...n, type: active.type!.key },
        }));
  const open = active && options.length > 0 ? active : null;
  const cur = Math.min(sel, options.length - 1);

  // After an insert, the caret goes after it once the new text is in the box.
  const moveTo = useRef<number>(undefined);
  useLayoutEffect(() => {
    if (moveTo.current === undefined) return;
    area.current?.focus();
    area.current?.setSelectionRange(moveTo.current, moveTo.current);
    moveTo.current = undefined;
  });
  const insert = (o: Option) => {
    if (!open) return;
    const text = value.text.slice(0, open.at) + o.insert + value.text.slice(caret);
    moveTo.current = open.at + o.insert.length;
    setCaret(moveTo.current);
    onChange({ text, picked: o.pick ? [...value.picked.filter((p) => !(p.type === o.pick!.type && p.name === o.pick!.name)), o.pick] : value.picked });
    setSel(0);
  };
  const ok = [...new Map(mentions.filter((m) => m.state === "ok").map((m) => [`${m.type}/${m.id}`, m])).values()];
  const remove = (gone: Mention) => {
    let text = value.text;
    for (const m of [...mentions].reverse())
      if (m.type === gone.type && m.id === gone.id) text = text.slice(0, m.start) + text.slice(text[m.end] === " " ? m.end + 1 : m.end);
    onChange({ text, picked: value.picked.filter((p) => !(p.type === gone.type && p.id === gone.id)) });
  };
  // The mention being picked from the list isn't marked wrong yet.
  const shown = mentions.filter((m) => m.start !== open?.at);
  const bad = shown.filter((m) => m.state === "unknown" || m.state === "ambiguous");

  return (
    <div className="forge-mention">
      <Mirrored
        ref={area}
        className="forge-instr"
        aria-label={props.label}
        placeholder={props.placeholder}
        autoFocus={props.autoFocus}
        value={value.text}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={!!open}
        aria-controls={listId}
        aria-activedescendant={open ? optionId(cur) : undefined}
        mirror={marked(value.text, shown)}
        onChange={(e) => {
          onChange({ ...value, text: e.target.value });
          setCaret(e.target.selectionStart);
          setSel(0);
        }}
        onSelect={(e) => setCaret(e.currentTarget.selectionStart)}
        onKeyDown={(e) => {
          if (open) {
            if (e.key === "ArrowDown") setSel((cur + 1) % options.length);
            else if (e.key === "ArrowUp") setSel((cur - 1 + options.length) % options.length);
            else if (e.key === "Enter" || e.key === "Tab") insert(options[cur]!);
            else if (e.key === "Escape") {
              setDismissed(open.at);
              e.stopPropagation();
            } else return;
            e.preventDefault();
            return;
          }
          if (e.key === "Enter" && !e.shiftKey && props.onEnter) {
            e.preventDefault();
            props.onEnter();
          }
        }}
      />
      {open && (
        <ul id={listId} className="plain forge-suggest" role="listbox" aria-label="Mentions">
          {options.map((o, i) => (
            <li
              key={o.key}
              id={optionId(i)}
              role="option"
              aria-selected={i === cur}
              className={i === cur ? "on" : undefined}
              // Keep the caret in the box.
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => insert(o)}
            >
              {o.label} <span className="muted">{o.hint}</span>
            </li>
          ))}
        </ul>
      )}
      {bad.map((m) => (
        <p key={m.start} className="error forge-bad">
          @{m.type}/{m.word}:{" "}
          {m.state === "unknown"
            ? typeOf(m.type).ofInstance
              ? `this model setup has no ${typeOf(m.type).noun} by this name.`
              : `no ${typeOf(m.type).noun} has this name.`
            : `more than one ${typeOf(m.type).noun} has this name; pick one from the list.`}
        </p>
      ))}
      {ok.length > 0 && (
        <div className="forge-chips" aria-label="Context">
          {ok.map((m) => (
            <span key={`${m.type}/${m.id}`} className="forge-chip">
              {m.name} <span className="muted">{typeOf(m.type).noun}</span>
              <button type="button" aria-label={`Remove ${m.name}`} onClick={() => remove(m)}>
                ×
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

// marked is text with each mention in a mark classed by its state, for the mirror layer.
function marked(text: string, mentions: Mention[]): ReactNode[] {
  const out: ReactNode[] = [];
  let at = 0;
  for (const m of mentions) {
    out.push(text.slice(at, m.start), <mark key={m.start} className={m.state}>{text.slice(m.start, m.end)}</mark>);
    at = m.end;
  }
  out.push(text.slice(at));
  return out;
}
