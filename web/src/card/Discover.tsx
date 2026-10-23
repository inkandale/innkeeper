import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, must } from "../api/client";
import { librariesQuery } from "../api/queries";
import type { components } from "../api/schema";

type Found = components["schemas"]["DiscoveredCard"];
type RelatedPage = components["schemas"]["RelatedPage"];

const groupNames: Record<RelatedPage["group"], string> = {
  creator: "Same creator",
  lorebook: "Shares a lorebook",
  collection: "Shares a collection",
};

/** CardList links each found card, naming its library when it isn't this card's. */
function CardList({ items, library }: { items: Found[]; library: number }) {
  const libraries = useQuery(librariesQuery);
  const libraryName = (n: number) => libraries.data?.find((l) => l.id === n)?.name ?? `Library ${n}`;
  return (
    <ul className="plain">
      {items.map((c) => (
        <li key={c.card_id}>
          <Link to={`/cards/${c.card_id}`}>{c.name || `#${c.card_id}`}</Link>
          {c.library_id !== library && <span className="muted"> in {libraryName(c.library_id)}</span>}
        </li>
      ))}
    </ul>
  );
}

/** Similar lists the cards that read like this one, in its library or all of them. */
export function Similar({ id, library }: { id: number; library: number }) {
  const [scope, setScope] = useState<"library" | "all">("library");
  const pages = useInfiniteQuery({
    queryKey: ["card", id, "similar", scope],
    queryFn: async ({ pageParam }) =>
      must(await api.GET("/cards/{id}/similar", { params: { path: { id }, query: { scope, cursor: pageParam } } })),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (last) => last.next_cursor || undefined,
  });
  if (pages.isPending) return null;
  const items = pages.data?.pages.flatMap((p) => p.items ?? []) ?? [];
  const basis = pages.data?.pages[0]?.basis;
  return (
    <section>
      <h2>Similar</h2>
      <label>
        <input type="checkbox" checked={scope === "all"} onChange={(e) => setScope(e.target.checked ? "all" : "library")} /> All
        libraries
      </label>
      {pages.isError && <p className="error">{pages.error.message}</p>}
      {basis === "tags" && (
        <p className="muted">
          Ranked by shared tags. <Link to="/settings/plugins">Pick an embed instance</Link> to rank by meaning.
        </p>
      )}
      {pages.isSuccess && !items.length && <p className="muted">None in {scope === "all" ? "any library" : "this library"}.</p>}
      <CardList items={items} library={library} />
      {pages.hasNextPage && (
        <button className="button ghost" disabled={pages.isFetchingNextPage} onClick={() => void pages.fetchNextPage()}>
          Show more
        </button>
      )}
    </section>
  );
}

/** RelatedGroup shows one group from the overview and fetches its later pages on demand. */
function RelatedGroup({ id, library, first }: { id: number; library: number; first: RelatedPage }) {
  const [later, setLater] = useState<RelatedPage[]>([]);
  const cursor = (later.at(-1) ?? first).next_cursor;
  const more = useMutation({
    mutationFn: async () =>
      must(await api.GET("/cards/{id}/related", { params: { path: { id }, query: { group: first.group, cursor } } })).groups?.[0],
    onSuccess: (page) => page && setLater((pages) => [...pages, page]),
  });
  const items = [first, ...later].flatMap((p) => p.items ?? []);
  return (
    <>
      <h3>{groupNames[first.group]}</h3>
      <CardList items={items} library={library} />
      {more.isError && <p className="error">{more.error.message}</p>}
      {cursor && (
        <button className="button ghost" disabled={more.isPending} onClick={() => more.mutate()}>
          Show more
        </button>
      )}
    </>
  );
}

/** Related lists the cards linked to this one by creator, lorebook, or collection. */
export function Related({ id, library }: { id: number; library: number }) {
  const related = useQuery({
    queryKey: ["card", id, "related"],
    queryFn: async () => must(await api.GET("/cards/{id}/related", { params: { path: { id } } })),
  });
  const groups = related.data?.groups ?? [];
  if (!related.isError && !groups.length) return null;
  return (
    <section>
      <h2>Related</h2>
      {related.isError && <p className="error">{related.error.message}</p>}
      {groups.map((g) => (
        <RelatedGroup key={`${id}:${g.group}`} id={id} library={library} first={g} />
      ))}
    </section>
  );
}
