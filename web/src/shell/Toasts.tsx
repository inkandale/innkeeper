import { useQueryClient } from "@tanstack/react-query";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router";

declare module "@tanstack/react-query" {
  interface Register {
    // toast: false is for a mutation that shows its own error.
    mutationMeta: { toast?: false };
  }
}

export type ToastSpec = { text: string; link?: { to: string; label: string }; error?: boolean };
type Toast = ToastSpec & { id: number };

const ToastContext = createContext<(t: ToastSpec) => void>(() => {});

/** useToast returns a function that shows a toast. */
export const useToast = () => useContext(ToastContext);

let nextId = 0;

/** Toasts shows toasts for its children, and every failed mutation's error. Errors stay until dismissed. */
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
  const qc = useQueryClient();
  useEffect(
    () =>
      qc.getMutationCache().subscribe((e) => {
        if (e.type === "updated" && e.action.type === "error" && e.mutation.meta?.toast !== false) show({ text: e.action.error.message, error: true });
      }),
    [qc, show],
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
