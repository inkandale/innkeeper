import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, ApiError, must, route, type Kind } from "../api/client";
import { useLeaveGuard } from "../shell/useLeaveGuard";
import { changed, rebase, type Data, type Draft } from "./draft";
import { toastError } from "../shell/Toasts";
import { toast } from "sonner";

/** useEditor loads a card or lorebook and holds its unsaved edits, its save, and the stale-save flow. */
export function useEditor(kind: Kind, id: number) {
  const qc = useQueryClient();
  const entity = useQuery({
    queryKey: [kind, id],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}"), { params: { path: { id } } })),
  });
  const [draft, setDraft] = useState<Draft | null>(null);
  const [stale, setStale] = useState(false);
  const [comparing, setComparing] = useState(false);
  const dirty = changed(draft);
  useLeaveGuard(dirty.size > 0);

  const save = useMutation({
    mutationFn: async (d: Draft) =>
      must(
        await api.PUT(route(kind, "/cards/{id}"), {
          params: { path: { id } },
          body: { base_revision_id: d.baseId, data: d.data, source: "ui" },
        }),
      ),
    onSuccess: (e, sent) => {
      qc.setQueryData([kind, id], e);
      void qc.invalidateQueries({ queryKey: [kind, id, "revisions"] });
      // Keep edits typed while the save was in flight, on top of the new head.
      setDraft((cur) => {
        if (!cur || cur === sent) return null;
        const next = rebase(cur, e.revision.id, (e.revision.data ?? {}) as Data);
        return changed(next).size ? next : null;
      });
      setStale(false);
      setComparing(false);
      toast.success("Saved");
    },
    meta: { toast: false },
    onError: (e) => (e instanceof ApiError && e.status === 409 ? setStale(true) : toastError(e.message)),
  });

  const head = entity.data;
  const headData = (head?.revision.data ?? {}) as Data;
  const edit = (key: string, value: unknown) =>
    setDraft((cur) => {
      if (!head) return cur;
      const d = cur ?? { baseId: head.revision.id, base: headData, data: headData };
      return { ...d, data: { ...d.data, [key]: value } };
    });
  // saveWith saves the draft with changes on top, as one step, such as a reviewed Forge proposal.
  const saveWith = (changes: Data) => {
    if (!head) return;
    const d = draft ?? { baseId: head.revision.id, base: headData, data: headData };
    const next = { ...d, data: { ...d.data, ...changes } };
    setDraft(next);
    save.mutate(next);
  };
  const reload = () => {
    setDraft(null);
    setStale(false);
    setComparing(false);
    void entity.refetch();
  };
  const compare = async () => {
    await entity.refetch();
    setComparing(true);
  };
  // Keep moves the draft onto the head that beat it, so Save can go ahead.
  const keep = () => {
    if (draft && head) setDraft(rebase(draft, head.revision.id, headData));
    setStale(false);
    setComparing(false);
  };
  const use = (key: string) => setDraft((cur) => cur && { ...cur, data: { ...cur.data, [key]: headData[key] } });

  return { entity, headData, data: draft?.data ?? headData, draft, setDraft, dirty, edit, save, saveWith, stale, comparing, reload, compare, keep, use };
}

export type Editor = ReturnType<typeof useEditor>;
