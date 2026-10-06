import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { Facets } from "./Facets";
import { pageLinks } from "./pages";
import { defaultSort, parseQuery, sizes, toggleTerm, toParams, toSearch, type CardQuery, type Sort, type Term } from "./query";
import { emptySelection, pick, target, type Selection } from "./selection";
import { SelectionBar } from "./SelectionBar";
import { Row, Tile, type Names } from "./Tile";
import "./Cards.css";

const sortLabels: Record<Sort, string> = { relevance: "Best match", updated: "Updated", name: "Name", created: "Created" };

function termLabel(t: Term, names: Names): string {
  let value = t.value;
  if (t.kind === "collection") value = names.collections.get(Number(t.value)) ?? "#" + t.value;
  if (t.kind === "target") {
    const [id, is] = t.value.split(":");
    value = `${names.instances.get(Number(id)) ?? "#" + id} ${is?.replace("_", " ")}`;
  }
  return `${t.not ? "not " : ""}${t.kind}: ${value}`;
}

export function Cards() {
  const [params, setParams] = useSearchParams();
  const q = parseQuery(params);
  // The selection belongs to one search: paging keeps it, a new search clears it.
  const key = JSON.stringify([q.text, q.terms]);
  const [stored, setSel] = useState<Selection>(() => emptySelection(key));
  // Reset rather than hide, so going back to an earlier search doesn't revive its picks.
  if (stored.key !== key) setSel(emptySelection(key));
  const sel = stored.key === key ? stored : emptySelection(key);
  const [facetsOpen, setFacetsOpen] = useState(true);
  const go = (next: CardQuery) => setParams(toParams(next));

  const body = toSearch(q);
  const res = useQuery({
    queryKey: ["search", body],
    queryFn: async () => must(await api.POST("/search", { body })),
    placeholderData: keepPreviousData,
  });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const collections = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.name])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
  };

  const search = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const text = String(new FormData(e.currentTarget).get("q") ?? "").trim();
    go({ ...q, text, sort: q.sort === defaultSort(q.text) ? defaultSort(text) : q.sort, page: 1 });
  };

  const total = res.data?.total ?? 0;
  const last = Math.max(1, Math.ceil(total / q.size));
  const hits = res.data?.hits ?? [];
  const pageIds = hits.map((h) => h.id);
  const pickFor = (id: number) => ({
    selected: sel.all || sel.ids.includes(id),
    locked: sel.all,
    onPick: (shift: boolean) => setSel(pick(sel, id, shift, pageIds)),
  });
  const picking = sel.all || sel.ids.length > 0;

  return (
    <div className={facetsOpen ? "cards" : "cards no-facets"}>
      {facetsOpen && <Facets facets={res.data?.facets} names={names} q={q} onToggle={(t) => go(toggleTerm(q, t))} />}
      {picking ? (
        <SelectionBar
          count={sel.ids.length}
          all={sel.all}
          total={total}
          query={target(sel, body)}
          onAll={() => setSel({ ...sel, all: true })}
          onClear={() => setSel(emptySelection(key))}
        />
      ) : (
        <div className="toolbar">
          <button className="button" onClick={() => setFacetsOpen(!facetsOpen)} aria-expanded={facetsOpen}>
            Filters
          </button>
          <form role="search" onSubmit={search}>
            <input key={q.text} className="input" type="search" name="q" defaultValue={q.text} placeholder="Search cards" />
          </form>
          <select
            className="input"
            aria-label="Sort"
            value={q.sort}
            onChange={(e) => go({ ...q, sort: e.target.value as Sort, page: 1 })}
          >
            {(Object.keys(sortLabels) as Sort[])
              .filter((s) => s !== "relevance" || q.text || q.sort === "relevance")
              .map((s) => (
                <option key={s} value={s}>
                  {sortLabels[s]}
                </option>
              ))}
          </select>
          <button className="button" onClick={() => go({ ...q, view: q.view === "grid" ? "list" : "grid" })}>
            {q.view === "grid" ? "List view" : "Grid view"}
          </button>
          <div className="chips">
            {q.terms.map((t) => (
              <button
                key={`${t.not ? "-" : ""}${t.kind}:${t.value}`}
                className={t.not ? "chip excluded" : "chip"}
                aria-label={`Remove ${termLabel(t, names)}`}
                onClick={() => go(toggleTerm(q, t))}
              >
                {termLabel(t, names)} ×
              </button>
            ))}
          </div>
        </div>
      )}
      <div className="results">
        {res.isError && <p className="error">{res.error.message}</p>}
        {res.data && <p className="total">{total} cards</p>}
        {res.data && q.page > last && (
          <p>
            No cards on this page. <button className="button" onClick={() => go({ ...q, page: last })}>Go to page {last}</button>
          </p>
        )}
        {q.view === "grid" ? (
          <div className="grid">
            {hits.map((h) => (
              <Tile key={h.id} hit={h} names={names} pick={pickFor(h.id)} />
            ))}
          </div>
        ) : (
          <table className="list">
            <tbody>
              {hits.map((h) => (
                <Row key={h.id} hit={h} names={names} pick={pickFor(h.id)} />
              ))}
            </tbody>
          </table>
        )}
        <nav className="pager" aria-label="Pages">
          {pageLinks(q.page, last).map((p, i) =>
            p === "…" ? (
              <span key={"gap" + i}>…</span>
            ) : (
              <button
                key={p}
                className={p === q.page ? "button primary" : "button"}
                aria-current={p === q.page ? "page" : undefined}
                onClick={(e) => {
                  go({ ...q, page: p });
                  e.currentTarget.closest(".main")?.scrollTo?.(0, 0);
                }}
              >
                {p}
              </button>
            ),
          )}
          <select
            className="input"
            aria-label="Cards per page"
            value={q.size}
            onChange={(e) => go({ ...q, size: Number(e.target.value), page: 1 })}
          >
            {sizes.map((s) => (
              <option key={s} value={s}>
                {s} per page
              </option>
            ))}
          </select>
        </nav>
      </div>
    </div>
  );
}
