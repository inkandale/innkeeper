import { useEffect } from "react";
import { useBlocker } from "react-router";

// useLeaveGuard asks before leaving the page while on is true: in the app
// through the router, and on reload or close through the browser.
export function useLeaveGuard(on: boolean) {
  const blocker = useBlocker(({ currentLocation, nextLocation }) => on && currentLocation.pathname !== nextLocation.pathname);
  useEffect(() => {
    if (blocker.state !== "blocked") return;
    if (window.confirm("Leave without saving your edits?")) blocker.proceed();
    else blocker.reset();
  }, [blocker]);
  useEffect(() => {
    if (!on) return;
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [on]);
}
