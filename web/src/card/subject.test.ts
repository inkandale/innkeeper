import { cardSubject } from "./subject";

test("a card review has a row per changed field, and applies only the taken ones", () => {
  const start = { name: "Brakka", description: "A dwarf.", scenario: "A forge." };
  const draft = { ...start, scenario: "A cold forge." };
  const { rows, apply } = cardSubject.review(start, draft, { name: "Brakka", description: "A tall dwarf.", scenario: "A hot forge." });
  expect(rows.map((r) => [r.key, r.label, r.edited])).toEqual([
    ["description", "Description", false],
    ["scenario", "Scenario", true],
  ]);
  expect(rows[1]).toMatchObject({ mine: "A cold forge.", before: "A forge.", after: "A hot forge." });
  expect(apply(new Set(["description"]))).toEqual({ description: "A tall dwarf." });
});
