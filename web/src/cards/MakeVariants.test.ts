import { sharedSource } from "./MakeVariants";

const chub = (ref: string) => ({ kind: "chub", ref });

test("sharedSource needs one source in every list, all loaded", () => {
  expect(sharedSource([[chub("a"), chub("b")], [chub("b")]])).toBe(true);
  expect(sharedSource([[chub("a")], [chub("b")]])).toBe(false);
  expect(sharedSource([[chub("a")], undefined])).toBe(false);
  expect(sharedSource([[], []])).toBe(false);
});
