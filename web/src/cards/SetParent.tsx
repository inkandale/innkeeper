import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, mustOk, route, type Kind } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { useModal } from "../shell/useModal";
import "../sync/Sync.css";

/** Picked is a selected card or lorebook as the list shows it. */
export type Picked = { id: number; name: string; family?: components["schemas"]["Family"] };

/** recommendParent picks the root of the biggest family among the picked, so the others join it; failing that, the
 * oldest, since IDs are assigned in creation order. */
export function recommendParent(picked: Picked[]): number {
  const size = (p: Picked) => (p.family?.root_id === p.id ? p.family.size : 0);
  return picked.reduce((best, p) => (size(p) > size(best) || (size(p) === size(best) && p.id < best.id) ? p : best)).id;
}

/** SetParent asks which picked card or lorebook the others become variants of, and links them. */
export function SetParent({ kind, picked, onClose, onDone }: { kind: Kind; picked: Picked[]; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const modal = useModal(onClose);
  const recommended = recommendParent(picked);
  const [parent, setParent] = useState(recommended);
  const set = useMutation({
    mutationFn: async (to: Picked) => {
      const failed: string[] = [];
      for (const p of picked.filter((p) => p.id !== to.id)) {
        try {
          mustOk(await api.PUT(route(kind, "/cards/{id}/variant_of"), { params: { path: { id: p.id } }, body: { id: to.id } }));
        } catch (e) {
          failed.push(`${p.name}: ${(e as Error).message}`);
        }
      }
      void qc.invalidateQueries({ queryKey: ["search"] });
      if (failed.length) throw new Error(failed.join("; "));
    },
    onSuccess: (_, to) => {
      toast({ text: `Made ${picked.length - 1} ${kind}${picked.length === 2 ? "" : "s"} variants of ${to.name}.` });
      onDone();
    },
  });
  return (
    <dialog {...modal} className="dialog" aria-label="Set parent">
      <h2>Set parent</h2>
      <p>The others become its variants.</p>
      {picked.map((p) => (
        <label key={p.id} className="check">
          <input type="radio" name="parent" checked={parent === p.id} onChange={() => setParent(p.id)} />
          <span>{p.id === recommended ? `${p.name} (recommended)` : p.name}</span>
        </label>
      ))}
      {set.error && <p className="error">{set.error.message}</p>}
      <div className="row">
        <button className="button primary" disabled={set.isPending} onClick={() => set.mutate(picked.find((p) => p.id === parent)!)}>
          Set parent
        </button>
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
