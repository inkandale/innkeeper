import { emptySelection, pick, target } from "./selection";

const page = [1, 2, 3, 4, 5];

test("a click toggles one card", () => {
  let s = pick(emptySelection("k"), 2, false, page);
  expect(s.ids).toEqual([2]);
  s = pick(s, 2, false, page);
  expect(s.ids).toEqual([]);
});

test("shift-click selects from the last click, in page order", () => {
  let s = pick(emptySelection("k"), 4, false, page);
  s = pick(s, 2, true, page);
  expect(s.ids.sort()).toEqual([2, 3, 4]);
  // Shift-clicking a selected card clears the range from the last click.
  s = pick(s, 3, true, page);
  expect(s.ids).toEqual([4]);
});

test("shift-click with no anchor on this page toggles one card", () => {
  const s = pick({ ...emptySelection("k"), ids: [9], anchor: 9 }, 2, true, page);
  expect(s.ids).toEqual([9, 2]);
});

test("target sends picked IDs, or the search without paging", () => {
  const search = { text: "dwarf", filter: { and: [{ tag: "fantasy" }] }, page: { size: 48, number: 2 }, sort: "name" as const };
  expect(target({ ...emptySelection("k"), ids: [3, 1] }, search)).toEqual({ filter: { ids: [3, 1] } });
  expect(target({ ...emptySelection("k"), all: true }, search)).toEqual({ text: "dwarf", filter: search.filter });
  expect(target({ ...emptySelection("k"), all: true }, {})).toEqual({ filter: { archived: false } });
});

test("an all-matching semantic selection keeps the filter but not the text", () => {
  const all = { ...emptySelection("k"), all: true };
  expect(target(all, { library: 1, text: "dwarf", semantic: true, filter: { tag: "smith" } })).toEqual({ library: 1, filter: { tag: "smith" } });
  expect(target(all, { library: 1, text: "dwarf", semantic: true })).toEqual({ library: 1, filter: { archived: false } });
});
