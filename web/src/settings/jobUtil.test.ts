import { age, importText, progressText, span, type Job } from "./jobUtil";

const job = (j: Partial<Job>): Job => ({ id: 1, kind: "bulk", status: "running", payload: {}, progress: {}, attempts: 1, max_attempts: 3, run_after: "", created_at: "", ...j }) as Job;

test("progressText shows children, then done of total", () => {
  expect(progressText(job({ children: { succeeded: 2, failed: 1, queued: 3 } }))).toBe("3 of 6");
  expect(progressText(job({ kind: "card.export", progress: { done: 4, total: 10 } }))).toBe("4 of 10");
  expect(progressText(job({ kind: "enrich", progress: { done: 3, failed: 1, skipped: 6, total: 10 } }))).toBe("3 of 10, 1 failed, 6 skipped");
  expect(progressText(job({ kind: "enrich", progress: { done: 4, skipped: 0, total: 4 } }))).toBe("4 of 4");
  expect(progressText(job({ progress: {} }))).toBe("");
});

test("importText counts what a finished import did, or says it found nothing", () => {
  const done = (progress: object) => job({ kind: "import", status: "succeeded", progress });
  expect(importText(done({ imported: 3, revised: 0, pending: 0, skipped: 1, failed: 0 }))).toBe("3 imported, 1 skipped");
  expect(importText(done({ imported: 0, revised: 2, pending: 1, skipped: 0, failed: 1 }))).toBe("2 updated, 1 waiting for review, 1 failed");
  expect(importText(done({ imported: 0, revised: 0, pending: 0, skipped: 0, failed: 0 }))).toBe("Nothing found");
  expect(importText(job({ kind: "import", status: "running", progress: { imported: 1 } }))).toBe("");
  expect(importText(job({ kind: "target.reconcile", status: "succeeded", progress: { totals: {} } }))).toBe("");
});

test("age is short", () => {
  const now = Date.parse("2026-10-06T12:00:00Z");
  expect(age("2026-10-06T11:59:30Z", now)).toBe("30s");
  expect(age("2026-10-06T11:15:00Z", now)).toBe("45m");
  expect(age("2026-10-06T02:00:00Z", now)).toBe("10h");
  expect(age("2026-10-02T12:00:00Z", now)).toBe("4d");
});

test("span is short", () => {
  expect(span(0)).toBe("0s");
  expect(span(45_000)).toBe("45s");
  expect(span(90 * 60_000)).toBe("1h");
});
