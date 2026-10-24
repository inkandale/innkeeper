import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import { settingsQuery } from "../api/queries";
import { JobLine } from "../cards/ImportForm";
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
    mutationFn: async (embed_instance: number | null) => {
      qc.setQueryData(settingsQuery.queryKey, must(await api.PATCH("/settings", { body: { embed_instance } })));
    },
    meta: { toast: false },
  });
  if (!embedders.data?.length) return null;
  const saved = settings.data?.embed_instance;
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

/** EmbedNow embeds one library's cards ahead of the hourly run over every library. */
export function EmbedNow({ library }: { library: number }) {
  const settings = useQuery(settingsQuery);
  const [job, setJob] = useState<number>();
  const start = useMutation({
    mutationFn: async () => must(await api.POST("/libraries/{id}/embed", { params: { path: { id: library } } })),
    onSuccess: (j) => setJob(j.id),
  });
  if (settings.data?.embed_instance == null) return null;
  return (
    <section>
      <button className="button" disabled={start.isPending} onClick={() => start.mutate()}>
        Embed now
      </button>{" "}
      <span className="muted">Gives this library's cards their vectors first, for similar cards and semantic search.</span>
      {job && <JobLine label="Embedding" id={job} to={`/settings/jobs/${job}`} />}
      {start.error && <p className="error">{start.error.message}</p>}
    </section>
  );
}
