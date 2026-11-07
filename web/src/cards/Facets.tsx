import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { useState, type CSSProperties, type MouseEvent } from "react";
import { type Kind } from "../api/client";
import { searchQuery } from "../api/queries";
import type { components } from "../api/schema";
import { stateLabel } from "../sync/sync";
import {
  countSearch,
  keyRef,
  matchesAll,
  onlyTerm,
  parseScore,
  sameValue,
  scoreTerm,
  sectionOf,
  sectionsFor,
  type FacetKind,
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
/** approx marks a count from a sample of the matches. */
type Row = { value: string; label: string; cards: number; dot?: string; approx?: boolean };

const titles: Record<FacetKind, string> = { target: "App", state: "State", tag: "Tags", source: "Sources", collection: "Collections" };

type BrowseKey = components["schemas"]["BrowseKey"];
type ValueCount = components["schemas"]["ValueCount"];

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

/** countQuery runs a count search, again every few seconds while its counts come from a sample, until the server has exact ones. */
const countQuery = (kind: Kind, body: Search, enabled = true) => ({
  ...searchQuery(kind, body),
  placeholderData: keepPreviousData,
  enabled,
  refetchInterval: (query: { state: { data?: { facets?: { approximate?: boolean } | null } } }) => (query.state.data?.facets?.approximate ? 3000 : false),
});

const approxCount = new Intl.NumberFormat(undefined, { notation: "compact", maximumFractionDigits: 1 });

/** useRows runs each open section's count searches and turns the results into rows, selected values included. */
function useRows(kind: Kind, q: CardQuery, names: Names, sections: TermKind[], open: (s: TermKind) => boolean): Partial<Record<TermKind, Row[]>> {
  const faceted = (["target", "tag", "source", "collection"] as const).filter((s) => sections.includes(s));
  const facetResults = useQueries({ queries: faceted.map((s) => countQuery(kind, countSearch(q, s), open(s))) });
  const stateResult = useQuery(countQuery(kind, stateCountSearch(q), open("state")));
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
  const approx = new Map<TermKind, boolean | undefined>([...results].map(([s, data]) => [s, data?.facets?.approximate]));
  approx.set("state", stateResult.data?.facets?.approximate);
  const marked = (s: TermKind, rows: Row[] | undefined) => (approx.get(s) ? rows?.map((r) => ({ ...r, approx: true })) : rows);
  return {
    target: marked("target", withPicked("target", targetRows, instanceLabel)),
    state: marked("state", stateRows),
    tag: marked("tag", withPicked("tag", listOf("tag", (f) => f.tags)?.map((t) => ({ value: t.name, label: t.name, cards: t.cards })), (v) => v)),
    source: marked("source", withPicked("source", listOf("source", (f) => f.sources)?.map((s) => ({ value: s.kind, label: s.kind, cards: s.cards })), (v) => v)),
    collection: marked("collection", withPicked(
      "collection",
      listOf("collection", (f) => f.collections)?.map((c) => ({ value: String(c.id), label: c.name, cards: c.cards })),
      (v) => names.collections.get(Number(v)) ?? "#" + v,
    )),
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
        <span className="count">{row.approx ? "~" + approxCount.format(row.cards) : row.cards.toLocaleString()}</span>
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
  kind: FacetKind | "value";
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
  const [more, setMore] = useState(false);
  const [find, setFind] = useState("");
  const name = title ?? (kind === "value" ? "" : titles[kind]);
  // A value key's section sits inside its enrichment section's heading.
  const Heading = kind === "value" ? "h3" : "h2";
  const picked = q.terms.filter((t) => sectionOf(t) === section).length;
  const included = q.terms.filter((t) => sectionOf(t) === section && !t.not).length;
  const all = q.all.includes(section);
  const browse = kind === "tag" || kind === "value";
  const findLabel = kind === "tag" ? "Find a tag" : `Find in ${name}`;
  const needle = find.trim().toLowerCase();
  const isPicked = (r: Row) => q.terms.some((t) => t.kind === kind && sameValue(kind, t.value, r.value));
  const listed = rows ?? [];
  const shown = !browse
    ? listed
    : needle
      ? listed.filter((r) => r.label.toLowerCase().includes(needle))
      : more
        ? listed
        : listed.filter((r, i) => i < 10 || isPicked(r));
  if (open && rows?.length === 0 && picked === 0) return null;
  return (
    <section className="facet-section">
      <Heading>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {name}
          {instance && <> <span className="facet-instance">· {instance}</span></>}
          {picked > 0 && ` · ${picked}`}
        </button>
      </Heading>
      {open && (
        <>
          {matchesAll(section) && included > 0 && (
            <div className="facet-match" role="group" aria-label={`${name} match`}>
              {(["any", "all"] as const).map((m) => (
                <button key={m} aria-pressed={(m === "all") === all}
                  onClick={() => onChange({ ...q, all: m === "all" ? [...q.all, section] : q.all.filter((s) => s !== section), page: 1 })}
                  disabled={(m === "all") === all}>
                  {m === "any" ? "Match any" : "Match all"}
                </button>
              ))}
            </div>
          )}
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
            <button className="facet-more" onClick={() => setMore(!more)}>
              {more ? "Show fewer" : `Show all ${listed.length}`}
            </button>
          )}
        </>
      )}
    </section>
  );
}

/** groups are the enrichment sections, in sidebar order; a key that names none goes under Other. */
const groups = [
  ["score", "Score"],
  ["popularity", "Popularity"],
  ["kind", "Kind"],
  ["warnings", "Content warnings"],
  ["problems", "Problems"],
  ["", "Other"],
] as const;

/** groupOf names a group as the collapsed list stores it. */
const groupOf = (section?: string) => `group:${section || "other"}`;
const isValueKey = (b: BrowseKey) => b.type === "text" || b.type === "list";

/** useValueCounts counts each open value section's values, ref to counts. Sections with nothing picked share one search, as their filters are the same. */
function useValueCounts(q: CardQuery, refs: string[]): Map<string, { counts?: ValueCount[]; approx?: boolean }> {
  const picked = (ref: string) => q.terms.some((t) => sectionOf(t) === `value:${ref}`);
  const shared = refs.filter((ref) => !picked(ref));
  const groupsOfRefs = [...(shared.length ? [shared] : []), ...refs.filter(picked).map((ref) => [ref])];
  const results = useQueries({ queries: groupsOfRefs.map((g) => countQuery("card", valueCountSearch(q, g))) });
  return new Map(
    groupsOfRefs.flatMap((g, i) => {
      const facets = results[i]?.data && (results[i].data.facets ?? {});
      return g.map((ref) => [ref, { counts: facets && (facets.enrichments?.[ref] ?? []), approx: facets?.approximate }] as const);
    }),
  );
}

/** ValueSection lists one text or list key's values, each a row to include or exclude. */
function ValueSection({ b, instanceLabel, counts, approx, q, open, onOpen, onChange }: { b: BrowseKey; instanceLabel?: string; counts?: ValueCount[]; approx?: boolean; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void }) {
  const ref = keyRef(b.instance, b.key);
  const section = `value:${ref}`;
  const counted = counts?.map((v) => ({ value: `${ref}=${v.value}`, label: valueLabel(v.value), cards: v.cards, approx }));
  // A selected value with no matches still gets a row, so it can be cleared.
  const rows = counted && [
    ...counted,
    ...q.terms
      .filter((t) => sectionOf(t) === section && !counted.some((r) => r.value === t.value))
      .map((t) => ({ value: t.value, label: valueLabel(t.value.slice(ref.length + 1)), cards: 0 })),
  ];
  return <Section kind="value" title={b.label} instance={instanceLabel} section={section} rows={rows} q={q} open={open} onOpen={onOpen} onChange={onChange} />;
}

/** scoreTermOf is the score term for a key, if any: an instance's, or a merged key's, which names none. */
function scoreTermOf(q: CardQuery, instance: number | undefined, key: string) {
  return q.terms.find((t) => {
    const s = t.kind === "score" ? parseScore(t.value) : undefined;
    return s?.key === key && s.instance === instance;
  });
}

/** ScoreFilter narrows to cards whose value of a score key is in a range, or that have none. */
function ScoreFilter({ b, q, onChange }: { b: BrowseKey; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const at = { instance: b.instance, key: b.key };
  const term = scoreTermOf(q, b.instance, b.key);
  const score = term ? parseScore(term.value) : undefined;
  const [low = 0, high = 100] = b.range ?? [];
  const step = high - low >= 10 ? 1 : (high - low) / 100;
  const [range, setRange] = useState<[number, number]>([score?.gte ?? low, score?.lte ?? high]);
  const [lo, hi] = range;
  // A thumb at either end means no bound on that side.
  const commit = () => {
    const s = { ...at, gte: lo > low ? lo : undefined, lte: hi < high ? hi : undefined };
    if (scoreTerm(s)?.value !== term?.value) onChange(setScore(q, s));
  };
  const pct = (v: number) => `${((v - low) / (high - low)) * 100}%`;
  const thumb = { disabled: score?.none, min: low, max: high, step, onPointerUp: commit, onKeyUp: commit };
  return (
    <div className={"score-filter" + (term ? " included" : "") + (score?.none ? " none" : "")}>
      <span className="score-name" title={b.label}>{b.label}</span>
      <span className="score-value">{!score?.none && (lo > low || hi < high) && `${lo}–${hi}`}</span>
      <label className="score-none" title="Not scored">
        <input type="checkbox" aria-label={`${b.label} not evaluated`} checked={!!score?.none}
          onChange={(e) => onChange(setScore(q, { ...at, none: e.target.checked }))} />
        none
      </label>
      <div className="score-range" style={{ "--lo": pct(lo), "--hi": pct(hi) } as CSSProperties}>
        {/* At the top end the min thumb must sit above the max one, or it can't be dragged back down. */}
        <input type="range" aria-label={`${b.label} at least`} value={lo} {...thumb} style={lo === high ? { zIndex: 2 } : undefined}
          onChange={(e) => setRange([Math.min(Number(e.target.value), hi), hi])} />
        <input type="range" aria-label={`${b.label} at most`} value={hi} {...thumb}
          onChange={(e) => setRange([lo, Math.max(Number(e.target.value), lo)])} />
      </div>
    </div>
  );
}

/**
 * Facets is the filter sidebar: one section per term kind, then the browsable enrichment keys under their section headings,
 * each counting its values over the other sections' filters.
 */
export function Facets({ kind, q, names, browse = [], onChange }: { kind: Kind; q: CardQuery; names: Names; browse?: BrowseKey[]; onChange: (q: CardQuery) => void }) {
  const [collapsed, setCollapsed] = useState<string[]>(readCollapsed);
  const sections = sectionsFor(kind).filter((s) => s !== "collection" || names.collections.size > 0);
  const open = (s: string) => !collapsed.includes(s);
  const rows = useRows(kind, q, names, sections, open);
  const listed = groups.flatMap(([id]) => browse.filter((b) => (b.section ?? "") === id));
  const valueRefs = listed
    .filter((b) => isValueKey(b) && open(groupOf(b.section)))
    .map((b) => keyRef(b.instance, b.key))
    .filter((ref) => open(`value:${ref}`));
  const valueCounts = useValueCounts(q, valueRefs);
  const flip = (s: string) => {
    const next = open(s) ? [...collapsed, s] : collapsed.filter((x) => x !== s);
    setCollapsed(next);
    saveCollapsed(next);
  };
  return (
    <aside className="facets" aria-label="Filters">
      <label className="check" title="Shows only cards or lorebooks with a source another one also has, such as the same page imported twice. A fork doesn't count.">
        <input type="checkbox" checked={q.sharesSource} onChange={(e) => onChange({ ...q, sharesSource: e.target.checked, page: 1 })} /> Shares a source
      </label>
      {sections.map((s) => (
        <Section key={s} kind={s} rows={rows[s]} q={q} open={open(s)} onOpen={() => flip(s)} onChange={onChange} />
      ))}
      {groups.map(([id, title]) => {
        const keys = browse.filter((b) => (b.section ?? "") === id);
        if (!keys.length) return null;
        const group = groupOf(id);
        // Counted so a collapsed group still shows it holds active filters.
        const picked = keys.reduce(
          (n, b) => n + q.terms.filter((t) => sectionOf(t) === `value:${keyRef(b.instance, b.key)}` || t === scoreTermOf(q, b.instance, b.key)).length,
          0,
        );
        return (
          <section key={group} className="facet-section facet-group">
            <h2>
              <button className="facet-head" aria-expanded={open(group)} onClick={() => flip(group)}>
                {title}
                {picked > 0 && ` · ${picked}`}
              </button>
            </h2>
            {open(group) &&
              keys.map((b) => {
                const ref = keyRef(b.instance, b.key);
                // Keyed by the term, so a cleared chip resets the slider.
                if (!isValueKey(b)) return <ScoreFilter key={ref + (scoreTermOf(q, b.instance, b.key)?.value ?? "")} b={b} q={q} onChange={onChange} />;
                const section = `value:${ref}`;
                const instanceLabel = b.instance ? names.instances.get(b.instance) : undefined;
                return <ValueSection key={section} b={b} instanceLabel={instanceLabel} {...valueCounts.get(ref)} q={q} open={open(section)} onOpen={() => flip(section)} onChange={onChange} />;
              })}
          </section>
        );
      })}
    </aside>
  );
}
