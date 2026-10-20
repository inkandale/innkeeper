import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router";
import { api } from "../api/client";
import { librariesQuery } from "../api/queries";
import { rememberLibrary, useLibrary, withLibrary } from "./library";
import { Icon } from "./Icon";
import { Mark } from "./Mark";
import { Toasts } from "./Toasts";
import "./Shell.css";

const sections = [
  { to: "/cards", label: "Cards", icon: "cards" },
  { to: "/lorebooks", label: "Lorebooks", icon: "lorebooks" },
  { to: "/collections", label: "Collections", icon: "collections" },
  { to: "/sync", label: "Sync", icon: "sync" },
  { to: "/settings", label: "Settings", icon: "settings" },
] as const;

async function logout() {
  try {
    await api.POST("/auth/logout");
  } catch {
    // Offline or failed: the login page is still the right place to go.
  } finally {
    window.location.assign("/login");
  }
}

// Pages that show one library; switching keeps the page, anywhere else opens Cards.
const listPages = ["/cards", "/lorebooks", "/collections", "/sync"];

/** LibrarySwitcher picks the current library, and remembers it once the list confirms it exists. */
function LibrarySwitcher() {
  const libraries = useQuery(librariesQuery);
  const library = useLibrary();
  const { pathname } = useLocation();
  const navigate = useNavigate();
  const loaded = !!libraries.data;
  useEffect(() => {
    if (loaded) rememberLibrary(library);
  }, [loaded, library]);
  if (!libraries.data) return null;
  return (
    <select
      className="input rail-library"
      aria-label="Library"
      value={library}
      onChange={(e) => {
        const id = Number(e.target.value);
        rememberLibrary(id);
        // Filters name collections and targets of the old library, so they're dropped.
        void navigate(withLibrary(listPages.includes(pathname) ? pathname : "/cards", id));
      }}
    >
      {libraries.data.map((l) => (
        <option key={l.id} value={l.id}>
          {l.name}
        </option>
      ))}
    </select>
  );
}

export function Shell() {
  const [collapsed, setCollapsed] = useState(false);
  const library = useLibrary();
  return (
    <div className={collapsed ? "shell collapsed" : "shell"}>
      <nav className="rail" aria-label="Sections">
        <div className="rail-brand">
          <Mark size={24} />
          <span className="rail-label">Innkeeper</span>
        </div>
        <LibrarySwitcher />
        {sections.map((s) => (
          <NavLink key={s.to} to={withLibrary(s.to, library)} aria-label={s.label}>
            <Icon name={s.icon} />
            <span className="rail-label">{s.label}</span>
          </NavLink>
        ))}
        <div className="rail-bottom">
          <button
            className="button rail-collapse"
            onClick={() => setCollapsed(!collapsed)}
            aria-expanded={!collapsed}
            aria-label={collapsed ? "Expand menu" : "Collapse menu"}
          >
            <Icon name={collapsed ? "expand" : "collapse"} />
          </button>{" "}
          <button className="button" onClick={logout} aria-label="Log out">
            <Icon name="logout" />
            <span className="rail-label">Log out</span>
          </button>
        </div>
      </nav>
      <main className="main">
        <Toasts>
          <Outlet />
        </Toasts>
      </main>
    </div>
  );
}
