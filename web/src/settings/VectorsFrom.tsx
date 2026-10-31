import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { settingsQuery } from "../api/queries";
import { SaveStatus } from "./SaveStatus";

/** VectorsFrom picks the enrich instance whose card vectors similar cards and semantic search read. */
export function VectorsFrom() {
  const qc = useQueryClient();
  const sources = useQuery({
    queryKey: ["instances", "offers", "vector.query"],
    queryFn: async () => must(await api.GET("/instances", { params: { query: { offers: "vector.query" } } })),
  });
  const settings = useQuery(settingsQuery);
  const save = useMutation({
    mutationFn: async (vector_instance: number | null) => {
      qc.setQueryData(settingsQuery.queryKey, must(await api.PATCH("/settings", { body: { vector_instance } })));
    },
    meta: { toast: false },
  });
  if (settings.data && !settings.data.vectors_available) {
    return (
      <p className="muted">
        Similar cards by meaning and semantic search need pgvector.{" "}
        <a href="https://github.com/inkandale/innkeeper/blob/main/docs/running.md" target="_blank" rel="noreferrer">How to enable it</a>
      </p>
    );
  }
  // With one vector source, core uses it without asking.
  if (!sources.data || sources.data.length < 2) return null;
  const saved = settings.data?.vector_instance;
  return (
    <section>
      <label title="The plugin instance whose card vectors find similar cards and rank semantic search.">
        Vectors from{" "}
        <select
          className="input"
          value={sources.data.some((i) => i.id === saved) ? saved! : ""}
          disabled={!settings.data || save.isPending}
          onChange={(e) => save.mutate(e.target.value === "" ? null : Number(e.target.value))}
        >
          <option value="">None</option>
          {sources.data.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
      </label>{" "}
      <SaveStatus save={save} />
    </section>
  );
}
