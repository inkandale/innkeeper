import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api, must, mustOk } from "../api/client";
import { pluginsQuery } from "../settings/Plugins";
import { groupCatalog, type Offer } from "./wizard";

/** ChoosePlugins offers the catalog's plugins by category and installs the ones the owner picks. */
export function ChoosePlugins() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const catalog = useQuery({ queryKey: ["plugin-catalog"], queryFn: async () => must(await api.GET("/plugin-catalog")) });
  const plugins = useQuery(pluginsQuery);
  const [chosen, setChosen] = useState<Set<string>>(new Set());
  const [errors, setErrors] = useState<Record<string, string>>({});
  const installed = new Set((plugins.data ?? []).map((p) => p.name));
  const groups = groupCatalog(catalog.data ?? []);

  const install = useMutation({
    mutationFn: async (offers: Offer[]) => {
      const failed: Record<string, string> = {};
      // One at a time, so one failure doesn't stop the rest and each error lands on its card.
      for (const o of offers) {
        try {
          mustOk(
            await api.POST("/plugins/{name}/install", {
              params: { path: { name: o.name } },
              body: { source: o.source, version: o.version },
            }),
          );
        } catch (e) {
          failed[o.name] = (e as Error).message;
        }
      }
      return failed;
    },
    onSuccess: (failed) => {
      setErrors(failed);
      qc.invalidateQueries({ queryKey: ["plugins"] });
      if (Object.keys(failed).length === 0) navigate("/welcome/setup");
    },
  });

  const toggle = (name: string) =>
    setChosen((s) => {
      const next = new Set(s);
      if (!next.delete(name)) next.add(name);
      return next;
    });
  const pending = groups.flatMap((g) => g.plugins).filter((o) => chosen.has(o.name) && !installed.has(o.name));

  return (
    <div className="panel">
      <h2>Plugins from the official catalog</h2>
      <p className="muted">Pick what you use. You can add or remove plugins later in Settings.</p>
      {catalog.isPending && <p className="muted">Reading the catalog…</p>}
      {(catalog.error ?? plugins.error) && <p className="error">{(catalog.error ?? plugins.error)!.message}</p>}
      {(catalog.data ?? [])
        .filter((s) => s.error)
        .map((s) => (
          <p key={s.source.id} className="error">
            {s.source.id}: {s.error}
          </p>
        ))}
      {groups.map((g) => (
        <div key={g.category}>
          <div className="group">{g.category}</div>
          <div className="grid">
            {g.plugins.map((p) => {
              const have = installed.has(p.name);
              return (
                <label key={p.name} className="offer">
                  <div className="top">
                    <span>
                      <input
                        type="checkbox"
                        aria-label={p.name}
                        checked={have || chosen.has(p.name)}
                        disabled={have || install.isPending}
                        onChange={() => toggle(p.name)}
                      />{" "}
                      {p.name}
                      {have && <span className="badge">installed</span>}
                    </span>
                    <span className="meta">
                      v{p.version}
                      {p.released && ` · ${p.released}`}
                    </span>
                  </div>
                  <div className="desc">{p.description}</div>
                  {errors[p.name] && <p className="error">{errors[p.name]}</p>}
                </label>
              );
            })}
          </div>
        </div>
      ))}
      <div className="toolbar">
        <button className="button primary" disabled={install.isPending} onClick={() => install.mutate(pending)}>
          {install.isPending ? "Installing…" : "Install and continue"}
        </button>
        <button className="button" onClick={() => navigate("/welcome/setup")}>
          Skip
        </button>
      </div>
    </div>
  );
}
