import { useMutation, useQueries, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, must, mustOk } from "../api/client";
import { CardPicker } from "../cards/CardPicker";
import type { Entity } from "./Card";
import { text, type Data } from "./draft";

/** CardLorebooks lists a card's linked lorebooks, with × to unlink, and links an existing one or a new empty one. */
export function CardLorebooks({ card }: { card: Entity }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [linking, setLinking] = useState(false);
  const ids = card.lorebooks ?? [];
  const books = useQueries({
    queries: ids.map((id) => ({
      queryKey: ["lorebook", id],
      queryFn: async () => must(await api.GET("/lorebooks/{id}", { params: { path: { id } } })),
    })),
  });
  // A link shows on the card, on the lorebook, and in both kinds' search rows.
  const refresh = () =>
    Promise.all(["card", "lorebook", "search"].map((key) => qc.invalidateQueries({ queryKey: [key] })));
  const linkPath = (lorebook_id: number) => ({ params: { path: { id: card.id, lorebook_id } } });
  const link = useMutation({
    mutationFn: async (lorebook: number) => mustOk(await api.PUT("/cards/{id}/lorebooks/{lorebook_id}", linkPath(lorebook))),
    onSuccess: () => {
      setLinking(false);
      return refresh();
    },
  });
  const unlink = useMutation({
    mutationFn: async (lorebook: number) => mustOk(await api.DELETE("/cards/{id}/lorebooks/{lorebook_id}", linkPath(lorebook))),
    onSuccess: refresh,
  });
  const create = useMutation({
    mutationFn: async () => {
      const name = text((card.revision.data as Data).name);
      const body = { data: { name, entries: [] }, library: card.library_id, source: "ui" as const, message: `New lorebook for ${name}` };
      const book = must(await api.POST("/lorebooks", { body }));
      mustOk(await api.PUT("/cards/{id}/lorebooks/{lorebook_id}", linkPath(book.id)));
      return book;
    },
    onSuccess: (book) => {
      void refresh();
      navigate(`/lorebooks/${book.id}`);
    },
  });
  return (
    <section>
      <h2>Lorebooks</h2>
      <ul className="plain">
        {ids.map((id, i) => {
          const name = text((books[i]?.data?.revision.data as Data | undefined)?.name) || `#${id}`;
          return (
            <li key={id}>
              <Link to={`/lorebooks/${id}`}>{name}</Link>
              <button className="chip-x" aria-label={`Unlink ${name}`} disabled={unlink.isPending} onClick={() => unlink.mutate(id)}>
                ×
              </button>
            </li>
          );
        })}
      </ul>
      <div className="row">
        <button className="button" disabled={create.isPending} onClick={() => create.mutate()}>
          New lorebook
        </button>
        <button className="button" aria-expanded={linking} onClick={() => setLinking(!linking)}>
          Link lorebook…
        </button>
      </div>
      {linking && (
        <CardPicker kind="lorebook" library={card.library_id} label="Find a lorebook" onPick={(id) => !link.isPending && link.mutate(id)} />
      )}
    </section>
  );
}
