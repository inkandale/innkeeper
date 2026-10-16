import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router";
import { api, must } from "../api/client";
import { strings, text, type Data } from "../card/draft";
import { thumbnailSrc } from "./archive";
import "../card/Card.css";
import "../cards/Cards.css";
import "./Archives.css";

/** ArchiveItem is one catalog item, read-only, with a version picker and Adopt. */
export function ArchiveItem() {
  const { catalog, item } = useParams();
  const [params, setParams] = useSearchParams();
  const version = params.get("version") ?? undefined;
  // Keyed by version so an adopt of one version doesn't show as done for another.
  return <ItemView key={version ?? ""} catalog={Number(catalog)} item={item!} version={version} pick={(v) => setParams({ version: v })} />;
}

type ItemProps = { catalog: number; item: string; version?: string; pick: (version: string) => void };

function ItemView({ catalog, item, version, pick }: ItemProps) {
  const qc = useQueryClient();
  const path = { id: catalog, item };
  const got = useQuery({
    queryKey: ["catalog", catalog, "item", item, version],
    queryFn: async () => must(await api.GET("/catalogs/{id}/items/{item}", { params: { path, query: { version } } })),
  });
  const adopt = useMutation({
    mutationFn: async (v: string) => must(await api.POST("/catalogs/{id}/items/{item}/adopt", { params: { path, query: { version: v } } })),
    // Search results badge library matches, and the library now has this item.
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["search"] }),
  });
  if (got.isPending) return null;
  if (got.isError) return <p className="error page">{got.error.message}</p>;
  const g = got.data;
  return (
    <section className="page archive-item">
      <p>
        <Link to={`/archives?catalog=${catalog}`}>Back to the archive</Link>
      </p>
      {g.item.thumbnail && <img className="portrait" src={thumbnailSrc(g.item.thumbnail)} alt="" />}
      <h1>{g.item.name}</h1>
      {g.item.author && <p className="muted">by {g.item.author}</p>}
      <div className="row">
        <label>
          Version{" "}
          <select className="input" value={g.version} onChange={(e) => pick(e.target.value)}>
            {(g.versions ?? []).map((v) => (
              <option key={v.version} value={v.version}>
                {v.version}
                {v.created ? ` (${new Date(v.created).toLocaleDateString()})` : ""}
              </option>
            ))}
          </select>
        </label>
        {adopt.data ? (
          <Link className="button primary" to={`/${g.item.kind}s/${adopt.data.id}`}>
            Open in library
          </Link>
        ) : (
          <button className="button primary" disabled={adopt.isPending} onClick={() => adopt.mutate(g.version)}>
            Adopt
          </button>
        )}
      </div>
      <Preview kind={g.item.kind} data={g.data} />
    </section>
  );
}

const cardSections = [
  ["description", "Description"],
  ["personality", "Personality"],
  ["scenario", "Scenario"],
  ["first_mes", "First message"],
  ["mes_example", "Example messages"],
] as const;

/** Preview shows a version's text: a card's main fields, or a lorebook's entries. */
function Preview({ kind, data }: { kind: string; data: unknown }) {
  if (kind === "lorebook") {
    const entries = ((data as Data | null)?.entries ?? []) as Data[];
    return (
      <section>
        <h2>
          {entries.length} {entries.length === 1 ? "entry" : "entries"}
        </h2>
        <ul className="plain">
          {entries.map((e, i) => (
            <li key={i}>
              <strong>{strings(e.keys).join(", ")}</strong>
              <p className="archive-text">{text(e.content)}</p>
            </li>
          ))}
        </ul>
      </section>
    );
  }
  // A CCv3 card keeps its fields under data.
  const fields = ((data as Data | null)?.data ?? {}) as Data;
  const tags = strings(fields.tags);
  return (
    <>
      {tags.length > 0 && (
        <p className="tile-tags">
          {tags.map((t) => (
            <span key={t} className="tag">
              {t}
            </span>
          ))}
        </p>
      )}
      {cardSections.map(
        ([key, label]) =>
          text(fields[key]) && (
            <section key={key}>
              <h2>{label}</h2>
              <p className="archive-text">{text(fields[key])}</p>
            </section>
          ),
      )}
    </>
  );
}
