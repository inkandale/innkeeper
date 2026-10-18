import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { useState, type CSSProperties, type MouseEvent } from "react";
import { api, must, route, type Kind } from "../api/client";
import type { components } from "../api/schema";
import { stateLabel } from "../sync/sync";
import {
  countSearch,
  onlyTerm,
  parseScore,
  sameValue,
  scoreTerm,
  sectionOf,
  sectionsFor,
  setScore,
  stateCountSearch,
  targetStates,
  toggleTerm,
  valueCountSearch,
  type CardQuery,
  type TermKind,
} from "./query";
import { badgeClass, type Names } from "./Tile";

type Search = components["schemas"]["Query"];
type FacetLists = components["schemas"]["Facets"];
type Row = { value: string; label: string; cards: number; dot?: string };

const titles: Record<Exclude<TermKind, "value">, string> = { target: "Target", state: "State", tag: "Tags", source: "Sources", collection: "Collections", score: "Scores" };

type EnrichmentKey = components["schemas"]["EnrichmentKey"];

const collapsedKey = "innkeeper.facets.collapsed";

// Storage can throw or be empty in private windows; the sidebar then just starts open.
// Sections are named as sectionOf names them: a kind, or value:<key>.
function readCollapsed(): string[] {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(collapsedKey) ?? "[]");
    return Array.isArray(v) ? v.filter((s): s is string => typeof s === "string") : [];
  } catch {
    return [];
  }
}

function saveCollapsed(v: string[]) {
  try {
    localStorage.setItem(collapsedKey, JSON.stringify(v));
  } catch {
    // A layout preference that can't be saved isn't worth an error.
  }
}

/** useRows runs each open section's count searches and turns the results into rows, selected values included. */
function useRows(kind: Kind, q: CardQuery, names: Names, sections: TermKind[], open: (s: TermKind) => boolean): Partial<Record<TermKind, Row[]>> {
  const search = (body: Search, enabled: boolean) => ({
    queryKey: ["search", kind, body],
    queryFn: async () => must(await api.POST(route(kind, "/cards/search"), { body })),
    placeholderData: keepPreviousData,
    enabled,
  });
  const faceted = (["target", "tag", "source", "collection"] as const).filter((s) => sections.includes(s));
  const facetResults = useQueries({ queries: faceted.map((s) => search(countSearch(q, s), open(s))) });
  const stateResults = useQueries({ queries: targetStates.map((is) => search(stateCountSearch(q, is), open("state"))) });
  const results = new Map(faceted.map((s, i) => [s as TermKind, facetResults[i]?.data]));
  // The API leaves empty facet lists out, so a section is loading only while its search has no data.
  const listOf = <T,>(s: TermKind, list: (f: FacetLists) => T[] | null | undefined): T[] | undefined => {
    const data = results.get(s);
    return data && (list(data.facets ?? {}) ?? []);
  };
  const picked = (s: TermKind) => q.terms.filter((t) => t.kind === s).map((t) => t.value);

  // A selected value with no matches still gets a row, so it can be cleared.
  const withPicked = (s: TermKind, rows: Row[] | undefined, label: (v: string) => string): Row[] | undefined =>
    rows && [...rows, ...picked(s).filter((v) => !rows.some((r) => sameValue(s, r.value, v))).map((v) => ({ value: v, label: label(v), cards: 0 }))];

  // An excluded state is already out of the targets facet; only included states narrow the sum.
  const states = q.terms.filter((t) => t.kind === "state" && !t.not).map((t) => t.value);
  const instanceLabel = (v: string) => names.instances.get(Number(v)) ?? "#" + v;
  const targetFacet = listOf("target", (f) => f.targets);
  const byInstance = new Map<number, number>();
  for (const t of targetFacet ?? [])
    if (!states.length || states.includes(t.is)) byInstance.set(t.instance, (byInstance.get(t.instance) ?? 0) + t.cards);
  const targetRows = targetFacet ? [...byInstance].map(([id, cards]) => ({ value: String(id), label: instanceLabel(String(id)), cards })) : undefined;
  const stateRows = stateResults.every((r) => r.data)
    ? targetStates
        .map((is, i) => ({ value: is, label: stateLabel(is), cards: stateResults[i]?.data?.total ?? 0, dot: badgeClass[is] }))
        .filter((r) => r.cards > 0 || picked("state").includes(r.value))
    : undefined;
  return {
    target: withPicked("target", targetRows, instanceLabel),
    state: stateRows,
    tag: withPicked("tag", listOf("tag", (f) => f.tags)?.map((t) => ({ value: t.name, label: t.name, cards: t.cards })), (v) => v),
    source: withPicked("source", listOf("source", (f) => f.sources)?.map((s) => ({ value: s.kind, label: s.kind, cards: s.cards })), (v) => v),
    collection: withPicked(
      "collection",
      listOf("collection", (f) => f.collections)?.map((c) => ({ value: String(c.id), label: c.name, cards: c.cards })),
      (v) => names.collections.get(Number(v)) ?? "#" + v,
    ),
  };
}

function FacetRow({ kind, row, q, onChange }: { kind: TermKind; row: Row; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const term = q.terms.find((t) => t.kind === kind && sameValue(kind, t.value, row.value));
  const toggle = (not: boolean) => onChange(toggleTerm(q, { kind, value: row.value, not }));
  return (
    <li className={"facet" + (term ? (term.not ? " excluded" : " included") : "")}>
      <button className="facet-pick" aria-pressed={!!term && !term.not} onClick={(e: MouseEvent) => toggle(e.shiftKey)}>
        {row.dot && <span className={"badge " + row.dot} />}
        <span className="facet-label">
          {term?.not ? "not " : ""}
          {row.label}
        </span>{" "}
        <span className="count">{row.cards.toLocaleString()}</span>
      </button>
      <button className="facet-act" aria-label={`Only ${row.label}`} onClick={() => onChange(onlyTerm(q, { kind, value: row.value, not: false }))}>
        only
      </button>
      <button className="facet-act" aria-label={`Exclude ${row.label}`} aria-pressed={!!term?.not} onClick={() => toggle(true)}>
        −
      </button>
    </li>
  );
}

type SectionProps = {
  kind: TermKind;
  /** title and section name a value key's section; other kinds take theirs from kind. */
  title?: string;
  section?: string;
  rows?: Row[];
  q: CardQuery;
  open: boolean;
  onOpen: () => void;
  onChange: (q: CardQuery) => void;
};

function Section({ kind, title, section = kind, rows, q, open, onOpen, onChange }: SectionProps) {
  const [all, setAll] = useState(false);
  const [find, setFind] = useState("");
  const name = title ?? titles[kind as Exclude<TermKind, "value">];
  const picked = q.terms.filter((t) => sectionOf(t) === section).length;
  const browse = kind === "tag" || kind === "value";
  const findLabel = kind === "tag" ? "Find a tag" : `Find in ${name}`;
  const needle = find.trim().toLowerCase();
  const isPicked = (r: Row) => q.terms.some((t) => t.kind === kind && sameValue(kind, t.value, r.value));
  const listed = rows ?? [];
  const shown = !browse
    ? listed
    : needle
      ? listed.filter((r) => r.label.toLowerCase().includes(needle))
      : all
        ? listed
        : listed.filter((r, i) => i < 10 || isPicked(r));
  if (open && rows?.length === 0 && picked === 0) return null;
  return (
    <section className="facet-section">
      <h2>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {name}
          {picked > 0 && ` · ${picked}`}
        </button>
      </h2>
      {open && (
        <>
          {/* Tags always get the box; a value key only once it lists more than fits. */}
          {browse && (kind === "tag" || listed.length > 10) && (
            <input className="input facet-find" type="search" aria-label={findLabel} placeholder={findLabel} value={find} onChange={(e) => setFind(e.target.value)} />
          )}
          <ul className="facet-list">
            {shown.map((r) => (
              <FacetRow key={r.value} kind={kind} row={r} q={q} onChange={onChange} />
            ))}
          </ul>
          {browse && !needle && listed.length > 10 && (
            <button className="facet-more" onClick={() => setAll(!all)}>
              {all ? "Show fewer" : `Show all ${listed.length}`}
            </button>
          )}
        </>
      )}
    </section>
  );
}

/** ValueSection counts one text or list key's values from the score instance, each a row to include or exclude. */
function ValueSection({ k, q, open, onOpen, onChange }: { k: EnrichmentKey; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void }) {
  const body = valueCountSearch(q, k.key);
  const res = useQuery({
    queryKey: ["search", "card", body],
    queryFn: async () => must(await api.POST("/cards/search", { body })),
    placeholderData: keepPreviousData,
    enabled: open,
  });
  const section = `value:${k.key}`;
  const counted = res.data && (res.data.facets?.enrichments?.[k.key] ?? []).map((v) => ({ value: `${k.key}=${v.value}`, label: v.value, cards: v.cards }));
  // A selected value with no matches still gets a row, so it can be cleared.
  const rows = counted && [
    ...counted,
    ...q.terms
      .filter((t) => sectionOf(t) === section && !counted.some((r) => r.value === t.value))
      .map((t) => ({ value: t.value, label: t.value.slice(k.key.length + 1), cards: 0 })),
  ];
  return <Section kind="value" title={k.label} section={section} rows={rows} q={q} open={open} onOpen={onOpen} onChange={onChange} />;
}

/** ScoreFilter narrows to cards whose score instance value of key is in a range, or that have none. */
function ScoreFilter({ k, q, onChange }: { k: EnrichmentKey; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const term = q.terms.find((t) => t.kind === "score" && parseScore(t.value)?.key === k.key);
  const score = term ? parseScore(term.value) : undefined;
  const [low = 0, high = 100] = k.range ?? [];
  const step = high - low >= 10 ? 1 : (high - low) / 100;
  const [range, setRange] = useState<[number, number]>([score?.gte ?? low, score?.lte ?? high]);
  const [lo, hi] = range;
  // A thumb at either end means no bound on that side.
  const commit = () => {
    const s = { key: k.key, gte: lo > low ? lo : undefined, lte: hi < high ? hi : undefined };
    if (scoreTerm(s)?.value !== term?.value) onChange(setScore(q, s));
  };
  const pct = (v: number) => `${((v - low) / (high - low)) * 100}%`;
  const thumb = { disabled: score?.none, min: low, max: high, step, onPointerUp: commit, onKeyUp: commit };
  return (
    <div className={"score-filter" + (term ? " included" : "") + (score?.none ? " none" : "")}>
      <span className="score-name" title={k.label}>{k.label}</span>
      <span className="score-value">{!score?.none && (lo > low || hi < high) && `${lo}–${hi}`}</span>
      <label className="score-none" title="Not evaluated">
        <input type="checkbox" aria-label={`${k.label} not evaluated`} checked={!!score?.none}
          onChange={(e) => onChange(setScore(q, { key: k.key, none: e.target.checked }))} />
        none
      </label>
      <div className="score-range" style={{ "--lo": pct(lo), "--hi": pct(hi) } as CSSProperties}>
        {/* At the top end the min thumb must sit above the max one, or it can't be dragged back down. */}
        <input type="range" aria-label={`${k.label} at least`} value={lo} {...thumb} style={lo === high ? { zIndex: 2 } : undefined}
          onChange={(e) => setRange([Math.min(Number(e.target.value), hi), hi])} />
        <input type="range" aria-label={`${k.label} at most`} value={hi} {...thumb}
          onChange={(e) => setRange([lo, Math.max(Number(e.target.value), lo)])} />
      </div>
    </div>
  );
}

/** ScoreSection is one collapsible section holding a range row per score key. */
function ScoreSection({ scores, q, open, onOpen, onChange }: { scores: EnrichmentKey[]; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void }) {
  const picked = q.terms.filter((t) => t.kind === "score").length;
  return (
    <section className="facet-section">
      <h2>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {titles.score}
          {picked > 0 && ` · ${picked}`}
        </button>
      </h2>
      {/* Keyed by the term, so a cleared chip resets the slider. */}
      {open && scores.map((k) => <ScoreFilter key={k.key + (q.terms.find((t) => t.kind === "score" && parseScore(t.value)?.key === k.key)?.value ?? "")} k={k} q={q} onChange={onChange} />)}
    </section>
  );
}

/**
 * Facets is the filter sidebar: one section per term kind, and one per text or list key of the score instance,
 * each counting its values over the other sections' filters.
 */
export function Facets({
  kind,
  q,
  names,
  scores = [],
  values = [],
  onChange,
}: {
  kind: Kind;
  q: CardQuery;
  names: Names;
  scores?: EnrichmentKey[];
  values?: EnrichmentKey[];
  onChange: (q: CardQuery) => void;
}) {
  const [collapsed, setCollapsed] = useState<string[]>(readCollapsed);
  const sections = sectionsFor(kind).filter((s) => s !== "collection" || names.collections.size > 0);
  const open = (s: string) => !collapsed.includes(s);
  const rows = useRows(kind, q, names, sections, open);
  const flip = (s: string) => {
    const next = open(s) ? [...collapsed, s] : collapsed.filter((x) => x !== s);
    setCollapsed(next);
    saveCollapsed(next);
  };
  return (
    <aside className="facets" aria-label="Filters">
      <label className="check">
        <input type="checkbox" checked={q.sharesSource} onChange={(e) => onChange({ ...q, sharesSource: e.target.checked, page: 1 })} /> Shares a source
      </label>
      {sections.map((s) => (
        <Section key={s} kind={s} rows={rows[s]} q={q} open={open(s)} onOpen={() => flip(s)} onChange={onChange} />
      ))}
      {values.map((k) => (
        <ValueSection key={k.key} k={k} q={q} open={open(`value:${k.key}`)} onOpen={() => flip(`value:${k.key}`)} onChange={onChange} />
      ))}
      {scores.length > 0 && <ScoreSection scores={scores} q={q} open={open("score")} onOpen={() => flip("score")} onChange={onChange} />}
    </aside>
  );
}
