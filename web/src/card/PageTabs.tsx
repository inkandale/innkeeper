import type { KeyboardEvent, ReactNode } from "react";

export type PageTab = { id: string; label: string; disabled?: boolean; panel: ReactNode };

type PageTabsProps = {
  label: string;
  tabs: PageTab[];
  /** selected falls back to the first usable tab when it names none, so at least one tab must be enabled. */
  selected: string;
  onSelect: (id: string) => void;
  /** actions sit at the bar's right end. */
  actions?: ReactNode;
  /** notices sit between the bar and the panel. */
  notices?: ReactNode;
};

/** PageTabs is a card or lorebook page's main column: its tab bar and the selected tab's panel. */
export function PageTabs({ label, tabs, selected, onSelect, actions, notices }: PageTabsProps) {
  const usable = tabs.filter((t) => !t.disabled);
  const shown = usable.find((t) => t.id === selected) ?? usable[0]!;
  const tabId = (id: string) => `page-tab-${id}`;
  const arrow = (e: KeyboardEvent<HTMLDivElement>) => {
    const step = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    e.preventDefault();
    const next = usable[(usable.indexOf(shown) + step + usable.length) % usable.length]!;
    onSelect(next.id);
    document.getElementById(tabId(next.id))?.focus();
  };
  return (
    <>
      <div className="page-tabs">
        <div role="tablist" aria-label={label} onKeyDown={arrow}>
          {tabs.map((t) => (
            <button
              key={t.id}
              id={tabId(t.id)}
              role="tab"
              aria-selected={shown.id === t.id}
              aria-controls="page-tabpanel"
              tabIndex={shown.id === t.id ? 0 : -1}
              className={shown.id === t.id ? "on" : undefined}
              disabled={t.disabled}
              onClick={() => onSelect(t.id)}
            >
              {t.label}
            </button>
          ))}
        </div>
        {actions}
      </div>
      {notices}
      <div id="page-tabpanel" role="tabpanel" aria-labelledby={tabId(shown.id)}>
        {shown.panel}
      </div>
    </>
  );
}
