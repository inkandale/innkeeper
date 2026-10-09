import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { NavLink, Outlet, useNavigate } from "react-router";
import { api, must } from "../api/client";
import { pluginsQuery } from "../settings/Plugins";
import { wizardPlugin } from "./wizard";
import "./Welcome.css";

/** useFinish marks the wizard done and goes to the library. */
export function useFinish() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: async () => must(await api.PATCH("/settings", { body: { onboarding_done: true } })),
    onSuccess: (s) => {
      qc.setQueryData(["settings"], s);
      navigate("/cards", { replace: true });
    },
  });
}

/** Welcome is the setup wizard: a rail of steps beside the current one. */
export function Welcome() {
  const plugins = useQuery(pluginsQuery);
  const finish = useFinish();
  const chosen = (plugins.data ?? []).filter((p) => p.source !== "builtin" && wizardPlugin(p));
  return (
    <div className="welcome">
      <nav className="welcome-rail" aria-label="Setup steps">
        <span className="done">✓ Claimed</span>
        <NavLink to="/welcome/plugins">Choose plugins</NavLink>
        <NavLink to="/welcome/setup">Set up plugins</NavLink>
        {chosen.map((p) => (
          <NavLink key={p.name} className="sub" to={`/welcome/setup/${encodeURIComponent(p.name)}`}>
            {p.name}
          </NavLink>
        ))}
        <NavLink to="/welcome/cards">Bring in cards</NavLink>
        <button className="button" disabled={finish.isPending} onClick={() => finish.mutate()}>
          Finish later
        </button>
        {finish.error && <p className="error">{finish.error.message}</p>}
      </nav>
      <section className="welcome-step">
        <Outlet />
      </section>
    </div>
  );
}
