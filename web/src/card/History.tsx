import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLayoutEffect, useRef, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { api, ApiError, must, route, thumb, type Kind } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { show } from "./draft";
import "./Card.css";

type Field = components["schemas"]["Field"];

type Hunk = components["schemas"]["Hunk"];

const unescape = (s: string) => s.replace(/\\(.)/g, "$1");

/** entryOf splits an entries hunk ID, entries[<entry>].<field>#n, into an entry label and its field. */
export function entryOf(id: string): { entry: string; field: string } {
  const m = /^entries\[((?:\\.|[^\]\\])*)\](?:\.(.*))?#\d+$/.exec(id);
  if (!m) return { entry: id, field: "" };
  const raw = unescape(m[1] ?? "").replace(/~\d+$/, "");
  const entry = raw.startsWith("c:") ? raw.slice(2) : raw.slice(2).split(",").join(", ") || "(no keys)";
  return { entry, field: unescape(m[2] ?? "") };
}

/** EntryHunks shows an entries field's changes, grouped by entry, each field named. */
function EntryHunks({ hunks }: { hunks: Hunk[] }) {
  const entries = new Map<string, Map<string, Hunk[]>>();
  for (const h of hunks) {
    const { entry, field } = entryOf(h.id);
    const fields = entries.get(entry) ?? new Map<string, Hunk[]>();
    fields.set(field, [...(fields.get(field) ?? []), h]);
    entries.set(entry, fields);
  }
  const changed = (hs: Hunk[]) => hs.some((h) => h.status !== "same");
  return (
    <>
      {[...entries].map(([entry, fields]) =>
        [...fields.values()].some(changed) ? (
          <section key={entry} className="entry-hunks">
            <h3>{entry}</h3>
            {[...fields].filter(([, hs]) => changed(hs)).map(([field, hs]) => (
              <div key={field}>
                {field && <span className="muted">{field}</span>}
                <HunkList hunks={hs} prose={field === "content"} />
              </div>
            ))}
          </section>
        ) : null,
      )}
    </>
  );
}

/** Hunks renders a field's hunks: the first revision's value struck out, the second's marked. */
export function Hunks({ field }: { field: Field }) {
  if (field.kind === "entries") return <EntryHunks hunks={field.hunks ?? []} />;
  return <HunkList hunks={field.hunks ?? []} prose={field.kind === "prose"} />;
}

function HunkList({ hunks, prose }: { hunks: Hunk[]; prose: boolean }) {
  // Only prose flows inline; list and JSON values print as blocks of JSON.
  return (
    <p className={prose ? "hunks" : "hunks blocks"}>
      {hunks.map((h) =>
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

type AvatarPick = "keep" | "revision";

/** AvatarChoice asks which image a restore keeps when the revision's differs from the head's. */
function AvatarChoice({ head, rev, busy, onPick, onCancel }: {
  head?: string;
  rev?: string;
  busy: boolean;
  onPick: (avatar: AvatarPick) => void;
  onCancel: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  // As in Crop: showModal gives Escape and a focus trap. jsdom lacks both.
  useLayoutEffect(() => {
    const d = dialog.current;
    d?.showModal?.();
    return () => d?.close?.();
  }, []);
  const image = (sha: string | undefined, alt: string) =>
    sha ? <img className="history-thumb" src={thumb(sha, 160)} alt={alt} /> : <span className="muted">No image</span>;
  return (
    <dialog
      ref={dialog}
      className="crop"
      aria-label="Choose an image"
      onCancel={(e) => {
        e.preventDefault();
        if (!busy) onCancel();
      }}
    >
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
  const toast = useToast();
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
      toast({ text: `Restored revision ${r.id}` });
    },
    meta: { toast: false },
    onError: (e) => {
      void qc.invalidateQueries({ queryKey: [kind, id, "revisions"] });
      const stale = e instanceof ApiError && e.status === 409;
      toast({ text: stale ? `The ${kind} changed since you opened this page. Check the new head, then restore again.` : e.message, error: true });
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
