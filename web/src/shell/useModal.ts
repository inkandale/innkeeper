import { useLayoutEffect, useRef, type SyntheticEvent } from "react";

/** useModal opens a dialog as modal while mounted, and returns the props to spread on it; Escape calls onClose. */
export function useModal(onClose: () => void) {
  const ref = useRef<HTMLDialogElement>(null);
  // showModal gives Escape and a focus trap, and closing before unmount hands focus back to the opener.
  useLayoutEffect(() => {
    const d = ref.current;
    // jsdom has no showModal.
    if (d?.showModal) d.showModal();
    else d?.setAttribute("open", "");
    return () => d?.close?.();
  }, []);
  return {
    ref,
    onCancel: (e: SyntheticEvent<HTMLDialogElement>) => {
      // The parent unmounts the dialog; the browser would only hide it.
      e.preventDefault();
      onClose();
    },
  };
}
