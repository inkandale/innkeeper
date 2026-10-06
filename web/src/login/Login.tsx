import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router";
import { api, must, safeNext } from "../api/client";
import { formFields, type Field } from "./fields";
import "./Login.css";

type Provider = { id: number; name: string; plugin: string };

export function Login() {
  const providers = useQuery({
    queryKey: ["login-providers"],
    queryFn: async () => must(await api.GET("/auth/providers")),
  });
  const [chosen, setChosen] = useState<Provider | null>(null);
  const list = providers.data ?? [];
  const provider = chosen ?? (list.length === 1 ? list[0]! : null);

  let body;
  if (providers.isPending) body = null;
  else if (providers.isError) body = <p className="error">{providers.error.message}</p>;
  else if (list.length === 0)
    body = <p>No login provider is set up. The README's "First login" section explains how to add one.</p>;
  else if (!provider)
    body = list.map((p) => (
      <button key={p.id} className="button" onClick={() => setChosen(p)}>
        {p.name}
      </button>
    ));
  else body = <ProviderLogin key={provider.id} provider={provider} />;

  return (
    <main className="login">
      <h1>Innkeeper</h1>
      {body}
    </main>
  );
}

function ProviderLogin({ provider }: { provider: Provider }) {
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [error, setError] = useState("");
  const begin = useQuery({
    queryKey: ["login-begin", provider.id],
    queryFn: async () => must(await api.POST("/auth/{id}/begin", { params: { path: { id: provider.id } } })),
    staleTime: Infinity,
  });

  if (begin.isPending) return null;
  if (begin.isError) return <p className="error">{begin.error.message}</p>;
  if (begin.data.redirect) {
    window.location.assign(begin.data.redirect);
    return null;
  }
  const fields: Field[] = formFields(begin.data.form);

  async function submit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault();
    const values = Object.fromEntries(
      fields.map((f) => [f.name, String(new FormData(e.currentTarget).get(f.name) ?? "")]),
    );
    const res = await api.POST("/auth/{id}/complete", {
      params: { path: { id: provider.id } },
      body: { params: values },
    });
    if (!res.response.ok) {
      setError((res.error as { detail?: string } | undefined)?.detail ?? res.response.statusText);
      return;
    }
    navigate(safeNext(params.get("next")), { replace: true });
  }

  return (
    <form onSubmit={submit} className="login" style={{ margin: 0, padding: 0, border: "none" }}>
      {fields.map((f) => (
        <label key={f.name}>
          {f.label}
          <input
            className="input"
            name={f.name}
            type={f.type}
            required={f.required}
            autoComplete={f.type === "password" ? "current-password" : "username"}
          />
        </label>
      ))}
      {error && <p className="error">{error}</p>}
      <button className="button primary" type="submit">
        Log in
      </button>
    </form>
  );
}
