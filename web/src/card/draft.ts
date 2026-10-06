export type Data = Record<string, unknown>;

/** Draft is unsaved edits on top of the revision they started from. */
export type Draft = { baseId: number; base: Data; data: Data };

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

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

/** show prints a field value for reading. */
export const show = (v: unknown) => (v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 2));
