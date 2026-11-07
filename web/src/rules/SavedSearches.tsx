import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api, must, mustOk } from "../api/client";
import { savedSearchesQuery } from "../api/queries";
import { useConfirm } from "../shell/ConfirmDialog";
import { count } from "../shell/count";
import { useLibrary, withLibrary } from "../shell/library";

/** openPath is the Cards page of library showing saved search s, in its sort. */
export function openPath(s: { id: number; sort: string }, library: number): string {
  const p = new URLSearchParams({ saved: String(s.id) });
  if (s.sort) p.set("sort", s.sort);
  return withLibrary(`/cards?${p}`, library);
}

/** SavedSearches lists the saved searches, each with its match count in the current library and the rules on it. */
export function SavedSearches() {
  const [ask, dialog] = useConfirm();
  const qc = useQueryClient();
  const library = useLibrary();
  const list = useQuery(savedSearchesQuery(library));
  const [renaming, setRenaming] = useState<number | null>(null);
  const done = () => qc.invalidateQueries({ queryKey: ["saved-searches"] });
  const rename = useMutation({
    mutationFn: async ({ id, name }: { id: number; name: string }) =>
      must(await api.PATCH("/saved-searches/{id}", { params: { path: { id } }, body: { name } })),
    onSuccess: () => {
      setRenaming(null);
      return done();
    },
  });
  const remove = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/saved-searches/{id}", { params: { path: { id } } })),
    onSuccess: done,
  });
  const submitName = (id: number, old: string) => (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const name = String(new FormData(e.currentTarget).get("name") ?? "").trim();
    if (name && name !== old) rename.mutate({ id, name });
    else setRenaming(null);
  };

  return (
    <section className="page">
      <h1>Saved searches</h1>
      <p className="muted">
        A saved search is a filter you named on the Cards page with Save search. Open one in any library, or add a rule that keeps
        archiving or collecting what it finds.
      </p>
      {list.error && <p className="error">{list.error.message}</p>}
      {list.data?.length === 0 && <p>No saved searches yet. Filter the cards, then choose Save search.</p>}
      <table>
        <tbody>
          {(list.data ?? []).map((s) => (
            <tr key={s.id}>
              <td>
                {renaming === s.id ? (
                  <form onSubmit={submitName(s.id, s.name)}>
                    <input className="input" name="name" aria-label="Name" defaultValue={s.name} autoFocus
                      onKeyDown={(e) => e.key === "Escape" && setRenaming(null)} onBlur={() => setRenaming(null)} />
                  </form>
                ) : (
                  <Link to={openPath(s, library)}>{s.name}</Link>
                )}
              </td>
              <td className="muted">{s.cards == null ? "Its filter no longer reads" : count(s.cards, "card")}</td>
              <td>
                {(s.rules ?? []).map((r) => (
                  <Link key={r.id} className="chip" to={`/rules/${r.id}`}>
                    {r.name}
                  </Link>
                ))}
              </td>
              <td>
                <Link className="button" to={`/rules/new?search=${s.id}`} aria-label={`Add rule to ${s.name}`}>
                  Add rule
                </Link>{" "}
                <button className="button" aria-label={`Rename ${s.name}`} onClick={() => setRenaming(s.id)}>
                  Rename
                </button>{" "}
                <button
                  className="button"
                  aria-label={`Delete ${s.name}`}
                  disabled={(s.rules?.length ?? 0) > 0}
                  title={(s.rules?.length ?? 0) > 0 ? "Delete its rules first" : undefined}
                  onClick={() =>
                    ask({ title: `Delete ${s.name}?`, confirmLabel: "Delete saved search", danger: true, onConfirm: () => remove.mutate(s.id) })
                  }
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {dialog}
    </section>
  );
}
