import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { api, must } from "../api/client";
import { useToast } from "../shell/Toasts";
import { pluginsQuery, type InstanceInfo, type Plugin } from "./Plugins";
import { SchemaForm, type Schema } from "./SchemaForm";

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
  return <InstanceForm key={inst.data?.id ?? name} plugin={plugin} instance={inst.data} />;
}

/** canon is v as JSON with object keys sorted, so edits that move a key don't count as changes. */
function canon(v: unknown): string {
  return JSON.stringify(v, (_, x) =>
    x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.entries(x).sort(([a], [b]) => (a < b ? -1 : 1))) : x,
  );
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

  const save = useMutation({
    mutationFn: async (form: FormData) => {
      const name = String(form.get("name")).trim();
      const cfg = (config ?? {}) as Record<string, unknown>;
      const owner = caps.includes("auth") ? String(form.get("owner_subject") ?? "").trim() : undefined;
      const hasSecrets = Object.keys(secrets).length > 0;
      if (!instance) {
        return must(
          await api.POST("/instances", {
            body: {
              plugin: plugin.name,
              name,
              config: cfg,
              secrets: hasSecrets ? (secrets as Record<string, string>) : undefined,
              owner_subject: owner || undefined,
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
            secrets: hasSecrets ? secrets : undefined,
            owner_subject: owner !== undefined && owner !== (instance.owner_subject ?? "") ? owner : undefined,
          },
        }),
      );
    },
    onSuccess: (saved) => {
      qc.setQueryData(["instance", saved.id], saved);
      qc.invalidateQueries({ queryKey: ["instances"] });
      toast({ text: `Saved ${saved.name}.` });
      if (onSaved) onSaved(saved);
      else navigate("/settings/plugins");
    },
  });

  const check = useMutation({
    mutationFn: async (id: number) => must(await api.POST("/instances/{id}/check", { params: { path: { id } } })),
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
    >
      <h2>{instance ? instance.name : `New ${plugin.name} instance`}</h2>
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
      <div className="form-field">
        <label>
          <span>Name</span>
          <input className="input" name="name" required defaultValue={instance?.name} />
        </label>
      </div>
      {plugin.config && <SchemaForm schema={plugin.config as Schema} label="" value={config} onChange={setConfig} />}
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
      {check.data && (check.data.ok ? <p>Works.</p> : <p className="error">Doesn't work: {check.data.error}</p>)}
      {check.error && <p className="error">{check.error.message}</p>}
      {save.error && <p className="error">{save.error.message}</p>}
      <div className="actions">
        <button className="button primary" disabled={save.isPending}>
          Save
        </button>
        {instance && (
          <button type="button" className="button" disabled={check.isPending} onClick={() => check.mutate(instance.id)}>
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
