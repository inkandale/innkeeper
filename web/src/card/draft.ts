export type Data = Record<string, unknown>;

/** Draft is unsaved edits on top of the revision they started from. */
export type Draft = { baseId: number; base: Data; data: Data };

/** nsfw reports whether tags, a card's tags field, has an NSFW tag in any case. */
export const nsfw = (tags: unknown) => Array.isArray(tags) && tags.some((t) => typeof t === "string" && t.toLowerCase() === "nsfw");

/** sorted is v with its object keys in order, so key order doesn't count; jsonb reorders them. */
const sorted = (_: string, v: unknown) =>
  v && typeof v === "object" && !Array.isArray(v) ? Object.fromEntries(Object.entries(v).sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) : v;

/** same compares two JSON values by content. */
export const same = (a: unknown, b: unknown) => JSON.stringify(a, sorted) === JSON.stringify(b, sorted);

/** changed lists the fields whose edited value differs from the base revision's. */
export function changed(d: Draft | null): Set<string> {
  const out = new Set<string>();
  if (!d) return out;
  for (const k of new Set([...Object.keys(d.base), ...Object.keys(d.data)])) if (!same(d.base[k], d.data[k])) out.add(k);
  return out;
}

/** rebase moves a draft onto a newer revision, keeping only the fields the draft changed. */
export function rebase(d: Draft, baseId: number, base: Data): Draft {
  const data = { ...base };
  for (const k of changed(d)) {
    if (k in d.data) data[k] = d.data[k];
    else delete data[k];
  }
  return { baseId, base, data };
}

/** clashes lists the fields both the draft and latest changed, to different values. */
export function clashes(d: Draft, latest: Data): string[] {
  return [...changed(d)].filter((k) => !same(latest[k], d.base[k]) && !same(latest[k], d.data[k])).sort();
}

/** tokens estimates a text's token count. The UI labels it with "~". */
export const tokens = (s: string) => Math.ceil(s.length / 4);

/** text returns v if it's a string, or "". */
export const text = (v: unknown) => (typeof v === "string" ? v : "");

/** strings returns the strings in v, or none. */
export const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((s): s is string => typeof s === "string") : []);

/** show prints a field value for reading. */
export const show = (v: unknown) => (v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 2));
