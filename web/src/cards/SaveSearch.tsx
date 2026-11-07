import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { toast } from "sonner";
import { api, must } from "../api/client";
import { both, defaultSort, filterFor, toParams, type CardQuery } from "./query";

/** SaveSearch saves the page's filter and sort under a name, or folds the page's terms into the saved search it opened. */
export function SaveSearch({ q, saved }: { q: CardQuery; saved?: { id: number; name: string } }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [naming, setNaming] = useState(false);
  const own = filterFor(q.terms, q.sharesSource, q.all);
  const filter = both(q.savedFilter, own);
  const sort = q.sort === defaultSort(q.text) ? "" : q.sort;
  // The saved search alone, now that its filter holds the page's terms.
  const open = (id: number) => navigate(`/cards?${toParams({ ...q, terms: [], all: [], sharesSource: false, saved: id, page: 1 })}`);
  const done = () => qc.invalidateQueries({ queryKey: ["saved-searches"] });
  const create = useMutation({
    mutationFn: async (name: string) => must(await api.POST("/saved-searches", { body: { name, filter: filter!, sort } })),
    onSuccess: (s) => {
      setNaming(false);
      toast.success(`Saved ${s.name}`);
      void done();
      void open(s.id);
    },
    meta: { toast: false },
  });
  const update = useMutation({
    mutationFn: async (id: number) =>
      must(await api.PATCH("/saved-searches/{id}", { params: { path: { id } }, body: { filter: filter!, sort } })),
    onSuccess: (s) => {
      toast.success(`Updated ${s.name}`);
      void done();
      void open(s.id);
    },
  });
  if (!filter) return null;
  if (!naming)
    return (
      <>
        <button className="button" onClick={() => setNaming(true)}>
          Save search
        </button>
        {saved && own && (
          <button className="button" disabled={update.isPending} onClick={() => update.mutate(saved.id)}>
            Update {saved.name}
          </button>
        )}
      </>
    );
  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const name = String(new FormData(e.currentTarget).get("name") ?? "").trim();
    if (name) create.mutate(name);
  };
  return (
    <form className="save-search" onSubmit={submit}>
      <input className="input" name="name" aria-label="Saved search name" placeholder="Name" autoFocus
        onKeyDown={(e) => e.key === "Escape" && setNaming(false)} />
      <button className="button primary" disabled={create.isPending}>
        Save
      </button>
      <button type="button" className="button" onClick={() => setNaming(false)}>
        Cancel
      </button>
      {q.text && <span className="muted">The search text isn't saved, only the filters and sort.</span>}
      {create.error && <span className="error">{create.error.message}</span>}
    </form>
  );
}
