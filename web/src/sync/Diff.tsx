import type { ReactNode } from "react";
import { thumb } from "../api/client";
import type { components } from "../api/schema";
import { show } from "../card/draft";
import { decidable, jsonKey, sentenceCount, wordDiff, type Comparison, type Run } from "./sync";
import "../card/Card.css";
import "./Sync.css";

type Field = components["schemas"]["Field"];
type Hunk = components["schemas"]["Hunk"];
export type Choices = Record<string, string>;

/** Sides names the two sides a comparison puts against each other. */
type Sides = { library: string; app: string };
const appSides: Sides = { library: "Library", app: "App" };
const sideLabel = (sides: Sides, s: string) => (s === "library" ? sides.library : s === "app" ? sides.app : "Both");

const absent = <em className="muted">absent</em>;

const unescape = (s: string) => s.replace(/\\(.)/g, "$1");

/** entryOf splits an entries hunk ID, entries[<entry>].<field>#n, into an entry label and its field. */
function entryOf(id: string): { entry: string; field: string } {
  const m = /^entries\[((?:\\.|[^\]\\])*)\](?:\.(.*))?#\d+$/.exec(id);
  if (!m) return { entry: id, field: "" };
  const raw = unescape(m[1] ?? "").replace(/~\d+$/, "");
  const entry = raw.startsWith("c:") ? raw.slice(2) : raw.slice(2).split(",").join(", ") || "(no keys)";
  return { entry, field: unescape(m[2] ?? "") };
}

/** Words renders a word diff: what only the library has struck through, what only the app has marked. */
export function Words({ runs }: { runs: Run[] }) {
  return <>{runs.map((r, i) => (r.op === "del" ? <del key={i}>{r.text}</del> : r.op === "ins" ? <ins key={i}>{r.text}</ins> : r.text))}</>;
}

/** HunkText shows one changed hunk, in one column or as a Library | App pair. */
function HunkText({ hunk, inline }: { hunk: Hunk; inline: boolean }) {
  const runs = wordDiff(show(hunk.library), show(hunk.app));
  if (inline)
    return (
      <p className="hunks">
        <Words runs={runs} />
      </p>
    );
  return (
    <>
      <p className="hunks">{hunk.library === undefined ? absent : <Words runs={runs.filter((r) => r.op !== "ins")} />}</p>
      <p className="hunks">{hunk.app === undefined ? absent : <Words runs={runs.filter((r) => r.op !== "del")} />}</p>
    </>
  );
}

function HunkChoice(props: {
  scope: string;
  hunk: Hunk;
  label: string;
  inline: boolean;
  list: boolean;
  sides: Sides;
  choice: string;
  onChoose?: (c: string) => void;
}) {
  const { scope, hunk, label, inline, list, sides, choice, onChoose } = props;
  const keeps = list ? ["library", "app", "both"] : ["library", "app"];
  return (
    <div className={inline ? "hunk-pair inline decide" : "hunk-pair decide"}>
      {label && <span className="muted">{label}</span>}
      <HunkText hunk={hunk} inline={inline} />
      {onChoose && (
        <span className="row" role="radiogroup" aria-label={`Keep for ${hunk.id}`}>
          {keeps.map((s) => (
            <label key={s} className="check">
              <input type="radio" name={`${scope}/${hunk.id}`} checked={choice === s} onChange={() => onChoose(s)} />{" "}
              {sideLabel(sides, s)}
            </label>
          ))}
        </span>
      )}
    </div>
  );
}

/** Unchanged shows a run both sides share, folding prose longer than two sentences. */
function Unchanged({ text, prose }: { text: string; prose: boolean }) {
  const n = prose ? sentenceCount(text) : 0;
  if (n <= 2) return <p className="hunks same">{text}</p>;
  return (
    <details className="unchanged">
      <summary className="muted">⋯ {n} unchanged sentences</summary>
      <p className="hunks same">{text}</p>
    </details>
  );
}

function FieldChoices(props: { scope: string; field: Field; sides: Sides; choices: Choices; onChoose?: (id: string, c: string) => void }) {
  const { scope, field, sides, choices, onChoose } = props;
  const hunks = field.hunks ?? [];
  const differs = hunks.some((h) => h.status !== "same");
  // Prose reads best as one column; values and structure read best side by side.
  const inline = (h: Hunk) => field.kind === "prose" || (field.kind === "entries" && entryOf(h.id).field === "content");
  const paired = hunks.some((h) => h.status !== "same" && !inline(h));
  const label = (h: Hunk) => {
    if (field.kind === "json") return jsonKey(field.path, h.id);
    if (field.kind !== "entries") return "";
    const e = entryOf(h.id);
    return e.field ? `${e.entry} · ${e.field}` : e.entry;
  };
  const out: ReactNode[] = [];
  let folded: Hunk[] = [];
  // A JSON field's unchanged keys fold into one line, each keeping its label.
  const fold = () => {
    if (folded.length)
      out.push(
        <details key={`same:${folded[0]?.id}`} className="unchanged">
          <summary className="muted">
            {folded.length} unchanged {folded.length === 1 ? "key" : "keys"}
          </summary>
          {folded.map((h) => (
            <p key={h.id} className="hunks same">
              <span className="hunk-key">{label(h)}</span> {show(h.library)}
            </p>
          ))}
        </details>,
      );
    folded = [];
  };
  for (const h of hunks) {
    if (h.status === "same" && field.kind === "json") {
      folded.push(h);
      continue;
    }
    fold();
    if (h.status === "same") out.push(<Unchanged key={h.id} text={show(h.library)} prose={inline(h)} />);
    else if (decidable(h))
      out.push(
        <HunkChoice
          key={h.id}
          scope={scope}
          hunk={h}
          label={label(h)}
          inline={inline(h)}
          list={field.kind === "list"}
          sides={sides}
          choice={choices[h.id] ?? "library"}
          onChoose={onChoose && ((c) => onChoose(h.id, c))}
        />,
      );
    else
      out.push(
        <div key={h.id} className={inline(h) ? "hunk-pair inline" : "hunk-pair"}>
          <span className="muted">
            {label(h) && `${label(h)} · `}
            {h.status === "auto_app" ? "App's change, merged" : "Library's change, kept"}
          </span>
          <HunkText hunk={h} inline={inline(h)} />
        </div>,
      );
  }
  fold();
  return (
    <details className="field" open={differs}>
      <summary>
        {field.path}
        {!differs && <span className="muted"> · same</span>}
      </summary>
      {paired && (
        <div className="hunk-pair hunk-head">
          <h4>{sides.library}</h4>
          <h4>{sides.app}</h4>
        </div>
      )}
      {out}
    </details>
  );
}

/** Diff lists a comparison field by field, with a choice on each hunk the owner must decide; without onChoose it only shows them. */
export function Diff(props: {
  scope: string;
  cmp: Pick<Comparison, "fields" | "avatar">;
  sides?: Sides;
  choices?: Choices;
  onChoose?: (id: string, c: string) => void;
}) {
  const { scope, cmp, sides = appSides, choices = {}, onChoose } = props;
  const fields = cmp.fields ?? [];
  const a = cmp.avatar;
  return (
    <>
      {a && (
        <div className="hunk-pair">
          <div>
            <h4>{sides.library} image</h4>
            {a.library ? <img className="history-thumb" src={thumb(a.library, 160)} alt={sides.library} /> : "None"}
          </div>
          <div>
            <h4>{sides.app} image</h4>
            {a.app ? <img className="history-thumb" src={thumb(a.app, 160)} alt={sides.app} /> : "None"}
          </div>
          {onChoose && (
            <span className="row" role="radiogroup" aria-label="Keep for avatar">
              {(a.app ? ["library", "app"] : ["library"]).map((s) => (
                <label key={s} className="check">
                  <input type="radio" name={`${scope}/avatar`} checked={(choices.avatar ?? "library") === s} onChange={() => onChoose("avatar", s)} />{" "}
                  {sideLabel(sides, s)}
                </label>
              ))}
            </span>
          )}
        </div>
      )}
      {!a && fields.every((f) => (f.hunks ?? []).every((h) => h.status === "same")) && <p>No differences.</p>}
      {fields.map((f) => (
        <FieldChoices key={f.path} scope={scope} field={f} sides={sides} choices={choices} onChoose={onChoose} />
      ))}
    </>
  );
}
