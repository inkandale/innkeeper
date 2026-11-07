import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import { api, must } from "../api/client";
import { librariesQuery, rulesQuery } from "../api/queries";
import type { components } from "../api/schema";
import { count } from "../shell/count";
import { RulePreview } from "./RulePreview";

export type Rule = components["schemas"]["RuleItem"];
export type Spec = components["schemas"]["RuleSpec"];

/** specOf is the part of a rule that PUT /rules/{id} takes. */
export const specOf = ({ name, saved_search_id, action, collection_id, all_libraries, libraries, enabled }: Spec): Spec => ({
  name,
  saved_search_id,
  action,
  collection_id,
  all_libraries,
  libraries,
  enabled,
});

/** actionLabel says what a rule does to the cards it matches. */
export const actionLabel = (r: Spec) => (r.action === "collect" ? "Add to a collection" : "Archive");

/** Rules lists the rules, and turns one on only after previewing what it would change. */
export function Rules() {
  const qc = useQueryClient();
  const list = useQuery(rulesQuery());
  const libraries = useQuery(librariesQuery);
  const [previewing, setPreviewing] = useState<Rule>();
  const libraryName = (id: number) => libraries.data?.find((l) => l.id === id)?.name ?? `Library ${id}`;
  const save = useMutation({
    mutationFn: async (r: Rule) => must(await api.PUT("/rules/{id}", { params: { path: { id: r.id } }, body: specOf(r) })),
    onSuccess: (r) => {
      toast.success(r.enabled ? `Turned on ${r.name}` : `Turned off ${r.name}`);
      return qc.invalidateQueries({ queryKey: ["rules"] });
    },
  });
  return (
    <section className="page">
      <h1>Rules</h1>
      <p className="muted">
        A rule keeps a saved search's cards archived or in a collection, and gives back the ones that stop matching. It never
        undoes what you did by hand. Add one from Saved searches.
      </p>
      {list.error && <p className="error">{list.error.message}</p>}
      {list.data?.length === 0 && <p>No rules yet.</p>}
      {(list.data?.length ?? 0) > 0 && (
        <table>
          <thead>
            <tr>
              <th>Rule</th>
              <th>Saved search</th>
              <th>Action</th>
              <th>Libraries</th>
              <th>Cards</th>
              <th>On</th>
            </tr>
          </thead>
          <tbody>
            {list.data!.map((r) => (
              <tr key={r.id}>
                <td>
                  <Link to={`/rules/${r.id}`}>{r.name}</Link>
                </td>
                <td>{r.saved_search}</td>
                <td>{actionLabel(r)}</td>
                <td>{r.all_libraries ? "All libraries" : (r.libraries ?? []).map(libraryName).join(", ")}</td>
                <td>{(r.affected ?? []).map((a) => `${libraryName(a.library)}: ${count(a.cards, "card")}`).join("; ") || "None"}</td>
                <td>
                  <input
                    type="checkbox"
                    role="switch"
                    aria-label={`${r.enabled ? "Turn off" : "Enable"} ${r.name}`}
                    checked={r.enabled}
                    disabled={save.isPending}
                    onChange={() => (r.enabled ? save.mutate({ ...r, enabled: false }) : setPreviewing(r))}
                  />
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {previewing && (
        <RulePreview
          id={previewing.id}
          spec={{ ...specOf(previewing), enabled: true }}
          confirmLabel="Enable rule"
          onConfirm={() => save.mutate({ ...previewing, enabled: true })}
          onClose={() => setPreviewing(undefined)}
        />
      )}
    </section>
  );
}
