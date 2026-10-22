import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { librarySettingsQuery, settingsQuery, useInstancesWith } from "../api/queries";
import { SaveStatus } from "./SaveStatus";

/**
 * ScoresFrom picks the enrich instance whose values the library grid, filters, and sort use: the default for
 * every library, or, given a library, that library's override.
 */
export function ScoresFrom({ library }: { library?: number }) {
  const qc = useQueryClient();
  const enrichers = useInstancesWith("enrich");
  const settings = useQuery({ ...settingsQuery, enabled: library === undefined });
  const libSettings = useQuery({ ...librarySettingsQuery(library ?? 0), enabled: library !== undefined });
  const save = useMutation({
    mutationFn: async (score_instance: number | null) => {
      if (library === undefined) {
        qc.setQueryData(settingsQuery.queryKey, must(await api.PATCH("/settings", { body: { score_instance } })));
        return;
      }
      const out = must(await api.PATCH("/libraries/{id}/settings", { params: { path: { id: library } }, body: { score_instance } }));
      qc.setQueryData(librarySettingsQuery(library).queryKey, out);
    },
    onSuccess: () => {
      // A new default changes every library that follows it.
      void qc.invalidateQueries({ queryKey: ["library-settings"] });
      void qc.invalidateQueries({ queryKey: ["enrichment-keys"] });
      void qc.invalidateQueries({ queryKey: ["search"] });
    },
    meta: { toast: false },
  });
  if (!enrichers.length) return null;
  const automatic = enrichers.length === 1 ? `Automatic (${enrichers[0]!.name})` : "None";
  let saved: number | null | undefined, empty: string, loaded: boolean;
  if (library === undefined) {
    saved = settings.data?.score_instance;
    empty = automatic;
    loaded = !!settings.data;
  } else {
    const s = libSettings.data?.score_instance;
    saved = s?.overridden ? s.value : null;
    empty = `Same as default (${enrichers.find((i) => i.id === s?.default)?.label ?? automatic})`;
    loaded = !!s;
  }
  return (
    <section className="scores-from">
      <label title="The enrich instance whose scores the library grid, score filters, sort, and Hide below score use.">
        Scores from{" "}
        <select
          className="input"
          value={saved ?? ""}
          disabled={!loaded || save.isPending}
          onChange={(e) => save.mutate(e.target.value === "" ? null : Number(e.target.value))}
        >
          <option value="">{empty}</option>
          {enrichers.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
      </label>{" "}
      <SaveStatus save={save} />
      {library === undefined && (
        <p className="muted">
          The library grid, score filters, and sort use this instance's values, unless a library picks its own under
          Libraries. Card pages show every instance's.
        </p>
      )}
    </section>
  );
}
