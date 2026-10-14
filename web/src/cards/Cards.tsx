import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useSearchParams } from "react-router";
import { api, must, route } from "../api/client";
import { instancesQuery, collectionsQuery, pluginsQuery } from "../api/queries";
import { apps } from "../sync/Logo";
import { stateLabel } from "../sync/sync";
import { Facets } from "./Facets";
import { pageLinks } from "./pages";
import { defaultSort, parseQuery, sizes, toggleTerm, toParams, toLorebookSearch, toSearch, type CardQuery, type Sort, type Term } from "./query";
import { emptySelection, pick, target, type Selection } from "./selection";
import { useJobEvents } from "../settings/jobUtil";
import { SelectionBar } from "./SelectionBar";
import { LorebookRow, Row, Tile, type Names } from "./Tile";
import "./Cards.css";

const sortLabels: Record<Sort, string> = { relevance: "Best match", updated: "Updated", name: "Name", created: "Created" };

function termLabel(t: Term, names: Names): string {
  const value =
    t.kind === "collection" ? (names.collections.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "target" ? (names.instances.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "state" ? stateLabel(t.value)
    : t.value;
  return `${t.not ? "not " : ""}${t.kind}: ${value}`;
}

export function Cards({ kind = "card" }: { kind?: "card" | "lorebook" }) {
  const noun = kind === "card" ? "cards" : "lorebooks";
  const Noun = kind === "card" ? "Cards" : "Lorebooks";
  const [params, setParams] = useSearchParams();
  const q = parseQuery(params);
  // The selection belongs to one search: paging keeps it, a new search clears it.
  const key = JSON.stringify([q.text, q.terms]);
  const [stored, setSel] = useState<Selection>(() => emptySelection(key));
  // Reset rather than hide, so going back to an earlier search doesn't revive its picks.
  if (stored.key !== key) setSel(emptySelection(key));
  const sel = stored.key === key ? stored : emptySelection(key);
  const [facetsOpen, setFacetsOpen] = useState(true);
  // The bar unmounts once its job starts, so the page follows the job and reloads when it ends.
  const [watching, setWatching] = useState<number>();
  useJobEvents(watching ?? 0, watching !== undefined);
  const go = (next: CardQuery) => setParams(toParams(next));

  const body = kind === "card" ? toSearch(q) : toLorebookSearch(q);
  const res = useQuery({
    queryKey: ["search", kind, body],
    queryFn: async () => must(await api.POST(route(kind, "/cards/search"), { body })),
    placeholderData: keepPreviousData,
  });
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const collections = useQuery(collectionsQuery);
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.name])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
    logos: apps(instances.data, plugins.data),
  };

  const search = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const text = String(new FormData(e.currentTarget).get("q") ?? "").trim();
    go({ ...q, text, sort: q.sort === defaultSort(q.text) ? defaultSort(text) : q.sort, page: 1 });
  };

  const total = res.data?.total ?? 0;
  const last = Math.max(1, Math.ceil(total / q.size));
  const hits = res.data?.hits ?? [];
  // Linked cards' names and avatars for the lorebook rows, in one search.
  const linkedIds = kind === "lorebook" ? [...new Set(hits.flatMap((h) => h.cards ?? []))].slice(0, 500) : [];
  const linkedBody = { filter: { ids: linkedIds }, fields: ["name", "avatar"], page: { size: 500 } };
  const linked = useQuery({
    queryKey: ["search", "card", linkedBody],
    enabled: linkedIds.length > 0,
    queryFn: async () => must(await api.POST("/cards/search", { body: linkedBody })),
  });
  const linkedCards = new Map((linked.data?.hits ?? []).map((h) => [h.id, h]));
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
          kind={kind}
          query={target(sel, body)}
          onAll={() => setSel({ ...sel, all: true })}
          onClear={() => setSel(emptySelection(key))}
          onStarted={setWatching}
        />
      ) : (
        <div className="toolbar">
          <button className="button" onClick={() => setFacetsOpen(!facetsOpen)} aria-expanded={facetsOpen}>
            Filters
          </button>
          <form role="search" onSubmit={search}>
            <input key={q.text} className="input" type="search" name="q" defaultValue={q.text} placeholder={`Search ${noun}`} />
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
          {kind === "card" && (
            <button className="button" onClick={() => go({ ...q, view: q.view === "grid" ? "list" : "grid" })}>
              {q.view === "grid" ? "List view" : "Grid view"}
            </button>
          )}
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
        {res.data && <p className="total">{total} {total === 1 ? noun.slice(0, -1) : noun}</p>}
        {res.data && q.page > last && (
          <p>
            No {noun} on this page. <button className="button" onClick={() => go({ ...q, page: last })}>Go to page {last}</button>
          </p>
        )}
        {kind === "lorebook" ? (
          <table className="list">
            <tbody>
              {hits.map((h) => (
                <LorebookRow key={h.id} hit={h} names={names} cards={linkedCards} pick={pickFor(h.id)} />
              ))}
            </tbody>
          </table>
        ) : q.view === "grid" ? (
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
            aria-label={`${Noun} per page`}
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
