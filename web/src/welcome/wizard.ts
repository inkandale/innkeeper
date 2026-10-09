import type { components } from "../api/schema";

type CatalogSource = components["schemas"]["CatalogSourceOut"];
type CatalogPlugin = components["schemas"]["CatalogPluginOut"];

export type Offer = CatalogPlugin & { source: string };
export type Group = { category: string; plugins: Offer[] };

// Categories the wizard knows, in the order it shows them.
const known = ["Apps", "Import", "Login"];

/** wizardPlugin reports whether the wizard offers a plugin: codecs are built in, themes live in Appearance. */
export function wizardPlugin(p: { capabilities?: string[] | null; category?: string }): boolean {
  const caps = p.capabilities ?? [];
  return !caps.includes("codec") && !caps.includes("theme") && p.category !== "Formats";
}

/** groupCatalog groups the plugins every source offers by category; the first source to offer a name wins. */
export function groupCatalog(sources: CatalogSource[]): Group[] {
  const seen = new Set<string>();
  const byCategory = new Map<string, Offer[]>();
  for (const s of sources) {
    for (const p of s.plugins ?? []) {
      if (seen.has(p.name) || !wizardPlugin(p)) continue;
      seen.add(p.name);
      const c = p.category || "Other";
      byCategory.set(c, [...(byCategory.get(c) ?? []), { ...p, source: s.source.id }]);
    }
  }
  const rank = (c: string) => (known.includes(c) ? known.indexOf(c) : c === "Other" ? Infinity : known.length);
  return [...byCategory]
    .sort(([a], [b]) => rank(a) - rank(b) || a.localeCompare(b))
    .map(([category, plugins]) => ({ category, plugins }));
}

type Installed = { name: string; source: string; capabilities?: string[] | null; category?: string };

/** setupPlugins is the installed plugins the wizard sets up: not built in, and ones it offers. */
export function setupPlugins<P extends Installed>(plugins: P[]): P[] {
  return plugins.filter((p) => p.source !== "builtin" && wizardPlugin(p));
}

/** nextStep is the first plugin after current with no instance yet, or the cards step when none is left. */
export function nextStep(plugins: Installed[], instances: { plugin: string }[], current?: string): string {
  const chosen = setupPlugins(plugins);
  const have = new Set(instances.map((i) => i.plugin));
  const next = chosen.slice(chosen.findIndex((p) => p.name === current) + 1).find((p) => !have.has(p.name));
  return next ? `/welcome/setup/${encodeURIComponent(next.name)}` : "/welcome/cards";
}
