import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { useState, type CSSProperties, type MouseEvent } from "react";
import { api, must, type Kind } from "../api/client";
import { searchQuery } from "../api/queries";
import type { components } from "../api/schema";
import { stateLabel } from "../sync/sync";
import {
  countSearch,
  keyRef,
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
  valueLabel,
  type TermKind,
} from "./query";
import { badgeClass, type Names } from "./Tile";

type Search = components["schemas"]["Query"];
type FacetLists = components["schemas"]["Facets"];
type Row = { value: string; label: string; cards: number; dot?: string };

const titles: Record<Exclude<TermKind, "value">, string> = { target: "Target", state: "State", tag: "Tags", source: "Sources", collection: "Collections", score: "Scores" };

type EnrichmentKey = components["schemas"]["EnrichmentKey"];
type Enricher = components["schemas"]["EnrichmentInstance"];

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
  const search = (body: Search, enabled: boolean) => ({ ...searchQuery(kind, body), placeholderData: keepPreviousData, enabled });
  const faceted = (["target", "tag", "source", "collection"] as const).filter((s) => sections.includes(s));
  const facetResults = useQueries({ queries: faceted.map((s) => search(countSearch(q, s), open(s))) });
  const stateResult = useQuery(search(stateCountSearch(q), open("state")));
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
  const stateCounts = stateResult.data && new Map((stateResult.data.facets?.states ?? []).map((s) => [s.is, s.cards]));
  const stateRows = stateCounts
    ? targetStates
        .map((is) => ({ value: is, label: stateLabel(is), cards: stateCounts.get(is) ?? 0, dot: badgeClass[is] }))
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
  /** instance names the enrich instance a value key comes from, when there are several. */
  instance?: string;
  section?: string;
  rows?: Row[];
  q: CardQuery;
  open: boolean;
  onOpen: () => void;
  onChange: (q: CardQuery) => void;
};

function Section({ kind, title, instance, section = kind, rows, q, open, onOpen, onChange }: SectionProps) {
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
          {instance && <> <span className="facet-instance">· {instance}</span></>}
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

/** ValueSection counts one text or list key's values from one enrich instance, each a row to include or exclude. */
function ValueSection({ instance, instanceLabel, k, q, open, onOpen, onChange }: { instance: number; instanceLabel?: string; k: EnrichmentKey; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void }) {
  const ref = keyRef(instance, k.key);
  const body = valueCountSearch(q, ref);
  const res = useQuery({
    queryKey: ["search", "card", body],
    queryFn: async () => must(await api.POST("/cards/search", { body })),
    placeholderData: keepPreviousData,
    enabled: open,
  });
  const section = `value:${ref}`;
  const counted = res.data && (res.data.facets?.enrichments?.[ref] ?? []).map((v) => ({ value: `${ref}=${v.value}`, label: valueLabel(v.value), cards: v.cards }));
  // A selected value with no matches still gets a row, so it can be cleared.
  const rows = counted && [
    ...counted,
    ...q.terms
      .filter((t) => sectionOf(t) === section && !counted.some((r) => r.value === t.value))
      .map((t) => ({ value: t.value, label: valueLabel(t.value.slice(ref.length + 1)), cards: 0 })),
  ];
  return <Section kind="value" title={k.label} instance={instanceLabel} section={section} rows={rows} q={q} open={open} onOpen={onOpen} onChange={onChange} />;
}

/** scoreTermOf is the score term for one instance's key, if any. */
function scoreTermOf(q: CardQuery, instance: number, key: string) {
  return q.terms.find((t) => {
    const s = t.kind === "score" ? parseScore(t.value) : undefined;
    return s?.key === key && s.instance === instance;
  });
}

/** ScoreFilter narrows to cards whose value of one instance's key is in a range, or that have none. */
function ScoreFilter({ instance, k, q, onChange }: { instance: number; k: EnrichmentKey; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const term = scoreTermOf(q, instance, k.key);
  const score = term ? parseScore(term.value) : undefined;
  const [low = 0, high = 100] = k.range ?? [];
  const step = high - low >= 10 ? 1 : (high - low) / 100;
  const [range, setRange] = useState<[number, number]>([score?.gte ?? low, score?.lte ?? high]);
  const [lo, hi] = range;
  // A thumb at either end means no bound on that side.
  const commit = () => {
    const s = { instance, key: k.key, gte: lo > low ? lo : undefined, lte: hi < high ? hi : undefined };
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
          onChange={(e) => onChange(setScore(q, { instance, key: k.key, none: e.target.checked }))} />
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

const scoreKeys = (e: Enricher) => (e.keys ?? []).filter((k) => k.type === "score");

/** ScoreSection is one collapsible section holding a range row per score key, under its instance's name when there are several. */
function ScoreSection({ enrichers, q, open, onOpen, onChange }: { enrichers: Enricher[]; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void }) {
  const picked = q.terms.filter((t) => t.kind === "score").length;
  return (
    <section className="facet-section">
      <h2>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {titles.score}
          {picked > 0 && ` · ${picked}`}
        </button>
      </h2>
      {open &&
        enrichers.map((e) => (
          <div key={e.id} className="score-instance">
            {enrichers.length > 1 && <h3>{e.label}</h3>}
            {/* Keyed by the term, so a cleared chip resets the slider. */}
            {scoreKeys(e).map((k) => (
              <ScoreFilter key={k.key + (scoreTermOf(q, e.id, k.key)?.value ?? "")} instance={e.id} k={k} q={q} onChange={onChange} />
            ))}
          </div>
        ))}
    </section>
  );
}

/**
 * Facets is the filter sidebar: one section per term kind, and one per text or list key of each enrich instance,
 * each counting its values over the other sections' filters.
 */
export function Facets({ kind, q, names, enrichers = [], onChange }: { kind: Kind; q: CardQuery; names: Names; enrichers?: Enricher[]; onChange: (q: CardQuery) => void }) {
  const [collapsed, setCollapsed] = useState<string[]>(readCollapsed);
  const sections = sectionsFor(kind).filter((s) => s !== "collection" || names.collections.size > 0);
  const open = (s: string) => !collapsed.includes(s);
  const rows = useRows(kind, q, names, sections, open);
  const scored = enrichers.filter((e) => scoreKeys(e).length > 0);
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
      {enrichers.flatMap((e) =>
        (e.keys ?? [])
          .filter((k) => k.type === "text" || k.type === "list")
          .map((k) => {
            const section = `value:${keyRef(e.id, k.key)}`;
            const instanceLabel = enrichers.length > 1 ? e.label : undefined;
            return <ValueSection key={section} instance={e.id} instanceLabel={instanceLabel} k={k} q={q} open={open(section)} onOpen={() => flip(section)} onChange={onChange} />;
          }),
      )}
      {scored.length > 0 && <ScoreSection enrichers={scored} q={q} open={open("score")} onOpen={() => flip("score")} onChange={onChange} />}
    </aside>
  );
}
