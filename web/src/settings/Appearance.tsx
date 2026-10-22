import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { api, must } from "../api/client";
import { pluginsQuery, settingsQuery } from "../api/queries";
import { showTheme } from "../theme/link";

export function Appearance() {
  const qc = useQueryClient();
  const plugins = useQuery(pluginsQuery);
  const settings = useQuery(settingsQuery);
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
    // The pick stays: clearing it would show the old saved theme until the cache update lands.
    onSuccess: (s) => qc.setQueryData(settingsQuery.queryKey, s),
  });

  const themes = (plugins.data ?? []).filter((p) => p.capabilities?.includes("theme")).map((p) => p.name);
  const failed = settings.error ?? plugins.error;
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
        <p className="muted">No theme plugins are installed. Install one under Plugins to add it here.</p>
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
