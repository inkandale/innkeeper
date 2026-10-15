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
 * pairEntries matches the entries in a with those in b: by id, then identical entries, then the same keys. Entries
 * with two different ids never pair. It returns index pairs in a's order, then b's unpaired ones; -1 is no partner.
 * Items that aren't entries are left out.
 */
export function pairEntries(a: unknown[], b: unknown[]): [number, number][] {
  const partner = new Map<number, number>();
  const taken = new Set<number>();
  const pass = (match: (x: Entry, y: Entry) => boolean) =>
    a.forEach((x, i) => {
      if (!isEntry(x) || partner.has(i)) return;
      const j = b.findIndex((y, j) => isEntry(y) && !taken.has(j) && !(hasId(x) && hasId(y) && !same(x.id, y.id)) && match(x, y));
      if (j < 0) return;
      partner.set(i, j);
      taken.add(j);
    });
  pass((x, y) => hasId(x) && same(x.id, y.id));
  pass(same);
  pass((x, y) => same(strings(x.keys), strings(y.keys)));
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
        label: `Entry ${strings((y ?? x)!.keys).join(", ") || "(no keys)"}`,
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
