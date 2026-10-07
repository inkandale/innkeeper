import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, mustOk } from "../api/client";
import type { components } from "../api/schema";

export type Plugin = components["schemas"]["PluginOut"];
export type InstanceInfo = components["schemas"]["InstanceInfo"];

export const pluginsQuery = { queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) };

export function Plugins() {
  const qc = useQueryClient();
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const remove = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/instances/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
  const failed = remove.error ?? plugins.error ?? instances.error;
  const all = instances.data ?? [];
  const names = new Set((plugins.data ?? []).map((p) => p.name));
  // Until the plugins load, every instance would look orphaned.
  const orphans = plugins.data ? all.filter((i) => !names.has(i.plugin)) : [];

  const table = (list: InstanceInfo[]) =>
    list.length > 0 && (
      <table>
        <tbody>
          {list.map((i) => (
            <tr key={i.id}>
              <td>
                <Link to={`/settings/instances/${i.id}`}>{i.name}</Link>
              </td>
              <td>
                {i.broken_error ? (
                  <span className="state failed">
                    Broken: <span>{i.broken_error}</span>
                  </span>
                ) : (
                  <span className="state succeeded">OK</span>
                )}
              </td>
              <td>
                <button
                  className="button"
                  aria-label={`Delete ${i.name}`}
                  onClick={() => confirm(`Delete ${i.name}? Its jobs stay, and logins through it end.`) && remove.mutate(i.id)}
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    );

  return (
    <section className="plugins">
      {failed && <p className="error">{failed.message}</p>}
      {(plugins.data ?? []).map((p) => (
        <article key={p.name} className="plugin">
          <header>
            <h2>{p.name}</h2>
            <span className="muted">{p.version}</span>
            <ul className="caps">
              {(p.capabilities ?? []).map((c) => (
                <li key={c}>{c}</li>
              ))}
            </ul>
            {p.capabilities?.includes("theme") ? (
              <Link to="/settings/appearance">Select in Appearance</Link>
            ) : (
              <Link className="button" to={`/settings/plugins/${encodeURIComponent(p.name)}/new`}>
                Add instance
              </Link>
            )}
          </header>
          {table(all.filter((i) => i.plugin === p.name))}
        </article>
      ))}
      {orphans.length > 0 && (
        <article className="plugin">
          <header>
            <h2>Not installed</h2>
            <span className="muted">These instances' plugins are gone.</span>
          </header>
          {table(orphans)}
        </article>
      )}
    </section>
  );
}
