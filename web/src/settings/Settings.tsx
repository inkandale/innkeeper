import { NavLink, Outlet } from "react-router";
import "./Settings.css";

/** Settings holds the settings tabs. */
export function Settings() {
  return (
    <div className="settings">
      <nav className="tabs" aria-label="Settings">
        <NavLink to="/settings/plugins">Plugins</NavLink>
        <NavLink to="/settings/jobs">Jobs</NavLink>
        <NavLink to="/settings/access">Access</NavLink>
        <NavLink to="/settings/appearance">Appearance</NavLink>
      </nav>
      <Outlet />
    </div>
  );
}
