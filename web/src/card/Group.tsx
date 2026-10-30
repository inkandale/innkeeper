import type { ReactNode } from "react";

/** Group is a collapsible block of the side panel. */
export function Group({ title, open = true, children }: { title: string; open?: boolean; children: ReactNode }) {
  return (
    <details className="aside-group" open={open}>
      <summary>
        <h2>{title}</h2>
      </summary>
      <div className="aside-group-body">{children}</div>
    </details>
  );
}
