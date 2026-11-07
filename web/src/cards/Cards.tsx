import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router";
import { instancesQuery, collectionsQuery, enrichmentKeysQuery, pluginsQuery, namesQuery, savedSearchQuery, searchQuery, settingsQuery } from "../api/queries";
import { text } from "../card/draft";
import { apps } from "../sync/Logo";
import { stateLabel } from "../sync/sync";
import { Facets } from "./Facets";
import { ImportForm, importers } from "./ImportForm";
import { pageLinks } from "./pages";
import { defaultSort, enrichmentValue, keyRef, parseQuery, parseScore, withoutUnnamed, sizes, toggleTerm, toParams, toLorebookSearch, toSearch, valueLabel, type CardQuery, type Sort, type Term } from "./query";
import { emptySelection, pick, target, type Selection } from "./selection";
import { useJobEvents } from "../settings/jobUtil";
import { Icon } from "../shell/Icon";
import { useLibrary, withLibrary } from "../shell/library";
import { Loading } from "../shell/Loading";
import { SaveSearch } from "./SaveSearch";
import { SelectionBar } from "./SelectionBar";
import { LorebookRow, Row, Tile, medals, type Names } from "./Tile";
import type { components } from "../api/schema";
import "./Cards.css";

const sortLabels: Record<string, string> = { relevance: "Best match", updated: "Updated", name: "Name", created: "Created" };

type Enricher = components["schemas"]["EnrichmentInstance"];

/** linkedSortLabel names an enrichment sort the menu doesn't list, from a link: the key's label and its instance's. */
function linkedSortLabel(sort: string, enrichers: Enricher[]): string {
  const ref = sort.slice("enrichment:".length);
  const at = ref.indexOf(":");
  const key = ref.slice(at + 1);
  const e = at > 0 ? enrichers.find((x) => String(x.id) === ref.slice(0, at)) : undefined;
  const k = e?.keys?.find((x) => x.key === key);
  return e && k ? `${k.label} · ${e.label}` : key;
}

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
    : t.kind === "lorebook" ? (names.lorebooks.get(Number(t.value)) || "#" + t.value)
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
  const saved = useQuery({ ...savedSearchQuery(asked.saved ?? 0), enabled: kind === "card" && asked.saved !== undefined });
  // Until the saved search loads, the page's own terms alone would match, and act on, more cards than asked.
  const blocked = kind === "card" && asked.saved !== undefined && !saved.data;
  const keys = useQuery({ ...enrichmentKeysQuery, enabled: kind === "card" });
  const scored = (keys.data?.scorers?.length ?? 0) > 0;
  const browse = keys.data?.browse ?? [];
  // The core refuses a term naming no instance unless it names a merged browsable key.
  const scoredQ = keys.data ? withoutUnnamed(asked, browse.filter((b) => !b.instance).map((b) => b.key)) : asked;
  const scoresOff = scoredQ !== asked;
  const settings = useQuery(settingsQuery);
  const embeds = kind === "card" && settings.data?.vector_instance != null && settings.data.vectors_available;
  // A semantic link with no text, or opened after the vector source went away, searches by words.
  const unsaved = scoredQ.semantic && !(embeds && scoredQ.text) ? { ...scoredQ, semantic: false } : scoredQ;
  const q: CardQuery = saved.data ? { ...unsaved, savedFilter: saved.data.filter } : unsaved;
  // The selection belongs to one search: paging keeps it, a new search clears it.
  const key = JSON.stringify([q.library, q.text, q.semantic, q.terms, q.saved]);
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
  const unscored = enrichers.filter((e) => !e.scored);
  const ranked = (keys.data?.scorers ?? []).map((s) => s.instance);
  // A tile shows the grid score, or the value sorted by, which may be any instance's.
  const scorers = new Map(
    enrichers.map((e) => {
      const place = ranked.indexOf(e.id);
      return [e.id, { keys: e.keys ?? [], ...(place >= 0 && { medal: medals[place] }) }] as const;
    }),
  );
  const body = { ...(kind === "card" ? toSearch(q, scored) : toLorebookSearch(q)), ...(ungrouped ? {} : { group: "family" as const }) };
  const res = useQuery({
    ...searchQuery(kind, body),
    placeholderData: keepPreviousData,
    // Waits for the keys and settings, so the grid doesn't search once without scores or by words and again with them.
    enabled: (kind !== "card" || !(keys.isPending || settings.isPending)) && !blocked,
  });
  const scoreSorts = browse.filter((b) => b.type === "score").map((b): [Sort, string] => [`enrichment:${keyRef(b.instance, b.key)}`, b.label]);
  const sorts: [Sort, string][] = [
    ...(Object.entries(sortLabels) as [Sort, string][]),
    ...scoreSorts,
    // A link may sort by a key the menu doesn't list, such as one that isn't browsable.
    ...(q.sort.startsWith("enrichment:") && !scoreSorts.some(([s]) => s === q.sort) ? [[q.sort, linkedSortLabel(q.sort, enrichers)] as [Sort, string]] : []),
  ];
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const collections = useQuery(collectionsQuery(q.library));
  const lorebookIds = q.terms.filter((t) => t.kind === "lorebook").map((t) => Number(t.value));
  const lorebooks = useQuery(namesQuery("lorebook", q.library, lorebookIds));
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.label])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
    lorebooks: new Map((lorebooks.data?.hits ?? []).map((h) => [h.id, text(h.fields.name)])),
    logos: apps(instances.data, plugins.data),
  };
  const imp = importers(instances.data, plugins.data);

  const search = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const text = String(new FormData(e.currentTarget).get("q") ?? "").trim();
    go({ ...q, text, sort: q.sort === defaultSort(q.text) ? defaultSort(text) : q.sort, page: 1 });
  };

  const total = blocked ? 0 : (res.data?.total ?? 0);
  const last = Math.max(1, Math.ceil(total / q.size));
  const hits = blocked ? [] : (res.data?.hits ?? []);
  // Linked cards' names and avatars for the lorebook rows, in one search.
  const linkedIds = kind === "lorebook" ? [...new Set(hits.flatMap((h) => h.cards ?? []))].slice(0, 500) : [];
  const linkedBody = { library: q.library, filter: { ids: linkedIds }, fields: ["name", "avatar"], page: { size: 500 } };
  const linked = useQuery({ ...searchQuery("card", linkedBody), enabled: linkedIds.length > 0 });
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
      {facetsOpen && <Facets kind={kind} q={q} names={names} browse={browse.filter((b) => b.instances?.some((id) => enrichers.some((e) => e.id === id && e.scored)))} onChange={go} />}
      {picking ? (
        <SelectionBar
          all={sel.all}
          total={ungrouped ? total : undefined}
          kind={kind}
          library={q.library}
          query={target(sel, body)}
          ids={sel.ids}
          shown={new Map(hits.map((h) => [h.id, { id: h.id, name: text(h.fields.name), family: h.family }]))}
          // A semantic search matches every embedded card, so "all matching" would act on far more than the results.
          onAll={q.semantic ? undefined : () => setSel({ ...sel, all: true })}
          onPage={pageIds.some((id) => !sel.ids.includes(id)) ? pickPage : undefined}
          onClear={() => setSel(emptySelection(key))}
          onStarted={setWatching}
        />
      ) : (
        <div className="toolbar">
          <button className="button ghost" onClick={() => setFacetsOpen(!facetsOpen)} aria-expanded={facetsOpen}>
            <Icon name="filters" />
            Filters
          </button>
          <form role="search" onSubmit={search}>
            <input key={q.text} className="input" type="search" name="q" defaultValue={q.text} placeholder={`Search ${noun}`} aria-label={`Search ${noun}`} />
          </form>
          {embeds && (
            <label title={q.text ? "Rank cards by meaning instead of matching words. Cards not embedded yet are left out." : "Semantic search needs search text."}>
              <input type="checkbox" checked={q.semantic} disabled={!q.text} onChange={(e) => go({ ...q, semantic: e.target.checked, page: 1 })} />
              Semantic
            </label>
          )}
          <select
            className="input"
            aria-label="Sort"
            value={q.semantic ? "relevance" : q.sort}
            disabled={q.semantic}
            onChange={(e) => go({ ...q, sort: e.target.value as Sort, page: 1 })}
          >
            {sorts
              .filter(([s]) => s !== "relevance" || q.text || q.sort === "relevance" || q.semantic)
              .map(([s, label]) => (
                <option key={s} value={s}>
                  {label}
                </option>
              ))}
          </select>
          <button
            className="button"
            aria-label="Reverse order"
            title="Reverse order"
            aria-pressed={q.reverse}
            disabled={q.semantic}
            onClick={() => go({ ...q, reverse: !q.reverse, page: 1 })}
          >
            {q.reverse ? "↑" : "↓"}
          </button>
          {kind === "card" && (
            <div className="segmented" role="group" aria-label="View">
              {(["grid", "list"] as const).map((v) => (
                <button key={v} className="button" aria-pressed={q.view === v} onClick={() => go({ ...q, view: v })}>
                  {v === "grid" ? "Grid" : "List"}
                </button>
              ))}
            </div>
          )}
          <label title="Shows a card and its variants, such as forks and alternate versions, as one tile.">
            <input
              type="checkbox"
              checked={!ungrouped}
              onChange={(e) => {
                setUngrouped(!e.target.checked);
                saveUngrouped(!e.target.checked);
              }}
            />
            Group variants
          </label>
          {kind === "card" && (
            <Link className="button primary" to={withLibrary("/cards/new", q.library)}>
              <Icon name="plus" />
              New card
            </Link>
          )}
          {(imp.file || imp.url || imp.folder) && (
            <button className="button" onClick={() => setImportOpen(!importOpen)} aria-expanded={importOpen}>
              Import {noun}
            </button>
          )}
          {kind === "card" && !blocked && <SaveSearch q={q} saved={saved.data} />}
          <div className="chips">
            {asked.saved !== undefined && (
              <button
                className="chip"
                aria-label={`Close saved search ${saved.data?.name ?? `#${asked.saved}`}`}
                onClick={() => go({ ...q, saved: undefined, page: 1 })}
              >
                Saved search: {saved.data?.name ?? `#${asked.saved}`}
                <Icon name="close" />
              </button>
            )}
            {q.terms.map((t) => (
              <button
                key={`${t.not ? "-" : ""}${t.kind}:${t.value}`}
                className={t.not ? "chip excluded" : "chip"}
                aria-label={`Remove ${termLabel(t, names)}`}
                onClick={() => go(toggleTerm(q, t))}
              >
                {termLabel(t, names)}
                <Icon name="close" />
              </button>
            ))}
          </div>
        </div>
      )}
      {/* Busy while the last search's hits stand in for the next one's. */}
      <div className="results" aria-busy={res.isPlaceholderData}>
        {/* Hidden, not unmounted, so closing it keeps the started imports listed. */}
        {(imp.file || imp.url || imp.folder) && (
          <section className="import-panel" aria-label={`Import ${noun}`} hidden={!importOpen}>
            {/* Keyed by library, so a switch clears the imports listed for the old one. */}
            <ImportForm key={q.library} {...imp} library={q.library} />
          </section>
        )}
        {res.isError && <p className="error">{res.error.message}</p>}
        {saved.isError && <p className="error">Saved search: {saved.error.message}</p>}
        {scoresOff && <p className="muted">Some score filters or the sort from this link are off: they name no instance or browsable key.</p>}
        {kind === "card" && total > 0 && unscored.length > 0 && (
          <p className="muted">
            {unscored.map((e) => e.label).join(", ")} {unscored.length === 1 ? "hasn't" : "haven't"} scored any cards yet. Select
            cards, then choose Score.
          </p>
        )}
        {res.data && total === 0 && !q.text && !q.terms.length && !q.sharesSource && !q.saved ? (
          <div className="empty-library">
            <p>No {noun} in this library yet.</p>
            <p>
              {(imp.file || imp.url || imp.folder) && (
                <button className="button primary" onClick={() => setImportOpen(true)}>
                  Import {noun}
                </button>
              )}{" "}
              <Link to="/welcome">Run the setup wizard</Link>
            </p>
          </div>
        ) : (
          res.data && !blocked && <p className="total">{total} {total === 1 ? noun.slice(0, -1) : noun}{ungrouped ? "" : ", variants grouped"}</p>
        )}
        {res.data && !blocked && q.page > last && (
          <p>
            No {noun} on this page. <button className="button" onClick={() => go({ ...q, page: last })}>Go to page {last}</button>
          </p>
        )}
        {res.isPending && !saved.isError ? (
          kind === "card" && q.view === "grid" ? (
            <div className="grid" role="status" aria-busy="true" aria-label={`Loading ${noun}`}>
              {Array.from({ length: 8 }, (_, i) => (
                <div key={i}>
                  <div className="skeleton skeleton-art" />
                  <div className="skeleton skeleton-name" />
                </div>
              ))}
            </div>
          ) : (
            <Loading />
          )
        ) : kind === "lorebook" ? (
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
              <Tile key={h.id} hit={h} names={names} pick={pickFor(h.id)} scorers={scorers} />
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
