import { ApiError, loginPath, mustOk, safeNext, thumb } from "./client";

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
  ["/\t/evil.example", "/"],
  ["/\n/evil.example", "/"],
  ["http://[", "/"],
])("safeNext(%s) = %s", (next, want) => {
  expect(safeNext(next)).toBe(want);
});

test("thumb asks for a resized image", () => {
  expect(thumb("ab".repeat(32), 160)).toBe("/api/blobs/" + "ab".repeat(32) + "?w=160");
});

test("mustOk throws the problem detail on an error status", () => {
  const ok = { error: undefined, response: new Response(null, { status: 204 }) };
  expect(() => mustOk(ok)).not.toThrow();
  const bad = { error: { detail: "nope" }, response: new Response(null, { status: 409 }) };
  expect(() => mustOk(bad)).toThrow(new ApiError(409, "nope"));
});
