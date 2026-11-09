import type { components } from "../api/schema";
import { keyRef } from "../cards/query";

type Filter = components["schemas"]["Filter"];
type BrowseKey = components["schemas"]["BrowseKey"];

export type Op = "contains" | "is" | "is_not" | "at_least" | "at_most" | "between" | "missing";
/** Condition is one row: a field's id, an operator, and its value, or two for between. */
export type Condition = { field: string; op: Op; value: string; value2?: string };
export type Conditions = { match: "all" | "any"; rows: Condition[] };
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
  is: "is",
  is_not: "is not",
  at_least: "is at least",
  at_most: "is at most",
  between: "is between",
  missing: "has no value",
};

/** opLabel names op for field, as the operator menu shows it. */
export const opLabel = (op: Op, field: Field) => (op === "missing" && field.input === "number" ? "has no score" : opLabels[op]);

/** fieldsFor lists what a row can test: the card's own fields, then each browsable enrichment key. */
export function fieldsFor(browse: BrowseKey[]): Field[] {
  const fixed: Field[] = [
    { id: "name", label: "Name", ops: ["contains"], input: "text" },
    { id: "creator", label: "Creator", ops: ["is", "is_not"], input: "text" },
    { id: "tag", label: "Tag", ops: ["is", "is_not"], input: "tag" },
    { id: "source", label: "Source", ops: ["is", "is_not"], input: "source" },
    { id: "shares_source", label: "Shares a source with another card", ops: ["is"], input: "none" },
  ];
  const keys = browse.map((b): Field => {
    const at = { key: b.key, ...(b.instance !== undefined && { instance: b.instance }) };
    const ref = keyRef(b.instance, b.key);
    return b.type === "score"
      ? { id: `score:${ref}`, label: b.label, ops: ["at_least", "at_most", "between", "missing"], input: "number", ...at }
      : { id: `value:${ref}`, label: b.label, ops: ["is", "is_not", "missing"], input: "value", ...at };
  });
  return [...fixed, ...keys];
}

/** complete says whether row has every value its operator needs. */
export function complete(row: Condition, field: Field | undefined): boolean {
  if (!field) return false;
  if (field.input === "none" || row.op === "missing") return true;
  if (field.input === "number") {
    const ok = (v?: string) => v !== undefined && v.trim() !== "" && Number.isFinite(Number(v));
    return row.op === "between" ? ok(row.value) && ok(row.value2) : ok(row.value);
  }
  return row.value.trim() !== "";
}

function condition(row: Condition, field: Field): Filter {
  const v = row.value.trim();
  const at = { enrichment: field.key!, ...(field.instance !== undefined && { instance: field.instance }) };
  let c: Filter;
  switch (field.input) {
    case "text":
      c = field.id === "name" ? { name: v } : { creator: v };
      break;
    case "tag":
      c = { tag: v };
      break;
    case "source":
      c = { source: v };
      break;
    case "none":
      return { shares_source: true };
    case "number":
      return row.op === "missing" ? { ...at, missing: true }
        : row.op === "at_least" ? { ...at, gte: Number(v) }
        : row.op === "at_most" ? { ...at, lte: Number(v) }
        : { ...at, gte: Number(v), lte: Number(row.value2!.trim()) };
    case "value":
      c = row.op === "missing" ? { ...at, missing: true } : { ...at, is: v };
      break;
  }
  return row.op === "is_not" ? { not: c } : c;
}

/** toFilter is the saved search filter for the complete rows, or undefined when none are. */
export function toFilter(c: Conditions, fields: Field[]): Filter | undefined {
  const byId = new Map(fields.map((f) => [f.id, f]));
  const parts = c.rows.filter((r) => complete(r, byId.get(r.field))).map((r) => condition(r, byId.get(r.field)!));
  if (!parts.length) return undefined;
  return c.match === "all" ? { and: parts } : { or: parts };
}

const only = (f: object, ...keys: string[]) => Object.keys(f).every((k) => keys.includes(k));

/** rowOf reads one condition back as a row, or undefined if no row makes it. */
function rowOf(f: Filter, fields: Field[]): Condition | undefined {
  const negated = f.not !== undefined && only(f, "not");
  const c = negated ? f.not! : f;
  const is: Op = negated ? "is_not" : "is";
  if (c.name !== undefined && only(c, "name") && !negated) return { field: "name", op: "contains", value: c.name };
  if (c.creator !== undefined && only(c, "creator")) return { field: "creator", op: is, value: c.creator };
  if (c.tag !== undefined && only(c, "tag")) return { field: "tag", op: is, value: c.tag };
  if (c.source !== undefined && only(c, "source")) return { field: "source", op: is, value: c.source };
  if (c.shares_source === true && only(c, "shares_source") && !negated) return { field: "shares_source", op: "is", value: "" };
  if (c.enrichment === undefined) return undefined;
  const ref = keyRef(c.instance, c.enrichment);
  const score = fields.find((x) => x.id === `score:${ref}`);
  const value = fields.find((x) => x.id === `value:${ref}`);
  const base = ["enrichment", "instance"];
  if (c.missing === true && only(c, ...base, "missing") && !negated) {
    const field = score ?? value;
    return field && { field: field.id, op: "missing", value: "" };
  }
  if (score && !negated && only(c, ...base, "gte", "lte")) {
    if (c.gte !== undefined && c.lte !== undefined) return { field: score.id, op: "between", value: String(c.gte), value2: String(c.lte) };
    if (c.gte !== undefined) return { field: score.id, op: "at_least", value: String(c.gte) };
    if (c.lte !== undefined) return { field: score.id, op: "at_most", value: String(c.lte) };
  }
  if (value && c.is !== undefined && only(c, ...base, "is")) return { field: value.id, op: is, value: c.is };
  return undefined;
}

/** fromFilter reads a saved search's filter as rows, or returns undefined when rows can't show it, as with nested any-of groups. */
export function fromFilter(f: Filter, fields: Field[]): Conditions | undefined {
  const group = f.and && only(f, "and") ? { match: "all" as const, parts: f.and } : f.or && only(f, "or") ? { match: "any" as const, parts: f.or } : undefined;
  const parts = group?.parts ?? [f];
  const rows = parts.map((p) => rowOf(p, fields));
  if (rows.some((r) => r === undefined)) return undefined;
  return { match: group?.match ?? "all", rows: rows as Condition[] };
}
