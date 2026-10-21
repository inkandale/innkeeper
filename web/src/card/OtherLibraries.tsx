import { useQueries, useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, route, type Kind } from "../api/client";
import { librariesQuery, searchQuery } from "../api/queries";
import { text } from "./draft";

/** OtherLibraries lists the cards or lorebooks in other libraries that look like this one. */
export function OtherLibraries({ kind, id }: { kind: Kind; id: number }) {
  const matches = useQuery({
    queryKey: [kind, id, "in-other-libraries"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/in-other-libraries"), { params: { path: { id } } })),
  });
  const libraries = useQuery(librariesQuery);
  const list = matches.data ?? [];
  // The route returns IDs only; names come from one search per library, since search covers one library.
  const libraryIds = [...new Set(list.map((m) => m.library_id))];
  const searches = useQueries({
    queries: libraryIds.map((library) => {
      const ids = list.filter((m) => m.library_id === library).map((m) => m.id);
      const body = { library, filter: { ids }, fields: ["name"], page: { size: ids.length } };
      return searchQuery(kind, body);
    }),
  });
  if (!matches.isError && !list.length) return null;
  const names = new Map(searches.flatMap((s) => s.data?.hits ?? []).map((h) => [h.id, text(h.fields.name)]));
  const libraryName = (n: number) => libraries.data?.find((l) => l.id === n)?.name ?? `Library ${n}`;
  return (
    <section>
      <h2>In other libraries</h2>
      {matches.isError && <p className="error">{matches.error.message}</p>}
      <ul className="plain">
        {list.map((m) => (
          <li key={m.id}>
            <Link to={`/${kind}s/${m.id}`}>{names.get(m.id) || `#${m.id}`}</Link> <span className="muted">in {libraryName(m.library_id)}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}
