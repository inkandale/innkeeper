import { useMutation, useQueries } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError, must, route, type Kind } from "../api/client";
import type { components } from "../api/schema";
import { show } from "../card/draft";
import { jobToast } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { useModal } from "../shell/useModal";
import "./Sync.css";

type Preview = components["schemas"]["Preview"];

/** Target is an app instance a push goes to. */
export type Target = { id: number; name: string };

/** PushPreview shows what pushing one card or lorebook changes in each target app, and pushes once the owner confirms. */
export function PushPreview(props: { kind: Kind; id: number; targets: Target[]; onClose: () => void }) {
  const { kind, id, targets, onClose } = props;
  const toast = useToast();
  const modal = useModal(onClose);
  const [pushed, setPushed] = useState<number[]>([]);
  const [errors, setErrors] = useState<Record<number, string>>({});
  const previews = useQueries({
    queries: targets.map((t) => ({
      queryKey: [kind, id, "push-preview", t.id],
      // Read the app afresh each time: the confirm vouches for this exact preview.
      gcTime: 0,
      queryFn: async () =>
        must(await api.GET(route(kind, "/cards/{id}/push-preview"), { params: { path: { id }, query: { instance_id: t.id } } })),
    })),
  });
  const left = targets.flatMap((t, i) => (pushed.includes(t.id) ? [] : [{ ...t, preview: previews[i]! }]));
  // Each target pushes on its own; one that fails keeps the dialog open without holding back the rest.
  const push = useMutation({
    mutationFn: async () => {
      const failed: Record<number, string> = {};
      for (const t of left) {
        const p = t.preview.data!;
        try {
          const body = { instance_id: t.id, confirm: p.confirm, ...(p.new_copy ? { new_copy: true } : {}) };
          toast(jobToast(must(await api.POST(route(kind, "/cards/{id}/push"), { params: { path: { id } }, body }))));
          setPushed((all) => [...all, t.id]);
        } catch (e) {
          const changed = e instanceof ApiError && e.status === 409;
          if (changed) void t.preview.refetch();
          failed[t.id] = changed ? "What the push sends has changed. Check the new preview." : (e as Error).message;
        }
      }
      setErrors(failed);
      if (!Object.keys(failed).length) onClose();
    },
    meta: { toast: false },
  });
  const ready = left.every((t) => t.preview.data && !t.preview.isFetching);
  const title = targets.length === 1 ? `Push to ${targets[0]!.name}` : `Push to ${targets.length} apps`;
  return (
    <dialog {...modal} className="dialog" aria-label={title}>
      <h2>{title}</h2>
      {targets.map((t, i) => {
        const preview = previews[i]!;
        const body = (
          <>
            {pushed.includes(t.id) ? (
              <p className="muted">Pushed.</p>
            ) : preview.isError ? (
              <p className="error">{preview.error.message}</p>
            ) : !preview.data ? (
              <p>Reading the app…</p>
            ) : (
              <Changes p={preview.data} />
            )}
            {errors[t.id] && <p className="error">{errors[t.id]}</p>}
          </>
        );
        return targets.length === 1 ? (
          <div key={t.id}>{body}</div>
        ) : (
          <section key={t.id} aria-label={t.name}>
            <h3>{t.name}</h3>
            {body}
          </section>
        );
      })}
      <div className="row">
        <button className="button primary" disabled={!left.length || !ready || push.isPending} onClick={() => push.mutate()}>
          Confirm push
        </button>
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}

/** Changes is what one push does to one app's copy. */
function Changes({ p }: { p: Preview }) {
  const changes = p.changes ?? [];
  return (
    <>
      {p.stale && <p className="banner">The app couldn't be read, so this compares with the last push.</p>}
      {p.new_copy ? (
        <p>The app can't update its copy, so this makes another one.</p>
      ) : !p.replaces ? (
        <p>The app has no copy yet, so this creates one.</p>
      ) : (
        !changes.length && <p>The app's copy already matches.</p>
      )}
      {changes.map((c) => (
        <section key={c.field}>
          <h4>{c.field}</h4>
          <p className="hunks blocks">
            {c.before !== undefined && <del>{show(c.before)}</del>}
            {c.after !== undefined && <ins>{show(c.after)}</ins>}
          </p>
        </section>
      ))}
      {!!p.dropped_fields?.length && <p className="muted">The app doesn't store {p.dropped_fields.join(", ")}, so they aren't sent.</p>}
      {!!p.create_lorebooks?.length && (
        <p>
          Also creates {p.create_lorebooks.length} lorebook{p.create_lorebooks.length === 1 ? "" : "s"} in the app.
        </p>
      )}
    </>
  );
}
