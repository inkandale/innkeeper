import { useState } from "react";
import { NavLink, Outlet } from "react-router";
import { api } from "../api/client";
import "./Shell.css";

const sections = [
  { to: "/cards", label: "Cards", icon: "▣" },
  { to: "/lorebooks", label: "Lorebooks", icon: "📖" },
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

export function Shell() {
  const [collapsed, setCollapsed] = useState(false);
  return (
    <div className={collapsed ? "shell collapsed" : "shell"}>
      <nav className="rail" aria-label="Sections">
        <div className="rail-brand">Innkeeper</div>
        {sections.map((s) => (
          <NavLink key={s.to} to={s.to} aria-label={s.label}>
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
        <Outlet />
      </main>
    </div>
  );
}
