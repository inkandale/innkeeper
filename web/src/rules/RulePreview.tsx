import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import { librariesQuery } from "../api/queries";
import type { components } from "../api/schema";
import { text } from "../card/draft";
import { count } from "../shell/count";
import { useModal } from "../shell/useModal";
import type { Spec } from "./Rules";

// The API's preview page size.
const pageSize = 24;
const verbs = { archive: ["archive", "unarchive"], collect: ["add to the collection", "take out of the collection"] } as const;

type Filter = components["schemas"]["Filter"];
type Props = { id?: number; spec: Spec; filter?: Filter; confirmLabel: string; onConfirm: () => void; onClose: () => void };

/** RulePreview shows what a rule would change once on, a page at a time, and runs onConfirm only when the owner confirms. */
export function RulePreview({ id, spec, filter, confirmLabel, onConfirm, onClose }: Props) {
  const modal = useModal(onClose);
  const [page, setPage] = useState(1);
  const libraries = useQuery(librariesQuery);
  const preview = useQuery({
    queryKey: ["rule-preview", id ?? 0, spec, filter, page],
    queryFn: async () => must(await api.POST("/rules/preview", { body: { ...(id !== undefined && { id }), rule: spec, ...(filter && { filter }), page } })),
    placeholderData: keepPreviousData,
  });
  const [apply, giveBack] = verbs[spec.action];
  const most = Math.max(0, ...(preview.data ?? []).flatMap((p) => [p.apply.total, p.give_back.total]));
  const last = Math.max(1, Math.ceil(most / pageSize));
  const names = (hits: { id: number; fields: Record<string, unknown> }[] | null) => (hits ?? []).map((h) => text(h.fields.name) || `#${h.id}`).join(", ");
  return (
    <dialog {...modal} className="dialog confirm" aria-label={`Preview ${spec.name}`}>
      <h2>Before {spec.name} runs</h2>
      {preview.error && <p className="error">{preview.error.message}</p>}
      {preview.isPending && <p role="status">Counting cards…</p>}
      {(preview.data ?? []).map((p) => (
        <section key={p.library}>
          <h3>{libraries.data?.find((l) => l.id === p.library)?.name ?? `Library ${p.library}`}</h3>
          <p>
            It would {apply} {count(p.apply.total, "card")} and {giveBack} {count(p.give_back.total, "card")}.
          </p>
          {(p.apply.hits ?? []).length > 0 && <p className="muted">To {apply}: {names(p.apply.hits)}</p>}
          {(p.give_back.hits ?? []).length > 0 && <p className="muted">To {giveBack}: {names(p.give_back.hits)}</p>}
        </section>
      ))}
      {last > 1 && (
        <div className="row">
          <button type="button" className="button" disabled={page === 1} onClick={() => setPage(page - 1)}>
            Previous
          </button>
          <span>
            Page {page} of {last}
          </span>
          <button type="button" className="button" disabled={page === last} onClick={() => setPage(page + 1)}>
            Next
          </button>
        </div>
      )}
      <div className="row">
        <button type="button" className="button" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="button primary"
          disabled={!preview.data || preview.isPlaceholderData}
          onClick={() => {
            onConfirm();
            onClose();
          }}
        >
          {confirmLabel}
        </button>
      </div>
    </dialog>
  );
}
