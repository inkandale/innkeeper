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
  await api.POST("/auth/logout");
  window.location.assign("/login");
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
          <button className="button" onClick={() => setCollapsed(!collapsed)} aria-label="Collapse menu">
            {collapsed ? "»" : "«"}
          </button>{" "}
          {!collapsed && (
            <button className="button" onClick={logout}>
              Log out
            </button>
          )}
        </div>
      </nav>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
