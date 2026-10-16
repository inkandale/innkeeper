import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, route, type Kind } from "../api/client";
import { itemPath } from "./archive";

/** FoundInArchives lists the archive items that match a card or lorebook, by archive. */
export function FoundInArchives({ kind, id }: { kind: Kind; id: number }) {
  const res = useQuery({
    queryKey: [kind, id, "catalog-matches"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/catalog-matches"), { params: { path: { id } } })),
  });
  const matches = res.data?.matches ?? [];
  const unreachable = res.data?.unreachable ?? [];
  if (!res.isError && !matches.length && !unreachable.length) return null;
  return (
    <section>
      <h2>Found in archives</h2>
      {res.isError && <p className="error">{res.error.message}</p>}
      {matches.map((m) => (
        <div key={m.catalog_id}>
          <h3>{m.catalog}</h3>
          <ul className="plain">
            {(m.items ?? []).map((it) => (
              <li key={it.id}>
                <Link to={itemPath(m.catalog_id, it.id)}>{it.name}</Link>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {unreachable.length > 0 && <p className="muted">Couldn't reach {unreachable.map((u) => u.name).join(", ")}.</p>}
    </section>
  );
}
