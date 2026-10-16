import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { catalogsQuery } from "../api/queries";
import { finished, jobToast, useJob } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { ingestSummary, itemPath, libraryLabel, thumbnailSrc, type Catalog, type CatalogHit, type FilterValues } from "./archive";
import { FilterBar } from "./FilterBar";
import "../cards/Cards.css";
import "./Archives.css";

/** readFilters parses the filters URL parameter, ignoring one that isn't a JSON object. */
function readFilters(raw: string | null): FilterValues {
  try {
    const v: unknown = JSON.parse(raw ?? "{}");
    return v && typeof v === "object" && !Array.isArray(v) ? (v as FilterValues) : {};
  } catch {
    return {};
  }
}

const plural = (kind: string) => kind.charAt(0).toUpperCase() + kind.slice(1) + "s";

/** Archives is the Archive page. The catalog, kind, search text, and filters live in the URL. */
export function Archives() {
  const [params, setParams] = useSearchParams();
  const catalogs = useQuery(catalogsQuery);
  if (catalogs.isPending) return null;
  if (catalogs.isError) return <p className="error page">{catalogs.error.message}</p>;
  const list = catalogs.data ?? [];
  if (!list.length)
    return (
      <section className="page">
        <h1>Archive</h1>
        <p>
          No archives yet. <Link to="/settings/plugins">Add a catalog plugin</Link> to search one.
        </p>
      </section>
    );
  const catalog = list.find((c) => c.id === Number(params.get("catalog"))) ?? list[0]!;
  const go = (patch: Record<string, string | undefined>) =>
    setParams((p) => {
      for (const [k, v] of Object.entries(patch)) {
        if (v) p.set(k, v);
        else p.delete(k);
      }
      return p;
    });
  return (
    <section className="page archives">
      <div className="toolbar">
        <select
          className="input"
          aria-label="Archive"
          value={catalog.id}
          // Filters and kinds belong to one catalog, so switching drops them.
          onChange={(e) => go({ catalog: e.target.value, kind: undefined, filters: undefined })}
        >
          {list.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
        {catalog.info?.ingest && <IngestStatus key={catalog.id} catalog={catalog} />}
      </div>
      {catalog.info ? (
        <Search key={catalog.id} catalog={catalog} info={catalog.info} params={params} go={go} />
      ) : (
        <p className="error">{catalog.error ?? "This archive didn't describe itself."}</p>
      )}
    </section>
  );
}

type SearchProps = {
  catalog: Catalog;
  info: NonNullable<Catalog["info"]>;
  params: URLSearchParams;
  go: (patch: Record<string, string | undefined>) => void;
};

function Search({ catalog, info, params, go }: SearchProps) {
  const kinds = info.kinds ?? [];
  const kind = params.get("kind") ?? kinds[0] ?? "";
  const q = params.get("q") ?? "";
  const filters = readFilters(params.get("filters"));
  const filtersParam = Object.keys(filters).length ? JSON.stringify(filters) : undefined;
  // The "search" prefix lets a finished ingest's job events refresh these results.
  const res = useInfiniteQuery({
    queryKey: ["search", "catalog", catalog.id, { kind, q, filtersParam }],
    initialPageParam: "",
    queryFn: async ({ pageParam }) =>
      must(
        await api.GET("/catalogs/{id}/search", {
          params: {
            path: { id: catalog.id },
            query: { kind: kind || undefined, q: q || undefined, filters: filtersParam, cursor: pageParam || undefined },
          },
        }),
      ),
    getNextPageParam: (last) => last.next || undefined,
  });
  const items = res.data?.pages.flatMap((p) => p.items ?? []) ?? [];
  return (
    <>
      <div className="toolbar">
        {kinds.length > 1 && (
          <span role="group" aria-label="Kind">
            {kinds.map((k) => (
              <button key={k} className={k === kind ? "button primary" : "button"} aria-pressed={k === kind} onClick={() => go({ kind: k })}>
                {plural(k)}
              </button>
            ))}
          </span>
        )}
        <form
          role="search"
          onSubmit={(e) => {
            e.preventDefault();
            go({ q: String(new FormData(e.currentTarget).get("q") ?? "").trim() || undefined });
          }}
        >
          <input key={q} className="input" type="search" name="q" defaultValue={q} placeholder={`Search ${catalog.name}`} aria-label={`Search ${catalog.name}`} />
        </form>
      </div>
      <FilterBar filters={info.filters ?? []} value={filters} onChange={(v) => go({ filters: Object.keys(v).length ? JSON.stringify(v) : undefined })} />
      {res.isError && <p className="error">{res.error.message}</p>}
      {res.data && !items.length && <p className="muted">Nothing matches.</p>}
      <div className="grid">
        {items.map((hit) => (
          <ArchiveTile key={hit.id} catalog={catalog.id} hit={hit} />
        ))}
      </div>
      {res.hasNextPage && (
        <button className="button" disabled={res.isFetchingNextPage} onClick={() => void res.fetchNextPage()}>
          More
        </button>
      )}
    </>
  );
}

function ArchiveTile({ catalog, hit }: { catalog: number; hit: CatalogHit }) {
  const to = itemPath(catalog, hit.id);
  const match = libraryLabel(hit);
  const tags = hit.tags ?? [];
  return (
    <article className="tile">
      <Link to={to} className="tile-image" aria-label={hit.name}>
        {hit.thumbnail ? (
          <img className="avatar" src={thumbnailSrc(hit.thumbnail)} alt="" loading="lazy" />
        ) : (
          <span className="avatar empty">{hit.name.slice(0, 1)}</span>
        )}
      </Link>
      <Link to={to} className="tile-name">
        {hit.name}
      </Link>
      {hit.author && <p className="muted">{hit.author}</p>}
      {match && (
        <Link className="library-match" to={`/${hit.kind}s/${hit.library![0]!.id}`}>
          {match}
        </Link>
      )}
      <p className="tile-tags">
        {tags.slice(0, 3).map((t) => (
          <span key={t} className="tag">
            {t}
          </span>
        ))}
      </p>
    </article>
  );
}

/** IngestStatus shows the catalog's last ingest, live while it runs, and starts a new one. */
function IngestStatus({ catalog }: { catalog: Catalog }) {
  const qc = useQueryClient();
  const toast = useToast();
  const last = catalog.last_ingest;
  const job = useJob(last?.id ?? 0, { enabled: !!last });
  const current = job.data ?? last;
  const start = useMutation({
    mutationFn: async () => must(await api.POST("/catalogs/{id}/ingest", { params: { path: { id: catalog.id } } })),
    onSuccess: (j) => {
      toast(jobToast(j, "Ingest started"));
      void qc.invalidateQueries({ queryKey: ["catalogs"] });
    },
  });
  return (
    <span className="ingest-status">
      {current ? <Link to={`/settings/jobs/${current.id}`}>{ingestSummary(current)}</Link> : <span className="muted">Never ingested</span>}{" "}
      <button className="button" disabled={start.isPending || (!!current && !finished(current))} onClick={() => start.mutate()}>
        Ingest
      </button>
    </span>
  );
}
