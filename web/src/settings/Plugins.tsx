import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router";
import { api, must, mustOk } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import type { components } from "../api/schema";
import { Menu } from "../shell/Menu";
import { ScoresFrom } from "./ScoresFrom";

export type Plugin = components["schemas"]["PluginOut"];
export type InstanceInfo = components["schemas"]["InstanceInfo"];
type CatalogSource = components["schemas"]["CatalogSourceOut"];

export function Plugins() {
  const navigate = useNavigate();
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
    onSuccess: changed,
  });
  const setDisabled = useMutation({
    mutationFn: async (v: { id: number; disabled: boolean }) =>
      mustOk(await api.PATCH("/instances/{id}", { params: { path: { id: v.id } }, body: { disabled: v.disabled } })),
    onSuccess: changed,
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
  // The index that names them isn't read until asked, so this goes by what they do.
  const showRecommended =
    plugins.data !== undefined && !installed.some((p) => p.capabilities?.some((c) => c === "import" || c === "push"));

  const needSetup = installed.filter((p) => p.status === "needs_setup").length;
  const addURL = (p: Plugin) => `/settings/plugins/${encodeURIComponent(p.name)}/new`;

  const instanceActions = (i: InstanceInfo) => (
    <>
      <button
        aria-label={`${i.disabled ? "Enable" : "Disable"} ${i.label}`}
        disabled={setDisabled.isPending}
        onClick={() => setDisabled.mutate({ id: i.id, disabled: !i.disabled })}
      >
        {i.disabled ? "Enable" : "Disable"}
      </button>
      <button
        aria-label={`Delete instance ${i.label}`}
        onClick={() => confirm(`Delete ${i.label}? Its jobs stay, and logins through it end.`) && remove.mutate(i.id)}
      >
        Delete instance
      </button>
    </>
  );

  const instanceRows = (list: InstanceInfo[]) => (
    <ul className="instances">
      {list.map((i) => (
        <li key={i.id}>
          <Link to={`/settings/instances/${i.id}`}>{i.label}</Link>
          <StatusText status={i.status} error={i.broken_error} />
          <Menu label={`More for ${i.label}`}>{instanceActions(i)}</Menu>
        </li>
      ))}
    </ul>
  );

  const card = (p: Plugin) => {
    const list = all.filter((i) => i.plugin === p.name);
    const update = offers.updates.get(p.name);
    const prevSource = p.previous_source;
    const prevVersion = p.previous_version;
    // A singleton keeps instances made before it was one; list them so they can be deleted.
    const listed = list.length > 1;
    const only = list.length === 1 ? list[0] : undefined;
    const canUninstall = p.source !== "builtin";
    const canAdd = p.multiple_instances === true && list.length > 0;
    return (
      <article key={p.name} className="plugin">
        <header>
          <div className="about">
            <h3>{p.name}</h3>{" "}
            <span className="muted">{p.source === "builtin" ? `${p.version} · built in` : `${p.source}/${p.version}`}</span>
            {p.description && <p className="muted">{p.description}</p>}
          </div>
          {!listed && <StatusText status={p.status} error={only?.broken_error} />}
          {update && (
            <button
              className="button primary"
              disabled={busy}
              onClick={() => install.mutate({ name: p.name, source: p.source, version: update })}
            >
              Update to {update}
            </button>
          )}
          {p.capabilities?.includes("theme") ? (
            <Link to="/settings/appearance">Select in Appearance</Link>
          ) : (
            <>
              {only && (
                <Link className="button" to={`/settings/instances/${only.id}`}>
                  Settings
                </Link>
              )}
              {list.length === 0 && (
                <Link className="button primary" to={addURL(p)}>
                  Set up
                </Link>
              )}
            </>
          )}
          {(canUninstall || only || canAdd || (prevSource && prevVersion)) && (
            <Menu label={`More for ${p.name}`}>
              {canAdd && <button onClick={() => navigate(addURL(p))}>Add instance</button>}
              {prevSource && prevVersion && (
                <button disabled={busy} onClick={() => install.mutate({ name: p.name, source: prevSource, version: prevVersion })}>
                  Roll back to {prevVersion}
                </button>
              )}
              {only && instanceActions(only)}
              {canUninstall && (
                <button
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
            </Menu>
          )}
        </header>
        {listed && instanceRows(list)}
      </article>
    );
  };

  return (
    <section className="plugins">
      {failed && <p className="error">{failed.message}</p>}
      <div className="toolbar">
        <span className="muted">
          {[
            `${installed.length} installed`,
            offers.updates.size > 0 && `${offers.updates.size} to update`,
            needSetup > 0 && `${needSetup} need setup`,
          ]
            .filter(Boolean)
            .join(" · ")}
        </span>
        <button className="button" disabled={catalog.isFetching} onClick={() => catalog.refetch()}>
          {catalog.isFetching ? "Checking…" : "Check for updates"}
        </button>
      </div>
      <ScoresFrom />
      {showRecommended && (
        <p className="created" role="status">
          Innkeeper comes with login and card downloads. Check for updates to install the importer and the app
          targets.
        </p>
      )}
      {byCategory(installed).map(([category, list]) => (
        <section key={category} className="plugin-group">
          <h2>{category}</h2>
          {list.map(card)}
        </section>
      ))}
      {orphanPlugins.length > 0 && (
        <section className="plugin-group">
          <h2>Not installed</h2>
          {orphanPlugins.map((name) => (
            <article key={name} className="plugin">
              <header>
                <div className="about">
                  <h3>{name}</h3>
                  <p className="muted">Its instances come back if you install it again.</p>
                </div>
                <Menu label={`More for ${name}`}>
                  <button
                    disabled={busy}
                    onClick={() =>
                      prompt(`Remove ${name}'s instances and database? This can't be undone. Type ${name} to confirm.`) ===
                        name && removeData.mutate(name)
                    }
                  >
                    Remove data
                  </button>
                </Menu>
              </header>
              {instanceRows(orphans.filter((i) => i.plugin === name))}
            </article>
          ))}
        </section>
      )}
      {catalog.data && (
        <section className="plugin-group">
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
            offers.available.map((o) => (
              <article key={`${o.source}/${o.name}`} className="plugin available">
                <header>
                  <div className="about">
                    <span>
                      {o.source}/{o.name}
                      {o.source === "official" && o.recommended && " (recommended)"}
                    </span>
                    <p className="muted">{o.description}</p>
                  </div>
                  <button
                    className="button primary"
                    disabled={busy}
                    aria-label={`Install ${o.source}/${o.name}`}
                    onClick={() => install.mutate({ name: o.name, source: o.source, version: o.version })}
                  >
                    Install {o.version}
                  </button>
                </header>
              </article>
            ))
          )}
        </section>
      )}
      <details className="plugin-sources">
        <summary>Plugin sources</summary>
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
      </details>
    </section>
  );
}

const statusWords = {
  ready: ["Ready", "succeeded"],
  needs_setup: ["Needs setup", "setup"],
  broken: ["Broken", "failed"],
  disabled: ["Disabled", "disabled"],
} as const;

/** StatusText shows a plugin's or instance's status in the page's four words. */
function StatusText({ status, error }: { status: keyof typeof statusWords; error?: string | null }) {
  const [word, tone] = statusWords[status];
  return (
    <span className={`state ${tone}`}>
      {word}
      {status === "broken" && error && (
        <>
          : <span>{error}</span>
        </>
      )}
    </span>
  );
}

/** byCategory groups plugins under their category, alphabetically, with Other last. */
function byCategory(list: Plugin[]): [string, Plugin[]][] {
  const groups = new Map<string, Plugin[]>();
  for (const p of list) {
    const c = p.category || "Other";
    groups.set(c, [...(groups.get(c) ?? []), p]);
  }
  return [...groups].sort(([a], [b]) => (a === "Other" ? 1 : b === "Other" ? -1 : a.localeCompare(b)));
}

type Offer = { source: string; name: string; version: string; description: string; recommended?: boolean };

/** catalogOffers splits what the sources list into updates for installed
 * plugins, from the source each came from, and plugins not installed. */
function catalogOffers(catalog: CatalogSource[], installed: Plugin[]) {
  const updates = new Map<string, string>();
  const available: Offer[] = [];
  const byName = new Map(installed.map((p) => [p.name, p]));
  for (const s of catalog) {
    for (const p of s.plugins ?? []) {
      const have = byName.get(p.name);
      if (!have) {
        const { name, version, description, recommended } = p;
        available.push({ source: s.source.id, name, version, description, recommended });
      } else if (have.source === s.source.id && p.version !== have.version) {
        // p.version is the source's newest, so anything else installed is older.
        updates.set(p.name, p.version);
      }
    }
  }
  return { updates, available };
}
