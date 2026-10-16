import type { components } from "../api/schema";
import { progressText, type Job } from "../settings/jobUtil";

export type Catalog = components["schemas"]["CatalogOut"];
export type CatalogHit = components["schemas"]["CatalogHit"];
export type CatalogFilter = components["schemas"]["CatalogFilter"];
/** FilterValues are a search's filter values by filter name, as the search route's filters parameter takes them. */
export type FilterValues = Record<string, unknown>;

/** itemPath is an item's page. Item IDs can hold "/", so the ID is escaped into one segment. */
export const itemPath = (catalog: number, item: string) => `/archives/${catalog}/items/${encodeURIComponent(item)}`;

/** libraryMatch is the library item a result links to: the same-content match if any, else the first near match. */
export function libraryMatch(hit: CatalogHit): { label: string; id: number } | undefined {
  const matches = hit.library ?? [];
  const best = matches.find((m) => m.reason === "content") ?? matches[0];
  if (!best) return undefined;
  return { label: best.reason === "content" ? "In library" : "Similar in library", id: best.id };
}

/** thumbnailSrc is the image URL for a catalog item's inline base64 JPEG. */
export const thumbnailSrc = (thumbnail: string) => `data:image/jpeg;base64,${thumbnail}`;

const counts = ["added", "updated", "removed", "skipped"] as const;
export const capitalized = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** ingestSummary is an ingest job's state: its counts once it succeeds, its progress while it runs, its error if it failed. */
export function ingestSummary(j: Job): string {
  const state = capitalized(j.status);
  if (j.status === "failed") return `${state}: ${j.error ?? "unknown error"}`;
  if (j.status === "succeeded") {
    const p = (j.progress ?? {}) as Record<string, unknown>;
    return `${state}: ${counts.map((c) => `${Number(p[c] ?? 0)} ${c}`).join(", ")}`;
  }
  const done = progressText(j);
  return done ? `${state}: ${done}` : state;
}
