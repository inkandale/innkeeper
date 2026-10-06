import type { ReactNode } from "react";
import { show, text, tokens, type Data } from "./draft";

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

/** Fields is the card's text, one collapsible section per field. */
export function Fields({ data, dirty, edit }: { data: Data; dirty: Set<string>; edit: (key: string, value: unknown) => void }) {
  const greetings = Array.isArray(data.alternate_greetings) ? data.alternate_greetings.map(text) : [];
  const area = ([key, label]: [string, string]) => (
    <Section key={key} label={label} dirty={dirty.has(key)} count={tokens(text(data[key]))}>
      <textarea className="input grow" aria-label={label} value={text(data[key])} onChange={(e) => edit(key, e.target.value)} />
    </Section>
  );
  return (
    <>
      {before.map(area)}
      <Section label="Alternate greetings" dirty={dirty.has("alternate_greetings")} count={tokens(greetings.join(""))}>
        {greetings.map((g, i) => (
          <div key={i} className="greeting">
            <textarea
              className="input grow"
              aria-label={`Greeting ${i + 1}`}
              value={g}
              onChange={(e) => edit("alternate_greetings", greetings.map((x, j) => (j === i ? e.target.value : x)))}
            />
            <button
              className="button"
              aria-label={`Remove greeting ${i + 1}`}
              onClick={() => edit("alternate_greetings", greetings.filter((_, j) => j !== i))}
            >
              ×
            </button>
          </div>
        ))}
        <button className="button" onClick={() => edit("alternate_greetings", [...greetings, ""])}>
          Add greeting
        </button>
      </Section>
      {after.map(area)}
      <Section label="Extensions">
        <pre className="extensions">{show(data.extensions ?? {})}</pre>
      </Section>
    </>
  );
}
