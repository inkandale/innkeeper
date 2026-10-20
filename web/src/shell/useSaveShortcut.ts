import { useEffect } from "react";

/** useSaveShortcut runs save on ⌘S or Ctrl+S while enabled, and keeps the browser's save dialog away either way. */
export function useSaveShortcut(enabled: boolean, save: () => void) {
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== "s") return;
      e.preventDefault();
      if (enabled) save();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [enabled, save]);
}
