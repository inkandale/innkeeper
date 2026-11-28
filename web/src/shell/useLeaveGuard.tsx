import { useEffect } from "react";
import { useBlocker } from "react-router";
import { ConfirmDialog } from "./ConfirmDialog";

// useLeaveGuard asks before leaving the page while on is true: in the app
// through the returned dialog, and on reload or close through the browser.
export function useLeaveGuard(on: boolean) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => on && currentLocation.pathname !== nextLocation.pathname);
  useEffect(() => {
    if (!on) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [on]);
  if (blocker.state !== "blocked") return null;
  return (
    <ConfirmDialog
      title="Leave without saving your edits?"
      confirmLabel="Leave"
      cancelLabel="Stay"
      danger
      onConfirm={() => blocker.proceed()}
      onCancel={() => blocker.reset()}
      onClose={() => {}}
    />
  );
}
