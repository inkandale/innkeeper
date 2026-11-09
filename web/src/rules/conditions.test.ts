import type { components } from "../api/schema";
import { fieldsFor, fromFilter, newCondition, newGroup, toFilter, type Condition, type Group, type Item } from "./conditions";

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
  ["merged score at least", { field: "score:quality", op: "at_least", value: "70" }, { enrichment: "quality", gte: 70 }],
  ["score at least 0", { field: "score:quality", op: "at_least", value: "0" }, { enrichment: "quality", gte: 0 }],
  ["instance score at most", { field: "score:3:rating", op: "at_most", value: "2" }, { enrichment: "rating", instance: 3, lte: 2 }],
  ["score between", { field: "score:quality", op: "between", value: "10", value2: "40" }, { enrichment: "quality", gte: 10, lte: 40 }],
  ["no score", { field: "score:quality", op: "missing", value: "" }, { enrichment: "quality", missing: true }],
  ["value is", { field: "value:language", op: "is", value: "English" }, { enrichment: "language", is: "English" }],
  ["no value", { field: "value:language", op: "missing", value: "" }, { enrichment: "language", missing: true }],
];

test.each(leaves)("%s round-trips, plain and negated", (_, c, leaf) => {
  for (const not of [false, true]) {
    const root = group("and", [cond({ ...c, not })]);
    const want = { and: [not ? { not: leaf } : leaf] };
    expect(toFilter(root, fields)).toEqual(want);
    expect(bare(fromFilter(want, fields)!)).toEqual(bare(root));
  }
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
  expect(fromFilter({ or: [{ tag: "a" }, { tag: "b" }] }, fields)!.op).toBe("or");
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

test("a Cards-page filter with any-of groups reads as groups", () => {
  const f = { and: [{ or: [{ tag: "a" }, { tag: "b" }] }, { not: { tag: "c" } }] };
  expect(toFilter(fromFilter(f, fields)!, fields)).toEqual(f);
});

test("filters with a condition no field covers read as undefined", () => {
  expect(fromFilter({ and: [{ enrichment: "gone", gte: 1 }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ tag: "a", source: "chub" }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ collection: 4 }] }, fields)).toBeUndefined();
  expect(fromFilter({ or: [{ tag: "a" }, { and: [{ collection: 4 }] }] }, fields)).toBeUndefined();
});
