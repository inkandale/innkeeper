import { useQuery } from "@tanstack/react-query";
import { createBrowserRouter, Navigate, Outlet } from "react-router";
import { api, must } from "./api/client";
import { Login } from "./login/Login";
import { Page } from "./shell/Page";
import { Shell } from "./shell/Shell";

// RequireAuth checks the session. A 401 sends the browser to /login via the
// client's middleware.
function RequireAuth() {
  const me = useQuery({ queryKey: ["me"], queryFn: async () => must(await api.GET("/auth/me")) });
  if (me.isPending) return null;
  if (me.isError) return <p className="error">{me.error.message}</p>;
  return <Outlet />;
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
          { index: true, element: <Navigate to="/cards" replace /> },
          { path: "cards", element: <Page title="Cards" /> },
          { path: "lorebooks", element: <Page title="Lorebooks" /> },
          { path: "collections", element: <Page title="Collections" /> },
          { path: "sync", element: <Page title="Sync" /> },
          { path: "settings", element: <Page title="Settings" /> },
        ],
      },
    ],
  },
]);
