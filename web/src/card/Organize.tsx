import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, mustOk, route, type Kind } from "../api/client";
import { bindingsQuery, collectionsQuery, useFollowers } from "../api/queries";
import type { components } from "../api/schema";
import { removeSource, sourceLabel, sourceRemoteId } from "../sync/sync";
import { useBindingEdits } from "./Upstream";

type Binding = components["schemas"]["Binding"];

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

/** Sources lists the sites a card or lorebook was published on, and how checking each for updates stands, with remove, add by URL, and a check now. */
export function Sources({ kind, id }: { kind: Kind; id: number }) {
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const list = useQuery({
    queryKey: [kind, id, "sources"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/sources"), { params: { path: { id } } })),
  });
  const bindings = useQuery(bindingsQuery(kind, id));
  const edits = useBindingEdits(kind, id);
  const followers = useFollowers();
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
    // Removing a source unbinds it too.
    onSuccess: () => Promise.all([done(), edits.refresh()]),
  });
  const following = (s: { kind: string; ref: string }) => bindings.data?.find((b) => !b.push && b.remote_id === sourceRemoteId(s));
  return (
    <section>
      <h2>Sources</h2>
      <ul className="plain">
        {(list.data ?? []).map((s) => {
          const b = following(s);
          return (
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
              {b && <Follow b={b} label={`${s.kind}: ${s.ref}`} edits={edits} />}
            </li>
          );
        })}
      </ul>
      {!!list.data?.length && !!followers?.size && (
        <button className="button" disabled={edits.check.isPending} onClick={() => edits.check.mutate()}>
          Check for updates
        </button>
      )}
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

/** Follow says how checking one source for updates stands, and pauses it, resumes it, or takes its updates without review. */
function Follow({ b, label, edits }: { b: Binding; label: string; edits: ReturnType<typeof useBindingEdits> }) {
  const busy = edits.edit.isPending;
  const set = (body: { paused?: boolean; autosync?: boolean }) => edits.edit.mutate({ binding: b.id, body });
  if (b.paused)
    return (
      <span className="muted">
        {" "}
        Not checking for updates{" "}
        <button className="button" disabled={busy} onClick={() => set({ paused: false })}>
          Check again
        </button>
      </span>
    );
  return (
    <span className="muted">
      {" "}
      {sourceLabel(b.remote_state, b.remote_id)}
      {b.check_error && <span className="error"> {b.check_error}</span>}{" "}
      <button className="button" disabled={busy} onClick={() => set({ paused: true })}>
        Ignore all
      </button>{" "}
      <label className="autosync">
        <input
          type="checkbox"
          checked={b.autosync}
          aria-label={`Autosync from ${label}`}
          disabled={busy}
          onChange={(e) => set({ autosync: e.target.checked })}
        />{" "}
        autosync
      </label>
    </span>
  );
}
