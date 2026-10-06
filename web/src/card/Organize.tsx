import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, mustOk, route, type Kind } from "../api/client";
import { useToast } from "../shell/Toasts";

// These save on change: tags, collections, and sources aren't part of a revision.

/** Tags lists a card's tags, with remove and add. */
export function Tags({ id, tags }: { id: number; tags: string[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const put = useMutation({
    mutationFn: async (next: string[]) => must(await api.PUT("/cards/{id}/tags", { params: { path: { id } }, body: { tags: next } })),
    // Pending lasts until the card refetches, so the next change builds on fresh tags.
    onSuccess: () => {
      setName("");
      return qc.invalidateQueries({ queryKey: ["card", id] });
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });
  return (
    <section>
      <h2>Tags</h2>
      <p className="chips">
        {tags.map((t) => (
          <span key={t} className="tag">
            {t}
            <button className="chip-x" aria-label={`Remove tag ${t}`} disabled={put.isPending} onClick={() => put.mutate(tags.filter((x) => x !== t))}>
              ×
            </button>
          </span>
        ))}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const t = name.trim();
          if (t && !tags.includes(t) && !put.isPending) put.mutate([...tags, t]);
        }}
      >
        <input className="input" aria-label="Add tag" placeholder="Add tag" value={name} onChange={(e) => setName(e.target.value)} />
      </form>
    </section>
  );
}

/** Collections has a box per collection, checked for the ones the card is in. */
export function Collections({ id, member }: { id: number; member: number[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const all = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const toggle = useMutation({
    mutationFn: async ({ collection, on }: { collection: number; on: boolean }) => {
      const opts = { params: { path: { id: collection, card_id: id } } };
      mustOk(on ? await api.PUT("/collections/{id}/cards/{card_id}", opts) : await api.DELETE("/collections/{id}/cards/{card_id}", opts));
    },
    onSuccess: () =>
      Promise.all([qc.invalidateQueries({ queryKey: ["card", id] }), qc.invalidateQueries({ queryKey: ["collections"] })]),
    onError: (e) => toast({ text: e.message, error: true }),
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
  const toast = useToast();
  const [url, setUrl] = useState("");
  const list = useQuery({
    queryKey: [kind, id, "sources"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/sources"), { params: { path: { id } } })),
  });
  const done = () => void qc.invalidateQueries({ queryKey: [kind, id, "sources"] });
  const fail = (e: Error) => toast({ text: e.message, error: true });
  const add = useMutation({
    mutationFn: async (u: string) => must(await api.PUT(route(kind, "/cards/{id}/sources"), { params: { path: { id } }, body: { url: u } })),
    onSuccess: () => {
      setUrl("");
      done();
    },
    onError: fail,
  });
  const remove = useMutation({
    mutationFn: async (sourceId: number) =>
      mustOk(await api.DELETE(route(kind, "/cards/{id}/sources/{source_id}"), { params: { path: { id, source_id: sourceId } } })),
    onSuccess: done,
    onError: fail,
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
