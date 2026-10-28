import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router";
import { api, must, mustOk } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import type { components } from "../api/schema";
import { useConfirm } from "../shell/ConfirmDialog";
import { Menu } from "../shell/Menu";
import { useToast } from "../shell/Toasts";

export type Plugin = components["schemas"]["PluginOut"];
export type InstanceInfo = components["schemas"]["InstanceInfo"];
type CatalogSource = components["schemas"]["CatalogSourceOut"];

export function Plugins() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const [ask, dialog] = useConfirm();
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery(instancesQuery);
  const sources = useQuery({ queryKey: ["plugin-sources"], queryFn: async () => must(await api.GET("/plugin-sources")) });
  // Read when the page opens and when the owner asks, never on a timer.
  const catalog = useQuery({
    queryKey: ["plugin-catalog"],
    queryFn: async () => must(await api.GET("/plugin-catalog")),
    staleTime: Infinity,
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
    mutationFn: async (v: { id: number; label: string; disabled: boolean }) =>
      mustOk(await api.PATCH("/instances/{id}", { params: { path: { id: v.id } }, body: { disabled: v.disabled } })),
    onSuccess: (_, v) => {
      changed();
      if (v.disabled)
        toast({ text: `Disabled ${v.label}`, action: { label: "Undo", run: () => setDisabled.mutate({ ...v, disabled: false }) } });
    },
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
  // A source needs naming only when there's another to tell it from.
  const from = (source: string) => ((sources.data?.length ?? 0) > 1 ? `${source}/` : "");

  const needSetup = installed.filter((p) => p.status === "needs_setup").length;
  const addURL = (p: Plugin) => `/settings/plugins/${encodeURIComponent(p.name)}/new`;

  const instanceActions = (i: InstanceInfo) => (
    <>
      <button
        aria-label={`${i.disabled ? "Enable" : "Disable"} ${i.label}`}
        disabled={setDisabled.isPending}
        onClick={() => setDisabled.mutate({ id: i.id, label: i.label, disabled: !i.disabled })}
      >
        {i.disabled ? "Enable" : "Disable"}
      </button>
      <button
        aria-label={`Delete instance ${i.label}`}
        onClick={() =>
          ask({
            title: `Delete ${i.label}?`,
            body: <p>Its jobs stay, and logins through it end.</p>,
            confirmLabel: "Delete instance",
            danger: true,
            onConfirm: () => remove.mutate(i.id),
          })
        }
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
            <h3>{p.title}</h3>{" "}
            <span className="muted">{p.source === "builtin" ? `${p.version} · built in` : `${from(p.source)}${p.version}`}</span>
            {p.description && <p className="muted">{p.description}</p>}
          </div>
          {!listed && <StatusText status={p.status} error={only?.broken_error} />}
          {update && (
            <button
              className="button primary"
              disabled={busy}
              onClick={() => install.mutate({ name: p.name, source: p.source, version: update.version })}
            >
              Update to {update.version}
            </button>
          )}
          {p.capabilities?.includes("theme") ? (
            <Link to="/settings/general">Select in General</Link>
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
                    ask({
                      title: `Uninstall ${p.name}?`,
                      body: (
                        <p>
                          {p.builtin
                            ? "Innkeeper goes back to its built-in copy."
                            : "Its instances and data stay, and come back if you install it again."}
                        </p>
                      ),
                      confirmLabel: "Uninstall",
                      onConfirm: () => uninstall.mutate(p.name),
                    })
                  }
                >
                  Uninstall
                </button>
              )}
            </Menu>
          )}
        </header>
        {update && update.notes.length > 0 && (
          <details className="release-notes">
            <summary>What's new</summary>
            {update.notes.map(([version, text]) => (
              <section key={version}>
                <h4>{version}</h4>
                <p>{text}</p>
              </section>
            ))}
          </details>
        )}
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
        <button
          className="button"
          title="Reads the plugin sources for newer versions and for plugins you haven't installed."
          disabled={catalog.isFetching}
          onClick={() => catalog.refetch()}
        >
          {catalog.isFetching ? "Checking…" : "Check for updates"}
        </button>
      </div>
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
                      ask({
                        title: `Remove ${name}'s data?`,
                        body: <p>This deletes its instances and database, for good.</p>,
                        confirmLabel: "Remove data",
                        danger: true,
                        typeToConfirm: name,
                        onConfirm: () => removeData.mutate(name),
                      })
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
        <section className="plugin-group" aria-labelledby="available-heading">
          <h2 id="available-heading">Available</h2>
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
            byCategory(offers.available).map(([category, list]) => (
              <div key={category}>
                <h3>{category}</h3>
                {list.map((o) => (
                  <article key={`${o.source}/${o.name}`} className="plugin available">
                    <header>
                      <div className="about">
                        <span>
                          {from(o.source)}
                          {o.title}
                          {o.source === "official" && o.recommended && " (recommended)"}
                        </span>
                        <p className="muted">{o.description}</p>
                      </div>
                      <button
                        className="button primary"
                        disabled={busy}
                        aria-label={`Install ${from(o.source)}${o.title}`}
                        onClick={() => install.mutate({ name: o.name, source: o.source, version: o.version })}
                      >
                        Install {o.version}
                      </button>
                    </header>
                  </article>
                ))}
              </div>
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
                    onClick={() =>
                      ask({
                        title: `Delete the source ${s.id}?`,
                        confirmLabel: "Delete source",
                        danger: true,
                        onConfirm: () => deleteSource.mutate(s.id),
                      })
                    }
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
      {dialog}
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
function byCategory<P extends { category?: string | null }>(list: P[]): [string, P[]][] {
  const groups = new Map<string, P[]>();
  for (const p of list) {
    const c = p.category || "Other";
    groups.set(c, [...(groups.get(c) ?? []), p]);
  }
  return [...groups].sort(([a], [b]) => (a === "Other" ? 1 : b === "Other" ? -1 : a.localeCompare(b)));
}

type Update = { version: string; notes: [version: string, text: string][] };
type Offer = { source: string; name: string; title: string; category?: string; version: string; description: string; recommended?: boolean };

/** catalogOffers splits what the sources list into updates for installed
 * plugins, from the source each came from, and plugins not installed. */
function catalogOffers(catalog: CatalogSource[], installed: Plugin[]) {
  const updates = new Map<string, Update>();
  const available: Offer[] = [];
  const byName = new Map(installed.map((p) => [p.name, p]));
  for (const s of catalog) {
    for (const p of s.plugins ?? []) {
      const have = byName.get(p.name);
      if (!have) {
        const { name, version, description, recommended, category } = p;
        available.push({ source: s.source.id, name, title: p.title || name, category, version, description, recommended });
      } else if (have.source === s.source.id && p.version !== have.version) {
        // p.version is the source's newest, so anything else installed is older.
        // versions is newest first; without the installed one listed, only the newest's notes apply.
        const versions = p.versions ?? [];
        const at = versions.indexOf(have.version);
        const newer = at < 0 ? [p.version] : versions.slice(0, at);
        const notes = newer.flatMap((v): Update["notes"] => (p.notes?.[v] ? [[v, p.notes[v]]] : []));
        updates.set(p.name, { version: p.version, notes });
      }
    }
  }
  return { updates, available };
}
