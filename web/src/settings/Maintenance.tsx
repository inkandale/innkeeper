import { keepPreviousData, useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import type { components } from "../api/schema";
import { toastJob } from "../shell/Toasts";

type Run = components["schemas"]["MaintenanceRunInBody"];

const mb = (bytes: number) => `${(bytes / 1048576).toFixed(0)} MB`;

/** Maintenance previews and starts the database cleanups. */
export function Maintenance() {
  const [keep, setKeep] = useState(3);
  const [days, setDays] = useState(30);
  const [confirm, setConfirm] = useState("");
  // The server refuses a count under 1, so a half-typed field waits.
  const valid = keep >= 1 && days >= 1;
  const preview = useQuery({
    queryKey: ["maintenance", keep, days],
    queryFn: async () => must(await api.GET("/maintenance", { params: { query: { keep, days } } })),
    enabled: valid,
    placeholderData: keepPreviousData,
  });
  const run = useMutation({
    mutationFn: async (body: Partial<Run> & Pick<Run, "action">) =>
      must(await api.POST("/maintenance/runs", { body: body as Run })),
    onSuccess: (j) => toastJob(j),
  });
  const p = preview.data;
  if (!p) return null;
  const busy = run.isPending;
  const nothingToPurge = p.purge.cards + p.purge.lorebooks === 0;
  return (
    <div className="maintenance">
      <p className="muted">
        Database: {mb(p.database_bytes)}. Largest: {(p.largest ?? []).map((t) => `${t.name} ${mb(t.bytes)}`).join(", ")}.
      </p>

      <section>
        <h3>Trim enrichment history</h3>
        <label>
          Keep the newest{" "}
          <input className="input" type="number" min={1} max={100} value={keep || ""} onChange={(e) => setKeep(Number(e.target.value))} />{" "}
          values per card and key
        </label>
        <p className="muted">
          Deletes {p.history.trimmable.toLocaleString()} of {p.history.rows.toLocaleString()} values. Current values don't change.
        </p>
        <button className="button" disabled={busy || !valid} onClick={() => run.mutate({ action: "trim-history", keep })}>
          Trim
        </button>
      </section>

      {p.vectors.available && (
        <section>
          <h3>Remove stale vectors</h3>
          <p className="muted">
            Deletes vectors from other plugins or made by an older model, which similar cards and search can't use. With no
            vector source set, nothing is removed. Those cards rank by tags until they're run again.{" "}
            {p.vectors.rows.toLocaleString()} vectors stored.
          </p>
          <button className="button" disabled={busy} onClick={() => run.mutate({ action: "stale-vectors" })}>
            Remove stale vectors
          </button>
        </section>
      )}

      <section>
        <h3>Clear recorded failures</h3>
        <p className="muted">
          {p.failures.toLocaleString()} cards a plugin failed on are retried automatically a day later. Clear them to retry now,
          such as after changing a model.
        </p>
        <button className="button" disabled={busy || p.failures === 0} onClick={() => run.mutate({ action: "clear-failures" })}>
          Clear failures
        </button>
      </section>

      <section>
        <h3>Vacuum and analyze</h3>
        <p className="muted">Makes space freed by deletes reusable and refreshes query plans. Files on disk don't shrink.</p>
        <button className="button" disabled={busy} onClick={() => run.mutate({ action: "vacuum" })}>
          Vacuum
        </button>
      </section>

      <section>
        <h3>Purge archived cards</h3>
        <label>
          Archived at least{" "}
          <input className="input" type="number" min={1} value={days || ""} onChange={(e) => setDays(Number(e.target.value))} /> days
          ago
        </label>
        <p className="muted">
          Deletes {p.purge.cards.toLocaleString()} cards and {p.purge.lorebooks.toLocaleString()} lorebooks for good. A pushed
          card loses its link to the app, but the app's copy stays; archive with unpublish removes it first.{" "}
          {p.last_backup ? `Last backup: ${new Date(p.last_backup).toLocaleString()}.` : "No backup has run."}
        </p>
        <label>
          Type <code>purge</code> to confirm{" "}
          <input className="input" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
        </label>{" "}
        <button
          className="button danger"
          disabled={busy || !valid || confirm !== "purge" || nothingToPurge}
          onClick={() => run.mutate({ action: "purge", days })}
        >
          Purge
        </button>
      </section>
    </div>
  );
}
