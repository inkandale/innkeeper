import type { components } from "../api/schema";

type CatalogSource = components["schemas"]["CatalogSourceOut"];
type CatalogPlugin = components["schemas"]["CatalogPluginOut"];

export type Offer = CatalogPlugin & { source: string };
type Group = { category: string; plugins: Offer[] };

/** wizardPlugin reports whether the wizard offers a plugin: codecs are built in, themes live in Appearance. */
export function wizardPlugin(p: { capabilities?: string[] | null }): boolean {
  const caps = p.capabilities ?? [];
  return !caps.includes("codec") && !caps.includes("theme");
}

/**
 * groupCatalog groups the plugins every source offers by category; the first source to offer a name wins.
 * Categories go in the order the sources' indexes list them, then alphabetically, then Other.
 */
export function groupCatalog(sources: CatalogSource[]): Group[] {
  const seen = new Set<string>();
  const byCategory = new Map<string, Offer[]>();
  const known = [...new Set(sources.flatMap((s) => s.categories ?? []))];
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

/** recommended reports whether the official source recommends an offer. */
export function recommended(o: Offer): boolean {
  return o.source === "official" && !!o.recommended;
}

const capsKey = (o: Offer) => [...(o.capabilities ?? [])].sort().join(",");

/** alternatives reports whether a group's offers all do the same job, so the owner picks the ones they use. */
export function alternatives(g: Group): boolean {
  return g.plugins.length > 1 && new Set(g.plugins.map(capsKey)).size === 1;
}

/**
 * startChosen is the offers the wizard starts with chosen: the recommended ones, except where a category
 * recommends several that do the same job, such as two apps; the owner picks among those.
 */
export function startChosen(groups: Group[]): Set<string> {
  const chosen = new Set<string>();
  for (const g of groups) {
    const rec = g.plugins.filter(recommended);
    for (const o of rec) if (rec.filter((r) => capsKey(r) === capsKey(o)).length === 1) chosen.add(o.name);
  }
  return chosen;
}

/** pickGroups keeps the offers keep accepts, dropping groups left empty. */
export function pickGroups(groups: Group[], keep: (o: Offer) => boolean): Group[] {
  return groups.map((g) => ({ ...g, plugins: g.plugins.filter(keep) })).filter((g) => g.plugins.length > 0);
}

type Capable = { name: string; capabilities?: string[] | null };
type Installed = Capable & { source: string; needs?: string[] | null };

const offering = (cap: string) => (p: Capable) => !!p.capabilities?.includes(cap);

/**
 * choose toggles name in chosen. Choosing a plugin also chooses, for each need that no chosen or installed
 * plugin meets, the first offer that does.
 */
export function choose(chosen: Set<string>, name: string, offers: Offer[], installed: Capable[]): Set<string> {
  const next = new Set(chosen);
  if (next.delete(name)) return next;
  next.add(name);
  const have = [...installed, ...offers.filter((o) => next.has(o.name))];
  for (const need of offers.find((o) => o.name === name)?.needs ?? []) {
    if (have.some(offering(need))) continue;
    const provider = offers.find(offering(need));
    if (provider) next.add(provider.name);
  }
  return next;
}

/** providerTitles lists, for each of o's needs, the offers that meet it, as "A or B". */
export function providerTitles(o: Offer, offers: Offer[]): string[] {
  return (o.needs ?? [])
    .map((need) =>
      offers
        .filter(offering(need))
        .map((p) => p.title || p.name)
        .join(" or "),
    )
    .filter(Boolean);
}

/** setupPlugins is the installed plugins the wizard sets up, not built in and ones it offers, each after the plugins that meet its needs. */
export function setupPlugins<P extends Installed>(plugins: P[]): P[] {
  const list = plugins.filter((p) => p.source !== "builtin" && wizardPlugin(p));
  const ordered: P[] = [];
  // path stops plugins that need each other from recursing forever.
  const visit = (p: P, path: Set<P>) => {
    if (ordered.includes(p) || path.has(p)) return;
    path.add(p);
    for (const need of p.needs ?? []) list.filter((q) => q !== p && offering(need)(q)).forEach((q) => visit(q, path));
    ordered.push(p);
  };
  list.forEach((p) => visit(p, new Set()));
  return ordered;
}

/** nextStep is the first plugin after current with no instance yet, or the cards step when none is left. */
export function nextStep(plugins: Installed[], instances: { plugin: string }[], current?: string): string {
  const chosen = setupPlugins(plugins);
  const have = new Set(instances.map((i) => i.plugin));
  const next = chosen.slice(chosen.findIndex((p) => p.name === current) + 1).find((p) => !have.has(p.name));
  return next ? `/welcome/setup/${encodeURIComponent(next.name)}` : "/welcome/cards";
}
