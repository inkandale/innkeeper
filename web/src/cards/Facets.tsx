import type { MouseEvent } from "react";
import type { components } from "../api/schema";
import type { CardQuery, Term } from "./query";
import type { Names } from "./Tile";

type FacetData = components["schemas"]["Facets"];
type Value = { term: Omit<Term, "not">; label: string; cards: number };

function Section({ title, values, q, onToggle }: { title: string; values: Value[]; q: CardQuery; onToggle: (t: Term) => void }) {
  if (values.length === 0) return null;
  return (
    <section className="facet-section">
      <h2>{title}</h2>
      {values.map((v) => {
        const active = q.terms.find((t) => t.kind === v.term.kind && t.value === v.term.value);
        return (
          <button
            key={v.term.value}
            className={"facet" + (active ? (active.not ? " excluded" : " included") : "")}
            onClick={(e: MouseEvent) => onToggle({ ...v.term, not: e.shiftKey })}
          >
            <span>{v.label}</span>
            <span className="count">{v.cards}</span>
          </button>
        );
      })}
    </section>
  );
}

export function Facets({ facets, names, q, onToggle }: { facets?: FacetData; names: Names; q: CardQuery; onToggle: (t: Term) => void }) {
  const status = (s: string) => s.replace("_", " ");
  return (
    <aside className="facets" aria-label="Filters">
      <Section
        title="Sync"
        q={q}
        onToggle={onToggle}
        values={(facets?.targets ?? []).map((t) => ({
          term: { kind: "target", value: `${t.instance}:${t.is}` },
          label: `${names.instances.get(t.instance) ?? "#" + t.instance}: ${status(t.is)}`,
          cards: t.cards,
        }))}
      />
      <Section
        title="Tags"
        q={q}
        onToggle={onToggle}
        values={(facets?.tags ?? []).map((t) => ({ term: { kind: "tag", value: t.name }, label: t.name, cards: t.cards }))}
      />
      <Section
        title="Collections"
        q={q}
        onToggle={onToggle}
        values={(facets?.collections ?? []).map((c) => ({
          term: { kind: "collection", value: String(c.id) },
          label: c.name,
          cards: c.cards,
        }))}
      />
      <Section
        title="Sources"
        q={q}
        onToggle={onToggle}
        values={(facets?.sources ?? []).map((s) => ({ term: { kind: "source", value: s.kind }, label: s.kind, cards: s.cards }))}
      />
    </aside>
  );
}
