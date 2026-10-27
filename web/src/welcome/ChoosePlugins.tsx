import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate } from "react-router";
import { api, must, mustOk } from "../api/client";
import { pluginsQuery } from "../api/queries";
import { groupCatalog, pickGroups, recommended, type Offer } from "./wizard";

/** ChoosePlugins offers the catalog's plugins by category and installs the ones the owner picks. */
export function ChoosePlugins() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const catalog = useQuery({ queryKey: ["plugin-catalog"], queryFn: async () => must(await api.GET("/plugin-catalog")) });
  const plugins = useQuery(pluginsQuery);
  // Null until the owner changes something, so the recommended plugins start chosen once the catalog loads.
  const [picked, setPicked] = useState<Set<string> | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const installed = new Set((plugins.data ?? []).map((p) => p.name));
  const groups = groupCatalog(catalog.data ?? []);
  const chosen = picked ?? new Set(groups.flatMap((g) => g.plugins).filter(recommended).map((o) => o.name));
  const top = pickGroups(groups, recommended);
  const more = pickGroups(groups, (o) => !recommended(o));

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
      qc.invalidateQueries({ queryKey: ["instances"] });
      if (Object.keys(failed).length === 0) navigate("/welcome/setup");
    },
  });

  const toggle = (name: string) =>
    setPicked(() => {
      const next = new Set(chosen);
      if (!next.delete(name)) next.add(name);
      return next;
    });
  const pending = groups.flatMap((g) => g.plugins).filter((o) => chosen.has(o.name) && !installed.has(o.name));
  // Until /plugins answers, an installed plugin would look unchosen and get installed again.
  const locked = !plugins.isSuccess || install.isPending;
  const empty = catalog.isSuccess && groups.length === 0 && !catalog.data?.some((s) => s.error);

  const group = (g: (typeof groups)[number]) => (
    <div key={g.category}>
      <div className="group">{g.category}</div>
      <div className="grid">
        {g.plugins.map((p) => {
          const have = installed.has(p.name);
          const errorId = `install-error-${p.name}`;
          return (
            <div key={p.name}>
              <label className="offer">
                <div className="top">
                  <span>
                    <input
                      type="checkbox"
                      aria-label={p.title || p.name}
                      aria-describedby={errors[p.name] ? errorId : undefined}
                      checked={have || chosen.has(p.name)}
                      disabled={have || locked}
                      onChange={() => toggle(p.name)}
                    />{" "}
                    {p.title || p.name}
                    {have && <span className="badge">installed</span>}
                  </span>
                </div>
                <div className="desc">{p.description}</div>
              </label>
              {errors[p.name] && (
                <p id={errorId} className="error" role="alert">
                  {errors[p.name]}
                </p>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );

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
      {empty && <p className="muted">Nothing in the catalog to install. Skip to continue.</p>}
      {(top.length ? top : more).map(group)}
      {top.length > 0 && more.length > 0 && (
        <details className="more">
          <summary>More plugins</summary>
          {more.map(group)}
        </details>
      )}
      <div className="toolbar">
        {!empty && (
          <button className="button primary" disabled={locked} onClick={() => install.mutate(pending)}>
            {install.isPending ? "Installing…" : "Install and continue"}
          </button>
        )}
        {/* A pending install navigates when it ends, so leaving first would be undone. */}
        <button className="button" disabled={install.isPending} onClick={() => navigate("/welcome/setup")}>
          Skip
        </button>
      </div>
    </div>
  );
}
