import { groupCatalog, wizardPlugin } from "./welcome";

const entry = (name: string, category?: string, capabilities = ["push"]) => ({
  name, version: "1.0.0", protocol: 1, capabilities, description: name, versions: ["1.0.0"], category,
});

test("groupCatalog orders known categories first, then others, then Other", () => {
  const groups = groupCatalog([
    { source: { id: "official", url: "u" }, plugins: [
      entry("z-tool", "Tools"), entry("card-import", "Import", ["import"]), entry("lumiverse", "Apps"),
      entry("nameless"), entry("card-codec", "Formats", ["codec"]), entry("dark", "Themes", ["theme"]),
      entry("oidc", "Login", ["auth"]),
    ] },
    { source: { id: "mine", url: "v" }, plugins: [entry("lumiverse", "Apps"), entry("extra", "Apps")] },
  ]);
  expect(groups.map((g) => g.category)).toEqual(["Apps", "Import", "Login", "Tools", "Other"]);
  expect(groups[0]!.plugins.map((p) => [p.name, p.source])).toEqual([["lumiverse", "official"], ["extra", "mine"]]);
});

test("wizardPlugin leaves out codecs and themes", () => {
  expect(wizardPlugin({ capabilities: ["codec"] })).toBe(false);
  expect(wizardPlugin({ capabilities: ["theme"] })).toBe(false);
  expect(wizardPlugin({ capabilities: ["push", "pull"] })).toBe(true);
});
