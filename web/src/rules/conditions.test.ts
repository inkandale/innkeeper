import type { components } from "../api/schema";
import { fieldsFor, fromFilter, newCondition, newGroup, noValue, toFilter, type Condition, type Group, type Item } from "./conditions";

const fields = fieldsFor([
  { key: "quality", label: "Quality", type: "score", instances: [1] },
  { key: "language", label: "Language", type: "text", instances: [2] },
  { key: "rating", label: "Rating", type: "score", instance: 3, instances: [3] },
]);

test("fields are the fixed ones, then each browsable key", () => {
  expect(fields.map((f) => f.id)).toEqual(["name", "creator", "tag", "source", "shares_source", "score:quality", "value:language", "score:3:rating"]);
  expect(fields.find((f) => f.id === "score:3:rating")).toMatchObject({ label: "Rating", input: "number", key: "rating", instance: 3 });
});

/** bare drops the keys, which differ on every call. */
const bare = (i: Item): unknown =>
  i.kind === "group" ? { kind: i.kind, not: i.not, op: i.op, items: i.items.map(bare) } : { ...i, key: undefined };
const cond = (c: Partial<Condition>): Condition => ({ ...newCondition(), ...c });
const group = (op: Group["op"], items: Item[], not = false): Group => ({ ...newGroup(op), not, items });

const leaves: [string, Partial<Condition>, components["schemas"]["Filter"]][] = [
  ["name contains", { field: "name", op: "contains", value: "junk" }, { name: "junk" }],
  ["creator is", { field: "creator", op: "is", value: "bob" }, { creator: "bob" }],
  ["tag is", { field: "tag", op: "is", value: "nsfw" }, { tag: "nsfw" }],
  ["source is", { field: "source", op: "is", value: "chub" }, { source: "chub" }],
  ["shares a source", { field: "shares_source", op: "is", value: "" }, { shares_source: true }],
  ["shares no source", { field: "shares_source", op: "is_not", value: "" }, { not: { shares_source: true } }],
  ["name doesn't contain", { field: "name", op: "not_contains", value: "junk" }, { not: { name: "junk" } }],
  ["creator is not", { field: "creator", op: "is_not", value: "bob" }, { not: { creator: "bob" } }],
  ["tag is not", { field: "tag", op: "is_not", value: "nsfw" }, { not: { tag: "nsfw" } }],
  ["tag is in", { field: "tag", op: "in", value: "", values: ["a", "b"] }, { or: [{ tag: "a" }, { tag: "b" }] }],
  ["source is not in", { field: "source", op: "not_in", value: "", values: ["chub"] }, { not: { or: [{ source: "chub" }] } }],
  ["value is not", { field: "value:language", op: "is_not", value: "English" }, { not: { enrichment: "language", is: "English" } }],
  ["value is in", { field: "value:language", op: "in", value: "", values: ["English", "French"] }, { or: [{ enrichment: "language", is: "English" }, { enrichment: "language", is: "French" }] }],
  ["has a value", { field: "value:language", op: "present", value: "" }, { not: { enrichment: "language", missing: true } }],
  ["has a score", { field: "score:quality", op: "present", value: "" }, { not: { enrichment: "quality", missing: true } }],
  ["merged score at least", { field: "score:quality", op: "at_least", value: "70" }, { enrichment: "quality", gte: 70 }],
  ["score at least 0", { field: "score:quality", op: "at_least", value: "0" }, { enrichment: "quality", gte: 0 }],
  ["instance score at most", { field: "score:3:rating", op: "at_most", value: "2" }, { enrichment: "rating", instance: 3, lte: 2 }],
  ["score between", { field: "score:quality", op: "between", value: "10", value2: "40" }, { enrichment: "quality", gte: 10, lte: 40 }],
  ["no score", { field: "score:quality", op: "missing", value: "" }, { enrichment: "quality", missing: true }],
  ["value is", { field: "value:language", op: "is", value: "English" }, { enrichment: "language", is: "English" }],
  ["no value", { field: "value:language", op: "missing", value: "" }, { enrichment: "language", missing: true }],
];

test.each(leaves)("%s round-trips", (_, c, leaf) => {
  const root = group("and", [cond(c)]);
  expect(toFilter(root, fields)).toEqual({ and: [leaf] });
  expect(bare(fromFilter({ and: [leaf] }, fields)!)).toEqual(bare(root));
});

test("No value in a list is the key's has no value test", () => {
  const root = group("and", [cond({ field: "value:language", op: "not_in", value: "", values: ["English", noValue] })]);
  const want = { and: [{ not: { or: [{ enrichment: "language", is: "English" }, { enrichment: "language", missing: true }] } }] };
  expect(toFilter(root, fields)).toEqual(want);
  expect(bare(fromFilter(want, fields)!)).toEqual(bare(root));
});

test("is in counts the value being typed", () => {
  expect(toFilter(group("and", [cond({ field: "tag", op: "in", value: "c ", values: ["a"] })]), fields)).toEqual({ and: [{ or: [{ tag: "a" }, { tag: "c" }] }] });
  expect(toFilter(group("and", [cond({ field: "tag", op: "in", value: "", values: [] })]), fields)).toBeUndefined();
});

test("a negated test with no opposite operator reads as a negated group", () => {
  const f = { and: [{ not: { enrichment: "quality", gte: 70 } }] };
  expect(fromFilter(f, fields)!.items[0]).toMatchObject({ kind: "group", not: true, items: [{ field: "score:quality", op: "at_least" }] });
  expect(toFilter(fromFilter(f, fields)!, fields)).toEqual({ and: [{ not: { and: [{ enrichment: "quality", gte: 70 }] } }] });
});

test("groups nest, each with its own connector and not", () => {
  const root = group("and", [
    cond({ field: "tag", op: "is", value: "nsfw" }),
    group("or", [cond({ field: "value:language", op: "is", value: "English" }), cond({ field: "source", op: "is", value: "chub" })], true),
  ]);
  const want = { and: [{ tag: "nsfw" }, { not: { or: [{ enrichment: "language", is: "English" }, { source: "chub" }] } }] };
  expect(toFilter(root, fields)).toEqual(want);
  expect(bare(fromFilter(want, fields)!)).toEqual(bare(root));
});

test("incomplete items and emptied groups are left out", () => {
  const root = group("and", [cond({ field: "tag", op: "is", value: "" }), group("or", [cond({ field: "tag", op: "is", value: "" })]), cond({ field: "tag", op: "is", value: "a" })]);
  expect(toFilter(root, fields)).toEqual({ and: [{ tag: "a" }] });
  expect(toFilter(group("and", []), fields)).toBeUndefined();
  expect(toFilter(group("and", [cond({ field: "score:quality", op: "between", value: "1", value2: "" })]), fields)).toBeUndefined();
  expect(toFilter(group("and", [cond({ field: "score:quality", op: "between", value: "40", value2: "10" })]), fields)).toBeUndefined();
});

test("a bare condition, an or at the top, and a negated top read as the page's group", () => {
  expect(bare(fromFilter({ tag: "a" }, fields)!)).toEqual(bare(group("and", [cond({ field: "tag", op: "is", value: "a" })])));
  expect(fromFilter({ or: [{ tag: "a" }, { source: "chub" }] }, fields)!.op).toBe("or");
  expect(fromFilter({ or: [{ tag: "a" }, { tag: "b" }] }, fields)!.items[0]).toMatchObject({ field: "tag", op: "in", values: ["a", "b"] });
  // The page's group can't be negated, so a negated top goes inside it.
  const top = fromFilter({ not: { and: [{ tag: "a" }] } }, fields)!;
  expect(top.not).toBe(false);
  expect(top.items).toHaveLength(1);
  expect(top.items[0]).toMatchObject({ kind: "group", not: true });
});

test("a not of a not keeps both", () => {
  const f = { and: [{ not: { not: { tag: "a" } } }] };
  expect(toFilter(fromFilter(f, fields)!, fields)).toEqual({ and: [{ not: { and: [{ not: { tag: "a" } }] } }] });
});

test("a Cards-page filter with any-of reads as is in, and a none-of as is not", () => {
  const f = { and: [{ or: [{ tag: "a" }, { tag: "b" }] }, { not: { tag: "c" } }] };
  expect(toFilter(fromFilter(f, fields)!, fields)).toEqual(f);
});

test("filters with a condition no field covers read as undefined", () => {
  expect(fromFilter({ and: [{ enrichment: "gone", gte: 1 }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ tag: "a", source: "chub" }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ collection: 4 }] }, fields)).toBeUndefined();
  expect(fromFilter({ or: [{ tag: "a" }, { and: [{ collection: 4 }] }] }, fields)).toBeUndefined();
});
