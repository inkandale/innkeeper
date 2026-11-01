import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { PluginPanel } from "./PluginPanel";

/** Launchers shows a corner bubble per enabled ui instance. It sits in the Shell, so an open panel survives navigation. */
export function Launchers() {
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const [open, setOpen] = useState<number>();
  const panels = (instances.data ?? []).flatMap((instance) => {
    const plugin = plugins.data?.find((p) => p.name === instance.plugin);
    return plugin?.ui_url && !instance.disabled && !instance.broken_error ? [{ instance, plugin, src: plugin.ui_url }] : [];
  });
  return (
    <div className="plugin-launchers">
      {panels.map(({ instance, plugin, src }) =>
        open === instance.id ? (
          <PluginPanel
            key={instance.id}
            instance={instance.id}
            title={plugin.title}
            src={src}
            grants={instance.grants ?? []}
            onClose={() => setOpen(undefined)}
          />
        ) : (
          <button key={instance.id} className="plugin-bubble" aria-label={`Open ${plugin.title}`} onClick={() => setOpen(instance.id)}>
            {plugin.icon ? <img src={plugin.icon} alt="" /> : "💬"}
          </button>
        ),
      )}
    </div>
  );
}
