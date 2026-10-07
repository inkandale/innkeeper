import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, mustOk } from "../api/client";
import type { components } from "../api/schema";

type Tier = components["schemas"]["Token"]["tier"];
type Created = components["schemas"]["CreatedToken"];
const tiers: Tier[] = ["read", "write", "destructive"];

const when = (t?: string) => (t ? new Date(t).toLocaleString() : "never");

export function Access() {
  const qc = useQueryClient();
  const me = useQuery({ queryKey: ["me"], queryFn: async () => must(await api.GET("/auth/me")) });
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: async () => must(await api.GET("/tokens")) });
  const grants = useQuery({ queryKey: ["grants"], queryFn: async () => must(await api.GET("/grants")) });
  const [created, setCreated] = useState<Created | null>(null);

  const create = useMutation({
    mutationFn: async (body: { name: string; tier: Tier }) => must(await api.POST("/tokens", { body })),
    onSuccess: (t) => {
      setCreated(t);
      return qc.invalidateQueries({ queryKey: ["tokens"] });
    },
  });
  const revokeToken = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/tokens/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
  });
  const revokeGrant = useMutation({
    mutationFn: async (id: number) => mustOk(await api.DELETE("/grants/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["grants"] }),
  });

  // A token can't have more access than the credentials creating it.
  const allowed = tiers.slice(0, tiers.indexOf((me.data?.tier ?? "read") as Tier) + 1);
  const failed = create.error ?? revokeToken.error ?? revokeGrant.error ?? tokens.error ?? grants.error;

  return (
    <section className="access">
      {failed && <p className="error">{failed.message}</p>}
      <h2>API tokens</h2>
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          const f = new FormData(e.currentTarget);
          create.mutate({ name: String(f.get("name")).trim(), tier: f.get("tier") as Tier });
          e.currentTarget.reset();
        }}
      >
        <input className="input" name="name" required aria-label="Token name" placeholder="Token name" />
        <select className="input" name="tier" aria-label="Tier">
          {allowed.map((t) => (
            <option key={t}>{t}</option>
          ))}
        </select>
        <button className="button primary" disabled={create.isPending}>
          Create
        </button>
      </form>
      {created && (
        <div className="created" role="status">
          <p>Copy the secret for {created.name} now. It isn't shown again.</p>
          <code>{created.secret}</code>
          <button type="button" className="button" onClick={() => navigator.clipboard?.writeText(created.secret)}>
            Copy
          </button>
          <button type="button" className="button" onClick={() => setCreated(null)}>
            Done
          </button>
        </div>
      )}
      <table>
        <thead>
          <tr>
            <th>Name</th>
            <th>Tier</th>
            <th>Created</th>
            <th>Last used</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {(tokens.data ?? []).map((t) => (
            <tr key={t.id}>
              <td>{t.name}</td>
              <td>{t.tier}</td>
              <td>{when(t.created_at)}</td>
              <td>{when(t.last_used_at)}</td>
              <td>
                <button
                  className="button"
                  aria-label={`Revoke ${t.name}`}
                  onClick={() => confirm(`Revoke ${t.name}? Anything using it stops working.`) && revokeToken.mutate(t.id)}
                >
                  Revoke
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      <h2>MCP clients</h2>
      <p className="muted">Clients approved through OAuth.</p>
      <table>
        <thead>
          <tr>
            <th>Client</th>
            <th>Tier</th>
            <th>Approved</th>
            <th>Last used</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {(grants.data ?? []).map((g) => (
            <tr key={g.id}>
              <td>{g.client_name}</td>
              <td>{g.tier}</td>
              <td>{when(g.created_at)}</td>
              <td>{when(g.last_used_at)}</td>
              <td>
                <button
                  className="button"
                  aria-label={`Revoke ${g.client_name}`}
                  onClick={() => confirm(`Revoke ${g.client_name}? It has to be approved again.`) && revokeGrant.mutate(g.id)}
                >
                  Revoke
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
