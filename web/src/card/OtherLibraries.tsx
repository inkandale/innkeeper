import { useQueries, useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, route, type Kind } from "../api/client";
import { librariesQuery } from "../api/queries";
import { text, type Data } from "./draft";

/** OtherLibraries lists the cards or lorebooks in other libraries that look like this one. */
export function OtherLibraries({ kind, id }: { kind: Kind; id: number }) {
  const matches = useQuery({
    queryKey: [kind, id, "in-other-libraries"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/in-other-libraries"), { params: { path: { id } } })),
  });
  const libraries = useQuery(librariesQuery);
  const list = matches.data ?? [];
  // The route returns IDs only, so each match's name comes from its own page's query.
  const entities = useQueries({
    queries: list.map((m) => ({
      queryKey: [kind, m.id],
      queryFn: async () => must(await api.GET(route(kind, "/cards/{id}"), { params: { path: { id: m.id } } })),
    })),
  });
  if (!matches.isError && !list.length) return null;
  const libraryName = (n: number) => libraries.data?.find((l) => l.id === n)?.name ?? `Library ${n}`;
  return (
    <section>
      <h2>In other libraries</h2>
      {matches.isError && <p className="error">{matches.error.message}</p>}
      <ul className="plain">
        {list.map((m, i) => (
          <li key={m.id}>
            <Link to={`/${kind}s/${m.id}`}>{text((entities[i]?.data?.revision.data as Data | undefined)?.name) || `#${m.id}`}</Link>{" "}
            <span className="muted">in {libraryName(m.library_id)}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}
