import { asEntries, blankEntry, firstLine, matches, moved } from "./entries";

const anvil = { keys: ["anvil", "forge"], secondary_keys: ["iron"], comment: "Smithy", content: "Heavy iron.\nRings when struck." };

test("asEntries keeps the objects in an array", () => {
  expect(asEntries([anvil, "junk", null])).toEqual([anvil]);
  expect(asEntries(undefined)).toEqual([]);
});

test("firstLine is the content's first line, cut at 80", () => {
  expect(firstLine(anvil)).toBe("Heavy iron.");
  expect(firstLine({ content: "x".repeat(100) })).toBe("x".repeat(80) + "…");
  expect(firstLine({})).toBe("");
});

test("matches looks in keys, secondary keys, comment, and content, ignoring case", () => {
  for (const q of ["ANVIL", "iron", "smithy", "rings", ""]) expect(matches(anvil, q)).toBe(true);
  expect(matches(anvil, "kraken")).toBe(false);
});

test("moved moves one item", () => {
  expect(moved(["a", "b", "c"], 0, 2)).toEqual(["b", "c", "a"]);
  expect(moved(["a", "b", "c"], 2, 1)).toEqual(["a", "c", "b"]);
});

test("blankEntry has the fields CCv3 requires", () => {
  expect(blankEntry()).toEqual({
    keys: [],
    content: "",
    enabled: true,
    insertion_order: 100,
    position: "before_char",
    extensions: {},
  });
});
