import { ApiError, exportFormats, followers, loginPath, mustOk, route, safeNext, thumb, upload, withCapability } from "./client";

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

test("upload sends the raw file and returns its sha256", async () => {
  let sent: { type: string | null; body: string } | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      sent = { type: req.headers.get("Content-Type"), body: await req.text() };
      return new Response(JSON.stringify({ sha256: "ab".repeat(32) }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  expect(await upload(new Blob(["png bytes"]))).toBe("ab".repeat(32));
  expect(sent).toEqual({ type: "application/octet-stream", body: "png bytes" });
  vi.unstubAllGlobals();
});

test("route swaps a card path for a lorebook one", () => {
  expect(route("card", "/cards/{id}/revisions")).toBe("/cards/{id}/revisions");
  expect(route("lorebook", "/cards/{id}/revisions")).toBe("/lorebooks/{id}/revisions");
});

test("withCapability keeps instances whose plugin offers any of the capabilities", () => {
  const plugins = [
    { name: "app", capabilities: ["push", "pull"] },
    { name: "puller", capabilities: ["pull"] },
    { name: "theme", capabilities: ["theme"] },
    { name: "bare", capabilities: null },
  ];
  const instances = [{ plugin: "app" }, { plugin: "puller" }, { plugin: "theme" }, { plugin: "bare" }, { plugin: "gone" }];
  expect(withCapability(instances, plugins, "push")).toEqual([{ plugin: "app" }]);
  expect(withCapability(instances, plugins, "push", "pull")).toEqual([{ plugin: "app" }, { plugin: "puller" }]);
  expect(withCapability(instances, undefined, "push")).toEqual([]);
});

test("exportFormats lists json, then what working codec instances write", () => {
  const plugins = [
    { name: "card-codec", capabilities: ["codec"], formats: ["png", "json-v2"] },
    { name: "other-codec", capabilities: ["codec"], formats: ["png", "charx"] },
    { name: "broken-codec", capabilities: ["codec"], formats: ["yaml"] },
    { name: "uninstanced", capabilities: ["codec"], formats: ["webp"] },
  ];
  const instances = [{ plugin: "card-codec" }, { plugin: "other-codec" }, { plugin: "broken-codec", broken_error: "down" }];
  expect(exportFormats(plugins, instances)).toEqual(["json", "png", "json-v2", "charx"]);
  expect(exportFormats(undefined, undefined)).toEqual(["json"]);
});

test("followers are the instances whose plugin pulls without pushing", () => {
  const plugins = [
    { name: "app", capabilities: ["push", "pull"] },
    { name: "url-import", capabilities: ["import", "sites", "pull"] },
    { name: "codec", capabilities: ["codec"] },
  ];
  const instances = [{ id: 1, plugin: "app" }, { id: 2, plugin: "url-import" }, { id: 3, plugin: "codec" }];
  expect([...followers(instances, plugins)]).toEqual([2]);
  expect(followers(instances, undefined).size).toBe(0);
});
