import { Link, NavLink, Outlet } from "react-router";
import "./Settings.css";

/** Settings holds the settings tabs. */
export function Settings() {
  return (
    <div className="settings">
      <nav className="tabs" aria-label="Settings">
        <NavLink to="/settings/general">General</NavLink>
        <NavLink to="/settings/plugins">Plugins</NavLink>
        <NavLink to="/settings/libraries">Libraries</NavLink>
        <NavLink to="/settings/jobs">Jobs</NavLink>
        <NavLink to="/settings/maintenance">Maintenance</NavLink>
        <NavLink to="/settings/access">Access</NavLink>
        <Link className="button ghost" to="/welcome">
          Run setup wizard
        </Link>
      </nav>
      <Outlet />
    </div>
  );
}
