import { age, kindLabel, progressText, type Job } from "./jobUtil";

const job = (j: Partial<Job>): Job => ({ id: 1, kind: "bulk", status: "running", payload: {}, progress: {}, attempts: 1, max_attempts: 3, run_after: "", created_at: "", ...j }) as Job;

test("progressText shows children, then done of total", () => {
  expect(progressText(job({ children: { succeeded: 2, failed: 1, queued: 3 } }))).toBe("3 of 6");
  expect(progressText(job({ kind: "card.export", progress: { done: 4, total: 10 } }))).toBe("4 of 10");
  expect(progressText(job({ progress: {} }))).toBe("");
});

test("kindLabel names a bulk job's action", () => {
  expect(kindLabel(job({ payload: { action: "card.tag" } }))).toBe("bulk card.tag");
  expect(kindLabel(job({ kind: "target.push" }))).toBe("target.push");
});

test("age is short", () => {
  const now = Date.parse("2026-10-06T12:00:00Z");
  expect(age("2026-10-06T11:59:30Z", now)).toBe("30s");
  expect(age("2026-10-06T11:15:00Z", now)).toBe("45m");
  expect(age("2026-10-06T02:00:00Z", now)).toBe("10h");
  expect(age("2026-10-02T12:00:00Z", now)).toBe("4d");
});
