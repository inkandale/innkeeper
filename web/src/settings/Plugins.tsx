import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, mustOk } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import type { components } from "../api/schema";

export type Plugin = components["schemas"]["PluginOut"];
export type InstanceInfo = components["schemas"]["InstanceInfo"];
type CatalogSource = components["schemas"]["CatalogSourceOut"];

// Offered after the first login, while none of them is installed.
const recommended = ["card-import", "lumiverse", "sillytavern"];

export function Plugins() {
  const qc = useQueryClient();
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery(instancesQuery);
  const sources = useQuery({ queryKey: ["plugin-sources"], queryFn: async () => must(await api.GET("/plugin-sources")) });
  // Sources are read only when the owner asks, never in the background.
  const catalog = useQuery({
    queryKey: ["plugin-catalog"],
    queryFn: async () => must(await api.GET("/plugin-catalog")),
    enabled: false,
  });
  const changed = () => {
    qc.invalidateQueries({ queryKey: ["plugins"] });
    qc.invalidateQueries({ queryKey: ["instances"] });
  };
  const remove = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/instances/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["instances"] }),
  });
  const install = useMutation({
    mutationFn: async (v: { name: string; source: string; version: string }) =>
      mustOk(
        await api.POST("/plugins/{name}/install", {
          params: { path: { name: v.name } },
          body: { source: v.source, version: v.version },
        }),
      ),
    onSuccess: changed,
  });
  const uninstall = useMutation({
    mutationFn: async (name: string) => mustOk(await api.DELETE("/plugins/{name}", { params: { path: { name } } })),
    onSuccess: changed,
  });
  const removeData = useMutation({
    mutationFn: async (name: string) => mustOk(await api.DELETE("/plugins/{name}/data", { params: { path: { name } } })),
    onSuccess: changed,
  });
  const addSource = useMutation({
    mutationFn: async (s: { id: string; url: string }) => mustOk(await api.POST("/plugin-sources", { body: s })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["plugin-sources"] }),
  });
  const deleteSource = useMutation({
    mutationFn: async (id: string) => mustOk(await api.DELETE("/plugin-sources/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["plugin-sources"] }),
  });
  const failed =
    remove.error ??
    install.error ??
    uninstall.error ??
    removeData.error ??
    addSource.error ??
    deleteSource.error ??
    plugins.error ??
    instances.error ??
    sources.error ??
    catalog.error;
  const busy = install.isPending || uninstall.isPending || removeData.isPending;

  const all = instances.data ?? [];
  const installed = plugins.data ?? [];
  const names = new Set(installed.map((p) => p.name));
  // Until the plugins load, every instance would look orphaned.
  const orphans = plugins.data ? all.filter((i) => !names.has(i.plugin)) : [];
  const orphanPlugins = [...new Set(orphans.map((i) => i.plugin))].sort();
  const offers = catalogOffers(catalog.data ?? [], installed);
  const showRecommended = plugins.data !== undefined && !recommended.some((n) => names.has(n));

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
      <div className="toolbar">
        <button className="button" disabled={catalog.isFetching} onClick={() => catalog.refetch()}>
          {catalog.isFetching ? "Checking…" : "Check for updates"}
        </button>
      </div>
      {showRecommended && (
        <p className="created" role="status">
          Innkeeper comes with login and card downloads. Check for updates to install the importer and the app
          targets: {recommended.join(", ")}.
        </p>
      )}
      {installed.map((p) => {
        const update = offers.updates.get(p.name);
        const prevSource = p.previous_source;
        const prevVersion = p.previous_version;
        return (
          <article key={p.name} className="plugin">
            <header>
              <h2>{p.name}</h2>
              <span className="muted">
                {p.source}/{p.version}
              </span>
              <ul className="caps">
                {(p.capabilities ?? []).map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
              <div className="actions">
                {update && (
                  <button
                    className="button primary"
                    disabled={busy}
                    onClick={() => install.mutate({ name: p.name, source: p.source, version: update })}
                  >
                    Update to {update}
                  </button>
                )}
                {prevSource && prevVersion && (
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() => install.mutate({ name: p.name, source: prevSource, version: prevVersion })}
                  >
                    Roll back to {prevVersion}
                  </button>
                )}
                {p.source !== "builtin" && (
                  <button
                    className="button"
                    disabled={busy}
                    onClick={() =>
                      confirm(
                        p.builtin
                          ? `Uninstall ${p.name}? Innkeeper goes back to its built-in copy.`
                          : `Uninstall ${p.name}? Its instances and data stay, and come back if you install it again.`,
                      ) && uninstall.mutate(p.name)
                    }
                  >
                    Uninstall
                  </button>
                )}
                {p.capabilities?.includes("theme") ? (
                  <Link to="/settings/appearance">Select in Appearance</Link>
                ) : (
                  <Link className="button" to={`/settings/plugins/${encodeURIComponent(p.name)}/new`}>
                    Add instance
                  </Link>
                )}
              </div>
            </header>
            {table(all.filter((i) => i.plugin === p.name))}
          </article>
        );
      })}
      {orphanPlugins.map((name) => (
        <article key={name} className="plugin">
          <header>
            <h2>{name}</h2>
            <span className="muted">Not installed. Its instances come back if you install it again.</span>
            <button
              className="button"
              disabled={busy}
              onClick={() =>
                prompt(`Remove ${name}'s instances and database? This can't be undone. Type ${name} to confirm.`) === name &&
                removeData.mutate(name)
              }
            >
              Remove data
            </button>
          </header>
          {table(orphans.filter((i) => i.plugin === name))}
        </article>
      ))}
      {catalog.data && (
        <>
          <h2>Available</h2>
          {catalog.data
            .filter((s) => s.error)
            .map((s) => (
              <p key={s.source.id} className="error">
                {s.source.id}: {s.error}
              </p>
            ))}
          {offers.available.length === 0 ? (
            <p className="muted">Every plugin the sources offer is installed.</p>
          ) : (
            <table>
              <tbody>
                {offers.available.map((o) => (
                  <tr key={`${o.source}/${o.name}`}>
                    <td>
                      {o.source}/{o.name}
                      {o.source === "official" && recommended.includes(o.name) && " (recommended)"}
                    </td>
                    <td className="muted">{o.description}</td>
                    <td>
                      <button
                        className="button primary"
                        disabled={busy}
                        aria-label={`Install ${o.source}/${o.name}`}
                        onClick={() => install.mutate({ name: o.name, source: o.source, version: o.version })}
                      >
                        Install {o.version}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
      <h2>Sources</h2>
      <p className="muted">Plugins run as code inside Innkeeper with their own database role. Add only sources you trust.</p>
      <table>
        <tbody>
          {(sources.data ?? []).map((s) => (
            <tr key={s.id}>
              <td>{s.id}</td>
              <td className="muted">{s.url}</td>
              <td>
                <button
                  className="button"
                  aria-label={`Delete source ${s.id}`}
                  onClick={() => confirm(`Delete the source ${s.id}?`) && deleteSource.mutate(s.id)}
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const f = new FormData(form);
          // Reset only on success, so a refused source stays to fix.
          addSource.mutate(
            { id: String(f.get("id")).trim(), url: String(f.get("url")).trim() },
            { onSuccess: () => form.reset() },
          );
        }}
      >
        <input className="input" name="id" required aria-label="Source id" placeholder="Id, such as mine" />
        <input className="input" name="url" required aria-label="Source URL" placeholder="https://…/index.toml" />
        <button className="button" disabled={addSource.isPending}>
          Add source
        </button>
      </form>
    </section>
  );
}

type Offer = { source: string; name: string; version: string; description: string };

/** catalogOffers splits what the sources list into updates for installed
 * plugins, from the source each came from, and plugins not installed. */
export function catalogOffers(catalog: CatalogSource[], installed: Plugin[]) {
  const updates = new Map<string, string>();
  const available: Offer[] = [];
  const byName = new Map(installed.map((p) => [p.name, p]));
  for (const s of catalog) {
    for (const p of s.plugins ?? []) {
      const have = byName.get(p.name);
      if (!have) {
        available.push({ source: s.source.id, name: p.name, version: p.version, description: p.description });
      } else if (have.source === s.source.id && p.version !== have.version) {
        // p.version is the source's newest, so anything else installed is older.
        updates.set(p.name, p.version);
      }
    }
  }
  return { updates, available };
}
