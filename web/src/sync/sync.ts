import { api, must } from "../api/client";
import type { components } from "../api/schema";

export type Comparison = components["schemas"]["Comparison"];
export type Resolution = components["schemas"]["Resolution"];
type Hunk = components["schemas"]["Hunk"];
type TargetCount = components["schemas"]["TargetCount"];

// The snapshot is a reconcile job's progress, which the schema leaves
// untyped. These match Snapshot in internal/targets/reconcile.go.
export type Option = { action: "bind" | "import" | "skip"; card_id?: number; why?: string };
export type Candidate = {
  card_id: number;
  name: string;
  reasons: Record<string, number> | null;
  differing_fields: number;
  conflicts: number;
  bound_to?: string;
};
export type Item = {
  remote_id: string;
  name: string;
  app_hash: string;
  verdict: "bound" | "exact" | "likely" | "new" | "error";
  error?: string;
  binding_id?: number;
  candidates?: Candidate[];
  options: Option[] | null;
  suggested?: Option;
  lorebooks?: Item[];
  auto_bound?: boolean;
};
export type Snapshot = {
  params: { instance_id: number };
  items: Item[] | null;
  lorebooks: Item[] | null;
  missing: number[] | null;
  unpushed: number[] | null;
  totals: Record<string, number> | null;
};

/** ItemKind is an app item's path segment in the API. */
export type ItemKind = "items" | "lorebook-items";
export type Row = { kind: ItemKind; item: Item };

/** snapshotOf is a finished reconcile's snapshot, or undefined while it runs or when a job list cut it to totals. */
export function snapshotOf(job: { status: string; progress: unknown } | undefined): Snapshot | undefined {
  const p = job?.status === "succeeded" ? job.progress : undefined;
  return p && typeof p === "object" && "params" in p ? (p as Snapshot) : undefined;
}

/** rows lists a snapshot's characters, then its world books no character links to. */
export function rows(s: Snapshot): Row[] {
  return [
    ...(s.items ?? []).map((item) => ({ kind: "items" as const, item })),
    ...(s.lorebooks ?? []).map((item) => ({ kind: "lorebook-items" as const, item })),
  ];
}

export const rowKey = (r: Row) => `${r.kind}/${r.item.remote_id}`;

export const optionKey = (o: Option) => (o.action === "bind" ? `bind:${o.card_id}` : o.action);

export function optionLabel(o: Option, candidates: Candidate[] = []): string {
  if (o.action === "import") return "Import as new";
  if (o.action === "skip") return "Skip";
  return `Bind to ${candidates.find((c) => c.card_id === o.card_id)?.name ?? "#" + o.card_id}`;
}

/** decidable hunks take the owner's choice; the others merge by their status. */
export const decidable = (h: Hunk) => h.status === "conflict" || h.status === "library_only" || h.status === "app_only";

/**
 * resolution decides one app item. A bind carries its comparison's hashes and an import the binding the owner saw,
 * so the API refuses either if anything changed since.
 */
export function resolution(
  remoteId: string,
  appHash: string,
  action: Option["action"],
  cmp?: Comparison,
  choices: Record<string, string> = {},
  bindingId = 0,
): Resolution {
  if (action === "import") return { remote_id: remoteId, app_hash: appHash, action, binding_id: bindingId };
  if (action !== "bind") return { remote_id: remoteId, app_hash: appHash, action };
  if (!cmp) throw new Error(`compare ${remoteId} before binding it`);
  const picked = Object.fromEntries(Object.entries(choices).filter(([, c]) => c !== "library"));
  return {
    remote_id: remoteId,
    action,
    card_id: cmp.card_id,
    app_hash: cmp.app_hash,
    base_revision_id: cmp.head_revision_id,
    accepted_hash: cmp.accepted_hash,
    default: "library",
    ...(Object.keys(picked).length ? { choices: picked } : {}),
  };
}

/** bulkResolutions resolves rows as picked. The snapshot has no library head, so each bind is compared first. */
export async function bulkResolutions(
  instance: number,
  picked: Row[],
  pick: (r: Row) => Option,
  compare: (r: Row, id: number) => Promise<Comparison>,
): Promise<Resolution[]> {
  const out: Resolution[] = [];
  // ponytail: four comparisons at a time, each reading the app; batch on the server if a few hundred binds is too slow.
  for (let i = 0; i < picked.length; i += 4) {
    const batch = picked.slice(i, i + 4).map(async (r) => {
      const o = pick(r);
      const cmp = o.action === "bind" ? await compare(r, o.card_id ?? 0) : undefined;
      return {
        ...resolution(r.item.remote_id, r.item.app_hash, o.action, cmp, {}, r.item.binding_id ?? 0),
        // The run's hash, so an item that changed after the owner saw it goes stale.
        app_hash: r.item.app_hash,
        instance_id: instance,
        ...(r.kind === "lorebook-items" ? { kind: "lorebook" as const } : {}),
      };
    });
    out.push(...(await Promise.all(batch)));
  }
  return out;
}

export async function compareItem(instance: number, kind: ItemKind, remoteId: string, id: number): Promise<Comparison> {
  const path = { id: instance, remote_id: remoteId };
  return kind === "items"
    ? must(await api.GET("/instances/{id}/items/{remote_id}/compare", { params: { path, query: { card_id: id } } }))
    : must(await api.GET("/instances/{id}/lorebook-items/{remote_id}/compare", { params: { path, query: { lorebook_id: id } } }));
}

export async function resolveItem(instance: number, kind: ItemKind, remoteId: string, body: Resolution) {
  const path = { id: instance, remote_id: remoteId };
  return kind === "items"
    ? must(await api.POST("/instances/{id}/items/{remote_id}/resolve", { params: { path }, body }))
    : must(await api.POST("/instances/{id}/lorebook-items/{remote_id}/resolve", { params: { path }, body }));
}

export function reviewPath(instance: number, kind: ItemKind, remoteId: string, params: Record<string, number> = {}): string {
  const q = new URLSearchParams(Object.entries(params).map(([k, v]) => [k, String(v)])).toString();
  return `/sync/${instance}/${kind}/${encodeURIComponent(remoteId)}${q ? "?" + q : ""}`;
}

export type Counts = Record<"in_sync" | "ahead" | "drifted" | "missing" | "cant_check" | "bound", number>;

/** stateCounts reads one instance's cards by status from a target facet. Drifted is remote_changed plus diverged. */
export function stateCounts(targets: TargetCount[] | null | undefined, instance: number): Counts {
  const c: Counts = { in_sync: 0, ahead: 0, drifted: 0, missing: 0, cant_check: 0, bound: 0 };
  for (const t of targets ?? []) {
    if (t.instance !== instance) continue;
    const k = t.is === "remote_changed" || t.is === "diverged" ? "drifted" : t.is;
    if (k in c && k !== "bound") c[k as keyof Counts] += t.cards;
    c.bound += t.cards;
  }
  return c;
}

/** pushSummary says what pushing total cards does to the app's copies. Unbound and missing ones become new copies. */
export function pushSummary(total: number, c: Counts): string {
  const parts: [number, string][] = [
    [total - c.bound + c.missing, "new"],
    [c.ahead, "ahead"],
    [c.in_sync, "in sync"],
    [c.cant_check, "can't check"],
    [c.drifted, "drifted (pushing overwrites their app edits)"],
  ];
  return parts
    .filter(([n]) => n > 0)
    .map(([n, s]) => `${n} ${s}`)
    .join(", ");
}
