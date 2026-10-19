import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { api, must, route } from "../api/client";
import { instancesQuery, collectionsQuery, enrichmentKeysQuery, pluginsQuery } from "../api/queries";
import { text } from "../card/draft";
import { apps } from "../sync/Logo";
import { stateLabel } from "../sync/sync";
import { Facets } from "./Facets";
import { ImportForm, importers } from "./ImportForm";
import { pageLinks } from "./pages";
import { defaultSort, enrichmentValue, keyRef, parseQuery, parseScore, withScoreInstance, sizes, toggleTerm, toParams, toLorebookSearch, toSearch, valueLabel, type CardQuery, type Sort, type Term } from "./query";
import { emptySelection, pick, target, type Selection } from "./selection";
import { useJobEvents } from "../settings/jobUtil";
import { useLibrary, withLibrary } from "../shell/library";
import { SelectionBar } from "./SelectionBar";
import { LorebookRow, Row, Tile, type Names } from "./Tile";
import "./Cards.css";

const sortLabels: Record<string, string> = { relevance: "Best match", updated: "Updated", name: "Name", created: "Created" };

const ungroupedKey = "innkeeper.library.ungrouped";

// Storage can throw or be empty in private windows; the list then groups, as by default.
function readUngrouped(): boolean {
  try {
    return localStorage.getItem(ungroupedKey) === "true";
  } catch {
    return false;
  }
}

function saveUngrouped(v: boolean) {
  try {
    localStorage.setItem(ungroupedKey, String(v));
  } catch {
    // A view preference that can't be saved isn't worth an error.
  }
}

function scoreLabel(value: string): string {
  const s = parseScore(value)!;
  if (s.none) return `${s.key} not evaluated`;
  return `${s.key} ${s.gte ?? "…"}–${s.lte ?? "…"}`;
}

function termLabel(t: Term, names: Names): string {
  const pair = t.kind === "value" ? enrichmentValue(t.value) : undefined;
  if (pair) return `${t.not ? "not " : ""}${pair.key}: ${valueLabel(pair.value)}`;
  const value =
    t.kind === "score" ? scoreLabel(t.value)
    : t.kind === "collection" ? (names.collections.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "target" ? (names.instances.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "state" ? stateLabel(t.value)
    : t.value;
  return `${t.not ? "not " : ""}${t.kind}: ${value}`;
}

export function Cards({ kind = "card" }: { kind?: "card" | "lorebook" }) {
  const noun = kind === "card" ? "cards" : "lorebooks";
  const Noun = kind === "card" ? "Cards" : "Lorebooks";
  const [params, setParams] = useSearchParams();
  const asked = parseQuery(params, useLibrary());
  const keys = useQuery({ ...enrichmentKeysQuery(asked.library), enabled: kind === "card" });
  const scored = keys.data?.instance_id != null;
  // Terms that name no instance, from older links, read the score instance; without one they'd be refused.
  const q = keys.data ? withScoreInstance(asked, keys.data.instance_id) : asked;
  const scoresOff = !scored && q !== asked;
  // The selection belongs to one search: paging keeps it, a new search clears it.
  const key = JSON.stringify([q.library, q.text, q.terms]);
  const [stored, setSel] = useState<Selection>(() => emptySelection(key));
  // Reset rather than hide, so going back to an earlier search doesn't revive its picks.
  if (stored.key !== key) setSel(emptySelection(key));
  const sel = stored.key === key ? stored : emptySelection(key);
  const [facetsOpen, setFacetsOpen] = useState(true);
  const [importOpen, setImportOpen] = useState(false);
  const [ungrouped, setUngrouped] = useState(readUngrouped);
  // The bar unmounts once its job starts, so the page follows the job and reloads when it ends.
  const [watching, setWatching] = useState<number>();
  useJobEvents(watching ?? 0, watching !== undefined);
  const go = (next: CardQuery) => setParams(toParams(next));

  const enrichers = keys.data?.instances ?? [];
  const grid = enrichers.find((e) => e.id === keys.data?.instance_id)?.keys?.find((k) => k.grid);
  const body = { ...(kind === "card" ? toSearch(q, scored) : toLorebookSearch(q)), ...(ungrouped ? {} : { group: "family" as const }) };
  const res = useQuery({
    queryKey: ["search", kind, body],
    queryFn: async () => must(await api.POST(route(kind, "/cards/search"), { body })),
    placeholderData: keepPreviousData,
    // Waits for the keys, so the grid doesn't search once without scores and again with them.
    enabled: kind !== "card" || !keys.isPending,
  });
  const sorts: [Sort, string][] = [
    ...(Object.entries(sortLabels) as [Sort, string][]),
    ...enrichers.flatMap((e) =>
      (e.keys ?? [])
        .filter((k) => k.type === "score")
        .map((k): [Sort, string] => [`enrichment:${keyRef(e.id, k.key)}`, enrichers.length > 1 ? `${k.label} · ${e.label}` : k.label]),
    ),
  ];
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const collections = useQuery(collectionsQuery(q.library));
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.label])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
    logos: apps(instances.data, plugins.data),
  };
  const imp = importers(instances.data, plugins.data);
  // With two or more, the core picks none until Scores from names one.
  const unpicked = !scored && enrichers.length > 1;

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
  const linkedBody = { library: q.library, filter: { ids: linkedIds }, fields: ["name", "avatar"], page: { size: 500 } };
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
  const pickPage = () => setSel({ ...sel, ids: [...new Set([...sel.ids, ...pageIds])], all: false });

  return (
    <div className={facetsOpen ? "cards" : "cards no-facets"}>
      {facetsOpen && <Facets kind={kind} q={q} names={names} enrichers={enrichers} onChange={go} />}
      {picking ? (
        <SelectionBar
          all={sel.all}
          total={ungrouped ? total : undefined}
          kind={kind}
          library={q.library}
          query={target(sel, body)}
          ids={sel.ids}
          shown={new Map(hits.map((h) => [h.id, { id: h.id, name: text(h.fields.name), family: h.family }]))}
          onAll={() => setSel({ ...sel, all: true })}
          onPage={pageIds.some((id) => !sel.ids.includes(id)) ? pickPage : undefined}
          onClear={() => setSel(emptySelection(key))}
          onStarted={setWatching}
        />
      ) : (
        <div className="toolbar">
          <button className="button" onClick={() => setFacetsOpen(!facetsOpen)} aria-expanded={facetsOpen}>
            Filters
          </button>
          <form role="search" onSubmit={search}>
            <input key={q.text} className="input" type="search" name="q" defaultValue={q.text} placeholder={`Search ${noun}`} aria-label={`Search ${noun}`} />
          </form>
          <select
            className="input"
            aria-label="Sort"
            value={q.sort}
            onChange={(e) => go({ ...q, sort: e.target.value as Sort, page: 1 })}
          >
            {sorts
              .filter(([s]) => s !== "relevance" || q.text || q.sort === "relevance")
              .map(([s, label]) => (
                <option key={s} value={s}>
                  {label}
                </option>
              ))}
          </select>
          <button
            className="button"
            onClick={() => {
              setUngrouped(!ungrouped);
              saveUngrouped(!ungrouped);
            }}
          >
            {ungrouped ? "Group variants" : "Ungroup variants"}
          </button>
          {hits.length > 0 && (
            <>
              <button className="button" onClick={pickPage}>
                Select page
              </button>
              <button className="button" onClick={() => setSel({ ...sel, all: true })}>
                Select all
              </button>
            </>
          )}
          {kind === "card" && (
            <button className="button" onClick={() => go({ ...q, view: q.view === "grid" ? "list" : "grid" })}>
              {q.view === "grid" ? "List view" : "Grid view"}
            </button>
          )}
          {kind === "card" && (
            <Link className="button" to={withLibrary("/cards/new", q.library)}>
              New card
            </Link>
          )}
          {(imp.file || imp.url) && (
            <button className="button" onClick={() => setImportOpen(!importOpen)} aria-expanded={importOpen}>
              Import {noun}
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
        {/* Hidden, not unmounted, so closing it keeps the started imports listed. */}
        {(imp.file || imp.url) && (
          <section className="import-panel" aria-label={`Import ${noun}`} hidden={!importOpen}>
            {/* Keyed by library, so a switch clears the imports listed for the old one. */}
            <ImportForm key={q.library} {...imp} library={q.library} />
          </section>
        )}
        {res.isError && <p className="error">{res.error.message}</p>}
        {scoresOff && <p className="muted">Score filters and sort from this link are off: they read the Scores from instance, and there is none.</p>}
        {unpicked && <p className="muted">Tiles show no score: more than one instance gives scores. Pick one in Settings, under Scores from.</p>}
        {res.data && <p className="total">{total} {total === 1 ? noun.slice(0, -1) : noun}{ungrouped ? "" : ", variants grouped"}</p>}
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
              <Tile key={h.id} hit={h} names={names} pick={pickFor(h.id)} grid={grid} />
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
