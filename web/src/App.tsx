import { useQuery } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { ApiError, nextKey, safeNext } from "./api/client";
import { meQuery, settingsQuery } from "./api/queries";
import { Card } from "./card/Card";
import { History } from "./card/History";
import { NewCard } from "./card/NewCard";
import { Cards } from "./cards/Cards";
import { Collections } from "./collections/Collections";
import { Login } from "./login/Login";
import { Setup } from "./login/Setup";
import { Lorebook } from "./lorebook/Lorebook";
import { Page } from "./shell/Page";
import { Shell } from "./shell/Shell";
import { Changes } from "./sync/Changes";
import { Queue } from "./sync/Queue";
import { Review } from "./sync/Review";
import { Sync } from "./sync/SyncPage";
import { Access } from "./settings/Access";
import { Appearance } from "./settings/Appearance";
import { Instance } from "./settings/Instance";
import { Job } from "./settings/Job";
import { Jobs } from "./settings/Jobs";
import { Libraries, Library } from "./settings/Libraries";
import { Plugins } from "./settings/Plugins";
import { Settings } from "./settings/Settings";
import { BringInCards } from "./welcome/BringInCards";
import { ChoosePlugins } from "./welcome/ChoosePlugins";
import { SetUpPlugins } from "./welcome/SetUpPlugins";
import { Welcome } from "./welcome/Welcome";

// RequireAuth checks the session. A 401 sends the browser to /login via the
// client's middleware.
function RequireAuth() {
  const me = useQuery(meQuery);
  if (me.isPending) return null;
  // The middleware is already sending the browser to /login.
  if (me.error instanceof ApiError && me.error.status === 401) return null;
  if (me.isError) return <p className="error">{me.error.message}</p>;
  return <Outlet />;
}

/** Home sends an owner who hasn't finished the setup wizard to it, else goes where a redirect login asked to return, or to the cards. */
export function Home() {
  const settings = useQuery(settingsQuery);
  if (settings.isPending) return null;
  // Read and drop the saved destination either way, so it can't hijack a later visit to /.
  let next = "/";
  try {
    next = safeNext(sessionStorage.getItem(nextKey));
    sessionStorage.removeItem(nextKey);
  } catch {
    // Storage is blocked: use the default.
  }
  if (settings.data && !settings.data.onboarding_done) return <Navigate to="/welcome" replace />;
  return <Navigate to={next !== "/" ? next : "/cards"} replace />;
}

export const router = createBrowserRouter([
  { path: "/login", element: <Login /> },
  { path: "/setup", element: <Setup /> },
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
          { path: "cards/new", element: <NewCard /> },
          { path: "cards/:id", element: <Card /> },
          { path: "cards/:id/history", element: <History /> },
          { path: "collections", element: <Collections /> },
          { path: "sync", element: <Sync /> },
          { path: "sync/changes", element: <Changes /> },
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
              { path: "libraries", element: <Libraries /> },
              { path: "libraries/:id", element: <Library /> },
              { path: "jobs", element: <Jobs /> },
              { path: "jobs/:id", element: <Job /> },
              { path: "access", element: <Access /> },
              { path: "appearance", element: <Appearance /> },
            ],
          },
          {
            path: "welcome",
            element: <Welcome />,
            children: [
              { index: true, element: <Navigate to="plugins" replace /> },
              { path: "plugins", element: <ChoosePlugins /> },
              { path: "setup", element: <SetUpPlugins /> },
              { path: "setup/:plugin", element: <SetUpPlugins /> },
              { path: "cards", element: <BringInCards /> },
            ],
          },
          { path: "*", element: <Page title="Not found" /> },
        ],
      },
    ],
  },
]);
