import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation, useNavigate, useSearchParams } from "react-router";
import { api } from "../api/client";
import { librariesQuery } from "../api/queries";
import { libraryParam, rememberLibrary, useLibrary, withLibrary } from "./library";
import { Mark } from "./Mark";
import { Toasts } from "./Toasts";
import "./Shell.css";

const sections = [
  { to: "/cards", label: "Cards", icon: "▣" },
  { to: "/lorebooks", label: "Lorebooks", icon: "📖" },
  { to: "/archives", label: "Archive", icon: "🗄" },
  { to: "/collections", label: "Collections", icon: "🗂" },
  { to: "/sync", label: "Sync", icon: "⇅" },
  { to: "/settings", label: "Settings", icon: "⚙" },
];

async function logout() {
  try {
    await api.POST("/auth/logout");
  } catch {
    // Offline or failed: the login page is still the right place to go.
  } finally {
    window.location.assign("/login");
  }
}

// Pages that list one library's items; switching keeps the page, anywhere else opens Cards.
const listPages = ["/cards", "/lorebooks", "/collections"];

/** LibrarySwitcher picks the current library, and remembers any library the URL names. */
function LibrarySwitcher() {
  const libraries = useQuery(librariesQuery);
  const library = useLibrary();
  const [params] = useSearchParams();
  const named = libraryParam(params);
  const { pathname } = useLocation();
  const navigate = useNavigate();
  useEffect(() => {
    if (named) rememberLibrary(named);
  }, [named]);
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
            <span aria-hidden="true">{s.icon}</span>
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
            {collapsed ? "»" : "«"}
          </button>{" "}
          <button className="button" onClick={logout} aria-label="Log out">
            <span aria-hidden="true">⏻</span>
            <span className="rail-label"> Log out</span>
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
