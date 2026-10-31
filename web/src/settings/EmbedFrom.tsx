import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { settingsQuery } from "../api/queries";
import { SaveStatus } from "./SaveStatus";

/** EmbedFrom picks the ai instance whose ai.embed every library's card vectors come from. */
export function EmbedFrom() {
  const qc = useQueryClient();
  const embedders = useQuery({
    queryKey: ["instances", "offers", "ai.embed"],
    queryFn: async () => must(await api.GET("/instances", { params: { query: { offers: "ai.embed" } } })),
  });
  const settings = useQuery(settingsQuery);
  const save = useMutation({
    mutationFn: async (vector_instance: number | null) => {
      qc.setQueryData(settingsQuery.queryKey, must(await api.PATCH("/settings", { body: { vector_instance } })));
    },
    meta: { toast: false },
  });
  if (!embedders.data?.length) return null;
  const saved = settings.data?.vector_instance;
  return (
    <section className="scores-from">
      <label title="The AI instance that turns every card into a vector, for similar cards and semantic search.">
        Embeddings from{" "}
        <select
          className="input"
          value={embedders.data.some((i) => i.id === saved) ? saved! : ""}
          disabled={!settings.data || save.isPending}
          onChange={(e) => save.mutate(e.target.value === "" ? null : Number(e.target.value))}
        >
          <option value="">None</option>
          {embedders.data.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
      </label>{" "}
      <SaveStatus save={save} />
      <p className="muted">
        Every card in every library is sent to this instance to find similar cards. Picking it again embeds every
        card again.
      </p>
    </section>
  );
}

/** EmbedNow is a stub: the embed-library route is gone; Task 7 removes it. */
export function EmbedNow(_: { library: number }) {
  return null;
}
