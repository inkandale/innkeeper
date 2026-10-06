import { pageLinks } from "./pages";

test("short ranges list every page", () => {
  expect(pageLinks(2, 5)).toEqual([1, 2, 3, 4, 5]);
});

test("long ranges keep the ends and two either side of the current page", () => {
  expect(pageLinks(10, 20)).toEqual([1, "…", 8, 9, 10, 11, 12, "…", 20]);
  expect(pageLinks(1, 20)).toEqual([1, 2, 3, "…", 20]);
  expect(pageLinks(4, 20)).toEqual([1, 2, 3, 4, 5, 6, "…", 20]);
});
