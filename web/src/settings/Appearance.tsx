import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, must } from "../api/client";
import { showTheme } from "../theme/link";
import { pluginsQuery } from "./Plugins";

export function Appearance() {
  const qc = useQueryClient();
  const plugins = useQuery(pluginsQuery);
  const settings = useQuery({ queryKey: ["settings"], queryFn: async () => must(await api.GET("/settings")) });
  const [picked, setPicked] = useState<string | null>(null);
  const saved = settings.data?.theme ?? "";
  const shown = picked ?? saved;

  // Preview the pick. The cleanup puts the saved theme back when the pick changes or the tab closes.
  const ready = settings.data !== undefined;
  useEffect(() => {
    if (!ready) return;
    showTheme(shown);
    return () => showTheme(saved);
  }, [ready, shown, saved]);

  const save = useMutation({
    mutationFn: async (theme: string) => must(await api.PATCH("/settings", { body: { theme } })),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      setPicked(null);
    },
  });

  const themes = (plugins.data ?? []).filter((p) => p.capabilities?.includes("theme")).map((p) => p.name);
  const failed = save.error ?? settings.error ?? plugins.error;
  const changed = picked !== null && picked !== saved;

  return (
    <section className="appearance">
      {failed && <p className="error">{failed.message}</p>}
      {/* Picks wait for the saved theme, and for a save in flight. */}
      <fieldset disabled={!ready || save.isPending}>
        <legend>Theme</legend>
        {["", ...themes].map((name) => (
          <label key={name} className="check">
            <input type="radio" name="theme" checked={shown === name} onChange={() => setPicked(name)} />
            {name || "Default"}
            {name === saved && <span className="muted"> (current)</span>}
          </label>
        ))}
      </fieldset>
      {themes.length === 0 && (
        <p className="muted">No theme plugins are installed. A theme is a plugin with capabilities = ["theme"] and a stylesheet.</p>
      )}
      <p className="muted">Picking a theme previews it. Saving sets it for every page, including login.</p>
      <div className="actions">
        <button className="button primary" disabled={!changed || save.isPending} onClick={() => save.mutate(picked!)}>
          Save
        </button>
        {changed && (
          <button className="button" onClick={() => setPicked(null)}>
            Revert
          </button>
        )}
      </div>
    </section>
  );
}
