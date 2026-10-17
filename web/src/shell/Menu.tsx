import { useId, type ReactNode } from "react";

/** Menu is a ⋯ button whose items open in a popover; the browser closes it on an outside click or Escape. */
export function Menu({ label, children }: { label: string; children: ReactNode }) {
  const id = useId();
  return (
    <>
      <button className="menu-button" popoverTarget={id} aria-label={label}>
        ⋯
      </button>
      {/* hidePopover is missing in jsdom. */}
      <div id={id} popover="auto" className="menu" onClick={(e) => e.currentTarget.hidePopover?.()}>
        {children}
      </div>
    </>
  );
}
