import { useQuery } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { api, ApiError, must, nextKey, safeNext } from "./api/client";
import { Card } from "./card/Card";
import { History } from "./card/History";
import { Cards } from "./cards/Cards";
import { Collections } from "./collections/Collections";
import { Login } from "./login/Login";
import { Lorebook } from "./lorebook/Lorebook";
import { Page } from "./shell/Page";
import { Shell } from "./shell/Shell";
import { Queue } from "./sync/Queue";
import { Review } from "./sync/Review";
import { Sync } from "./sync/SyncPage";
import { Access } from "./settings/Access";
import { Appearance } from "./settings/Appearance";
import { Instance } from "./settings/Instance";
import { Job } from "./settings/Job";
import { Jobs } from "./settings/Jobs";
import { Plugins } from "./settings/Plugins";
import { Settings } from "./settings/Settings";

// RequireAuth checks the session. A 401 sends the browser to /login via the
// client's middleware.
function RequireAuth() {
  const me = useQuery({ queryKey: ["me"], queryFn: async () => must(await api.GET("/auth/me")) });
  if (me.isPending) return null;
  // The middleware is already sending the browser to /login.
  if (me.error instanceof ApiError && me.error.status === 401) return null;
  if (me.isError) return <p className="error">{me.error.message}</p>;
  return <Outlet />;
}

/** Home goes where a redirect login asked to return, or to the cards. */
export function Home() {
  let next = "/";
  try {
    next = safeNext(sessionStorage.getItem(nextKey));
    sessionStorage.removeItem(nextKey);
  } catch {
    // Storage is blocked: use the default.
  }
  return <Navigate to={next !== "/" ? next : "/cards"} replace />;
}

export const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  {
    element: <RequireAuth />,
    children: [
      {
        path: "/",
        element: <Shell />,
        children: [
          { index: true, element: <Home /> },
          { path: "cards", element: <Cards key="cards" /> },
          { path: "lorebooks", element: <Cards key="lorebooks" kind="lorebook" /> },
          { path: "lorebooks/:id", element: <Lorebook /> },
          { path: "lorebooks/:id/history", element: <History kind="lorebook" /> },
          { path: "cards/:id", element: <Card /> },
          { path: "cards/:id/history", element: <History /> },
          { path: "collections", element: <Collections /> },
          { path: "sync", element: <Sync /> },
          { path: "sync/reconciles/:job", element: <Queue /> },
          { path: "sync/:instance/:kind/:remoteId", element: <Review /> },
          {
            path: "settings",
            element: <Settings />,
            children: [
              { index: true, element: <Navigate to="plugins" replace /> },
              { path: "plugins", element: <Plugins /> },
              { path: "plugins/:plugin/new", element: <Instance /> },
              { path: "instances/:id", element: <Instance /> },
              { path: "jobs", element: <Jobs /> },
              { path: "jobs/:id", element: <Job /> },
              { path: "access", element: <Access /> },
              { path: "appearance", element: <Appearance /> },
            ],
          },
          { path: "*", element: <Page title="Not found" /> },
        ],
      },
    ],
  },
]);
