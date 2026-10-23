import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, route, type Kind } from "../api/client";
import { instancesQuery, librariesQuery } from "../api/queries";
import type { components } from "../api/schema";
import { count } from "../shell/count";
import { useToast } from "../shell/Toasts";
import { useModal } from "../shell/useModal";

type MoveResult = components["schemas"]["MoveResult"];

/** MoveLibrary asks which library to move cards or lorebooks to, previews what moving them changes, and moves them. */
export function MoveLibrary({ kind, ids, library, onClose, onDone }: { kind: Kind; ids: number[]; library: number; onClose: () => void; onDone: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const modal = useModal(onClose);
  const [to, setTo] = useState<number>();
  const libraries = useQuery(librariesQuery);
  const instances = useQuery(instancesQuery);
  const others = (libraries.data ?? []).filter((l) => l.id !== library);
  const body = { ids, library: to ?? 0 };
  // The preview runs the move and rolls it back, so it counts the variants that come along.
  const preview = useQuery({
    queryKey: ["move-preview", kind, ids, to],
    enabled: to !== undefined,
    // Each run moves and rolls back, so don't keep or refresh one nobody is looking at.
    gcTime: 0,
    refetchOnWindowFocus: false,
    queryFn: async () => must(await api.POST(route(kind, "/cards/move/preview"), { body })),
  });
  const move = useMutation({
    mutationFn: async () => must(await api.POST(route(kind, "/cards/move"), { body })),
    onSuccess: (r) => {
      void qc.invalidateQueries({ queryKey: [kind] });
      void qc.invalidateQueries({ queryKey: librariesQuery.queryKey });
      // Moved cards leave their collections.
      void qc.invalidateQueries({ queryKey: ["collections"] });
      toast({ text: `Moved ${count(r.moved?.length ?? 0, kind)} to ${others.find((l) => l.id === to)?.name}.` });
      onDone();
    },
    meta: { toast: false },
  });
  const label = (id: number) => instances.data?.find((i) => i.id === id)?.label ?? `#${id}`;
  return (
    <dialog {...modal} className="dialog" aria-label="Move to library">
      <h2>Move to library</h2>
      <select className="input" aria-label="Library" value={to ?? ""} onChange={(e) => setTo(Number(e.target.value))}>
        <option value="" disabled>
          Pick a library
        </option>
        {others.map((l) => (
          <option key={l.id} value={l.id}>
            {l.name}
          </option>
        ))}
      </select>
      {preview.data && <Summary kind={kind} selected={ids.length} result={preview.data} label={label} />}
      {preview.error && <p className="error">{preview.error.message}</p>}
      {move.error && <p className="error">{move.error.message}</p>}
      <div className="row">
        <button className="button primary" disabled={!preview.data || preview.isFetching || move.isPending} onClick={() => move.mutate()}>
          Move
        </button>
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}

function Summary({ kind, selected, result, label }: { kind: Kind; selected: number; result: MoveResult; label: (id: number) => string }) {
  const moved = result.moved?.length ?? 0;
  const variants = moved - selected;
  const cleared = result.variant_links_cleared?.length ?? 0;
  const dropped = result.collection_memberships_dropped;
  const bound = result.bound_instances ?? [];
  const detail = [variants > 0 && `${selected} selected + ${count(variants, "variant")}`, result.archived > 0 && `${result.archived} archived`].filter(Boolean);
  return (
    <ul className="plain">
      <li>
        Moves {count(moved, kind)}
        {detail.length > 0 && ` (${detail.join(", ")})`}
      </li>
      {cleared > 0 && <li>{cleared === 1 ? "1 loses its parent link" : `${cleared} lose their parent link`}</li>}
      {dropped > 0 && <li>Leaves {count(dropped, "collection place")}</li>}
      {bound.length > 0 && <li>Keeps syncing to: {bound.map(label).join(", ")}</li>}
    </ul>
  );
}
