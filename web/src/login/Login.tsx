import { useQuery } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { api, must, nextKey, safeNext } from "../api/client";
import type { components } from "../api/schema";
import { formFields, type Field } from "./fields";
import { Loading } from "../shell/Loading";
import { Mark } from "../shell/Mark";
import "./Login.css";

type Provider = components["schemas"]["Provider"];

export function Login() {
  const providers = useQuery({
    queryKey: ["login-providers"],
    queryFn: async () => must(await api.GET("/auth/providers")),
  });
  // An unclaimed server has no providers; send the browser to claim it.
  const setup = useQuery({
    queryKey: ["setup"],
    queryFn: async () => must(await api.GET("/setup")),
    enabled: providers.isSuccess && (providers.data?.length ?? 0) === 0,
  });
  const [chosen, setChosen] = useState<Provider | null>(null);
  const list = providers.data ?? [];
  // Skip the choice only after a 401 sent the user here (next is set). After
  // a logout, auto-redirecting a lone provider would sign them straight back in.
  const [params] = useSearchParams();
  const provider = chosen ?? (list.length === 1 && params.has("next") ? list[0]! : null);

  let body;
  if (providers.isPending) body = <Loading />;
  else if (providers.isError) body = <p className="error">{providers.error.message}</p>;
  else if (list.length === 0)
    body = setup.isPending ? <Loading /> : setup.isSuccess ? (
      <Navigate to="/setup" replace />
    ) : (
      <p>No login provider is set up. The README's "First login" section explains how to add one.</p>
    );
  else if (!provider)
    body = list.map((p) => (
      <button key={p.id} className="button" onClick={() => setChosen(p)}>
        {p.name}
      </button>
    ));
  else body = <ProviderLogin key={provider.id} provider={provider} />;

  return (
    <main className="login">
      <Mark size={40} />
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

  const redirect = begin.data?.redirect;
  const next = params.get("next");
  useEffect(() => {
    // Only http(s): a provider's redirect must not be a javascript: URL.
    if (!redirect || !/^https?:/i.test(redirect)) return;
    try {
      // The callback lands on "/", so keep next for the index route.
      sessionStorage.setItem(nextKey, safeNext(next));
    } catch {
      // Storage is blocked: the user lands on the default page.
    }
    window.location.assign(redirect);
  }, [redirect, next]);

  if (begin.isPending) return <Loading />;
  if (begin.isError) return <p className="error">{begin.error.message}</p>;
  if (redirect) return null;
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
    <form onSubmit={submit} className="login-form">
      {fields.map((f) => (
        <label key={f.name}>
          {f.label}
          <input
            className="input"
            name={f.name}
            type={f.type}
            required={f.required}
            autoComplete={f.autoComplete}
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
