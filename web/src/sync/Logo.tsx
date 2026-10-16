import { useState } from "react";
import type { components } from "../api/schema";

type Instance = components["schemas"]["InstanceInfo"];
type Plugin = components["schemas"]["PluginOut"];

/** App is what a logo shows: the instance's plugin, and its icon when the plugin ships one. */
export type App = { plugin: string; icon?: string };

/** apps maps each instance ID to its plugin's logo. */
export function apps(instances?: Instance[] | null, plugins?: Plugin[] | null): Map<number, App> {
  const icons = new Map((plugins ?? []).map((p) => [p.name, p.icon]));
  return new Map((instances ?? []).map((i) => [i.id, { plugin: i.plugin, icon: icons.get(i.plugin) }]));
}

/** Logo is a plugin's icon, or its first letter when it has none or the icon fails to load. */
export function Logo({ app, label }: { app?: App; label: string }) {
  const [broken, setBroken] = useState(false);
  const plugin = app?.plugin ?? "";
  return (
    <span className="logo" role="img" aria-label={label} title={label}>
      {app?.icon && !broken ? (
        <img src={app.icon} alt="" onError={() => setBroken(true)} />
      ) : (
        <span aria-hidden="true">{plugin.slice(0, 1).toUpperCase() || "?"}</span>
      )}
    </span>
  );
}
