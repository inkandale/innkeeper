import { recommendParent } from "./SetParent";

test("recommendParent prefers the root of the biggest family", () => {
  const picked = [
    { id: 3, name: "a" },
    { id: 5, name: "b", family: { root_id: 5, size: 2, matched: null } },
    { id: 9, name: "c", family: { root_id: 9, size: 4, matched: null } },
  ];
  expect(recommendParent(picked)).toBe(9);
});

test("recommendParent falls back to the oldest", () => {
  // 4 fronts a family rooted elsewhere, so it's no root to join.
  expect(recommendParent([{ id: 8, name: "a" }, { id: 4, name: "b", family: { root_id: 2, size: 3, matched: null } }])).toBe(4);
});
