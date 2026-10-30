import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, must, thumb } from "../api/client";
import { librariesQuery } from "../api/queries";
import type { components } from "../api/schema";
import { parseQuery, toParams, type Term } from "../cards/query";
import type { Entity } from "./Card";
import { nsfw, text, type Data } from "./draft";
import "../cards/Cards.css";
import "./Card.css";

type Found = components["schemas"]["DiscoveredCard"];
type RelatedPage = components["schemas"]["RelatedPage"];

const groupNames: Record<RelatedPage["group"], string> = {
  creator: "Same creator",
  lorebook: "Shares a lorebook",
  collection: "Shares a collection",
};

// Enough to recognize the group; See all opens the rest.
const shown = 8;

/** galleryTerms are the gallery terms that list a related group in full. */
function galleryTerms(group: RelatedPage["group"], card: Entity): Term[] {
  const term = (kind: Term["kind"], value: string): Term => ({ kind, value, not: false });
  if (group === "creator") return [term("creator", text((card.revision.data as Data).creator))];
  if (group === "lorebook") return (card.lorebooks ?? []).map((id) => term("lorebook", String(id)));
  return (card.collections ?? []).map((id) => term("collection", String(id)));
}

/** CardList shows the found cards as tiles, naming a card's library when it isn't this card's. */
function CardList({ items, library }: { items: Found[]; library: number }) {
  const libraries = useQuery(librariesQuery);
  const libraryName = (n: number) => libraries.data?.find((l) => l.id === n)?.name ?? `Library ${n}`;
  return (
    <div className="grid discover">
      {items.map((c) => {
        const name = c.name || `#${c.card_id}`;
        return (
          <article key={c.card_id} className="tile">
            <Link to={`/cards/${c.card_id}`} className="tile-name">
              {c.avatar ? (
                <img className={nsfw(c.tags) ? "avatar nsfw" : "avatar"} src={thumb(c.avatar, 320)} alt="" loading="lazy" />
              ) : (
                <span className="avatar empty" aria-hidden="true">
                  {name.slice(0, 1)}
                </span>
              )}
              {name}
            </Link>
            {c.library_id !== library && <span className="muted">in {libraryName(c.library_id)}</span>}
          </article>
        );
      })}
    </div>
  );
}

/** Similar lists the cards that read like this one, in its library or all of them. */
export function Similar({ id, library }: { id: number; library: number }) {
  const [scope, setScope] = useState<Scope>("library");
  const pages = useInfiniteQuery({
    queryKey: ["card", id, "similar", scope],
    queryFn: async ({ pageParam }) =>
      must(await api.GET("/cards/{id}/similar", { params: { path: { id }, query: { scope, cursor: pageParam, limit: shown } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
    placeholderData: keepPreviousData,
  });
  if (pages.isPending) return null;
  const items = pages.data?.pages.flatMap((p) => p.items ?? []) ?? [];
  const basis = pages.data?.pages[0]?.basis;
  return (
    <div>
      <h3>
        Similar{" "}
        <span className="scope">
          (<ScopeOption scope="library" current={scope} onPick={setScope} /> /{" "}
          <ScopeOption scope="all" current={scope} onPick={setScope} />)
        </span>
      </h3>
      {pages.isError && <p className="error">{pages.error.message}</p>}
      {basis === "tags" && (
        <p className="muted">
          Ranked by shared tags until this card has an embedding. <Link to="/settings/plugins">Embed instance</Link>
        </p>
      )}
      {pages.isSuccess && !pages.isPlaceholderData && !items.length && <p className="muted">None in {scope === "all" ? "any library" : "this library"}.</p>}
      <CardList items={items} library={library} />
      {pages.hasNextPage && (
        <button className="button ghost" disabled={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>
          Show more
        </button>
      )}
    </div>
  );
}

type Scope = "library" | "all";

/** ScopeOption is one of Similar's scopes, a link until it's picked. */
function ScopeOption({ scope, current, onPick }: { scope: Scope; current: Scope; onPick: (s: Scope) => void }) {
  return (
    <button className="link-button" aria-pressed={scope === current} onClick={() => onPick(scope)}>
      {scope === "library" ? "This library" : "All libraries"}
    </button>
  );
}

/** Related shows the first cards linked to this one by creator, lorebook, or collection, each group linking to the gallery for the rest. */
export function Related({ card }: { card: Entity }) {
  const related = useQuery({
    queryKey: ["card", card.id, "related"],
    queryFn: async () => must(await api.GET("/cards/{id}/related", { params: { path: { id: card.id }, query: { limit: shown } } })),
  });
  const groups = related.data?.groups ?? [];
  if (!related.isError && !groups.length) return null;
  // Search covers one library, so the gallery shows the rest from the card's own library.
  const seeAll = (g: RelatedPage) => `/cards?${toParams({ ...parseQuery(new URLSearchParams(), card.library_id), terms: galleryTerms(g.group, card) })}`;
  return (
    <>
      {related.isError && <p className="error">{related.error.message}</p>}
      {groups.map((g) => (
        <div key={g.group}>
          <h3>{groupNames[g.group]}</h3>
          <CardList items={g.items ?? []} library={card.library_id} />
          {g.next_cursor && <Link to={seeAll(g)}>See all</Link>}
        </div>
      ))}
    </>
  );
}

/** Discover gathers the cards like this one and the cards linked to it. */
export function Discover({ card }: { card: Entity }) {
  return (
    <section className="metadata-section">
      <h2>Discover</h2>
      <Similar id={card.id} library={card.library_id} />
      <Related card={card} />
    </section>
  );
}
