import { strings } from "../card/draft";

export type Entry = Record<string, unknown>;

/** isEntry says whether v is an entry object. */
export const isEntry = (v: unknown): v is Entry => typeof v === "object" && v !== null && !Array.isArray(v);

/** asEntries returns the entry objects in v, or none. */
export const asEntries = (v: unknown): Entry[] => (Array.isArray(v) ? v.filter(isEntry) : []);

/** firstLine is an entry's first line of content, for its collapsed row. */
export function firstLine(e: Entry): string {
  const line = (typeof e.content === "string" ? e.content : "").split("\n")[0] ?? "";
  return line.length > 80 ? line.slice(0, 80) + "…" : line;
}

/** matches says whether an entry's keys, secondary keys, comment, or content contain q, ignoring case. */
export function matches(e: Entry, q: string): boolean {
  const needle = q.toLowerCase();
  const hay = [...strings(e.keys), ...strings(e.secondary_keys), e.comment, e.content];
  return hay.some((s) => typeof s === "string" && s.toLowerCase().includes(needle));
}

/** moved returns list with the item at from moved to index to. */
export function moved<T>(list: T[], from: number, to: number): T[] {
  const out = [...list];
  const [item] = out.splice(from, 1);
  if (item !== undefined) out.splice(to, 0, item);
  return out;
}

/** blankEntry is a new entry with the fields CCv3 requires. */
export const blankEntry = (): Entry => ({
  keys: [],
  content: "",
  enabled: true,
  insertion_order: 100,
  position: "before_char",
  extensions: {},
});
