import { same, strings, text, type Data } from "../card/draft";
import { changedKeys } from "../forge/actions";
import { fieldChanges, fieldRows, type Row, type Subject } from "../forge/subject";
import { isEntry, type Entry } from "./entries";

const labels: Record<string, string> = {
  name: "Name",
  description: "Description",
  scan_depth: "Scan depth",
  token_budget: "Token budget",
  recursive_scanning: "Recursive scanning",
  entries: "Entries",
};

const label = (key: string) => labels[key] ?? key;

const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);

const hasId = (e: Entry) => e.id !== undefined && e.id !== null;

/**
 * pairEntries matches the entries in a with those in b: by id, then identical entries, then the same keys. An id or
 * key list pairs only when it is unique among the unpaired entries on both sides, and entries with two different ids
 * never pair. It returns index pairs in a's order, then b's unpaired ones; -1 is no partner. Items that aren't entries
 * are left out.
 */
export function pairEntries(a: unknown[], b: unknown[]): [number, number][] {
  const partner = new Map<number, number>();
  const taken = new Set<number>();
  const free = (list: unknown[], used: (i: number) => boolean) => list.flatMap((e, i) => (isEntry(e) && !used(i) ? [i] : []));
  const clash = (x: Entry, y: Entry) => hasId(x) && hasId(y) && !same(x.id, y.id);
  const link = (i: number, j: number) => {
    partner.set(i, j);
    taken.add(j);
  };
  // byIdentity pairs entries whose identity, when they have one, appears once among each side's unpaired entries.
  const byIdentity = (identity: (e: Entry) => string | undefined) => {
    const [xs, ys] = [free(a, (i) => partner.has(i)), free(b, (j) => taken.has(j))];
    const count = (list: unknown[], is: number[]) => {
      const n = new Map<string, number>();
      for (const i of is) {
        const k = identity(list[i] as Entry);
        if (k !== undefined) n.set(k, (n.get(k) ?? 0) + 1);
      }
      return n;
    };
    const [na, nb] = [count(a, xs), count(b, ys)];
    for (const i of xs) {
      const k = identity(a[i] as Entry);
      if (k === undefined || na.get(k) !== 1 || nb.get(k) !== 1) continue;
      const j = ys.find((j) => identity(b[j] as Entry) === k)!;
      if (!clash(a[i] as Entry, b[j] as Entry)) link(i, j);
    }
  };
  byIdentity((e) => (hasId(e) ? JSON.stringify(e.id) : undefined));
  // Identical entries are interchangeable, so the first free match will do.
  for (const i of free(a, (i) => partner.has(i))) {
    const j = free(b, (j) => taken.has(j)).find((j) => same(a[i], b[j]));
    if (j !== undefined) link(i, j);
  }
  byIdentity((e) => JSON.stringify(strings(e.keys)));
  const out: [number, number][] = a.flatMap((x, i) => (isEntry(x) ? [[i, partner.get(i) ?? -1] as [number, number]] : []));
  b.forEach((y, j) => isEntry(y) && !taken.has(j) && out.push([-1, j]));
  return out;
}

const shown = new Set(["id", "keys", "secondary_keys", "content"]);

// entryText shows an entry for review: its keys and content, then each of the other properties named in others.
function entryText(e: Entry, others: string[]): string {
  const secondary = strings(e.secondary_keys);
  return [
    `Keys: ${strings(e.keys).join(", ")}${secondary.length ? `\nSecondary keys: ${secondary.join(", ")}` : ""}`,
    text(e.content),
    ...(others.length ? [others.map((k) => `${k}: ${JSON.stringify(e[k])}`).join("\n")] : []),
  ].join("\n\n");
}

/** lorebookSubject reviews a lorebook proposal with a row per changed lorebook field and per changed entry. */
export const lorebookSubject: Subject = {
  kind: "lorebook",
  label,
  jobFilter: (id) => ({ lorebook_id: id }),
  placeholder: "Reforge this lorebook… e.g. add entries for the northern clans · @cards/Billy is their chief",
  review: (start, draft, proposal) => {
    const keys = changedKeys(start, proposal).filter((k) => k !== "entries");
    const [before, after, mine] = [list(start.entries), list(proposal.entries), list(draft.entries)];
    const inDraft = new Map(pairEntries(before, mine));
    const rows: (Row & { s: number; p: number })[] = [];
    for (const [s, p] of pairEntries(before, after)) {
      const [x, y] = [before[s] as Entry | undefined, after[p] as Entry | undefined];
      if (x && y && same(x, y)) continue;
      const d = s < 0 ? -1 : (inDraft.get(s) ?? -1);
      const ours = mine[d] as Entry | undefined;
      const others = [...new Set([...Object.keys(x ?? {}), ...Object.keys(y ?? {})])].filter((k) => !shown.has(k) && !same(x?.[k], y?.[k])).sort();
      rows.push({
        key: `entries/${s}/${p}`,
        // The position is the entry's in the proposal, or in the start for a removed one.
        label: `Entry ${(y ? p : s) + 1} · ${strings((y ?? x)!.keys).join(", ") || "(no keys)"}`,
        change: !x ? "added" : !y ? "removed" : "changed",
        mine: ours && entryText(ours, others),
        before: x ? entryText(x, others) : "",
        after: y && entryText(y, others),
        edited: !!x && !same(x, ours),
        s,
        p,
      });
    }
    const apply = (taken: Set<string>): Data => {
      const changes = fieldChanges(proposal, keys, taken);
      const chosen = rows.filter((r) => taken.has(r.key));
      if (!chosen.length) return changes;
      const out = [...mine];
      const drop = new Set<number>();
      const added: unknown[] = [];
      for (const r of chosen) {
        const d = r.s < 0 ? -1 : (inDraft.get(r.s) ?? -1);
        if (r.change === "removed") drop.add(d);
        else if (d >= 0) out[d] = after[r.p];
        else added.push(after[r.p]);
      }
      return { ...changes, entries: [...out.filter((_, i) => !drop.has(i)), ...added] };
    };
    return { rows: [...fieldRows(start, draft, proposal, label, keys), ...rows.map(({ s: _s, p: _p, ...r }) => r)], apply };
  },
};
