import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { settingsQuery, useInstancesWith } from "../api/queries";

/** ScoresFrom picks the enrich instance whose values the library grid, filters, and sort use. */
export function ScoresFrom() {
  const qc = useQueryClient();
  const enrichers = useInstancesWith("enrich");
  const settings = useQuery(settingsQuery);
  const save = useMutation({
    mutationFn: async (score_instance: number | null) => must(await api.PATCH("/settings", { body: { score_instance } })),
    onSuccess: (s) => {
      qc.setQueryData(settingsQuery.queryKey, s);
      void qc.invalidateQueries({ queryKey: ["enrichment-keys"] });
      void qc.invalidateQueries({ queryKey: ["search"] });
    },
  });
  if (!enrichers.length) return null;
  const saved = settings.data?.score_instance;
  return (
    <section className="scores-from">
      <label>
        Scores from{" "}
        <select
          className="input"
          value={saved ?? ""}
          disabled={!settings.data || save.isPending}
          onChange={(e) => save.mutate(e.target.value === "" ? null : Number(e.target.value))}
        >
          <option value="">{enrichers.length === 1 ? `Automatic (${enrichers[0]!.name})` : "None"}</option>
          {enrichers.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
      </label>
      <p className="muted">The library grid, score filters, and sort use this instance's values. Card pages show every instance's.</p>
      {save.error && <p className="error">{save.error.message}</p>}
    </section>
  );
}
