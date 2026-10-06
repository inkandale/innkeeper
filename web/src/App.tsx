import { useQuery } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { api, ApiError, must, nextKey, safeNext } from "./api/client";
import { Card } from "./card/Card";
import { History } from "./card/History";
import { Cards } from "./cards/Cards";
import { Collections } from "./collections/Collections";
import { Login } from "./login/Login";
import { Page } from "./shell/Page";
import { Shell } from "./shell/Shell";
import { Job } from "./settings/Job";
import { Jobs } from "./settings/Jobs";
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
          { path: "cards", element: <Cards /> },
          { path: "cards/:id", element: <Card /> },
          { path: "cards/:id/history", element: <History /> },
          { path: "lorebooks", element: <Page title="Lorebooks" /> },
          { path: "collections", element: <Collections /> },
          { path: "sync", element: <Page title="Sync" /> },
          {
            path: "settings",
            element: <Settings />,
            children: [
              { index: true, element: <Navigate to="jobs" replace /> },
              { path: "jobs", element: <Jobs /> },
              { path: "jobs/:id", element: <Job /> },
            ],
          },
          { path: "*", element: <Page title="Not found" /> },
        ],
      },
    ],
  },
]);
