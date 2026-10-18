import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, must } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { useToast } from "../shell/Toasts";
import { type InstanceInfo, type Plugin } from "./Plugins";
import { Records } from "./Records";
import { FormContext, pointerKey, SchemaForm, unpointerKey, type Schema } from "./SchemaForm";

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
  if (plugins.isPending || (id !== undefined && inst.isPending)) return null;
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
  const props = Object.entries(schema?.properties ?? {});
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
  /** Runs after a save in place of going back to Settings. */
  onSaved?: (saved: InstanceInfo) => void;
  cancelTo?: string;
}) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const caps = plugin.capabilities ?? [];
  const [config, setConfig] = useState<unknown>(instance?.config ?? {});
  // What the form started from; a refetch mustn't make an untouched config look changed.
  const [initial] = useState(() => canon(instance?.config ?? {}));
  // Only typed secrets (a string) and removed ones (null) are sent.
  const [secrets, setSecrets] = useState<Record<string, string | null>>({});
  const [libraryAccess, setLibraryAccess] = useState(instance?.library_access ?? false);
  const schema = plugin.config as Schema | undefined;
  const { general, tabs } = tabsOf(schema);
  const others = useQuery(instancesQuery).data?.filter((i) => i.plugin === plugin.name && i.id !== instance?.id) ?? [];
  // A plugin may have one instance without a name; a singleton's never has one.
  const named = plugin.multiple_instances === true;
  const needsName = named && others.some((i) => !i.name);
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
    mutationFn: async (form: FormData) => {
      const name = named ? String(form.get("name")).trim() : undefined;
      const cfg = (config ?? {}) as Record<string, unknown>;
      const owner = caps.includes("auth") ? String(form.get("owner_subject") ?? "").trim() : undefined;
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
              name: name || undefined,
              config: cfg,
              default_bases,
              secrets: hasSecrets ? (secrets as Record<string, string>) : undefined,
              owner_subject: owner || undefined,
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
            name,
            config: canon(cfg) !== initial ? cfg : undefined,
            default_bases: canon(cfg) !== initial ? default_bases : undefined,
            secrets: hasSecrets ? secrets : undefined,
            owner_subject: owner !== undefined && owner !== (instance.owner_subject ?? "") ? owner : undefined,
            library_access: libraryAccess !== (instance.library_access ?? false) ? libraryAccess : undefined,
          },
        }),
      );
    },
    onSuccess: (saved) => {
      qc.setQueryData(["instance", saved.id], saved);
      qc.invalidateQueries({ queryKey: ["instances"] });
      qc.invalidateQueries({ queryKey: ["plugin-defaults", plugin.name] });
      toast({ text: `Saved ${saved.label}.` });
      if (onSaved) onSaved(saved);
      else navigate("/settings/plugins");
    },
    meta: { toast: false },
  });

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
        save.mutate(new FormData(e.currentTarget));
      }}
      // A required field on a hidden tab would block the save without showing why, so open the first one's tab.
      onInvalidCapture={(e) => {
        const first = e.currentTarget.querySelector("input:invalid, textarea:invalid, select:invalid");
        const panel = first?.closest('[role="tabpanel"]');
        if (panel) setTab(panel.id.replace("panel-", ""));
      }}
    >
      <h2>{instance ? instance.label : `New ${plugin.name} instance`}</h2>
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
            <input className="input" name="name" required={needsName} placeholder={needsName ? undefined : plugin.name} defaultValue={instance?.name} />
          </label>
          {needsName && <p className="muted">Another {plugin.name} instance has no name, so this one needs one.</p>}
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
            const set = instance?.secrets?.includes(s) ?? false;
            const pending = secrets[s];
            return (
              <div className="form-field" key={s}>
                <label>
                  <span>{s}</span>
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
                <input className="input" name="owner_subject" defaultValue={instance?.owner_subject} />
              </label>
              <small className="muted">The login that becomes the owner. Leave empty for none.</small>
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
      <div className="actions">
        {/* Saving before the defaults load would leave out the bases of the overrides. */}
        <button className="button primary" disabled={save.isPending || defaults.isPending}>
          Save
        </button>
        {instance && (
          <button type="button" className="button" title="Tests the saved settings" disabled={check.isPending} onClick={() => check.mutate(instance.id)}>
            Check
          </button>
        )}
        <Link className="button" to={cancelTo}>
          Cancel
        </Link>
      </div>
    </form>
  );
}
