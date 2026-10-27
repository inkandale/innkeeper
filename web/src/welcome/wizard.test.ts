import { choose, groupCatalog, nextStep, providerTitles, setupPlugins, wizardPlugin, type Offer } from "./wizard";

const entry = (name: string, category?: string, capabilities = ["push"]) => ({
  name, version: "1.0.0", protocol: 1, capabilities, description: name, versions: ["1.0.0"], category,
});

test("groupCatalog orders the index's categories first, then others, then Other", () => {
  const groups = groupCatalog([
    { source: { id: "official", url: "u" }, categories: ["Apps", "Import", "Login"], plugins: [
      entry("z-tool", "Tools"), entry("card-import", "Import", ["import"]), entry("lumiverse", "Apps"),
      entry("nameless"), entry("card-codec", "Formats", ["codec"]), entry("dark", "Themes", ["theme"]),
      entry("oidc", "Login", ["auth"]),
    ] },
    { source: { id: "mine", url: "v" }, categories: ["Tools", "Apps"], plugins: [entry("lumiverse", "Apps"), entry("extra", "Apps")] },
  ]);
  expect(groups.map((g) => g.category)).toEqual(["Apps", "Import", "Login", "Tools", "Other"]);
  expect(groups[0]!.plugins.map((p) => [p.name, p.source])).toEqual([["lumiverse", "official"], ["extra", "mine"]]);
});

test("groupCatalog sorts categories alphabetically when no index orders them", () => {
  const groups = groupCatalog([
    { source: { id: "official", url: "u" }, plugins: [entry("a", "Login"), entry("b", "Apps"), entry("c"), entry("d", "Import")] },
  ]);
  expect(groups.map((g) => g.category)).toEqual(["Apps", "Import", "Login", "Other"]);
});

test("wizardPlugin leaves out codecs and themes", () => {
  expect(wizardPlugin({ capabilities: ["codec"] })).toBe(false);
  expect(wizardPlugin({ capabilities: ["theme"] })).toBe(false);
  expect(wizardPlugin({ capabilities: ["push", "pull"] })).toBe(true);
});

test("nextStep goes to the next chosen plugin without an instance, then to cards", () => {
  const plugins = [
    { name: "card-codec", source: "builtin", capabilities: ["codec"] },
    { name: "a", source: "official", capabilities: ["push"] },
    { name: "b", source: "official", capabilities: ["push"] },
    { name: "c", source: "official", capabilities: ["push"] },
  ];
  expect(nextStep(plugins, [{ plugin: "a" }])).toBe("/welcome/setup/b");
  expect(nextStep(plugins, [], "b")).toBe("/welcome/setup/c");
  expect(nextStep(plugins, [{ plugin: "c" }], "b")).toBe("/welcome/cards");
  expect(nextStep(plugins.slice(0, 1), [])).toBe("/welcome/cards");
});

const offer = (name: string, capabilities: string[], needs?: string[]): Offer => ({
  ...entry(name, undefined, capabilities), title: name.toUpperCase(), needs, source: "official",
});

test("choose also chooses a provider for each need nothing chosen or installed offers", () => {
  const offers = [offer("forge", ["action"], ["ai"]), offer("ai", ["ai"]), offer("ai2", ["ai"])];
  expect([...choose(new Set(), "forge", offers, [])]).toEqual(["forge", "ai"]);
  expect([...choose(new Set(["ai2"]), "forge", offers, [])]).toEqual(["ai2", "forge"]);
  expect([...choose(new Set(), "forge", offers, [{ name: "local-ai", capabilities: ["ai"] }])]).toEqual(["forge"]);
  expect([...choose(new Set(["forge", "ai"]), "forge", offers, [])]).toEqual(["ai"]);
});

test("providerTitles names what can meet each need", () => {
  const offers = [offer("forge", ["action"], ["ai"]), offer("ai", ["ai"]), offer("ai2", ["ai"])];
  expect(providerTitles(offers[0]!, offers)).toEqual(["AI or AI2"]);
  expect(providerTitles(offers[1]!, offers)).toEqual([]);
});

test("setupPlugins sets up providers before the plugins that need them", () => {
  const plugins = [
    { name: "forge", source: "official", capabilities: ["action"], needs: ["ai"] },
    { name: "lumiverse", source: "official", capabilities: ["push"] },
    { name: "ai", source: "official", capabilities: ["ai"] },
  ];
  expect(setupPlugins(plugins).map((p) => p.name)).toEqual(["ai", "forge", "lumiverse"]);
});
