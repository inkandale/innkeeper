import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router";
import { api, must, thumb } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { show } from "./draft";
import "./Card.css";

type Field = components["schemas"]["Field"];

/** Hunks renders a field's hunks inline: the first revision's text struck out, the second's marked. */
export function Hunks({ field }: { field: Field }) {
  return (
    <p className="hunks">
      {(field.hunks ?? []).map((h) =>
        h.status === "same" ? (
          <span key={h.id}>{show(h.library)}</span>
        ) : (
          <span key={h.id}>
            {h.library !== undefined && <del>{show(h.library)}</del>}
            {h.app !== undefined && <ins>{show(h.app)}</ins>}
          </span>
        ),
      )}
    </p>
  );
}

/** History lists a card's revisions and diffs the chosen one against the one before it or the head. */
export function History() {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const toast = useToast();
  const revs = useQuery({
    queryKey: ["card", id, "revisions"],
    queryFn: async () => must(await api.GET("/cards/{id}/revisions", { params: { path: { id } } })),
  });
  const list = revs.data ?? [];
  // Revisions list newest first, so the head is first.
  const head = list[0];
  const at = Math.max(0, list.findIndex((r) => r.id === Number(params.get("rev"))));
  const rev = list[at];
  const vsHead = params.get("against") === "head";
  const [from, to] = vsHead ? [rev, head] : [list[at + 1], rev];

  const diff = useQuery({
    queryKey: ["card", id, "compare", from?.id, to?.id],
    enabled: !!from && !!to && from.id !== to.id,
    queryFn: async () =>
      must(
        await api.GET("/cards/{id}/revisions/{revision_id}/compare/{other_id}", {
          params: { path: { id, revision_id: from!.id, other_id: to!.id } },
        }),
      ),
  });
  const restore = useMutation({
    mutationFn: async (r: { id: number; base: number }) =>
      must(await api.POST("/cards/{id}/restore", { params: { path: { id } }, body: { revision_id: r.id, base_revision_id: r.base, source: "ui" } })),
    onSuccess: (_, r) => {
      void qc.invalidateQueries({ queryKey: ["card", id] });
      setParams({});
      toast({ text: `Restored revision ${r.id}` });
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });

  const set = (next: Record<string, string>) => setParams({ ...Object.fromEntries(params), ...next });
  const changes = (diff.data?.fields ?? []).filter((f) => (f.hunks ?? []).some((h) => h.status !== "same"));

  if (revs.isError) return <p className="error page">{revs.error.message}</p>;
  return (
    <div className="history">
      <nav className="card-aside" aria-label="Revisions">
        <Link to={`/cards/${id}`}>← Back to card</Link>
        <ol className="plain">
          {list.map((r) => (
            <li key={r.id}>
              <button className="revision" aria-current={r.id === rev?.id ? "true" : undefined} onClick={() => set({ rev: String(r.id) })}>
                <strong>{r.message || `Revision ${r.id}`}</strong>
                <span className="muted">
                  {new Date(r.created_at).toLocaleString()} · {r.source}
                </span>
              </button>
            </li>
          ))}
        </ol>
      </nav>
      <div className="card-main">
        {rev && (
          <>
            <div className="row">
              <h1>{rev.message || `Revision ${rev.id}`}</h1>
              <label className="check">
                <input type="radio" name="against" checked={!vsHead} onChange={() => set({ against: "prev" })} /> Against previous
              </label>
              <label className="check">
                <input type="radio" name="against" checked={vsHead} onChange={() => set({ against: "head" })} /> Against head
              </label>
              {head && rev.id !== head.id && (
                <button className="button primary" disabled={restore.isPending} onClick={() => restore.mutate({ id: rev.id, base: head.id })}>
                  Restore
                </button>
              )}
            </div>
            {!from ? (
              <p>This is the first revision.</p>
            ) : from.id === to?.id ? (
              <p>This is the head.</p>
            ) : diff.isError ? (
              <p className="error">{diff.error.message}</p>
            ) : diff.isPending ? (
              <p>Loading…</p>
            ) : (
              <>
                {from.avatar !== to?.avatar && (
                  <section>
                    <h2>Image</h2>
                    <p className="row">
                      {from.avatar ? <img className="history-thumb" src={thumb(from.avatar, 160)} alt="Before" /> : "None"} →{" "}
                      {to?.avatar ? <img className="history-thumb" src={thumb(to.avatar, 160)} alt="After" /> : "None"}
                    </p>
                  </section>
                )}
                {changes.length === 0 && <p>No field changed.</p>}
                {changes.map((f) => (
                  <section key={f.path}>
                    <h2>{f.path}</h2>
                    <Hunks field={f} />
                  </section>
                ))}
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
}
