import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, mustOk, route, type Kind } from "../api/client";
import { collectionsQuery } from "../api/queries";
import { removeSource } from "../sync/sync";

// These save on change: collections and sources aren't part of a revision.

/** Collections has a box per collection, checked for the ones the card is in. */
export function Collections({ id, member }: { id: number; member: number[] }) {
  const qc = useQueryClient();
  const all = useQuery(collectionsQuery);
  const toggle = useMutation({
    mutationFn: async ({ collection, on }: { collection: number; on: boolean }) => {
      const opts = { params: { path: { id: collection, card_id: id } } };
      mustOk(on ? await api.PUT("/collections/{id}/cards/{card_id}", opts) : await api.DELETE("/collections/{id}/cards/{card_id}", opts));
    },
    onSuccess: () =>
      Promise.all([qc.invalidateQueries({ queryKey: ["card", id] }), qc.invalidateQueries({ queryKey: ["collections"] })]),
  });
  if (!all.data?.length) return null;
  return (
    <section>
      <h2>Collections</h2>
      {all.data.map((c) => (
        <label key={c.id} className="check">
          <input
            type="checkbox"
            checked={member.includes(c.id)}
            disabled={toggle.isPending}
            onChange={(e) => toggle.mutate({ collection: c.id, on: e.target.checked })}
          />{" "}
          {c.name}
        </label>
      ))}
    </section>
  );
}

/** Sources lists the sites a card or lorebook was published on, with remove and add by URL. */
export function Sources({ kind, id }: { kind: Kind; id: number }) {
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const list = useQuery({
    queryKey: [kind, id, "sources"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/sources"), { params: { path: { id } } })),
  });
  const done = () => void qc.invalidateQueries({ queryKey: [kind, id, "sources"] });
  const add = useMutation({
    mutationFn: async (u: string) => must(await api.PUT(route(kind, "/cards/{id}/sources"), { params: { path: { id } }, body: { url: u } })),
    onSuccess: () => {
      setUrl("");
      done();
    },
  });
  const remove = useMutation({
    mutationFn: (sourceId: number) => removeSource(kind, id, sourceId),
    onSuccess: done,
  });
  return (
    <section>
      <h2>Sources</h2>
      <ul className="plain">
        {(list.data ?? []).map((s) => (
          <li key={s.id}>
            {/* Only https links, so a stored URL can't run script. */}
            {s.url?.startsWith("https://") ? (
              <a href={s.url} target="_blank" rel="noreferrer">
                {s.kind}: {s.ref}
              </a>
            ) : (
              `${s.kind}: ${s.ref}`
            )}
            <button className="chip-x" aria-label={`Remove source ${s.ref}`} disabled={remove.isPending} onClick={() => remove.mutate(s.id)}>
              ×
            </button>
          </li>
        ))}
      </ul>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (url.trim()) add.mutate(url.trim());
        }}
      >
        <input className="input" aria-label="Add source URL" placeholder="Source URL" value={url} onChange={(e) => setUrl(e.target.value)} />
      </form>
    </section>
  );
}
