import { useQuery } from "@tanstack/react-query";
import { api, must } from "../api/client";
import { librariesQuery } from "../api/queries";
import type { components } from "../api/schema";
import { text } from "../card/draft";
import { count } from "../shell/count";
import { useModal } from "../shell/useModal";
import type { Spec } from "./Rules";

const verbs = { archive: ["archive", "unarchive"], collect: ["add to the collection", "take out of the collection"] } as const;

type Filter = components["schemas"]["Filter"];
type Props = { id?: number; spec: Spec; filter?: Filter; confirmLabel: string; onConfirm: () => void; onClose: () => void };

/** RulePreview shows what a rule would change once on, with the first few names per library, and runs onConfirm only when the owner confirms. */
export function RulePreview({ id, spec, filter, confirmLabel, onConfirm, onClose }: Props) {
  const modal = useModal(onClose);
  const libraries = useQuery(librariesQuery);
  const preview = useQuery({
    queryKey: ["rule-preview", id ?? 0, spec, filter],
    queryFn: async () => must(await api.POST("/rules/preview", { body: { ...(id !== undefined && { id }), rule: spec, ...(filter && { filter }), page: 1 } })),
  });
  const [apply, giveBack] = verbs[spec.action];
  const names = ({ total, hits }: { total: number; hits: { id: number; fields: Record<string, unknown> }[] | null }) =>
    (hits ?? []).map((h) => text(h.fields.name) || `#${h.id}`).join(", ") + (total > (hits ?? []).length ? `, and ${total - (hits ?? []).length} more` : "");
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
          {(p.apply.hits ?? []).length > 0 && <p className="muted">To {apply}: {names(p.apply)}</p>}
          {(p.give_back.hits ?? []).length > 0 && <p className="muted">To {giveBack}: {names(p.give_back)}</p>}
        </section>
      ))}
      <div className="row">
        <button type="button" className="button" onClick={onClose}>
          Cancel
        </button>
        <button
          type="button"
          className="button primary"
          disabled={!preview.data}
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
