import { NavLink, Outlet } from "react-router";
import "./Settings.css";

/** Settings holds the settings tabs. Plugins, Access, and Appearance come in a later phase. */
export function Settings() {
  return (
    <div className="settings">
      <nav className="tabs" aria-label="Settings">
        <NavLink to="/settings/jobs">Jobs</NavLink>
      </nav>
      <Outlet />
    </div>
  );
}
