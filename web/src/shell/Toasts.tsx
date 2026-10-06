import { createContext, useCallback, useContext, useState, type ReactNode } from "react";
import { Link } from "react-router";

export type ToastSpec = { text: string; link?: { to: string; label: string }; error?: boolean };
type Toast = ToastSpec & { id: number };

const ToastContext = createContext<(t: ToastSpec) => void>(() => {});

/** useToast returns a function that shows a toast. */
export const useToast = () => useContext(ToastContext);

let nextId = 0;

/** Toasts shows toasts for its children. Errors stay until dismissed. */
export function Toasts({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const dismiss = useCallback((id: number) => setList((l) => l.filter((t) => t.id !== id)), []);
  const show = useCallback(
    (t: ToastSpec) => {
      const id = ++nextId;
      setList((l) => [...l, { ...t, id }]);
      if (!t.error) setTimeout(() => dismiss(id), 8000);
    },
    [dismiss],
  );
  return (
    <ToastContext.Provider value={show}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className={t.error ? "toast error" : "toast"}>
            <span>{t.text}</span>
            {t.link && <Link to={t.link.to}>{t.link.label}</Link>}
            <button className="button" aria-label="Dismiss" onClick={() => dismiss(t.id)}>
              ×
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
