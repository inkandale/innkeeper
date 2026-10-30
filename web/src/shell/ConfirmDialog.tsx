import { useState, type ReactNode } from "react";
import { useModal } from "./useModal";
import "../sync/Sync.css";

/** Confirmation is what a ConfirmDialog asks before an action that can't be undone. */
export type Confirmation = {
  title: string;
  body?: ReactNode;
  /** Names the action, like "Delete library". */
  confirmLabel: string;
  danger?: boolean;
  /** The confirm button stays disabled until this is typed. */
  typeToConfirm?: string;
  onConfirm: () => void;
  /** Names the cancel button, which defaults to "Cancel". */
  cancelLabel?: string;
  /** onCancel runs on Cancel or Escape. */
  onCancel?: () => void;
};

/** ConfirmDialog asks before an action that can't be undone, and runs it on confirm. Cancel and Escape close it. */
export function ConfirmDialog(props: Confirmation & { onClose: () => void }) {
  const { title, body, confirmLabel, danger, typeToConfirm, onConfirm, cancelLabel, onCancel, onClose } = props;
  const cancel = () => {
    onCancel?.();
    onClose();
  };
  const modal = useModal(cancel);
  const [typed, setTyped] = useState("");
  const ready = typeToConfirm === undefined || typed === typeToConfirm;
  return (
    <dialog {...modal} className="dialog confirm" aria-label={title}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!ready) return;
          onConfirm();
          onClose();
        }}
      >
        <h2>{title}</h2>
        {body}
        {typeToConfirm !== undefined && (
          <label>
            <span>
              Type <strong>{typeToConfirm}</strong> to confirm
            </span>
            <input className="input" value={typed} onChange={(e) => setTyped(e.target.value)} />
          </label>
        )}
        <div className="row">
          <button type="button" className="button" onClick={cancel}>
            {cancelLabel ?? "Cancel"}
          </button>
          <button className={danger ? "button danger" : "button primary"} disabled={!ready}>
            {confirmLabel}
          </button>
        </div>
      </form>
    </dialog>
  );
}

/** useConfirm returns a function that opens a ConfirmDialog, and the dialog to render while one is open. */
export function useConfirm() {
  const [asking, setAsking] = useState<Confirmation>();
  const dialog = asking && <ConfirmDialog {...asking} onClose={() => setAsking(undefined)} />;
  return [setAsking, dialog] as const;
}
