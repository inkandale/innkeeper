import type { components } from "../api/schema";
import { keyRef } from "../cards/query";

type Filter = components["schemas"]["Filter"];
type BrowseKey = components["schemas"]["BrowseKey"];

export type Op = "contains" | "not_contains" | "is" | "is_not" | "in" | "not_in" | "at_least" | "at_most" | "between" | "missing" | "present";
/**
 * Condition is one row: a field's id, an operator, and its value, or two for between.
 * values holds is in's list; value is the one being typed. key tells rows apart while they're edited.
 */
export type Condition = { kind: "condition"; key: string; field: string; op: Op; value: string; value2?: string; values: string[] };
/** Group joins its items with one connector. The page's own group is never negated. */
export type Group = { kind: "group"; key: string; not: boolean; op: "and" | "or"; items: Item[] };
export type Item = Condition | Group;

// A counter, as crypto.randomUUID is missing on a page served over plain http.
let lastKey = 0;
const nextKey = () => String(++lastKey);
export const newCondition = (): Condition => ({ kind: "condition", key: nextKey(), field: "tag", op: "is", value: "", values: [] });
export const newGroup = (op: Group["op"] = "and"): Group => ({ kind: "group", key: nextKey(), not: false, op, items: [] });

/** conditionsOf lists every condition in item, however deep. */
export const conditionsOf = (item: Item): Condition[] => (item.kind === "condition" ? [item] : item.items.flatMap(conditionsOf));
/** Field is what a row can test. input says which control takes its value. */
export type Field = {
  id: string;
  label: string;
  ops: Op[];
  input: "text" | "tag" | "source" | "number" | "value" | "none";
  key?: string;
  instance?: number;
};

const opLabels: Record<Op, string> = {
  contains: "contains",
  not_contains: "doesn't contain",
  is: "is",
  is_not: "is not",
  in: "is in",
  not_in: "is not in",
  at_least: "is at least",
  at_most: "is at most",
  between: "is between",
  missing: "has no value",
  present: "has a value",
};

/** opLabel names op for field, as the operator menu shows it. */
export function opLabel(op: Op, field: Field): string {
  if (field.input === "none") return op === "is" ? "yes" : "no";
  if (field.input === "number" && op === "missing") return "has no score";
  if (field.input === "number" && op === "present") return "has a score";
  return opLabels[op];
}

/** isList says whether op takes a list of values. */
export const isList = (op: Op) => op === "in" || op === "not_in";

// Each operator's opposite, so a negated condition reads back as one row.
const negations: Partial<Record<Op, Op>> = { contains: "not_contains", is: "is_not", in: "not_in", missing: "present" };

/** valuesOf is row's list for is in: its picked values and the one being typed. */
export const valuesOf = (row: Condition) => [...new Set([...row.values, row.value.trim()].filter(Boolean))];

/** fieldsFor lists what a row can test: the card's own fields, then each browsable enrichment key. */
export function fieldsFor(browse: BrowseKey[]): Field[] {
  const fixed: Field[] = [
    { id: "name", label: "Name", ops: ["contains", "not_contains"], input: "text" },
    { id: "creator", label: "Creator", ops: ["is", "is_not", "in", "not_in"], input: "text" },
    { id: "tag", label: "Tag", ops: ["is", "is_not", "in", "not_in"], input: "tag" },
    { id: "source", label: "Source", ops: ["is", "is_not", "in", "not_in"], input: "source" },
    { id: "shares_source", label: "Shares a source with another card", ops: ["is", "is_not"], input: "none" },
  ];
  const keys = browse.map((b): Field => {
    const at = { key: b.key, ...(b.instance !== undefined && { instance: b.instance }) };
    const ref = keyRef(b.instance, b.key);
    return b.type === "score"
      ? { id: `score:${ref}`, label: b.label, ops: ["at_least", "at_most", "between", "missing", "present"], input: "number", ...at }
      : { id: `value:${ref}`, label: b.label, ops: ["is", "is_not", "in", "not_in", "missing", "present"], input: "value", ...at };
  });
  return [...fixed, ...keys];
}

/** complete says whether row has every value its operator needs. */
export function complete(row: Condition, field: Field | undefined): boolean {
  if (!field) return false;
  if (field.input === "none" || row.op === "missing" || row.op === "present") return true;
  if (isList(row.op)) return valuesOf(row).length > 0;
  if (field.input === "number") {
    const ok = (v?: string) => v !== undefined && v.trim() !== "" && Number.isFinite(Number(v));
    return row.op === "between" ? ok(row.value) && ok(row.value2) && Number(row.value) <= Number(row.value2) : ok(row.value);
  }
  return row.value.trim() !== "";
}

/** leaf is the filter for one positive test of field. */
function leaf(field: Field, op: Op, v: string, v2?: string): Filter {
  const at = { enrichment: field.key!, ...(field.instance !== undefined && { instance: field.instance }) };
  switch (field.input) {
    case "text":
      return field.id === "name" ? { name: v } : { creator: v };
    case "tag":
      return { tag: v };
    case "source":
      return { source: v };
    case "none":
      return { shares_source: true };
    case "number":
      return op === "missing" ? { ...at, missing: true }
        : op === "at_least" ? { ...at, gte: Number(v) }
        : op === "at_most" ? { ...at, lte: Number(v) }
        : { ...at, gte: Number(v), lte: Number(v2!.trim()) };
    case "value":
      return op === "missing" ? { ...at, missing: true } : { ...at, is: v };
  }
}

function condition(row: Condition, field: Field): Filter {
  const v = row.value.trim();
  switch (row.op) {
    case "in":
      return { or: valuesOf(row).map((x) => leaf(field, "is", x)) };
    case "not_in":
      return { not: { or: valuesOf(row).map((x) => leaf(field, "is", x)) } };
    case "is_not":
      return { not: leaf(field, "is", v) };
    case "not_contains":
      return { not: leaf(field, "contains", v) };
    case "present":
      return { not: leaf(field, "missing", v) };
    default:
      return leaf(field, row.op, v, row.value2);
  }
}

function itemFilter(item: Item, byId: Map<string, Field>): Filter | undefined {
  const field = item.kind === "condition" ? byId.get(item.field) : undefined;
  if (item.kind === "condition") return complete(item, field) ? condition(item, field!) : undefined;
  const f = groupFilter(item, byId);
  return f && item.not ? { not: f } : f;
}

function groupFilter(g: Group, byId: Map<string, Field>): Filter | undefined {
  const parts = g.items.map((i) => itemFilter(i, byId)).filter((f): f is Filter => f !== undefined);
  if (!parts.length) return undefined;
  return g.op === "and" ? { and: parts } : { or: parts };
}

/** toFilter is the saved search filter for root's complete conditions, or undefined when it has none. */
export function toFilter(root: Group, fields: Field[]): Filter | undefined {
  return itemFilter(root, new Map(fields.map((f) => [f.id, f])));
}

const only = (f: object, ...keys: string[]) => Object.keys(f).every((k) => keys.includes(k));

/** conditionOf reads one plain condition back as a row, or undefined if no row makes it. */
function conditionOf(c: Filter, fields: Field[]): Condition | undefined {
  const row = (r: Pick<Condition, "field" | "op" | "value" | "value2">): Condition => ({ ...newCondition(), ...r });
  if (c.name !== undefined && only(c, "name")) return row({ field: "name", op: "contains", value: c.name });
  if (c.creator !== undefined && only(c, "creator")) return row({ field: "creator", op: "is", value: c.creator });
  if (c.tag !== undefined && only(c, "tag")) return row({ field: "tag", op: "is", value: c.tag });
  if (c.source !== undefined && only(c, "source")) return row({ field: "source", op: "is", value: c.source });
  if (c.shares_source === true && only(c, "shares_source")) return row({ field: "shares_source", op: "is", value: "" });
  if (c.enrichment === undefined) return undefined;
  const ref = keyRef(c.instance, c.enrichment);
  const score = fields.find((x) => x.id === `score:${ref}`);
  const value = fields.find((x) => x.id === `value:${ref}`);
  const base = ["enrichment", "instance"];
  if (c.missing === true && only(c, ...base, "missing")) {
    const field = score ?? value;
    return field && row({ field: field.id, op: "missing", value: "" });
  }
  if (score && only(c, ...base, "gte", "lte")) {
    if (c.gte !== undefined && c.lte !== undefined) return row({ field: score.id, op: "between", value: String(c.gte), value2: String(c.lte) });
    if (c.gte !== undefined) return row({ field: score.id, op: "at_least", value: String(c.gte) });
    if (c.lte !== undefined) return row({ field: score.id, op: "at_most", value: String(c.lte) });
  }
  if (value && c.is !== undefined && only(c, ...base, "is")) return row({ field: value.id, op: "is", value: c.is });
  return undefined;
}

/** listOf reads an or of one field's is tests, as the Cards page saves any-of, as one is in row. */
function listOf(parts: Filter[], fields: Field[]): Condition | undefined {
  const rows = parts.map((p) => conditionOf(p, fields));
  const first = rows[0];
  if (!first || rows.some((r) => !r || r.op !== "is" || r.field !== first.field || first.field === "shares_source")) return undefined;
  return { ...first, op: "in", value: "", values: rows.map((r) => r!.value) };
}

function itemOf(f: Filter, fields: Field[]): Item | undefined {
  if (f.not && only(f, "not")) {
    const inner = itemOf(f.not, fields);
    if (!inner) return undefined;
    const negated = inner.kind === "condition" ? negations[inner.op] : undefined;
    if (negated) return { ...inner as Condition, op: negated };
    // Only a group holds a not, so anything else becomes a negated group around it.
    return inner.kind === "group" && !inner.not ? { ...inner, not: true } : { ...newGroup(), not: true, items: [inner] };
  }
  if (f.or && only(f, "or")) {
    const list = listOf(f.or, fields);
    if (list) return list;
  }
  const parts = f.and && only(f, "and") ? f.and : f.or && only(f, "or") ? f.or : undefined;
  if (parts) {
    const items = parts.map((p) => itemOf(p, fields));
    if (items.some((i) => i === undefined)) return undefined;
    return { ...newGroup(f.and ? "and" : "or"), items: items as Item[] };
  }
  return conditionOf(f, fields);
}

/** fromFilter reads a saved search's filter as the page's group, or returns undefined when a condition has no field, such as a collection. */
export function fromFilter(f: Filter, fields: Field[]): Group | undefined {
  const item = itemOf(f, fields);
  if (!item) return undefined;
  return item.kind === "group" && !item.not ? item : { ...newGroup(), items: [item] };
}
