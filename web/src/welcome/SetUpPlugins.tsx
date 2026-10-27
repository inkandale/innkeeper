import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, Navigate, useNavigate, useParams } from "react-router";
import { api, must } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { InstanceForm } from "../settings/Instance";
import { type InstanceInfo, type Plugin } from "../settings/Plugins";
import type { Schema } from "../settings/SchemaForm";
import { nextStep, setupPlugins } from "./wizard";
import { Loading } from "../shell/Loading";

/** SetUpPlugins sets up an instance of each chosen plugin, one plugin per page. */
export function SetUpPlugins() {
  const { plugin: name } = useParams();
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery(instancesQuery);
  const failed = plugins.error ?? instances.error;
  if (failed) return <p className="error">{failed.message}</p>;
  if (plugins.isPending || instances.isPending) return <Loading />;
  const chosen = setupPlugins(plugins.data ?? []);
  const all = instances.data ?? [];
  if (chosen.length === 0) {
    return (
      <div className="panel">
        <p>
          No plugins to set up. <Link to="/welcome/plugins">Choose some first.</Link>
        </p>
      </div>
    );
  }
  if (!name) return <Navigate to={nextStep(chosen, all)} replace />;
  const plugin = chosen.find((p) => p.name === name);
  if (!plugin) return <p className="error">No plugin named {name} is installed.</p>;
  return (
    <PluginStep
      key={name}
      plugin={plugin}
      done={all.filter((i) => i.plugin === name)}
      next={nextStep(chosen, all, name)}
    />
  );
}

/** PluginStep sets up one plugin, then checks it can reach its app if it reads one. */
function PluginStep({ plugin, done, next }: { plugin: Plugin; done: InstanceInfo[]; next: string }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [saved, setSaved] = useState<InstanceInfo>();
  const [editing, setEditing] = useState(false);
  const check = useMutation({
    mutationFn: async (id: number) => must(await api.GET("/instances/{id}/items", { params: { path: { id } } })),
    meta: { toast: false },
  });
  const afterSave = (inst: InstanceInfo) => {
    setEditing(false);
    if (!plugin.capabilities?.includes("pull") || !plugin.capabilities.includes("push")) return navigate(next);
    setSaved(inst);
    check.mutate(inst.id);
  };

  // Nothing to ask for, so create the instance straight away.
  const bare = !Object.keys((plugin.config as Schema | undefined)?.properties ?? {}).length && !plugin.secrets?.length;
  const create = useMutation({
    mutationFn: async () => must(await api.POST("/instances", { body: { plugin: plugin.name } })),
    meta: { toast: false },
    onSuccess: (inst) => {
      qc.invalidateQueries({ queryKey: ["instances"] });
      afterSave(inst);
    },
  });
  // A ref, not state, so StrictMode's second effect run doesn't create a second instance.
  const started = useRef(false);
  useEffect(() => {
    if (!bare || done.length > 0 || started.current) return;
    started.current = true;
    create.mutate();
  }, [bare, done.length, create]);

  if (saved && editing) {
    return (
      <div className="panel">
        <InstanceForm plugin={plugin} instance={saved} onSaved={afterSave} cancelTo={next} />
      </div>
    );
  }
  if (saved) {
    return (
      <div className="panel">
        <h2>{saved.label}</h2>
        {check.isPending && <p className="muted">Checking the connection…</p>}
        {check.isSuccess && (
          <>
            <p>✓ Connected</p>
            <button className="button primary" onClick={() => navigate(next)}>
              Next
            </button>
          </>
        )}
        {check.isError && (
          <>
            <p className="error">{check.error.message}</p>
            <div className="toolbar">
              <button className="button" onClick={() => check.mutate(saved.id)}>
                Retry
              </button>
              <button className="button" onClick={() => setEditing(true)}>
                Edit
              </button>
              <Link className="button" to={next}>
                Skip
              </Link>
            </div>
          </>
        )}
      </div>
    );
  }
  if (done.length > 0) {
    return (
      <div className="panel">
        <h2>{plugin.name}</h2>
        {done.map((i) => (
          <p key={i.id}>✓ {i.label}</p>
        ))}
        <Link className="button primary" to={next}>
          Next
        </Link>
      </div>
    );
  }
  if (bare) {
    return (
      <div className="panel">
        <h2>{plugin.name}</h2>
        {create.error ? <p className="error">{create.error.message}</p> : <p className="muted">Setting up…</p>}
        {create.error && (
          <Link className="button" to={next}>
            Skip this plugin
          </Link>
        )}
      </div>
    );
  }
  return (
    <div className="panel">
      <InstanceForm plugin={plugin} onSaved={afterSave} cancelTo="/welcome/plugins" />
      <Link to={next}>Skip this plugin</Link>
    </div>
  );
}
