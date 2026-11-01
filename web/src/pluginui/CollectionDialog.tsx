import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, mustOk } from "../api/client";
import { collectionsQuery } from "../api/queries";
import { useLibrary } from "../shell/library";
import { useModal } from "../shell/useModal";
import { useDialogCard } from "./dialogCard";
import "../sync/Sync.css";

/** CollectionDialog adds one card to a collection of the current library that the owner picks. */
export function CollectionDialog({ card, onClose }: { card: number; onClose: () => void }) {
  const { name, problem } = useDialogCard(card);
  const library = useLibrary();
  const collections = useQuery(collectionsQuery(library));
  const [picked, setPicked] = useState<number>();
  const modal = useModal(onClose);
  const qc = useQueryClient();
  const add = useMutation({
    mutationFn: async (collection: number) =>
      mustOk(await api.PUT("/collections/{id}/cards/{card_id}", { params: { path: { id: collection, card_id: card } } })),
    onSuccess: async () => {
      await Promise.all([qc.invalidateQueries({ queryKey: ["card", card] }), qc.invalidateQueries({ queryKey: ["collections"] })]);
      onClose();
    },
    meta: { toast: false },
  });
  const title = name ? `Add ${name} to a collection` : "Add a card to a collection";
  return (
    <dialog {...modal} className="dialog" aria-label={title}>
      <h2>{title}</h2>
      {problem && <p className="error">{problem}</p>}
      {name && (
        <select className="input" aria-label="Collection" value={picked ?? ""} onChange={(e) => setPicked(Number(e.target.value))}>
          <option value="" disabled>
            Choose a collection
          </option>
          {collections.data?.map((c) => (
            <option key={c.id} value={c.id}>
              {c.name}
            </option>
          ))}
        </select>
      )}
      {add.isError && (
        <p className="error" role="alert">
          {add.error.message}
        </p>
      )}
      <div className="row">
        {name && (
          <button className="button primary" disabled={!picked || add.isPending} onClick={() => picked && add.mutate(picked)}>
            Add
          </button>
        )}
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
