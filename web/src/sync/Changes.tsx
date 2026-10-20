import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useSearchParams } from "react-router";
import { api, ApiError, must } from "../api/client";
import { instancesQuery, useFollowers } from "../api/queries";
import type { components } from "../api/schema";
import { badgeClass } from "../cards/Tile";
import { jobToast } from "../settings/jobUtil";
import { useConfirm } from "../shell/ConfirmDialog";
import { useToast } from "../shell/Toasts";
import { useModal } from "../shell/useModal";
import { PushPreview } from "./PushPreview";
import {
  bulkResolutions,
  changePath,
  changesFilter,
  compareItem,
  dismissBinding,
  editBinding,
  pushSummary,
  queueReviews,
  removeSource,
  reviewable,
  siteOf,
  sourceLabel,
  stateLabel,
  type AppChange,
  type Option,
} from "./sync";
import type { Kind } from "../api/client";
import "./Sync.css";

type ChangeRow = components["schemas"]["ChangeRow"];
type Job = components["schemas"]["JobOut"];

const states = ["differs", "both_changed", "new", "error", "ahead", "missing", "update"] as const;
const rowKey = (r: ChangeRow) =>
  r.card_id ? `card/${r.card_id}`
  : r.lorebook_id ? `lorebook/${r.lorebook_id}`
  : `app/${r.kind}/${r.apps?.[0]?.instance_id}/${r.apps?.[0]?.remote_id}`;
// Card calls leave kind out, as they did before it existed.
const kindField = (kind: Kind) => (kind === "lorebook" ? { kind } : {});
/** entity is the row's card or lorebook ID, 0 for an app item nothing matches. */
const entity = (r: ChangeRow) => r.card_id ?? r.lorebook_id ?? 0;
/** applicable entries are what Apply suggested settles: a review with a suggestion, or a push. */
const applicable = (c: AppChange) => (reviewable(c) && !!c.suggested) || c.state === "ahead";

/** groupBy lists values by key. */
function groupBy<T, K>(xs: T[], key: (x: T) => K): Map<K, T[]> {
  const m = new Map<K, T[]>();
  for (const x of xs) m.set(key(x), [...(m.get(key(x)) ?? []), x]);
  return m;
}

/**
 * Changes lists every card some app needs the owner for, and every card or lorebook whose followed source changed, one
 * row each, and settles the checked ones.
 */
export function Changes() {
  const qc = useQueryClient();
  const toast = useToast();
  const [params, setParams] = useSearchParams();
  const filter = changesFilter(params);
  const { instance_id: instance, state } = filter;
  const changes = useQuery({
    queryKey: ["sync", "changes", instance, state],
    queryFn: async () => must(await api.GET("/sync/changes", { params: { query: { instance_id: instance, state } } })),
  });
  const instances = useQuery(instancesQuery);
  const name = (id: number) => instances.data?.find((i) => i.id === id)?.label ?? `#${id}`;
  const follows = useFollowers();
  // The owner's changes from the defaults.
  const [checked, setChecked] = useState<Record<string, boolean>>({});
  const [pushing, setPushing] = useState<{ kind: Kind; id: number; instance: number }>();
  const [asking, setAsking] = useState(false);

  const rows = changes.data?.rows ?? [];
  const apps = [...new Set(rows.flatMap((r) => (r.apps ?? []).map((c) => c.instance_id)))].sort((a, b) => a - b);
  const isChecked = (r: ChangeRow) =>
    (r.apps ?? []).some(applicable) && // A push overwrites the app's copy, so it waits for the owner to check it.
    (checked[rowKey(r)] ?? (r.apps ?? []).every((c) => applicable(c) && c.state !== "ahead"));
  const chosen = rows.filter(isChecked).flatMap((r) => (r.apps ?? []).filter(applicable).map((c) => ({ kind: r.kind, id: entity(r), c })));
  const reviews = chosen.filter(({ c }) => c.state !== "ahead");
  // One push per app and kind: a push takes cards or lorebooks.
  const pushes = groupBy(chosen.filter(({ c }) => c.state === "ahead"), ({ kind, c }) => `${c.instance_id}/${kind}`);
  const first = queueReviews(rows)[0];

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
    queryKey: ["push-preview", "changes", [...pushes].map(([k, cs]) => [k, cs.map((p) => p.id)])],
    gcTime: 0,
    queryFn: async () =>
      new Map(
        await Promise.all(
          [...pushes].map(async ([k, cs]) => {
            const { kind, c } = cs[0]!;
            const body = { instance_id: c.instance_id, ...kindField(kind), query: { filter: { ids: cs.map((p) => p.id) } } };
            return [k, must(await api.POST("/pushes/preview", { body }))] as const;
          }),
        ),
      ),
  });

  const apply = useMutation({
    mutationFn: async () => {
      const jobs: Job[] = [];
      const resolutions = (
        await Promise.all(
          [...groupBy(reviews, ({ c }) => c.instance_id)].map(([i, cs]) =>
            bulkResolutions(
              i,
              cs.map(({ kind, c }) => ({ kind, item: { remote_id: c.remote_id ?? "", app_hash: c.app_hash ?? "", binding_id: c.binding_id }, c })),
              (r) => r.c.suggested as Option,
              (r, card) =>
                compareItem(i, r.kind, r.item.remote_id, card).catch((e: Error) => {
                  throw new Error(`${r.c.name || r.item.remote_id}: ${e.message}`);
                }),
            ),
          ),
        )
      ).flat();
      if (resolutions.length) jobs.push(must(await api.POST("/resolutions", { body: { resolutions } })));
      for (const [k, cs] of pushes) {
        const { kind, c } = cs[0]!;
        const body = { instance_id: c.instance_id, ...kindField(kind), query: { filter: { ids: cs.map((p) => p.id) } }, confirm: previews.data?.get(k)?.confirm };
        const res = await api.POST("/pushes", { body });
        // A 409 means the copies changed since the preview; show the new one.
        if (res.response.status === 409) void previews.refetch();
        jobs.push(must(res));
      }
      return jobs;
    },
    onSuccess: (jobs) => {
      for (const j of jobs) toast(jobToast(j));
      setAsking(false);
      setChecked({});
      done();
    },
  });

  const [ask, dialog] = useConfirm();
  const unbind = useMutation({
    mutationFn: async (id: number) => must(await api.DELETE("/bindings/{id}", { params: { path: { id } } })),
    onSuccess: done,
  });

  // A followed source has no app copy to push or unbind: its owner ignores the change, stops checking, or drops the source.
  const source = useMutation({
    mutationFn: async ({ r, c, act }: { r: ChangeRow; c: AppChange; act: "dismiss" | "pause" | "remove" }) => {
      const binding = c.binding_id ?? 0;
      if (act === "dismiss") await dismissBinding(binding, c.app_hash ?? "");
      else if (act === "pause") await editBinding(binding, { paused: true });
      else if (r.lorebook_id) await removeSource("lorebook", r.lorebook_id, c.source_id ?? 0);
      else await removeSource("card", r.card_id ?? 0, c.source_id ?? 0);
    },
    onSuccess: done,
    meta: { toast: false },
    onError: (e, { r, c }) => {
      const stale = e instanceof ApiError && e.status === 409;
      if (stale) done();
      toast({ text: stale ? `${r.name} changed on ${siteOf(c.remote_id ?? "")} again. Check the new change.` : e.message, error: true });
    },
  });

  const sourceActions = (r: ChangeRow, c: AppChange) => {
    const button = (text: string, act: "dismiss" | "pause" | "remove") => (
      <button className="button" disabled={source.isPending} onClick={() => source.mutate({ r, c, act })}>
        {text}
      </button>
    );
    switch (c.state) {
      case "update":
        return (
          <>
            <Link to={changePath({ ...c, kind: r.kind, card_id: r.card_id, lorebook_id: r.lorebook_id }, filter)}>Review</Link>{" "}
            {button("Ignore this change", "dismiss")} {button("Ignore all", "pause")}
          </>
        );
      case "missing":
        return (
          <>
            {button("Ignore all", "pause")} {button("Remove source", "remove")}
          </>
        );
      default:
        return <span className="error">{c.error}</span>;
    }
  };

  const actions = (r: ChangeRow, c: AppChange) => {
    if (!c.push) return sourceActions(r, c);
    const where = (r.apps?.length ?? 0) > 1 && <span className="muted"> {name(c.instance_id)}</span>;
    const push = (text: string) => (
      <button className="button" onClick={() => setPushing({ kind: r.kind, id: entity(r), instance: c.instance_id })}>
        {text}
        {where}
      </button>
    );
    const review = (
      <Link to={changePath({ ...c, kind: r.kind, card_id: r.card_id, lorebook_id: r.lorebook_id }, filter)}>
        Review{where}
      </Link>
    );
    switch (c.state) {
      case "differs":
      case "both_changed":
      case "new":
        return review;
      case "ahead":
        return (
          <>
            {review} {push("Push")}
          </>
        );
      case "missing":
        return (
          <>
            {push("Push again")}{" "}
            <button
              className="button"
              disabled={unbind.isPending}
              onClick={() =>
                ask({
                  title: `Unbind ${r.name} from ${name(c.instance_id)}?`,
                  body: <p>The {r.kind} stays in the library, but its push history with this app is forgotten.</p>,
                  confirmLabel: "Unbind",
                  danger: true,
                  onConfirm: () => unbind.mutate(c.binding_id ?? 0),
                })
              }
            >
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
              {follows?.has(i) && " (source)"}
            </option>
          ))}
        </select>
        <select className="input" aria-label="State" value={state ?? ""} onChange={(e) => setFilter("state", e.target.value)}>
          <option value="">All states</option>
          {states.map((s) => (
            <option key={s} value={s}>
              {stateLabel(s)}
            </option>
          ))}
        </select>
        {first && (
          <Link className="button" to={changePath(first, filter)}>
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
                <th>Card or lorebook</th>
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
                  <td>
                    {r.card_id ? (
                      <Link to={`/cards/${r.card_id}`}>{r.name}</Link>
                    ) : r.lorebook_id ? (
                      <Link to={`/lorebooks/${r.lorebook_id}`}>{r.name}</Link>
                    ) : (
                      r.name
                    )}
                  </td>
                  {apps.map((i) => (
                    <td key={i}>
                      {(r.apps ?? [])
                        .filter((c) => c.instance_id === i)
                        .map((c) => (
                          <span key={c.state}>
                            <span className={"badge " + (badgeClass[c.state] ?? "drift")} aria-hidden="true" />{" "}
                            {!c.push ? sourceLabel(c.state, c.remote_id ?? "") : stateLabel(c.state)}
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
          kind={pushing.kind}
          id={pushing.id}
          targets={[{ id: pushing.instance, label: name(pushing.instance) }]}
          onClose={() => {
            setPushing(undefined);
            done();
          }}
        />
      )}
      {dialog}
    </section>
  );
}

/** AskPush shows what pushing the checked cards and lorebooks does to each app's copies before Apply sends anything. */
function AskPush(props: {
  name: (id: number) => string;
  /** previews are by app and kind, as "<instance>/<kind>". */
  previews: { data?: Map<string, components["schemas"]["BulkPreview"]>; error: Error | null; isFetching: boolean };
  busy: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const { name, previews, busy, onConfirm, onClose } = props;
  const modal = useModal(onClose);
  return (
    <dialog {...modal} className="dialog" aria-label="Apply suggested">
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
        [...previews.data].map(([k, p]) => {
          const [i, kind] = k.split("/");
          return (
            <p key={k}>
              Push {kind === "lorebook" ? "lorebooks " : ""}to {name(Number(i))}: {pushSummary(p)}
            </p>
          );
        })
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
