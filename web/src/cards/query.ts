import type { components } from "../api/schema";

type Filter = components["schemas"]["Filter"];
type Search = components["schemas"]["Query"];
type TargetIs = components["schemas"]["TargetState"]["is"];

const kinds = ["tag", "collection", "source", "target"] as const;
const sorts = ["relevance", "updated", "name", "created"] as const;
export const sizes = [24, 48, 96];

/** Term is one filter: a facet value, or its negation. */
export type Term = { kind: (typeof kinds)[number]; value: string; not: boolean };
export type Sort = (typeof sorts)[number];
export type CardQuery = { text: string; terms: Term[]; sort: Sort; page: number; size: number; view: "grid" | "list" };

export const defaultSort = (text: string): Sort => (text ? "relevance" : "updated");

/** parseQuery reads the Cards page state from its URL, ignoring anything malformed. */
export function parseQuery(p: URLSearchParams): CardQuery {
  const terms: Term[] = [];
  for (const raw of p.getAll("f")) {
    const not = raw.startsWith("-");
    const s = not ? raw.slice(1) : raw;
    const i = s.indexOf(":");
    const kind = s.slice(0, i) as Term["kind"];
    if (i > 0 && kinds.includes(kind)) terms.push({ kind, value: s.slice(i + 1), not });
  }
  const text = p.get("q") ?? "";
  const sort = p.get("sort") as Sort;
  const page = Number(p.get("page"));
  const size = Number(p.get("size"));
  return {
    text,
    terms,
    sort: sorts.includes(sort) ? sort : defaultSort(text),
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

function termFilter(t: Term): Filter | null {
  let f: Filter;
  if (t.kind === "tag") f = { tag: t.value };
  else if (t.kind === "source") f = { source: t.value };
  else if (t.kind === "collection") {
    const id = Number(t.value);
    if (!Number.isInteger(id)) return null;
    f = { collection: id };
  } else {
    const [instance, is] = t.value.split(":");
    if (!Number.isInteger(Number(instance)) || !is) return null;
    f = { target_state: { instance: Number(instance), is: is as TargetIs } };
  }
  return t.not ? { not: f } : f;
}

/** toSearch is the POST /api/search body for the state. */
export function toSearch(q: CardQuery): Search {
  const filters = q.terms.map(termFilter).filter((f): f is Filter => f !== null);
  return {
    text: q.text || undefined,
    filter: filters.length ? { and: filters } : undefined,
    sort: q.sort,
    fields: ["name", "description", "avatar"],
    include: ["tags", "lorebooks", "targets"],
    facets: ["tags", "collections", "targets", "sources"],
    page: { size: q.size, number: q.page },
  };
}

/** toggleTerm adds t, flips its negation, or removes it if it's already there as given. */
export function toggleTerm(q: CardQuery, t: Term): CardQuery {
  const i = q.terms.findIndex((x) => x.kind === t.kind && x.value === t.value);
  const terms = q.terms.filter((_, j) => j !== i);
  if (i < 0 || q.terms[i]?.not !== t.not) terms.push(t);
  return { ...q, terms, page: 1 };
}
