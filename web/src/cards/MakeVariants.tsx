import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, mustOk, route, type Kind } from "../api/client";
import { useToast } from "../shell/Toasts";
import { CardPicker } from "./CardPicker";

type SourceKey = { kind: string; ref: string };

/** sharedSource says whether every list is loaded and some source is in all of them. */
export function sharedSource(lists: (SourceKey[] | null | undefined)[]): boolean {
  if (!lists.length || lists.some((l) => !l)) return false;
  const [first, ...rest] = lists as SourceKey[][];
  return first!.some((s) => rest.every((l) => l.some((o) => o.kind === s.kind && o.ref === s.ref)));
}

/** MakeVariants makes each picked card or lorebook a variant of one parent. When they all share a source, the parent
 * starts as the oldest of them: IDs are assigned in creation order, so the lowest. */
export function MakeVariants({ kind, ids, nameOf, onDone }: { kind: Kind; ids: number[]; nameOf: Map<number, string>; onDone: () => void }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [picked, setPicked] = useState<{ id: number; name: string }>();
  const sources = useQueries({
    queries: ids.map((id) => ({
      queryKey: [kind, id, "sources"],
      queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/sources"), { params: { path: { id } } })),
    })),
  });
  const name = (id: number) => nameOf.get(id) || `#${id}`;
  const oldest = sharedSource(sources.map((s) => s.data)) ? Math.min(...ids) : undefined;
  const parent = picked ?? (oldest === undefined ? undefined : { id: oldest, name: name(oldest) });
  const make = useMutation({
    mutationFn: async (to: { id: number; name: string }) => {
      const failed: string[] = [];
      for (const id of ids.filter((i) => i !== to.id)) {
        try {
          mustOk(await api.PUT(route(kind, "/cards/{id}/variant_of"), { params: { path: { id } }, body: { id: to.id } }));
        } catch (e) {
          failed.push(`${name(id)}: ${(e as Error).message}`);
        }
      }
      void qc.invalidateQueries({ queryKey: ["search"] });
      if (failed.length) throw new Error(failed.join("; "));
    },
    onSuccess: (_, to) => {
      toast({ text: `Made them variants of ${to.name}.` });
      onDone();
    },
  });
  return (
    <>
      {parent && <span>Parent: {parent.name}</span>}
      <CardPicker kind={kind} label="Parent" onPick={(id, n) => setPicked({ id, name: n })} />
      <button className="button primary" disabled={!parent || make.isPending} onClick={() => parent && make.mutate(parent)}>
        Make variants
      </button>
    </>
  );
}
