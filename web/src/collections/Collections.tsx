import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { api, must, mustOk, thumb } from "../api/client";
import "./Collections.css";

function nameFrom(e: FormEvent<HTMLFormElement>): string {
  e.preventDefault();
  return String(new FormData(e.currentTarget).get("name") ?? "").trim();
}

export function Collections() {
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const [renaming, setRenaming] = useState<number | null>(null);
  const [failed, setFailed] = useState<Error | null>(null);
  const onSuccess = () => qc.invalidateQueries({ queryKey: ["collections"] });
  // Clear on start so an old failure never outlives a newer action.
  const track = { onMutate: () => setFailed(null), onError: setFailed };

  const create = useMutation({
    mutationFn: async (name: string) => must(await api.POST("/collections", { body: { name } })),
    onSuccess,
    ...track,
  });
  const rename = useMutation({
    mutationFn: async ({ id, name }: { id: number; name: string }) =>
      must(await api.PATCH("/collections/{id}", { params: { path: { id } }, body: { name } })),
    onSuccess: () => {
      setRenaming(null);
      return onSuccess();
    },
    ...track,
  });
  const remove = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/collections/{id}", { params: { path: { id } } })),
    onSuccess,
    ...track,
  });

  const shown = failed ?? list.error;

  return (
    <section className="page">
      <h1>Collections</h1>
      <form
        className="collection-new"
        onSubmit={(e) => {
          const name = nameFrom(e);
          if (name) create.mutate(name, { onSuccess: () => (e.target as HTMLFormElement).reset() });
        }}
      >
        <input className="input" name="name" aria-label="New collection" placeholder="New collection" />
        <button className="button primary">Create</button>
      </form>
      {shown && <p className="error">{shown.message}</p>}
      <div className="collections">
        {(list.data ?? []).map((c) => (
          <article key={c.id} className="collection">
            <Link to={`/cards?f=${encodeURIComponent(`collection:${c.id}`)}`}>
              <span className="mosaic">
                {[0, 1, 2, 3].map((i) =>
                  c.avatars?.[i] ? <img key={i} src={thumb(c.avatars[i], 160)} alt="" loading="lazy" /> : <span key={i} />,
                )}
              </span>
              <span className="collection-name">{c.name}</span>
              <span className="collection-count">{c.cards} {c.cards === 1 ? "card" : "cards"}</span>
            </Link>
            {renaming === c.id ? (
              <form
                onSubmit={(e) => {
                  const name = nameFrom(e);
                  if (name && name !== c.name) rename.mutate({ id: c.id, name });
                  else setRenaming(null);
                }}
              >
                <input
                  className="input"
                  name="name"
                  aria-label="Name"
                  defaultValue={c.name}
                  autoFocus
                  onKeyDown={(e) => e.key === "Escape" && setRenaming(null)}
                />
              </form>
            ) : (
              <div className="collection-actions">
                <button className="button" aria-label={`Rename ${c.name}`} onClick={() => setRenaming(c.id)}>
                  Rename
                </button>
                <button
                  className="button"
                  aria-label={`Delete ${c.name}`}
                  onClick={() => confirm(`Delete ${c.name}? Its cards stay in the library.`) && remove.mutate(c.id)}
                >
                  Delete
                </button>
              </div>
            )}
          </article>
        ))}
      </div>
    </section>
  );
}
