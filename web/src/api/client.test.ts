import { loginPath, safeNext } from "./client";

test("loginPath keeps where the user was", () => {
  expect(loginPath("/cards", "?tag=elf")).toBe("/login?next=%2Fcards%3Ftag%3Delf");
});

test("loginPath doesn't loop on the login page", () => {
  expect(loginPath("/login", "?next=%2Fcards")).toBe("/login?next=%2Fcards");
});

test.each([
  ["/cards?page=2", "/cards?page=2"],
  [null, "/"],
  ["https://evil.example", "/"],
  ["//evil.example", "/"],
  ["/\\evil.example", "/"],
])("safeNext(%s) = %s", (next, want) => {
  expect(safeNext(next)).toBe(want);
});
