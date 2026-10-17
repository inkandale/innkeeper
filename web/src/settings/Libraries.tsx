import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, must } from "../api/client";
import { librariesQuery } from "../api/queries";
import { JobLine } from "../cards/ImportForm";
import { rememberedLibrary, rememberLibrary } from "../shell/library";

function nameFrom(e: FormEvent<HTMLFormElement>): string {
  e.preventDefault();
  return String(new FormData(e.currentTarget).get("name") ?? "").trim();
}

const count = (n: number, noun: string) => `${n} ${noun}${n === 1 ? "" : "s"}`;

/** Libraries lists the libraries with their counts, and creates, renames, and deletes them. Library 1 can't be deleted. */
export function Libraries() {
  const qc = useQueryClient();
  const list = useQuery(librariesQuery);
  const [renaming, setRenaming] = useState<number | null>(null);
  // Delete jobs started here, by library.
  const [deleting, setDeleting] = useState<Record<number, number>>({});
  const [mistyped, setMistyped] = useState<string>();
  const onSuccess = () => qc.invalidateQueries({ queryKey: ["libraries"] });

  const create = useMutation({
    mutationFn: async (name: string) => must(await api.POST("/libraries", { body: { name } })),
    meta: { toast: false },
    onSuccess,
  });
  const rename = useMutation({
    mutationFn: async ({ id, name }: { id: number; name: string }) =>
      must(await api.PATCH("/libraries/{id}", { params: { path: { id } }, body: { name } })),
    meta: { toast: false },
    onSuccess: () => {
      setRenaming(null);
      return onSuccess();
    },
  });
  const remove = useMutation({
    mutationFn: async ({ id, confirm }: { id: number; confirm: string }) =>
      must(await api.DELETE("/libraries/{id}", { params: { path: { id } }, body: { confirm } })),
    meta: { toast: false },
    onSuccess: (job, { id }) => {
      setDeleting((d) => ({ ...d, [id]: job.id }));
      // The switcher would otherwise open a library that's gone.
      if (rememberedLibrary() === id) rememberLibrary(1);
    },
  });

  const failed = list.error ?? create.error ?? rename.error ?? remove.error;

  return (
    <section>
      <form
        className="toolbar"
        onSubmit={(e) => {
          const name = nameFrom(e);
          if (name) create.mutate(name, { onSuccess: () => (e.target as HTMLFormElement).reset() });
        }}
      >
        <input className="input" name="name" aria-label="New library" placeholder="New library" />
        <button className="button primary" disabled={create.isPending}>
          Create
        </button>
      </form>
      {failed && <p className="error">{failed.message}</p>}
      {mistyped && <p className="error">{mistyped}</p>}
      <ul className="plain">
        {(list.data ?? []).map((l) => (
          <li key={l.id}>
            {renaming === l.id ? (
              <form
                onSubmit={(e) => {
                  const name = nameFrom(e);
                  if (name && name !== l.name) rename.mutate({ id: l.id, name });
                  else setRenaming(null);
                }}
              >
                <input
                  className="input"
                  name="name"
                  aria-label="Name"
                  defaultValue={l.name}
                  autoFocus
                  onKeyDown={(e) => e.key === "Escape" && setRenaming(null)}
                />
              </form>
            ) : (
              <div className="row">
                <strong>{l.name}</strong>
                <span className="muted">
                  {count(l.cards, "card")}, {count(l.lorebooks, "lorebook")}
                </span>
                <button className="button" aria-label={`Rename ${l.name}`} onClick={() => setRenaming(l.id)}>
                  Rename
                </button>
                {l.id !== 1 && !deleting[l.id] && (
                  <button
                    className="button danger"
                    aria-label={`Delete ${l.name}`}
                    disabled={remove.isPending}
                    onClick={() => {
                      const typed = prompt(
                        `Delete ${l.name} with its ${count(l.cards, "card")} and ${count(l.lorebooks, "lorebook")}? This can't be undone. Type ${l.name} to confirm.`,
                      );
                      setMistyped(undefined);
                      if (typed === l.name) remove.mutate({ id: l.id, confirm: typed });
                      else if (typed !== null) setMistyped(`That isn't ${l.name}'s name, so nothing was deleted.`);
                    }}
                  >
                    Delete
                  </button>
                )}
              </div>
            )}
            {deleting[l.id] && <JobLine label={`Deleting ${l.name}`} id={deleting[l.id]!} to={`/settings/jobs/${deleting[l.id]}`} />}
          </li>
        ))}
      </ul>
    </section>
  );
}
