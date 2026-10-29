import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, ApiError, must, route, type Kind } from "../api/client";
import { bindingsQuery } from "../api/queries";
import { dismissBinding, editBinding, reviewPath, siteOf, type BindingEdit } from "../sync/sync";
import { toastError } from "../shell/Toasts";

/** useBindingEdits changes a card's or lorebook's bindings, or checks its sources now, then refreshes them and the queue. */
export function useBindingEdits(kind: Kind, id: number) {
  const qc = useQueryClient();
  const key = bindingsQuery(kind, id).queryKey;
  const refresh = () => Promise.all([qc.invalidateQueries({ queryKey: key }), qc.invalidateQueries({ queryKey: ["sync", "changes"] })]);
  const edit = useMutation({
    mutationFn: ({ binding, body }: { binding: number; body: BindingEdit }) => editBinding(binding, body),
    onSuccess: refresh,
  });
  const dismiss = useMutation({
    mutationFn: ({ binding, appHash }: { binding: number; appHash: string; remoteId: string }) => dismissBinding(binding, appHash),
    onSuccess: refresh,
    meta: { toast: false },
    onError: (e, { remoteId }) => {
      const stale = e instanceof ApiError && e.status === 409;
      if (stale) void refresh();
      toastError(stale ? `This changed on ${siteOf(remoteId)} again. Check the new change.` : e.message);
    },
  });
  const check = useMutation({
    mutationFn: async () => must(await api.POST(route(kind, "/cards/{id}/source-check"), { params: { path: { id } } })),
    onSuccess: (list) => {
      qc.setQueryData(key, list);
      void qc.invalidateQueries({ queryKey: ["sync", "changes"] });
    },
  });
  return { edit, dismiss, check, refresh };
}

/** UpdateBanner offers each followed source's update: review it, or ignore this change. */
export function UpdateBanner({ kind, id }: { kind: Kind; id: number }) {
  const bindings = useQuery(bindingsQuery(kind, id));
  const { dismiss } = useBindingEdits(kind, id);
  const updates = (bindings.data ?? []).filter((b) => !b.push && !b.paused && b.remote_state === "changed");
  return (
    <>
      {updates.map((b) => (
        <p key={b.id} className="banner">
          Update on {siteOf(b.remote_id)}. <Link to={reviewPath(b.instance_id, kind, b.remote_id, { card: id })}>Review</Link>{" "}
          <button
            className="button"
            disabled={dismiss.isPending}
            onClick={() => dismiss.mutate({ binding: b.id, appHash: b.app_hash ?? "", remoteId: b.remote_id })}
          >
            Ignore this change
          </button>
        </p>
      ))}
    </>
  );
}
