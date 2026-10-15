import type { components } from "../api/schema";

type Filter = components["schemas"]["Filter"];
type Search = components["schemas"]["Query"];
type TargetIs = components["schemas"]["TargetState"]["is"];

// Typed from the schema so a new state fails the build here.
export const targetStates: TargetIs[] = ["in_sync", "ahead", "remote_changed", "diverged", "missing", "cant_check"];
/** kinds are the filter sections, in sidebar order. score filters on enrichment values. */
const kinds = ["target", "state", "tag", "source", "collection", "score"] as const;
const sorts = ["relevance", "updated", "name", "created"] as const;
const enrichmentSort = /^enrichment:[a-z][a-z0-9_]*$/;
// A score term's value: key:low..high, either end optional, or key:none for cards without a value.
const scoreValue = /^([a-z][a-z0-9_]*):(none|(\d+(?:\.\d+)?)?\.\.(\d+(?:\.\d+)?)?)$/;
export const sizes = [24, 48, 96];

export type TermKind = (typeof kinds)[number];
/** Term is one filter: a section's value, or its negation. */
export type Term = { kind: TermKind; value: string; not: boolean };
export type Sort = (typeof sorts)[number] | `enrichment:${string}`;
export type CardQuery = { text: string; terms: Term[]; sort: Sort; page: number; size: number; view: "grid" | "list" };

export const defaultSort = (text: string): Sort => (text ? "relevance" : "updated");

/** sectionsFor lists the sidebar sections a kind of entity has; lorebooks have no tags or collections. */
export const sectionsFor = (entity: "card" | "lorebook"): TermKind[] =>
  entity === "card" ? ["target", "state", "tag", "source", "collection"] : ["target", "state", "source"];

/** Score is a score term's value: a range, or none for cards without a value. */
export type Score = { key: string; gte?: number; lte?: number; none?: boolean };

/** parseScore reads a score term's value, or returns undefined if it is malformed. */
export function parseScore(value: string): Score | undefined {
  const m = scoreValue.exec(value);
  if (!m) return undefined;
  if (m[2] === "none") return { key: m[1]!, none: true };
  if (m[3] === undefined && m[4] === undefined) return undefined;
  return { key: m[1]!, ...(m[3] !== undefined && { gte: Number(m[3]) }), ...(m[4] !== undefined && { lte: Number(m[4]) }) };
}

/** scoreTerm is the term for a score, or undefined for one with no condition. */
export function scoreTerm(s: Score): Term | undefined {
  if (s.none) return { kind: "score", value: `${s.key}:none`, not: false };
  if (s.gte === undefined && s.lte === undefined) return undefined;
  return { kind: "score", value: `${s.key}:${s.gte ?? ""}..${s.lte ?? ""}`, not: false };
}

/** setScore replaces key's score term with s's. */
export function setScore(q: CardQuery, s: Score): CardQuery {
  const t = scoreTerm(s);
  const rest = q.terms.filter((x) => x.kind !== "score" || parseScore(x.value)?.key !== s.key);
  return { ...q, terms: t ? [...rest, t] : rest, page: 1 };
}

const id = (v: string) => /^[1-9]\d*$/.test(v);

/** valid says whether value can be a term of kind. */
function valid(kind: TermKind, value: string): boolean {
  if (kind === "target" || kind === "collection") return id(value);
  if (kind === "state") return targetStates.some((s) => s === value);
  if (kind === "score") return parseScore(value) !== undefined;
  return value !== "";
}

/** parseQuery reads the Cards page state from its URL, ignoring anything malformed. */
export function parseQuery(p: URLSearchParams): CardQuery {
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
    text,
    terms,
    sort: sorts.some((s) => s === sort) || enrichmentSort.test(sort ?? "") ? sort : defaultSort(text),
    page: Number.isInteger(page) && page > 0 ? page : 1,
    size: sizes.includes(size) ? size : 48,
    view: p.get("view") === "list" ? "list" : "grid",
  };
}

/** toParams writes the state back, leaving defaults out. */
export function toParams(q: CardQuery): URLSearchParams {
  const p = new URLSearchParams();
  if (q.text) p.set("q", q.text);
  for (const t of q.terms) p.append("f", `${t.not ? "-" : ""}${t.kind}:${t.value}`);
  if (q.sort !== defaultSort(q.text)) p.set("sort", q.sort);
  if (q.page !== 1) p.set("page", String(q.page));
  if (q.size !== 48) p.set("size", String(q.size));
  if (q.view !== "grid") p.set("view", q.view);
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
    case "score": {
      const { key, gte, lte, none } = parseScore(value)!;
      return none ? { enrichment: key, missing: true } : { enrichment: key, gte, lte };
    }
  }
}

/** filterFor ANDs the sections: a section's included values OR together, and each excluded value is its own NOT. */
export function filterFor(terms: Term[]): Filter | undefined {
  const included = (kind: TermKind) => terms.filter((t) => t.kind === kind && !t.not).map((t) => t.value);
  const parts: Filter[] = [];
  // One target_state for both sections, so a state applies to the selected targets.
  const instances = included("target").map(Number);
  const states = included("state") as TargetIs[];
  if (instances.length || states.length)
    parts.push({ target_state: { ...(instances.length ? { instances } : {}), ...(states.length ? { states } : {}) } });
  for (const kind of ["tag", "source", "collection"] as const) {
    const [first, ...rest] = included(kind).map((v) => condition(kind, v));
    if (first) parts.push(rest.length ? { or: [first, ...rest] } : first);
  }
  // Each score key narrows on its own.
  for (const t of terms.filter((t) => t.kind === "score" && !t.not)) parts.push(condition(t.kind, t.value));
  for (const t of terms.filter((t) => t.not)) parts.push({ not: condition(t.kind, t.value) });
  return parts.length ? { and: parts } : undefined;
}

/** toSearch is the POST /api/search body for the state. scores adds each card's enrichment values, which needs a score instance. */
export function toSearch(q: CardQuery, scores = false): Search {
  return {
    text: q.text || undefined,
    filter: filterFor(q.terms),
    sort: q.sort,
    fields: ["name", "avatar"],
    include: ["tags", "lorebooks", "targets", ...(scores ? ["enrichments"] : [])],
    page: { size: q.size, number: q.page },
  };
}

/** toLorebookSearch is the POST /api/lorebooks/search body for the state. */
export function toLorebookSearch(q: CardQuery): Search {
  return { ...toSearch(q), fields: ["name", "description"], include: ["cards", "targets"] };
}

const facetOf = { target: "targets", tag: "tags", source: "sources", collection: "collections" } as const;

/** countSearch counts a section's values over every other section's terms; size 1 because 0 means the default. */
export function countSearch(q: CardQuery, section: keyof typeof facetOf): Search {
  return {
    text: q.text || undefined,
    filter: filterFor(q.terms.filter((t) => t.kind !== section)),
    facets: [facetOf[section]],
    page: { size: 1 },
  };
}

/** stateCountSearch counts the matches in one state; the targets facet can't, as it counts a card once per target. */
export function stateCountSearch(q: CardQuery, is: TargetIs): Search {
  const terms: Term[] = [...q.terms.filter((t) => t.kind !== "state"), { kind: "state", value: is, not: false }];
  return { text: q.text || undefined, filter: filterFor(terms), page: { size: 1 } };
}

// The tag facet groups by lower-cased name and the API filter ignores case, so tags match that way here too.
export const sameValue = (kind: TermKind, a: string, b: string) => (kind === "tag" ? a.toLowerCase() === b.toLowerCase() : a === b);

/** toggleTerm adds t, flips its negation, or removes it if it's already there as given. */
export function toggleTerm(q: CardQuery, t: Term): CardQuery {
  const i = q.terms.findIndex((x) => x.kind === t.kind && sameValue(t.kind, x.value, t.value));
  const terms = q.terms.filter((_, j) => j !== i);
  if (i < 0 || q.terms[i]?.not !== t.not) terms.push(t);
  return { ...q, terms, page: 1 };
}

/** onlyTerm makes t the only value of its section. */
export function onlyTerm(q: CardQuery, t: Term): CardQuery {
  return { ...q, terms: [...q.terms.filter((x) => x.kind !== t.kind), { ...t, not: false }], page: 1 };
}
