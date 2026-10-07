import { useMutation, useQuery } from "@tanstack/react-query";
import { useLayoutEffect, useRef } from "react";
import { api, ApiError, must, route, type Kind } from "../api/client";
import { show } from "../card/draft";
import { useToast } from "../shell/Toasts";
import "./Sync.css";

/** PushPreview shows what pushing one card or lorebook changes in the app, and pushes once the owner confirms. */
export function PushPreview(props: { kind: Kind; id: number; instance: number; name: string; onClose: () => void }) {
  const { kind, id, instance, name, onClose } = props;
  const toast = useToast();
  const dialog = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const d = dialog.current;
    // jsdom has no showModal.
    if (d?.showModal) d.showModal();
    else d?.setAttribute("open", "");
    return () => d?.close?.();
  }, []);
  const preview = useQuery({
    queryKey: [kind, id, "push-preview", instance],
    // Read the app afresh each time: the confirm vouches for this exact preview.
    gcTime: 0,
    queryFn: async () =>
      must(await api.GET(route(kind, "/cards/{id}/push-preview"), { params: { path: { id }, query: { instance_id: instance } } })),
  });
  const p = preview.data;
  const push = useMutation({
    mutationFn: async () =>
      must(
        await api.POST(route(kind, "/cards/{id}/push"), {
          params: { path: { id } },
          body: { instance_id: instance, confirm: p!.confirm, ...(p!.new_copy ? { new_copy: true } : {}) },
        }),
      ),
    onSuccess: (j) => {
      toast({ text: `Job ${j.id} started`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } });
      onClose();
    },
    onError: (e) => {
      const changed = e instanceof ApiError && e.status === 409;
      if (changed) void preview.refetch();
      toast({ text: changed ? "What the push sends has changed. Check the new preview." : e.message, error: true });
    },
  });
  const changes = p?.changes ?? [];
  return (
    <dialog
      ref={dialog}
      className="dialog"
      aria-label={`Push to ${name}`}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <h2>Push to {name}</h2>
      {preview.isError ? (
        <p className="error">{preview.error.message}</p>
      ) : !p ? (
        <p>Reading the app…</p>
      ) : (
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
              <h3>{c.field}</h3>
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
      )}
      <div className="row">
        <button className="button primary" disabled={!p || push.isPending || preview.isFetching} onClick={() => push.mutate()}>
          Confirm push
        </button>
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
