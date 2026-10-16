import type { Job } from "../settings/jobUtil";
import { ingestSummary, itemPath, libraryLabel, type CatalogHit } from "./archive";

const hit = (library: CatalogHit["library"]): CatalogHit => ({
  id: "a", kind: "card", name: "A", latest_version: "1", hashes: { content_hash: "0".repeat(64) }, library,
});

const job = (over: Partial<Job>): Job => ({
  id: 4, kind: "catalog.ingest", status: "succeeded", attempts: 1, max_attempts: 3,
  created_at: "2026-10-16T00:00:00Z", run_after: "2026-10-16T00:00:00Z", payload: {}, progress: {}, ...over,
});

test("itemPath keeps an item ID with a slash in one segment", () => {
  expect(itemPath(2, "sub/a.png")).toBe("/archives/2/items/sub%2Fa.png");
});

test("libraryLabel says In library for a content match, Similar for a near one, and nothing without matches", () => {
  expect(libraryLabel(hit([{ id: 1, reason: "text", distance: 3 }, { id: 2, reason: "content", distance: 0 }]))).toBe("In library");
  expect(libraryLabel(hit([{ id: 1, reason: "avatar", distance: 5 }]))).toBe("Similar in library");
  expect(libraryLabel(hit([]))).toBeUndefined();
  expect(libraryLabel(hit(null))).toBeUndefined();
});

test("ingestSummary gives a finished ingest's counts, a running one's progress, and a failed one's error", () => {
  expect(ingestSummary(job({ progress: { added: 3, updated: 1, removed: 0, skipped: 2 } }))).toBe("Succeeded: 3 added, 1 updated, 0 removed, 2 skipped");
  expect(ingestSummary(job({ status: "running", progress: { done: 10, total: 40 } }))).toBe("Running: 10 of 40");
  expect(ingestSummary(job({ status: "queued", progress: {} }))).toBe("Queued");
  expect(ingestSummary(job({ status: "failed", error: "dump not found" }))).toBe("Failed: dump not found");
});
