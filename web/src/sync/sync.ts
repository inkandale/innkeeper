import { api, must, mustOk, route, type Kind } from "../api/client";
import type { components } from "../api/schema";

export type Comparison = components["schemas"]["Comparison"];
export type Resolution = components["schemas"]["Resolution"];
type Hunk = components["schemas"]["Hunk"];
type TargetCount = components["schemas"]["TargetCount"];
type BulkPreview = components["schemas"]["BulkPreview"];
export type AppChange = components["schemas"]["AppChange"];

// The snapshot is a sync job's progress, which the schema leaves
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
  auto_imported?: boolean;
};
export type Snapshot = {
  params: { instance_id: number };
  items: Item[] | null;
  lorebooks: Item[] | null;
  missing: number[] | null;
  unpushed: number[] | null;
  totals: Record<string, number> | null;
};

export type Row = { kind: Kind; item: Item };

/** snapshotOf is a finished sync's snapshot, or undefined while it runs or when a job list cut it to totals. */
export function snapshotOf(job: { status: string; progress: unknown } | undefined): Snapshot | undefined {
  const p = job?.status === "succeeded" ? job.progress : undefined;
  return p && typeof p === "object" && "params" in p ? (p as Snapshot) : undefined;
}

/** rows lists a snapshot's characters, then its world books no character links to. */
export function rows(s: Snapshot): Row[] {
  return [
    ...(s.items ?? []).map((item) => ({ kind: "card" as const, item })),
    ...(s.lorebooks ?? []).map((item) => ({ kind: "lorebook" as const, item })),
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
    ...(cmp.base === "none" && cmp.accepted_hash ? { base: "none" as const } : {}),
    default: "library",
    ...(Object.keys(picked).length ? { choices: picked } : {}),
  };
}

/** bulkResolutions resolves rows as picked. The snapshot has no library head, so each bind is compared first. */
export async function bulkResolutions<R extends { kind: Kind; item: Pick<Item, "remote_id" | "app_hash" | "binding_id"> }>(
  instance: number,
  picked: R[],
  pick: (r: R) => Option,
  compare: (r: R, id: number) => Promise<Comparison>,
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
        ...(r.kind === "lorebook" ? { kind: "lorebook" as const } : {}),
      };
    });
    out.push(...(await Promise.all(batch)));
  }
  return out;
}

export async function compareItem(instance: number, kind: Kind, remoteId: string, id: number, twoWay = false): Promise<Comparison> {
  const path = { id: instance, remote_id: remoteId };
  return kind === "card"
    ? must(
        await api.GET("/instances/{id}/cards/{remote_id}/compare", {
          params: { path, query: { card_id: id, ...(twoWay ? { base: "none" as const } : {}) } },
        }),
      )
    : must(await api.GET("/instances/{id}/lorebooks/{remote_id}/compare", { params: { path, query: { lorebook_id: id } } }));
}

export async function resolveItem(instance: number, kind: Kind, remoteId: string, body: Resolution) {
  return must(
    await api.POST(route(kind, "/instances/{id}/cards/{remote_id}/resolve"), { params: { path: { id: instance, remote_id: remoteId } }, body }),
  );
}

/** query is a URL query string of the params that are set. */
const query = (params: Record<string, number | string | undefined>) =>
  new URLSearchParams(Object.entries(params).flatMap(([k, v]) => (v === undefined ? [] : [[k, String(v)]]))).toString();

export function reviewPath(
  instance: number,
  kind: Kind,
  remoteId: string,
  params: Record<string, number | string | undefined> = {},
): string {
  const q = query(params);
  return `/sync/${instance}/${kind}/${encodeURIComponent(remoteId)}${q ? "?" + q : ""}`;
}

/** reviewable changes open Review; the others are pushed again, unbound, ignored, or only shown. */
export const reviewable = (c: AppChange) =>
  c.state === "differs" || c.state === "both_changed" || c.state === "new" || c.state === "ahead" || c.state === "update";

/** QueueChange is a queue entry with its row's card or lorebook, which Review compares with when there's no snapshot. */
type QueueChange = AppChange & { card_id?: number; lorebook_id?: number };

/** queueReviews lists the queue's reviewable entries in order. */
export const queueReviews = (rows: { card_id?: number; lorebook_id?: number; apps: AppChange[] | null }[]): QueueChange[] =>
  rows.flatMap((r) => (r.apps ?? []).filter(reviewable).map((c) => ({ ...c, card_id: r.card_id, lorebook_id: r.lorebook_id })));

/** stateLabel names a binding or queue state from the library's side. */
export const stateLabel = (s: string) =>
  ({ ahead: "not pushed", remote_changed: "changed in app", diverged: "changed in both", both_changed: "changed in both" })[s] ??
  s.replaceAll("_", " ");

/** ChangesFilter narrows the changes queue, as GET /sync/changes takes it. */
type ChangesFilter = { instance_id?: number; state?: AppChange["state"] };

/** changesFilter reads the queue's filter from a URL's search params. */
export const changesFilter = (p: URLSearchParams): ChangesFilter => ({
  instance_id: Number(p.get("instance_id")) || undefined,
  state: (p.get("state") || undefined) as AppChange["state"] | undefined,
});

/** changesPath is the changes queue, filtered. */
export function changesPath(f: ChangesFilter = {}): string {
  const q = query(f);
  return `/sync/changes${q ? "?" + q : ""}`;
}

/**
 * changePath reviews a change from the queue, so Review steps on to the next one within the queue's filter. A change
 * not pushed has no snapshot, so it compares with its card, and without the accepted copy: a bind accepts the app's
 * copy, which would keep the library's side of every difference unasked. A source update compares three ways with its
 * card or lorebook.
 */
export const changePath = (c: QueueChange, f: ChangesFilter = {}) =>
  c.state === "update"
    ? reviewPath(c.instance_id, c.lorebook_id ? "lorebook" : "card", c.remote_id ?? "", { card: c.lorebook_id ?? c.card_id, queue: 1, ...f })
    : reviewPath(
        c.instance_id,
        "card",
        c.remote_id ?? "",
        c.state === "ahead" ? { card: c.card_id, base: "none", queue: 1, ...f } : { job: c.job_id ?? 0, queue: 1, ...f },
      );

/** needsYou counts an app's changes that need a decision, from GET /sync/changes counts. */
export const needsYou = (c: Record<string, number> = {}) => (c.differs ?? 0) + (c.both_changed ?? 0) + (c.error ?? 0) + (c.new ?? 0);

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

/** pushSummary says what a query push does to the app's copies. */
export function pushSummary(c: BulkPreview): string {
  const parts: [number, string][] = [
    [c.new, "new"],
    [c.ahead, "not pushed"],
    [c.in_sync, "in sync"],
    [c.cant_check, "can't check"],
    [c.drifted, "drifted (pushing overwrites their app edits)"],
  ];
  return parts
    .filter(([n]) => n > 0)
    .map(([n, s]) => `${n} ${s}`)
    .join(", ");
}

export type Run = { op: "same" | "del" | "ins"; text: string };

// Each word keeps its trailing whitespace, as the server's words() does, so runs rejoin to the input.
const words = (s: string) => s.match(/\s*\S+\s*/g) ?? (s ? [s] : []);

/** wordDiff lines up two texts word by word: same runs, then what only the library (del) or the app (ins) has. */
export function wordDiff(library: string, app: string): Run[] {
  const a = words(library);
  const b = words(app);
  const w = b.length + 1;
  // ponytail: O(n·m) LCS table; fine for one hunk, switch to Myers if hunks grow to whole fields.
  const lcs = new Int32Array((a.length + 1) * w);
  const at = (i: number, j: number) => lcs[i * w + j] ?? 0;
  for (let i = a.length - 1; i >= 0; i--)
    for (let j = b.length - 1; j >= 0; j--)
      lcs[i * w + j] = a[i] === b[j] ? at(i + 1, j + 1) + 1 : Math.max(at(i + 1, j), at(i, j + 1));
  const out: Run[] = [];
  const push = (op: Run["op"], text: string) => {
    const last = out.at(-1);
    if (last?.op === op) last.text += text;
    else out.push({ op, text });
  };
  let i = 0;
  let j = 0;
  while (i < a.length || j < b.length) {
    if (i < a.length && j < b.length && a[i] === b[j]) {
      push("same", a[i++] ?? "");
      j++;
    } else if (j >= b.length || (i < a.length && at(i + 1, j) >= at(i, j + 1))) push("del", a[i++] ?? "");
    else push("ins", b[j++] ?? "");
  }
  return out;
}

/** sentenceCount counts sentences the way the server's sentences() splits them: after a terminator or a line break. */
export const sentenceCount = (s: string) =>
  s.split(/(?<=[.!?…]["'”’*)]*\s+|\n\s*)(?=\S)/).filter((p) => p.trim()).length;

/** jsonKey is a JSON field hunk's key path, without the field's own path or the hunk number. */
export const jsonKey = (path: string, id: string) => id.replace(/#\d+$/, "").slice(path.length + 1);

/** sourceRemoteId is the remote ID of a followed source's binding: its kind and ref, as the core forms it. */
export const sourceRemoteId = (s: { kind: string; ref: string }) => `${s.kind}:${s.ref}`;

/** siteOf names a followed source's site: the kind before the remote ID's colon. */
export const siteOf = (remoteId: string) => {
  const i = remoteId.indexOf(":");
  return i > 0 ? remoteId.slice(0, i) : remoteId;
};

/** sourceLabel names a followed source's queue state or binding remote_state. */
export function sourceLabel(state: string, remoteId: string): string {
  const site = siteOf(remoteId);
  const labels: Record<string, string> = {
    update: `update on ${site}`,
    changed: `update on ${site}`,
    missing: `no longer on ${site}`,
    error: "check failing",
    unchanged: "up to date",
    unknown: "not checked yet",
  };
  return labels[state] ?? state;
}

/** BindingEdit is what PATCH /bindings/{id} changes; a field left out stays as it is. Only a followed source pauses. */
export type BindingEdit = { autosync?: boolean; paused?: boolean };

export async function editBinding(id: number, body: BindingEdit) {
  return must(await api.PATCH("/bindings/{id}", { params: { path: { id } }, body }));
}

/**
 * dismissBinding ignores a followed source's change, the one with appHash; the next one on the site shows again. The
 * API answers 409 with a fresh comparison if the site's data changed since.
 */
export async function dismissBinding(id: number, appHash: string) {
  return must(await api.POST("/bindings/{id}/dismiss", { params: { path: { id } }, body: { app_hash: appHash } }));
}

/** removeSource removes a card's or lorebook's source, as a queue entry's source_id names it; the server unbinds it at once. */
export async function removeSource(kind: Kind, id: number, sourceId: number) {
  mustOk(await api.DELETE(route(kind, "/cards/{id}/sources/{source_id}"), { params: { path: { id, source_id: sourceId } } }));
}
