import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, must, mustOk } from "../api/client";
import { meQuery } from "../api/queries";
import type { components } from "../api/schema";

type Tier = components["schemas"]["Token"]["tier"];
const tiers: Tier[] = ["read", "write", "destructive"];

const when = (t?: string) => (t ? new Date(t).toLocaleString() : "never");

export function Access() {
  const qc = useQueryClient();
  const me = useQuery(meQuery);
  const tokens = useQuery({ queryKey: ["tokens"], queryFn: async () => must(await api.GET("/tokens")) });
  const grants = useQuery({ queryKey: ["grants"], queryFn: async () => must(await api.GET("/grants")) });

  const create = useMutation({
    mutationFn: async (body: { name: string; tier: Tier }) => must(await api.POST("/tokens", { body })),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tokens"] }),
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
  const failed = create.error ?? revokeToken.error ?? revokeGrant.error ?? tokens.error ?? grants.error ?? me.error;

  return (
    <section className="access">
      {failed && <p className="error">{failed.message}</p>}
      <h2>API tokens</h2>
      <form
        className="toolbar"
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const f = new FormData(form);
          // Reset only on success, so a refused name stays to fix.
          create.mutate({ name: String(f.get("name")).trim(), tier: f.get("tier") as Tier }, { onSuccess: () => form.reset() });
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
      {create.data && (
        <div className="created" role="status">
          <p>Copy the secret for {create.data.name} now. It isn't shown again.</p>
          <code>{create.data.secret}</code>
          <button type="button" className="button" onClick={() => navigator.clipboard?.writeText(create.data.secret)}>
            Copy
          </button>
          <button type="button" className="button" onClick={() => create.reset()}>
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

      <h2>OAuth clients</h2>
      <p className="muted">MCP clients and ink logins you approved.</p>
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
