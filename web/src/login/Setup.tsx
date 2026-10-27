import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { api, ApiError, must } from "../api/client";
import { formFields } from "./fields";
import { Mark } from "../shell/Mark";
import "./Login.css";

/** Setup claims a server that has no login provider yet. */
export function Setup() {
  const setup = useQuery({ queryKey: ["setup"], queryFn: async () => must(await api.GET("/setup")) });
  const [chosen, setChosen] = useState<string | null>(null);
  const list = setup.data ?? [];
  const plugin = list.find((p) => p.plugin === chosen) ?? (list.length === 1 ? list[0] : undefined);

  let body;
  if (setup.isPending) body = null;
  else if (setup.error instanceof ApiError && setup.error.status === 404)
    body = (
      <p>
        This server is already set up. <Link to="/login">Log in</Link>
      </p>
    );
  else if (setup.isError) body = <p className="error">{setup.error.message}</p>;
  else if (list.length === 0)
    body = <p>No installed login plugin can claim a server. "First login" in docs/running.md explains how to add one.</p>;
  else if (!plugin)
    body = list.map((p) => (
      <button key={p.plugin} className="button" onClick={() => setChosen(p.plugin)}>
        {p.plugin}
      </button>
    ));
  else body = <ClaimForm plugin={plugin.plugin} form={plugin.form} />;

  return (
    <main className="login">
      <Mark size={40} />
      <h1>Set up Innkeeper</h1>
      {body}
    </main>
  );
}

function ClaimForm({ plugin, form }: { plugin: string; form: unknown }) {
  const navigate = useNavigate();
  const [error, setError] = useState("");
  const fields = formFields(form);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const data = new FormData(e.currentTarget);
    const params = Object.fromEntries(fields.map((f) => [f.name, String(data.get(f.name) ?? "")]));
    const res = await api.POST("/setup", { body: { code: String(data.get("code") ?? ""), plugin, params } });
    if (!res.response.ok) {
      setError((res.error as { detail?: string } | undefined)?.detail ?? res.response.statusText);
      return;
    }
    navigate("/", { replace: true });
  }

  return (
    <form onSubmit={submit} className="login-form">
      <p>Enter the setup code from the server log, then choose your password. To find it, run:</p>
      <pre className="hint">docker compose logs api | grep "setup code"</pre>
      <p className="hint">
        On Kubernetes: <code>kubectl logs deploy/innkeeper -c api | grep "setup code"</code>
      </p>
      <label>
        Setup code
        <input className="input" name="code" required autoComplete="off" />
      </label>
      {fields.map((f) => (
        <label key={f.name}>
          {f.label}
          <input
            className="input"
            name={f.name}
            type={f.type}
            required={f.required}
            autoComplete={f.type === "password" ? "new-password" : f.autoComplete}
          />
        </label>
      ))}
      {error && <p className="error">{error}</p>}
      <button className="button primary" type="submit">
        Claim this server
      </button>
      <p className="hint">
        Or set the password in the deployment with <code>INNKEEPER_OWNER_PASSWORD_HASH</code> and restart. Other login
        providers, such as OIDC, can be added in Settings after setup.
      </p>
    </form>
  );
}
