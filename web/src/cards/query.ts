import type { components } from "../api/schema";
import { libraryParam } from "../shell/library";

type Filter = components["schemas"]["Filter"];
type Search = components["schemas"]["Query"];
type TargetIs = components["schemas"]["TargetState"]["is"];

// Typed from the schema so a new state fails the build here.
export const targetStates: TargetIs[] = ["in_sync", "ahead", "remote_changed", "diverged", "missing", "cant_check"];
/** facetKinds are the terms with a sidebar section, in sidebar order. */
const facetKinds = ["target", "state", "tag", "source", "collection"] as const;
export type FacetKind = (typeof facetKinds)[number];
/** kinds are the filter terms; sectionsFor lists the ones with a sidebar section, and creator and lorebook arrive only by link. score filters on enrichment numbers, value on text and list enrichments. */
const kinds = [...facetKinds, "creator", "lorebook", "score", "value"] as const;
const sorts = ["relevance", "updated", "name", "created"] as const;
const enrichmentSort = /^enrichment:(?:[1-9]\d*:)?[a-z][a-z0-9_]*$/;
// Score and value terms name their key as instance:key; a bare grid key reads the grid score.
const ref = String.raw`(?:([1-9]\d*):)?([a-z][a-z0-9_]*)`;
// A score term's value: key:low..high, either end optional, or key:none for cards without a value.
const num = String.raw`-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?`;
const scoreValue = new RegExp(String.raw`^${ref}:(none|(${num})?\.\.(${num})?)$`);
// A value term's value: key=value.
const valueTerm = new RegExp(String.raw`^${ref}=(.+)$`);
export const sizes = [24, 48, 96];

export type TermKind = (typeof kinds)[number];
/** Term is one filter: a section's value, or its negation. */
export type Term = { kind: TermKind; value: string; not: boolean };
export type Sort = (typeof sorts)[number] | `enrichment:${string}`;
export type CardQuery = {
  library: number;
  text: string;
  /** semantic ranks by meaning instead of matching words. It needs text and an embed instance. */
  semantic: boolean;
  terms: Term[];
  sort: Sort;
  /** reverse flips the sort; cards without the value sorted by stay last. */
  reverse: boolean;
  page: number;
  size: number;
  view: "grid" | "list";
  sharesSource: boolean;
  /** all names the sections, as sectionOf names them, whose included values must all match rather than any. */
  all: string[];
};

export const defaultSort = (text: string): Sort => (text ? "relevance" : "updated");

/** sectionsFor lists the sidebar sections a kind of entity has; lorebooks have no tags or collections. */
export const sectionsFor = (entity: "card" | "lorebook"): FacetKind[] =>
  entity === "card" ? [...facetKinds] : ["target", "state", "source"];

/** matchesAll says whether a section can switch between any and all: a card has one state, and targets go with states. */
export const matchesAll = (section: string) => section !== "target" && section !== "state" && section !== "score";

/** keyRef names an enrichment key as terms, sorts, and facets do: instance:key, or a grid key alone for the grid score. */
export const keyRef = (instance: number | undefined, key: string) => (instance ? `${instance}:${key}` : key);

/** Score is a score term's value: a range, or none for cards without a value. */
export type Score = { instance?: number; key: string; gte?: number; lte?: number; none?: boolean };

/** parseScore reads a score term's value, or returns undefined if it is malformed. */
export function parseScore(value: string): Score | undefined {
  const m = scoreValue.exec(value);
  if (!m) return undefined;
  const at = { ...(m[1] !== undefined && { instance: Number(m[1]) }), key: m[2]! };
  if (m[3] === "none") return { ...at, none: true };
  if (m[4] === undefined && m[5] === undefined) return undefined;
  return { ...at, ...(m[4] !== undefined && { gte: Number(m[4]) }), ...(m[5] !== undefined && { lte: Number(m[5]) }) };
}

/** scoreTerm is the term for a score, or undefined for one with no condition. */
export function scoreTerm(s: Score): Term | undefined {
  const k = keyRef(s.instance, s.key);
  if (s.none) return { kind: "score", value: `${k}:none`, not: false };
  if (s.gte === undefined && s.lte === undefined) return undefined;
  return { kind: "score", value: `${k}:${s.gte ?? ""}..${s.lte ?? ""}`, not: false };
}

/** enrichmentValue reads a value term's key and value, or returns undefined if it is malformed. */
/** valueLabel shows a plugin's code, such as no_char_macro, as words: "No char macro". Other values show as they are. */
export function valueLabel(value: string): string {
  return /^[a-z0-9_]+$/.test(value) ? value[0]!.toUpperCase() + value.slice(1).replaceAll("_", " ") : value;
}

export function enrichmentValue(value: string): { instance?: number; key: string; value: string } | undefined {
  const m = valueTerm.exec(value);
  return m ? { ...(m[1] !== undefined && { instance: Number(m[1]) }), key: m[2]!, value: m[3]! } : undefined;
}

/**
 * withoutUnnamed drops the score and value terms and enrichment sort that name no instance, from older links,
 * unless they name one of gridKeys, which the core reads as the grid score. It returns q itself when nothing changes.
 */
export function withoutUnnamed(q: CardQuery, gridKeys: string[]): CardQuery {
  const kept = (ref?: { instance?: number; key: string }) => !ref || ref.instance !== undefined || gridKeys.includes(ref.key);
  const terms = q.terms.filter((t) => kept(t.kind === "score" ? parseScore(t.value) : t.kind === "value" ? enrichmentValue(t.value) : undefined));
  const key = q.sort.startsWith("enrichment:") ? q.sort.slice("enrichment:".length) : undefined;
  const sort = key && !key.includes(":") && !gridKeys.includes(key) ? defaultSort(q.text) : q.sort;
  return terms.length !== q.terms.length || sort !== q.sort ? { ...q, terms, sort } : q;
}

/** setScore replaces the score term for s's instance and key with s's. */
export function setScore(q: CardQuery, s: Score): CardQuery {
  const t = scoreTerm(s);
  const same = (x: Term) => {
    const p = x.kind === "score" ? parseScore(x.value) : undefined;
    return p?.key === s.key && p.instance === s.instance;
  };
  const rest = q.terms.filter((x) => !same(x));
  return { ...q, terms: t ? [...rest, t] : rest, page: 1 };
}

const id = (v: string) => /^[1-9]\d*$/.test(v);

/** valid says whether value can be a term of kind. */
function valid(kind: TermKind, value: string): boolean {
  if (kind === "target" || kind === "collection" || kind === "lorebook") return id(value);
  if (kind === "state") return targetStates.some((s) => s === value);
  if (kind === "score") return parseScore(value) !== undefined;
  if (kind === "value") return enrichmentValue(value) !== undefined;
  return value !== "";
}

/** parseQuery reads the Cards page state from its URL, ignoring anything malformed; library is used when the URL names none. */
export function parseQuery(p: URLSearchParams, library: number): CardQuery {
  const terms: Term[] = [];
  for (const raw of p.getAll("f")) {
    const not = raw.startsWith("-");
    const s = not ? raw.slice(1) : raw;
    const i = s.indexOf(":");
    const kind = s.slice(0, i) as TermKind;
    const value = s.slice(i + 1);
    if (i > 0 && kinds.includes(kind) && valid(kind, value)) terms.push({ kind, value, not });
  }
  const text = p.get("q") ?? "";
  const sort = p.get("sort") as Sort;
  const page = Number(p.get("page"));
  const size = Number(p.get("size"));
  return {
    library: libraryParam(p) ?? library,
    text,
    semantic: p.get("semantic") === "1",
    terms,
    sort: sorts.some((s) => s === sort) || enrichmentSort.test(sort ?? "") ? sort : defaultSort(text),
    reverse: p.get("rev") === "1",
    page: Number.isInteger(page) && page > 0 ? page : 1,
    size: sizes.includes(size) ? size : 48,
    view: p.get("view") === "list" ? "list" : "grid",
    sharesSource: p.get("shared") === "1",
    all: p.getAll("all").filter(matchesAll),
  };
}

/** toParams writes the state back, leaving defaults out but always naming the library. */
export function toParams(q: CardQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (q.text) p.set("q", q.text);
  if (q.semantic) p.set("semantic", "1");
  for (const t of q.terms) p.append("f", `${t.not ? "-" : ""}${t.kind}:${t.value}`);
  if (q.sort !== defaultSort(q.text)) p.set("sort", q.sort);
  if (q.reverse) p.set("rev", "1");
  if (q.page !== 1) p.set("page", String(q.page));
  if (q.size !== 48) p.set("size", String(q.size));
  if (q.view !== "grid") p.set("view", q.view);
  if (q.sharesSource) p.set("shared", "1");
  for (const s of q.all) p.append("all", s);
  p.set("library", String(q.library));
  return p;
}

/** condition is the filter for one value of kind. */
function condition(kind: TermKind, value: string): Filter {
  switch (kind) {
    case "target":
      return { target_state: { instances: [Number(value)] } };
    case "state":
      return { target_state: { states: [value as TargetIs] } };
    case "tag":
      return { tag: value };
    case "source":
      return { source: value };
    case "collection":
      return { collection: Number(value) };
    case "creator":
      return { creator: value };
    case "lorebook":
      return { lorebook: Number(value) };
    case "score": {
      const { instance, key, gte, lte, none } = parseScore(value)!;
      return { enrichment: key, ...(instance && { instance }), ...(none ? { missing: true } : { gte, lte }) };
    }
    case "value": {
      const { instance, key, value: is } = enrichmentValue(value)!;
      return { enrichment: key, ...(instance && { instance }), is };
    }
  }
}

/**
 * filterFor ANDs the sections: a section's included values OR together, or AND when all names it,
 * and each excluded value is its own NOT. sharesSource adds shares_source.
 */
export function filterFor(terms: Term[], sharesSource = false, all: string[] = []): Filter | undefined {
  const included = (kind: TermKind) => terms.filter((t) => t.kind === kind && !t.not).map((t) => t.value);
  const parts: Filter[] = [];
  // One target_state for both sections, so a state applies to the selected targets.
  const instances = included("target").map(Number);
  const states = included("state") as TargetIs[];
  if (instances.length || states.length)
    parts.push({ target_state: { ...(instances.length ? { instances } : {}), ...(states.length ? { states } : {}) } });
  const join = (section: string, [first, ...rest]: Filter[]) => {
    if (!first) return;
    if (all.includes(section)) parts.push(first, ...rest);
    else parts.push(rest.length ? { or: [first, ...rest] } : first);
  };
  for (const kind of ["tag", "source", "collection", "creator", "lorebook"] as const) join(kind, included(kind).map((v) => condition(kind, v)));
  // Each score key narrows on its own.
  for (const t of terms.filter((t) => t.kind === "score" && !t.not)) parts.push(condition(t.kind, t.value));
  // A value key is a section of its own.
  const byKey = new Map<string, Filter[]>();
  for (const t of terms.filter((t) => t.kind === "value" && !t.not)) {
    const section = sectionOf(t);
    byKey.set(section, [...(byKey.get(section) ?? []), condition(t.kind, t.value)]);
  }
  for (const [section, filters] of byKey) join(section, filters);
  if (sharesSource) parts.push({ shares_source: true });
  for (const t of terms.filter((t) => t.not)) parts.push({ not: condition(t.kind, t.value) });
  return parts.length ? { and: parts } : undefined;
}

/** toSearch is the POST /api/search body for the state. scores adds each card's enrichment values, which needs a score instance. */
export function toSearch(q: CardQuery, scores = false): Search {
  return {
    library: q.library,
    text: q.text || undefined,
    ...(q.semantic && { semantic: true }),
    // Semantic search sorts by similarity alone.
    sort: q.semantic ? "relevance" : q.sort,
    ...(q.reverse && !q.semantic && { reverse: true }),
    filter: filterFor(q.terms, q.sharesSource, q.all),
    fields: ["name", "avatar"],
    include: ["tags", "lorebooks", "targets", ...(scores ? ["score"] : [])],
    page: { size: q.size, number: q.page },
  };
}

/** toLorebookSearch is the POST /api/lorebooks/search body for the state. */
export function toLorebookSearch(q: CardQuery): Search {
  return { ...toSearch(q), fields: ["name", "description"], include: ["cards", "targets"] };
}

const facetOf = { target: "targets", tag: "tags", source: "sources", collection: "collections" } as const;

/** othersFilter is q's filter without the sections' terms, but for their included values when they must all match, as each narrows the counts. */
function othersFilter(q: CardQuery, ...sections: string[]): Filter | undefined {
  const kept = q.terms.filter((t) => !sections.includes(sectionOf(t)) || (q.all.includes(sectionOf(t)) && !t.not));
  return filterFor(kept, q.sharesSource, q.all);
}

/** countText is the text facet counts search by: none for a semantic search, which would embed it once per count. */
const countText = (q: CardQuery) => (q.semantic ? undefined : q.text || undefined);

/** countSearch counts a section's values over every other section's terms. */
export function countSearch(q: CardQuery, section: keyof typeof facetOf): Search {
  return {
    library: q.library,
    text: countText(q),
    filter: othersFilter(q, section),
    facets: [facetOf[section]],
    facets_only: true,
  };
}

/** valueCountSearch counts value keys' values over every other filter, the keys' own terms aside; refs are as keyRef names them. */
export function valueCountSearch(q: CardQuery, refs: string[]): Search {
  return {
    library: q.library,
    text: countText(q),
    filter: othersFilter(q, ...refs.map((ref) => `value:${ref}`)),
    facets: refs.map((ref) => `enrichment:${ref}`),
    facets_only: true,
  };
}

/** stateCountSearch counts the matches in each state over every other section's terms, on the selected targets when there are any. */
export function stateCountSearch(q: CardQuery): Search {
  const targets = q.terms.filter((t) => t.kind === "target" && !t.not).map((t) => t.value);
  return {
    library: q.library,
    text: countText(q),
    // The states facet narrows to the selected targets itself, so they leave the filter.
    filter: filterFor(q.terms.filter((t) => t.kind !== "state" && (t.kind !== "target" || t.not)), q.sharesSource, q.all),
    facets: [targets.length ? `states:${targets.join(",")}` : "states"],
    facets_only: true,
  };
}

// The tag facet groups by lower-cased name and the API filter ignores case, so tags match that way here too.
export const sameValue = (kind: TermKind, a: string, b: string) => (kind === "tag" ? a.toLowerCase() === b.toLowerCase() : a === b);

/** sectionOf names a term's sidebar section: its kind, or value:<ref> for a value, as each value key has its own. */
export function sectionOf(t: Term): string {
  const v = t.kind === "value" ? enrichmentValue(t.value) : undefined;
  return v ? `value:${keyRef(v.instance, v.key)}` : t.kind;
}

/** toggleTerm adds t, flips its negation, or removes it if it's already there as given. */
export function toggleTerm(q: CardQuery, t: Term): CardQuery {
  const i = q.terms.findIndex((x) => x.kind === t.kind && sameValue(t.kind, x.value, t.value));
  const terms = q.terms.filter((_, j) => j !== i);
  if (i < 0 || q.terms[i]?.not !== t.not) terms.push(t);
  return { ...q, terms, page: 1 };
}

/** onlyTerm makes t the only value of its section. */
export function onlyTerm(q: CardQuery, t: Term): CardQuery {
  return { ...q, terms: [...q.terms.filter((x) => sectionOf(x) !== sectionOf(t)), { ...t, not: false }], page: 1 };
}
