import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { instancesQuery, pluginsQuery } from "../api/queries";
import { PluginPanel } from "./PluginPanel";

/** Launchers shows a corner bubble per enabled ui instance whose Show switch is on. It sits in the Shell, so an open panel survives navigation. */
export function Launchers() {
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const [open, setOpen] = useState<number>();
  const panels = (instances.data ?? []).flatMap((instance) => {
    const plugin = plugins.data?.find((p) => p.name === instance.plugin);
    const on = !instance.disabled && !instance.broken_error && instance.grants?.includes("panel");
    return plugin?.ui_url && plugin.ui_title && on ? [{ instance, title: plugin.ui_title, icon: plugin.icon, src: plugin.ui_url }] : [];
  });
  return (
    <div className="plugin-launchers">
      {panels.map(({ instance, title, icon, src }) =>
        open === instance.id ? (
          <PluginPanel
            key={instance.id}
            instance={instance.id}
            title={title}
            src={src}
            grants={instance.grants ?? []}
            onClose={() => setOpen(undefined)}
          />
        ) : (
          <button key={instance.id} className="plugin-bubble" aria-label={`Open ${title}`} onClick={() => setOpen(instance.id)}>
            {icon ? <img src={icon} alt="" /> : "💬"}
          </button>
        ),
      )}
    </div>
  );
}
