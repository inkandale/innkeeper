import { changed, clashes, rebase, show, tokens, type Draft } from "./draft";

const draft: Draft = { baseId: 1, base: { a: "x", b: "x", list: ["1"] }, data: { a: "y", b: "x", list: ["1"] } };

test("changed lists fields that differ from the base", () => {
  expect([...changed(draft)]).toEqual(["a"]);
  expect([...changed({ ...draft, data: { ...draft.data, list: ["1", "2"] } })].sort()).toEqual(["a", "list"]);
  expect(changed(null).size).toBe(0);
});

test("rebase keeps the draft's changes on top of a newer revision", () => {
  const latest = { a: "x", b: "z", c: "new", list: ["1"] };
  expect(rebase(draft, 2, latest)).toEqual({ baseId: 2, base: latest, data: { a: "y", b: "z", c: "new", list: ["1"] } });
  const { list: _, ...rest } = draft.data;
  expect(rebase({ ...draft, data: rest }, 2, latest).data).not.toHaveProperty("list");
});

test("clashes lists fields both sides changed differently", () => {
  expect(clashes(draft, { a: "z", b: "z", list: ["1"] })).toEqual(["a"]);
  expect(clashes(draft, { a: "y", b: "z", list: ["1"] })).toEqual([]);
  // latest left a at its base value, so keeping the draft loses nothing.
  expect(clashes(draft, { a: "x", b: "z", list: ["1"] })).toEqual([]);
});

test("tokens estimates a quarter of the characters", () => {
  expect(tokens("")).toBe(0);
  expect(tokens("abcde")).toBe(2);
});

test("show prints strings as they are and anything else as JSON", () => {
  expect(show("hi")).toBe("hi");
  expect(show(undefined)).toBe("");
  expect(show(["a"])).toBe('[\n  "a"\n]');
});
