import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, must } from "../api/client";
import { instancesQuery, librariesQuery, pluginsQuery } from "../api/queries";
import { Loading } from "../shell/Loading";
import { useLeaveGuard } from "../shell/useLeaveGuard";
import { type InstanceInfo, type Plugin } from "./Plugins";
import { Records } from "./Records";
import { FormContext, orderedProperties, pointerKey, SchemaForm, shownWhen, unpointerKey, type Schema } from "./SchemaForm";
import { toast } from "sonner";

/** Instance adds an instance at /settings/plugins/:plugin/new and edits one at /settings/instances/:id. */
export function Instance() {
  const { plugin: pluginName, id } = useParams();
  const plugins = useQuery(pluginsQuery);
  const inst = useQuery({
    queryKey: ["instance", Number(id)],
    enabled: id !== undefined,
    queryFn: async () => must(await api.GET("/instances/{id}", { params: { path: { id: Number(id) } } })),
  });
  const failed = plugins.error ?? inst.error;
  if (failed) return <p className="error">{failed.message}</p>;
  if (plugins.isPending || (id !== undefined && inst.isPending)) return <Loading />;
  const name = inst.data?.plugin ?? pluginName;
  const plugin = plugins.data?.find((p) => p.name === name);
  if (!plugin) return <p className="error">No plugin named {name} is installed.</p>;
  const saved = inst.data;
  return (
    <>
      <InstanceForm key={saved?.id ?? name} plugin={plugin} instance={saved} />
      {saved && Object.entries(plugin.records ?? {}).map(([type, rt]) => <Records key={type} instance={saved.id} type={type} recordType={rt} />)}
    </>
  );
}

/** canon is v as JSON with object keys sorted, so edits that move a key don't count as changes. */
function canon(v: unknown): string {
  return JSON.stringify(v, (_, x) =>
    x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => (a < b ? -1 : 1))) : x,
  );
}

/** at returns the value at a JSON pointer into v. */
function at(v: unknown, pointer: string): unknown {
  return pointer
    .split("/")
    .slice(1)
    .map(unpointerKey)
    .reduce<unknown>((o, k) => (o && typeof o === "object" ? (o as Record<string, unknown>)[k] : undefined), v);
}

const isTab = (s: Schema) => s.type === "object" && s.properties !== undefined;

/** tabsOf splits a config schema: each top-level object is a tab, and the rest go on General. */
function tabsOf(schema: Schema | undefined): { general: Schema; tabs: [string, Schema][] } {
  const props = orderedProperties(schema);
  return {
    general: { ...schema, properties: Object.fromEntries(props.filter(([, s]) => !isTab(s))) },
    tabs: props.filter(([, s]) => isTab(s)),
  };
}

/** services lists the x-offers of the schema's instance pickers, so each list is fetched once. */
function services(schema: Schema | undefined): string[] {
  const out = new Set<string>();
  const walk = (s: Schema) => {
    if (s.format === "instance" && s["x-offers"]) out.add(s["x-offers"]);
    Object.values(s.properties ?? {}).forEach(walk);
  };
  if (schema) walk(schema);
  return [...out];
}

/** InstanceForm adds or edits an instance of plugin; Settings and the setup wizard both use it. */
export function InstanceForm({
  plugin,
  instance,
  onSaved,
  cancelTo = "/settings/plugins",
}: {
  plugin: Plugin;
  instance?: InstanceInfo;
  /** Runs after a save in place of staying on, or opening, the instance's page. */
  onSaved?: (saved: InstanceInfo) => void;
  cancelTo?: string;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const caps = plugin.capabilities ?? [];
  const [config, setConfig] = useState<unknown>(instance?.config ?? {});
  // What the form started from; a refetch mustn't make an untouched config look changed.
  const [initial, setInitial] = useState(() => canon(instance?.config ?? {}));
  // Only typed secrets (a string) and removed ones (null) are sent.
  const [secrets, setSecrets] = useState<Record<string, string | null>>({});
  const stored = {
    name: instance?.name ?? "",
    owner: instance?.owner_subject ?? "",
    libraryAccess: instance?.library_access ?? false,
    syncLibrary: instance?.sync_library ?? 1,
  };
  const [name, setName] = useState(stored.name);
  const [owner, setOwner] = useState(stored.owner);
  const [libraryAccess, setLibraryAccess] = useState(stored.libraryAccess);
  const [syncLibrary, setSyncLibrary] = useState(stored.syncLibrary);
  const [created, setCreated] = useState<number>();
  const libraries = useQuery(librariesQuery).data ?? [];
  // A target app syncs with one library; with only one there's nothing to pick.
  const syncs = !!instance && (caps.includes("push") || caps.includes("pull")) && libraries.length > 1;
  const schema = plugin.config as Schema | undefined;
  const { general, tabs } = tabsOf(schema);
  const others = useQuery(instancesQuery).data?.filter((i) => i.plugin === plugin.name && i.id !== instance?.id) ?? [];
  // A plugin may have one instance without a name; a singleton's never has one.
  const named = plugin.multiple_instances === true;
  const needsName = named && others.some((i) => !i.name);
  const dirty =
    canon(config ?? {}) !== initial ||
    Object.keys(secrets).length > 0 ||
    // Save trims these, so trailing spaces alone aren't a change.
    (named && name.trim() !== stored.name) ||
    (caps.includes("auth") && owner.trim() !== stored.owner) ||
    libraryAccess !== stored.libraryAccess ||
    syncLibrary !== stored.syncLibrary;
  const discard = () => {
    setConfig(JSON.parse(initial));
    setSecrets({});
    setName(stored.name);
    setOwner(stored.owner);
    setLibraryAccess(stored.libraryAccess);
    setSyncLibrary(stored.syncLibrary);
  };
  const [tab, setTab] = useState("general");
  const tabIds = ["general", ...tabs.map(([k]) => k)];
  // Arrow keys move between the tabs, selecting as they go.
  const arrow = (key: string) => {
    const step = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    const next = tabIds[(tabIds.indexOf(tab) + step + tabIds.length) % tabIds.length]!;
    setTab(next);
    document.getElementById(`tab-${next}`)?.focus();
  };
  const defaults = useQuery({
    queryKey: ["plugin-defaults", plugin.name, instance?.id],
    queryFn: async () =>
      must(
        await api.GET("/plugins/{name}/settings/defaults", {
          params: { path: { name: plugin.name }, query: instance ? { instance: instance.id } : {} },
        }),
      ),
  });
  const defs = defaults.data?.defaults ?? {};
  const draft = () => ({ instance: instance?.id, config: (config ?? {}) as Record<string, unknown>, secrets });
  const offering = useQueries({
    queries: services(schema).map((offers) => ({
      queryKey: ["instances", { offers }],
      queryFn: async () => ({ offers, list: must(await api.GET("/instances", { params: { query: { offers } } })) }),
    })),
  });
  const helpers = {
    defaults: defs,
    updated: new Set(defaults.data?.updated ?? []),
    loadOptions: async (field: string) =>
      must(await api.POST("/plugins/{name}/settings/options", { params: { path: { name: plugin.name } }, body: { ...draft(), field } })).options ?? [],
    instances: (service: string) => offering.find((q) => q.data?.offers === service)?.data?.list ?? [],
  };
  const run = useMutation({
    mutationFn: async (action: string) =>
      must(await api.POST("/plugins/{name}/settings/run", { params: { path: { name: plugin.name } }, body: { ...draft(), action } })),
    meta: { toast: false },
  });

  const save = useMutation({
    mutationFn: async () => {
      const trimmedName = named ? name.trim() : undefined;
      const cfg = (config ?? {}) as Record<string, unknown>;
      const trimmedOwner = caps.includes("auth") ? owner.trim() : undefined;
      const hasSecrets = Object.keys(secrets).length > 0;
      // For each override this edit set, the default it replaced, so the form can say when that default changes.
      const startedFrom = JSON.parse(initial) as unknown;
      const bases = Object.fromEntries(
        Object.entries(defs).filter(([p]) => at(cfg, p) !== undefined && canon(at(cfg, p)) !== canon(at(startedFrom, p))),
      );
      const default_bases = Object.keys(bases).length > 0 ? bases : undefined;
      if (!instance) {
        return must(
          await api.POST("/instances", {
            body: {
              plugin: plugin.name,
              name: trimmedName || undefined,
              config: cfg,
              default_bases,
              secrets: hasSecrets ? (secrets as Record<string, string>) : undefined,
              owner_subject: trimmedOwner || undefined,
              library_access: libraryAccess || undefined,
            },
          }),
        );
      }
      // Changing config, secrets, or owner ends the instance's logins, so send them only when they changed.
      return must(
        await api.PATCH("/instances/{id}", {
          params: { path: { id: instance.id } },
          body: {
            name: trimmedName,
            config: canon(cfg) !== initial ? cfg : undefined,
            default_bases: canon(cfg) !== initial ? default_bases : undefined,
            secrets: hasSecrets ? secrets : undefined,
            owner_subject: trimmedOwner !== undefined && trimmedOwner !== stored.owner ? trimmedOwner : undefined,
            library_access: libraryAccess !== (instance.library_access ?? false) ? libraryAccess : undefined,
            sync_library: syncLibrary !== instance.sync_library ? syncLibrary : undefined,
          },
        }),
      );
    },
    onSuccess: (saved) => {
      qc.setQueryData(["instance", saved.id], saved);
      qc.invalidateQueries({ queryKey: ["instances"] });
      qc.invalidateQueries({ queryKey: ["plugin-defaults", plugin.name] });
      toast.success(`Saved ${saved.label}.`);
      if (onSaved) return onSaved(saved);
      if (!instance) return setCreated(saved.id);
      // The rest of the baseline comes from the instance, which the cache update above refreshes.
      setInitial(canon(saved.config ?? {}));
      setSecrets({});
    },
    meta: { toast: false },
  });

  useLeaveGuard(dirty && created === undefined);
  // Opening the new instance waits for the render that lifts the guard.
  useEffect(() => {
    if (created !== undefined) navigate(`/settings/instances/${created}`);
  }, [created, navigate]);

  const check = useMutation({
    mutationFn: async (id: number) => must(await api.POST("/instances/{id}/check", { params: { path: { id } } })),
    meta: { toast: false },
    // A check can mark the instance broken or clear that.
    onSettled: () => {
      qc.invalidateQueries({ queryKey: ["instance", instance?.id] });
      qc.invalidateQueries({ queryKey: ["instances"] });
    },
  });

  return (
    <form
      className="instance-form"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
      // A required field on a hidden tab would block the save without showing why, so open the first one's tab.
      onInvalidCapture={(e) => {
        const first = e.currentTarget.querySelector("input:invalid, textarea:invalid, select:invalid");
        const panel = first?.closest('[role="tabpanel"]');
        if (panel) setTab(panel.id.replace("panel-", ""));
      }}
    >
      <h2>{instance ? instance.label : `Set up ${plugin.title}`}</h2>
      {instance?.broken_error && (
        <p className="error">
          Broken since {new Date(instance.broken_at ?? instance.updated_at).toLocaleString()}: {instance.broken_error}.
          Saving or a check that starts the plugin clears this.
        </p>
      )}
      {instance && caps.includes("auth") && (
        <p className="muted">
          Changing the owner, config, or secrets ends every login through this provider, including yours if you used it.
        </p>
      )}
      {named && (
        <div className="form-field">
          <label>
            <span>Name</span>
            <input
              className="input"
              name="name"
              required={needsName}
              placeholder={needsName ? undefined : plugin.name}
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </label>
          {needsName && <p className="muted">Another {plugin.title} instance has no name, so this one needs one.</p>}
        </div>
      )}
      <FormContext.Provider value={helpers}>
        {tabs.length > 0 && (
          <div role="tablist" aria-label="Settings" className="instance-tabs" onKeyDown={(e) => arrow(e.key)}>
            {[["general", "General"] as const, ...tabs.map(([k, s]) => [k, s.title ?? k] as const)].map(([k, label]) => (
              <button
                key={k}
                type="button"
                role="tab"
                id={`tab-${k}`}
                aria-selected={tab === k}
                aria-controls={`panel-${k}`}
                tabIndex={tab === k ? 0 : -1}
                className={tab === k ? "on" : undefined}
                onClick={() => setTab(k)}
              >
                {label}
              </button>
            ))}
          </div>
        )}
        <div
          role={tabs.length > 0 ? "tabpanel" : undefined}
          id="panel-general"
          aria-labelledby={tabs.length > 0 ? "tab-general" : undefined}
          hidden={tab !== "general"}
        >
          {schema && <SchemaForm schema={general} label="" value={config} onChange={setConfig} />}
          {(plugin.secrets ?? []).map((s) => {
            const about = schema?.["x-secrets"]?.[s];
            const configured = (config ?? {}) as Record<string, unknown>;
            if (!shownWhen(about?.["x-when"], (k) => configured[k] ?? defs[`/${pointerKey(k)}`])) return null;
            const set = instance?.secrets?.includes(s) ?? false;
            const pending = secrets[s];
            return (
              <div className="form-field" key={s}>
                <label>
                  <span>{about?.title ?? secretLabel(s)}</span>
                  <input
                    className="input"
                    type="password"
                    autoComplete="new-password"
                    value={pending ?? ""}
                    onChange={(e) =>
                      setSecrets(({ [s]: _, ...rest }) => (e.target.value ? { ...rest, [s]: e.target.value } : rest))
                    }
                  />
                </label>
                {about?.description && <small className="muted">{about.description}</small>}
                <small className="muted">{pending === null ? "Removed when you save." : set ? "Set. Type to replace it." : "Not set."}</small>
                {set && pending === undefined && (
                  <button type="button" className="button" aria-label={`Remove ${s}`} onClick={() => setSecrets((x) => ({ ...x, [s]: null }))}>
                    Remove
                  </button>
                )}
              </div>
            );
          })}
          {caps.includes("auth") && (
            <div className="form-field">
              <label>
                <span>Owner subject</span>
                <input className="input" name="owner_subject" value={owner} onChange={(e) => setOwner(e.target.value)} />
              </label>
              <small className="muted">Your user ID at this login provider (its subject claim). That login becomes the owner. Leave empty for none.</small>
            </div>
          )}
          {syncs && (
            <div className="form-field">
              <label>
                <span>Syncs with</span>
                <select className="input" value={syncLibrary} onChange={(e) => setSyncLibrary(Number(e.target.value))}>
                  {libraries.map((l) => (
                    <option key={l.id} value={l.id}>
                      {l.name}
                    </option>
                  ))}
                </select>
              </label>
              <small className="muted">The library this app's syncs match its items against and import new ones into.</small>
            </div>
          )}
          {caps.includes("action") && (
            <div className="form-field">
              <label className="check">
                <span>Library access</span>
                <input type="checkbox" checked={libraryAccess} onChange={(e) => setLibraryAccess(e.target.checked)} />
              </label>
              <small className="muted">Lets actions that ask for it read your cards and lorebooks while they run.</small>
            </div>
          )}
          {Object.keys(plugin.settings_actions ?? {}).length > 0 && (
            <div className="actions">
              {Object.entries(plugin.settings_actions ?? {}).map(([id, label]) => (
                <button key={id} type="button" className="button" disabled={run.isPending} onClick={() => run.mutate(id)}>
                  {label}
                </button>
              ))}
            </div>
          )}
          {run.data && (
            <div role="status" className={run.data.ok ? undefined : "error"}>
              {run.data.message && <p>{run.data.message}</p>}
              <ul>
                {(run.data.checks ?? []).map((c, i) => (
                  <li key={i}>
                    {c.ok ? "✓" : "✗"} {c.name}
                    {c.latency_ms ? ` — ${c.latency_ms} ms` : ""}
                    {c.message ? `: ${c.message}` : ""}
                  </li>
                ))}
              </ul>
            </div>
          )}
          {run.error && <p className="error">{run.error.message}</p>}
        </div>
        {tabs.map(([k, s]) => (
          <div key={k} role="tabpanel" id={`panel-${k}`} aria-labelledby={`tab-${k}`} hidden={tab !== k}>
            <SchemaForm
              schema={{ ...s, title: "" }}
              label=""
              path={`/${pointerKey(k)}`}
              value={(config as Record<string, unknown> | undefined)?.[k]}
              onChange={(v) =>
                setConfig((c: unknown) => {
                  const { [k]: _, ...rest } = (c ?? {}) as Record<string, unknown>;
                  return v === undefined ? rest : { ...rest, [k]: v };
                })
              }
            />
          </div>
        ))}
      </FormContext.Provider>
      {defaults.error && <p className="error">Couldn't load the defaults: {defaults.error.message}</p>}
      {check.data && (check.data.ok ? <p>Works.</p> : <p className="error">Doesn't work: {check.data.error}</p>)}
      {check.error && <p className="error">{check.error.message}</p>}
      {save.error && <p className="error">{save.error.message}</p>}
      <div className={dirty ? "actions unsaved" : "actions"}>
        {dirty && (
          <>
            <span className="muted">Unsaved changes</span>
            <button type="button" className="button" onClick={discard}>
              Discard
            </button>
          </>
        )}
        {instance && (
          <button type="button" className="button" title="Tests the saved settings. Save first to test your changes." disabled={check.isPending} onClick={() => check.mutate(instance.id)}>
            Check
          </button>
        )}
        {/* Saving before the defaults load would leave out the bases of the overrides. */}
        <button className="button primary" disabled={save.isPending || defaults.isPending}>
          Save
        </button>
        {!dirty && (
          <Link className="button" to={cancelTo}>
            Cancel
          </Link>
        )}
      </div>
    </form>
  );
}

/** secretLabel turns a manifest's snake_case secret name into a label: api_key reads "API key". */
export function secretLabel(name: string): string {
  const words = name.split("_").map((w) => (["api", "id", "url"].includes(w) ? w.toUpperCase() : w));
  const first = words[0] ?? "";
  return [first.charAt(0).toUpperCase() + first.slice(1), ...words.slice(1)].join(" ");
}
