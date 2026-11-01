import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { bindingsQuery, useInstancesWith } from "../api/queries";
import { useModal } from "../shell/useModal";
import { PushPreview } from "../sync/PushPreview";
import { TargetPicker } from "../sync/TargetPicker";
import { useDialogCard } from "./dialogCard";

/** PushDialog picks target apps, then hands over to PushPreview, where the owner confirms. */
export function PushDialog({ card, onClose }: { card: number; onClose: () => void }) {
  const { name, problem } = useDialogCard(card);
  const targets = useInstancesWith("push");
  const bindings = useQuery(bindingsQuery("card", card));
  const [picked, setPicked] = useState<number[]>();
  const [previewing, setPreviewing] = useState(false);
  const modal = useModal(onClose);
  // Pushing to an app without a copy makes a new character there, so that's picked on purpose, not by default.
  const holding = (bindings.data ?? []).filter((b) => b.push).map((b) => b.instance_id);
  const chosen = targets.filter((t) => (picked ?? holding).includes(t.id));
  if (previewing) return <PushPreview kind="card" id={card} targets={chosen} onClose={onClose} />;
  const title = name ? `Push ${name}` : "Push a card";
  return (
    <dialog {...modal} className="dialog" aria-label={title}>
      <h2>{title}</h2>
      {problem && <p className="error">{problem}</p>}
      {name && <TargetPicker targets={targets} picked={chosen.map((t) => t.id)} onChange={setPicked} />}
      <div className="row">
        {name && (
          <button className="button primary" disabled={!chosen.length} onClick={() => setPreviewing(true)}>
            Preview
          </button>
        )}
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
