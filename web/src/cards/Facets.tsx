import { keepPreviousData, useQueries } from "@tanstack/react-query";
import { useState, type MouseEvent } from "react";
import { api, must, route, type Kind } from "../api/client";
import type { components } from "../api/schema";
import { stateLabel } from "../sync/sync";
import { countSearch, onlyTerm, sectionsFor, stateCountSearch, targetStates, toggleTerm, type CardQuery, type TermKind } from "./query";
import { badgeClass, type Names } from "./Tile";

type Search = components["schemas"]["Query"];
type Row = { value: string; label: string; cards: number; dot?: string };

const titles: Record<TermKind, string> = { target: "Target", state: "State", tag: "Tags", source: "Sources", collection: "Collections" };

/** useRows runs each open section's count searches and turns the results into rows, selected values included. */
function useRows(kind: Kind, q: CardQuery, names: Names, open: (s: TermKind) => boolean): Partial<Record<TermKind, Row[]>> {
  const search = (body: Search, enabled: boolean) => ({
    queryKey: ["search", kind, body],
    queryFn: async () => must(await api.POST(route(kind, "/cards/search"), { body })),
    placeholderData: keepPreviousData,
    enabled,
  });
  const faceted = (["target", "tag", "source", "collection"] as const).filter((s) => sectionsFor(kind).includes(s));
  const facetResults = useQueries({ queries: faceted.map((s) => search(countSearch(q, s), open(s))) });
  const stateResults = useQueries({ queries: targetStates.map((is) => search(stateCountSearch(q, is), open("state"))) });
  const facets = (s: TermKind) => facetResults[faceted.indexOf(s as (typeof faceted)[number])]?.data?.facets;
  const picked = (s: TermKind) => q.terms.filter((t) => t.kind === s).map((t) => t.value);

  // A selected value with no matches still gets a row, so it can be cleared.
  const withPicked = (s: TermKind, rows: Row[] | undefined, label: (v: string) => string): Row[] | undefined =>
    rows && [...rows, ...picked(s).filter((v) => !rows.some((r) => r.value === v)).map((v) => ({ value: v, label: label(v), cards: 0 }))];

  const states = picked("state");
  const instanceLabel = (v: string) => names.instances.get(Number(v)) ?? "#" + v;
  const targetFacet = facets("target")?.targets;
  const byInstance = new Map<number, number>();
  for (const t of targetFacet ?? [])
    if (!states.length || states.includes(t.is)) byInstance.set(t.instance, (byInstance.get(t.instance) ?? 0) + t.cards);
  const targetRows = targetFacet ? [...byInstance].map(([id, cards]) => ({ value: String(id), label: instanceLabel(String(id)), cards })) : undefined;
  const stateRows = stateResults.every((r) => r.data)
    ? targetStates
        .map((is, i) => ({ value: is, label: stateLabel(is), cards: stateResults[i]?.data?.total ?? 0, dot: badgeClass[is] }))
        .filter((r) => r.cards > 0 || states.includes(r.value))
    : undefined;
  return {
    target: withPicked("target", targetRows, instanceLabel),
    state: stateRows,
    tag: withPicked("tag", facets("tag")?.tags?.map((t) => ({ value: t.name, label: t.name, cards: t.cards })), (v) => v),
    source: withPicked("source", facets("source")?.sources?.map((s) => ({ value: s.kind, label: s.kind, cards: s.cards })), (v) => v),
    collection: withPicked(
      "collection",
      facets("collection")?.collections?.map((c) => ({ value: String(c.id), label: c.name, cards: c.cards })),
      (v) => names.collections.get(Number(v)) ?? "#" + v,
    ),
  };
}

function FacetRow({ kind, row, q, onChange }: { kind: TermKind; row: Row; q: CardQuery; onChange: (q: CardQuery) => void }) {
  const term = q.terms.find((t) => t.kind === kind && t.value === row.value);
  const toggle = (not: boolean) => onChange(toggleTerm(q, { kind, value: row.value, not }));
  return (
    <li className={"facet" + (term ? (term.not ? " excluded" : " included") : "")}>
      <button className="facet-pick" aria-pressed={!!term} onClick={(e: MouseEvent) => toggle(e.shiftKey)}>
        {row.dot && <span className={"badge " + row.dot} />}
        <span className="facet-label">
          {term?.not ? "not " : ""}
          {row.label}
        </span>{" "}
        <span className="count">{row.cards}</span>
      </button>
      <button className="facet-act" aria-label={`Only ${row.label}`} onClick={() => onChange(onlyTerm(q, { kind, value: row.value, not: false }))}>
        only
      </button>
      <button className="facet-act" aria-label={`Exclude ${row.label}`} onClick={() => toggle(true)}>
        −
      </button>
    </li>
  );
}

type SectionProps = { kind: TermKind; rows?: Row[]; q: CardQuery; open: boolean; onOpen: () => void; onChange: (q: CardQuery) => void };

function Section({ kind, rows, q, open, onOpen, onChange }: SectionProps) {
  const picked = q.terms.filter((t) => t.kind === kind).length;
  if (open && rows?.length === 0 && picked === 0) return null;
  return (
    <section className="facet-section">
      <h2>
        <button className="facet-head" aria-expanded={open} onClick={onOpen}>
          {titles[kind]}
          {picked > 0 && ` · ${picked}`}
        </button>
      </h2>
      {open && (
        <ul className="facet-list">
          {(rows ?? []).map((r) => (
            <FacetRow key={r.value} kind={kind} row={r} q={q} onChange={onChange} />
          ))}
        </ul>
      )}
    </section>
  );
}

/** Facets is the filter sidebar: one section per term kind, each counting its values over the other sections' filters. */
export function Facets({ kind, q, names, onChange }: { kind: Kind; q: CardQuery; names: Names; onChange: (q: CardQuery) => void }) {
  const [collapsed, setCollapsed] = useState<TermKind[]>([]);
  const open = (s: TermKind) => !collapsed.includes(s);
  const rows = useRows(kind, q, names, open);
  const flip = (s: TermKind) => setCollapsed(open(s) ? [...collapsed, s] : collapsed.filter((x) => x !== s));
  return (
    <aside className="facets" aria-label="Filters">
      {sectionsFor(kind)
        .filter((s) => s !== "collection" || names.collections.size > 0)
        .map((s) => (
          <Section key={s} kind={s} rows={rows[s]} q={q} open={open(s)} onOpen={() => flip(s)} onChange={onChange} />
        ))}
    </aside>
  );
}
