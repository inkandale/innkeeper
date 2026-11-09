import { fieldsFor, fromFilter, toFilter, type Condition } from "./conditions";

const fields = fieldsFor([
  { key: "quality", label: "Quality", type: "score", instances: [1] },
  { key: "language", label: "Language", type: "text", instances: [2] },
  { key: "rating", label: "Rating", type: "score", instance: 3, instances: [3] },
]);

test("fields are the fixed ones, then each browsable key", () => {
  expect(fields.map((f) => f.id)).toEqual(["name", "creator", "tag", "source", "shares_source", "score:quality", "value:language", "score:3:rating"]);
  expect(fields.find((f) => f.id === "score:3:rating")).toMatchObject({ label: "Rating", input: "number", key: "rating", instance: 3 });
});

const cases: [string, Condition, unknown][] = [
  ["name contains", { field: "name", op: "contains", value: "junk" }, { name: "junk" }],
  ["creator is not", { field: "creator", op: "is_not", value: "bob" }, { not: { creator: "bob" } }],
  ["tag is", { field: "tag", op: "is", value: "nsfw" }, { tag: "nsfw" }],
  ["source is", { field: "source", op: "is", value: "chub" }, { source: "chub" }],
  ["shares a source", { field: "shares_source", op: "is", value: "" }, { shares_source: true }],
  ["merged score at least", { field: "score:quality", op: "at_least", value: "70" }, { enrichment: "quality", gte: 70 }],
  ["instance score at most", { field: "score:3:rating", op: "at_most", value: "2" }, { enrichment: "rating", instance: 3, lte: 2 }],
  ["score between", { field: "score:quality", op: "between", value: "10", value2: "40" }, { enrichment: "quality", gte: 10, lte: 40 }],
  ["no score", { field: "score:quality", op: "missing", value: "" }, { enrichment: "quality", missing: true }],
  ["value is not", { field: "value:language", op: "is_not", value: "English" }, { not: { enrichment: "language", is: "English" } }],
];

test.each(cases)("%s round-trips", (_, row, cond) => {
  const all = toFilter({ match: "all", rows: [row] }, fields);
  expect(all).toEqual({ and: [cond] });
  expect(fromFilter(all!, fields)).toEqual({ match: "all", rows: [row] });
  const any = toFilter({ match: "any", rows: [row] }, fields);
  expect(any).toEqual({ or: [cond] });
  expect(fromFilter(any!, fields)).toEqual({ match: "any", rows: [row] });
});

test("incomplete rows are left out, and none at all is no filter", () => {
  expect(toFilter({ match: "all", rows: [{ field: "tag", op: "is", value: "" }] }, fields)).toBeUndefined();
  expect(toFilter({ match: "all", rows: [{ field: "score:quality", op: "between", value: "1", value2: "" }] }, fields)).toBeUndefined();
});

test("a bare condition reads as one row", () => {
  expect(fromFilter({ tag: "nsfw" }, fields)).toEqual({ match: "all", rows: [{ field: "tag", op: "is", value: "nsfw" }] });
});

test("filters rows can't show read as undefined", () => {
  expect(fromFilter({ and: [{ or: [{ tag: "a" }, { tag: "b" }] }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ enrichment: "gone", gte: 1 }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ tag: "a", source: "chub" }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ not: { name: "x" } }] }, fields)).toBeUndefined();
  expect(fromFilter({ and: [{ collection: 4 }] }, fields)).toBeUndefined();
});
