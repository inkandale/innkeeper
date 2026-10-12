import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useLayoutEffect, useRef, useState } from "react";
import { Link, useSearchParams } from "react-router";
import { api, ApiError, must } from "../api/client";
import type { components } from "../api/schema";
import { badgeClass } from "../cards/Tile";
import { useToast } from "../shell/Toasts";
import { PushPreview } from "./PushPreview";
import { bulkResolutions, changePath, compareItem, pushSummary, reviewable, type AppChange, type Option } from "./sync";
import "./Sync.css";

type ChangeRow = components["schemas"]["ChangeRow"];
type Job = components["schemas"]["JobOut"];

const states = ["differs", "both_changed", "new", "error", "ahead", "missing"] as const;
const label = (s: string) => s.replaceAll("_", " ");
const rowKey = (r: ChangeRow) => (r.card_id ? `card/${r.card_id}` : `app/${r.apps?.[0]?.instance_id}/${r.apps?.[0]?.remote_id}`);
/** applicable entries are what Apply suggested settles: a review with a suggestion, or a push. */
const applicable = (c: AppChange) => (reviewable(c) && !!c.suggested) || c.state === "ahead";

/** group lists values by the instance each belongs to. */
function group<T>(xs: T[], inst: (x: T) => number): Map<number, T[]> {
  const m = new Map<number, T[]>();
  for (const x of xs) m.set(inst(x), [...(m.get(inst(x)) ?? []), x]);
  return m;
}

/** Changes lists every card some app needs the owner for, one row per card, and settles the checked ones. */
export function Changes() {
  const qc = useQueryClient();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const instance = Number(params.get("instance_id")) || undefined;
  const state = (params.get("state") || undefined) as (typeof states)[number] | undefined;
  const changes = useQuery({
    queryKey: ["sync", "changes", instance, state],
    queryFn: async () => must(await api.GET("/sync/changes", { params: { query: { instance_id: instance, state } } })),
  });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const name = (id: number) => instances.data?.find((i) => i.id === id)?.name ?? `#${id}`;
  // The owner's changes from the defaults.
  const [checked, setChecked] = useState<Record<string, boolean>>({});
  const [pushing, setPushing] = useState<{ card: number; instance: number }>();
  const [asking, setAsking] = useState(false);

  const rows = changes.data?.rows ?? [];
  const apps = [...new Set(rows.flatMap((r) => (r.apps ?? []).map((c) => c.instance_id)))].sort((a, b) => a - b);
  const isChecked = (r: ChangeRow) =>
    (r.apps ?? []).some(applicable) && (checked[rowKey(r)] ?? (r.apps ?? []).every(applicable));
  const chosen = rows.filter(isChecked).flatMap((r) => (r.apps ?? []).filter(applicable).map((c) => ({ card: r.card_id ?? 0, c })));
  const reviews = chosen.filter(({ c }) => c.state !== "ahead").map(({ c }) => c);
  const pushes = group(chosen.filter(({ c }) => c.state === "ahead"), ({ c }) => c.instance_id);
  const first = rows.flatMap((r) => r.apps ?? []).find(reviewable);

  const setFilter = (k: string, v: string) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next);
  };
  const done = () => void qc.invalidateQueries({ queryKey: ["sync", "changes"] });

  // What each push does to the app's copies; its confirm makes the push leave alone any copy that changes after this.
  const previews = useQuery({
    enabled: asking,
    queryKey: ["push-preview", "changes", [...pushes].map(([i, cs]) => [i, cs.map((p) => p.card)])],
    gcTime: 0,
    queryFn: async () =>
      new Map(
        await Promise.all(
          [...pushes].map(async ([i, cs]) => {
            const body = { instance_id: i, query: { filter: { ids: cs.map((p) => p.card) } } };
            return [i, must(await api.POST("/pushes/preview", { body }))] as const;
          }),
        ),
      ),
  });

  const apply = useMutation({
    mutationFn: async () => {
      const jobs: Job[] = [];
      const resolutions = (
        await Promise.all(
          [...group(reviews, (c) => c.instance_id)].map(([i, cs]) =>
            bulkResolutions(
              i,
              cs.map((c) => ({ kind: "items" as const, item: { remote_id: c.remote_id ?? "", app_hash: c.app_hash ?? "", binding_id: c.binding_id }, c })),
              (r) => r.c.suggested as Option,
              (r, card) =>
                compareItem(i, "items", r.item.remote_id, card).catch((e: Error) => {
                  throw new Error(`${r.c.name || r.item.remote_id}: ${e.message}`);
                }),
            ),
          ),
        )
      ).flat();
      if (resolutions.length) jobs.push(must(await api.POST("/resolutions", { body: { resolutions } })));
      for (const [i, cs] of pushes) {
        const body = { instance_id: i, query: { filter: { ids: cs.map((p) => p.card) } }, confirm: previews.data?.get(i)?.confirm };
        const res = await api.POST("/pushes", { body });
        // A 409 means the copies changed since the preview; show the new one.
        if (res.response.status === 409) void previews.refetch();
        jobs.push(must(res));
      }
      return jobs;
    },
    onSuccess: (jobs) => {
      for (const j of jobs) toast({ text: `Job ${j.id} started`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } });
      setAsking(false);
      setChecked({});
      done();
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });

  const unbind = useMutation({
    mutationFn: async (id: number) => must(await api.DELETE("/bindings/{id}", { params: { path: { id } } })),
    onSuccess: done,
    onError: (e) => toast({ text: e.message, error: true }),
  });

  const actions = (r: ChangeRow, c: AppChange) => {
    const where = (r.apps?.length ?? 0) > 1 && <span className="muted"> {name(c.instance_id)}</span>;
    const push = (text: string) => (
      <button className="button" onClick={() => setPushing({ card: r.card_id ?? 0, instance: c.instance_id })}>
        {text}
        {where}
      </button>
    );
    switch (c.state) {
      case "differs":
      case "both_changed":
      case "new":
        return (
          <Link to={changePath(c)}>
            Review{where}
          </Link>
        );
      case "ahead":
        return push("Push");
      case "missing":
        return (
          <>
            {push("Push again")}{" "}
            <button className="button" disabled={unbind.isPending} onClick={() => unbind.mutate(c.binding_id ?? 0)}>
              Unbind{where}
            </button>
          </>
        );
      case "error":
        return <span className="error">{c.error}</span>;
    }
  };

  if (changes.isError) return <p className="error page">{changes.error.message}</p>;
  return (
    <section className="page sync">
      <p>
        <Link to="/sync">← Sync</Link>
      </p>
      <h1>Changes</h1>
      <div className="toolbar">
        <select className="input" aria-label="App" value={instance ?? ""} onChange={(e) => setFilter("instance_id", e.target.value)}>
          <option value="">All apps</option>
          {[...new Set([...Object.keys(changes.data?.counts ?? {}).map(Number), ...(instance ? [instance] : [])])].map((i) => (
            <option key={i} value={i}>
              {name(i)}
            </option>
          ))}
        </select>
        <select className="input" aria-label="State" value={state ?? ""} onChange={(e) => setFilter("state", e.target.value)}>
          <option value="">All states</option>
          {states.map((s) => (
            <option key={s} value={s}>
              {label(s)}
            </option>
          ))}
        </select>
        {first && (
          <Link className="button" to={changePath(first)}>
            Review one by one
          </Link>
        )}
      </div>
      {!changes.data ? (
        <p>Loading…</p>
      ) : !rows.length ? (
        <p>Nothing needs you.</p>
      ) : (
        <>
          <table>
            <thead>
              <tr>
                <th>
                  <span className="visually-hidden">Apply</span>
                </th>
                <th>Card</th>
                {apps.map((i) => (
                  <th key={i}>{name(i)}</th>
                ))}
                <th>Action</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={rowKey(r)}>
                  <td>
                    <input
                      type="checkbox"
                      aria-label={`Apply ${r.name}`}
                      disabled={!(r.apps ?? []).some(applicable)}
                      checked={isChecked(r)}
                      onChange={(e) => setChecked({ ...checked, [rowKey(r)]: e.target.checked })}
                    />
                  </td>
                  <td>{r.card_id ? <Link to={`/cards/${r.card_id}`}>{r.name}</Link> : r.name}</td>
                  {apps.map((i) => (
                    <td key={i}>
                      {(r.apps ?? [])
                        .filter((c) => c.instance_id === i)
                        .map((c) => (
                          <span key={c.state}>
                            <span className={"badge " + (badgeClass[c.state] ?? "drift")} aria-hidden="true" /> {label(c.state)}
                          </span>
                        ))}
                    </td>
                  ))}
                  <td>
                    {(r.apps ?? []).map((c) => (
                      <div key={`${c.instance_id}/${c.state}`}>{actions(r, c)}</div>
                    ))}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          <button
            className="button primary"
            disabled={!chosen.length || apply.isPending}
            onClick={() => (pushes.size ? setAsking(true) : apply.mutate())}
          >
            Apply {chosen.length} suggested
          </button>
        </>
      )}
      {asking && (
        <AskPush name={name} previews={previews} busy={apply.isPending} onConfirm={() => apply.mutate()} onClose={() => setAsking(false)} />
      )}
      {pushing && (
        <PushPreview
          kind="card"
          id={pushing.card}
          instance={pushing.instance}
          name={name(pushing.instance)}
          onClose={() => {
            setPushing(undefined);
            done();
          }}
        />
      )}
    </section>
  );
}

/** AskPush shows what pushing the checked cards does to each app's copies before Apply sends anything. */
function AskPush(props: {
  name: (id: number) => string;
  previews: { data?: Map<number, components["schemas"]["BulkPreview"]>; error: Error | null; isFetching: boolean };
  busy: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const { name, previews, busy, onConfirm, onClose } = props;
  const dialog = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const d = dialog.current;
    // jsdom has no showModal.
    if (d?.showModal) d.showModal();
    else d?.setAttribute("open", "");
    return () => d?.close?.();
  }, []);
  return (
    <dialog
      ref={dialog}
      className="dialog"
      aria-label="Apply suggested"
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <h2>Apply suggested</h2>
      {previews.error ? (
        <p className="error">
          {previews.error instanceof ApiError && previews.error.status === 409
            ? "What the push sends has changed. Check the new preview."
            : previews.error.message}
        </p>
      ) : !previews.data ? (
        <p>Reading the apps…</p>
      ) : (
        [...previews.data].map(([i, p]) => (
          <p key={i}>
            Push to {name(i)}: {pushSummary(p)}
          </p>
        ))
      )}
      <div className="row">
        <button className="button primary" disabled={!previews.data || previews.isFetching || busy} onClick={onConfirm}>
          Confirm
        </button>
        <button className="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </dialog>
  );
}
