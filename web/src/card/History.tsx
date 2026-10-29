import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { api, ApiError, must, route, thumb, type Kind } from "../api/client";
import { pluginsQuery } from "../api/queries";
import { useModal } from "../shell/useModal";
import { Diff } from "../sync/Diff";
import "./Card.css";
import { toastError } from "../shell/Toasts";
import { toast } from "sonner";

type AvatarPick = "keep" | "revision";

/** actor names who made a revision, from its source: ui, mcp, reconcile, or plugin:<name>. */
function actor(source: string, plugins: { name: string; title?: string }[]): string {
  if (source.startsWith("plugin:")) {
    const name = source.slice("plugin:".length);
    return plugins.find((p) => p.name === name)?.title || name;
  }
  return { ui: "You", mcp: "An AI assistant", reconcile: "Sync" }[source] ?? source;
}

/** AvatarChoice asks which image a restore keeps when the revision's differs from the head's. */
function AvatarChoice({ head, rev, busy, onPick, onCancel }: {
  head?: string;
  rev?: string;
  busy: boolean;
  onPick: (avatar: AvatarPick) => void;
  onCancel: () => void;
}) {
  const modal = useModal(() => !busy && onCancel());
  const image = (sha: string | undefined, alt: string) =>
    sha ? <img className="history-thumb" src={thumb(sha, 160)} alt={alt} /> : <span className="muted">No image</span>;
  return (
    <dialog {...modal} className="crop" aria-label="Choose an image">
      <p>This revision has a different image. Which one should the restore keep?</p>
      <div className="row image-choice">
        <div>
          {image(head, "Current image")}
          <button className="button primary" disabled={busy} onClick={() => onPick("keep")}>
            Keep the current image
          </button>
        </div>
        <div>
          {image(rev, "This revision's image")}
          <button className="button" disabled={busy} onClick={() => onPick("revision")}>
            Use this revision's image
          </button>
        </div>
      </div>
      <button className="button" disabled={busy} onClick={onCancel}>
        Cancel
      </button>
    </dialog>
  );
}

/** History lists a card's or lorebook's revisions and diffs the chosen one against the one before it or the head. */
export function History({ kind = "card" }: { kind?: Kind }) {
  const id = Number(useParams().id);
  const [params, setParams] = useSearchParams();
  const qc = useQueryClient();
  const plugins = useQuery(pluginsQuery);
  const revs = useQuery({
    queryKey: [kind, id, "revisions"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/revisions"), { params: { path: { id } } })),
  });
  const list = revs.data ?? [];
  // Revisions list newest first, so the head is first.
  const head = list[0];
  const at = Math.max(0, list.findIndex((r) => r.id === Number(params.get("rev"))));
  const rev = list[at];
  const vsHead = params.get("against") === "head";
  const [from, to] = vsHead ? [rev, head] : [list[at + 1], rev];

  const diff = useQuery({
    queryKey: [kind, id, "compare", from?.id, to?.id],
    enabled: !!from && !!to && from.id !== to.id,
    queryFn: async () =>
      must(
        await api.GET(route(kind, "/cards/{id}/revisions/{revision_id}/compare/{other_id}"), {
          params: { path: { id, revision_id: from!.id, other_id: to!.id } },
        }),
      ),
  });
  const [asking, setAsking] = useState(false);
  const restore = useMutation({
    mutationFn: async (r: { id: number; base: number; avatar: AvatarPick }) =>
      must(
        await api.POST(route(kind, "/cards/{id}/restore"), {
          params: { path: { id } },
          body: { revision_id: r.id, base_revision_id: r.base, avatar: r.avatar, source: "ui" },
        }),
      ),
    onSettled: () => setAsking(false),
    onSuccess: (_, r) => {
      void qc.invalidateQueries({ queryKey: [kind, id] });
      setParams({});
      toast.success(`Restored revision ${r.id}`);
    },
    meta: { toast: false },
    onError: (e) => {
      void qc.invalidateQueries({ queryKey: [kind, id, "revisions"] });
      const stale = e instanceof ApiError && e.status === 409;
      toastError(stale ? `The ${kind} changed since you opened this page. Check the new head, then restore again.` : e.message);
    },
  });

  const set = (next: Record<string, string>) => setParams({ ...Object.fromEntries(params), ...next });
  const changes = (diff.data?.fields ?? []).filter((f) => (f.hunks ?? []).some((h) => h.status !== "same"));

  if (revs.isError) return <p className="error page">{revs.error.message}</p>;
  return (
    <div className="history">
      <nav className="card-aside" aria-label="Revisions">
        <Link to={`/${kind}s/${id}`}>← Back to {kind}</Link>
        <ol className="plain">
          {list.map((r) => (
            <li key={r.id}>
              <button className="revision" aria-current={r.id === rev?.id ? "true" : undefined} onClick={() => set({ rev: String(r.id) })}>
                <strong>{r.message || `Revision ${r.id}`}</strong>
                <span className="muted">
                  {new Date(r.created_at).toLocaleString()} · {actor(r.source, plugins.data ?? [])}
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
              <span className="row" role="radiogroup" aria-label="Compare against">
                <label className="check">
                  <input type="radio" name="against" checked={!vsHead} onChange={() => set({ against: "prev" })} /> Against previous
                </label>
                <label className="check">
                  <input type="radio" name="against" checked={vsHead} onChange={() => set({ against: "head" })} /> Against head
                </label>
              </span>
              {head && rev.id !== head.id && (
                <button
                  className="button primary"
                  disabled={restore.isPending}
                  onClick={() => (rev.avatar === head.avatar ? restore.mutate({ id: rev.id, base: head.id, avatar: "keep" }) : setAsking(true))}
                >
                  Restore
                </button>
              )}
              {asking && head && (
                <AvatarChoice
                  head={head.avatar}
                  rev={rev.avatar}
                  busy={restore.isPending}
                  onPick={(avatar) => restore.mutate({ id: rev.id, base: head.id, avatar })}
                  onCancel={() => setAsking(false)}
                />
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
                {changes.length === 0 ? (
                  <p>No field changed.</p>
                ) : (
                  <Diff scope={`revision:${from.id}:${to?.id}`} cmp={{ fields: changes }} sides={{ library: "Before", app: "After" }} />
                )}
              </>
            )}
          </>
        )}
      </div>
    </div>
  );
}
