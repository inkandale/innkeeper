import { lorebookSubject, pairEntries } from "./subject";

const anvil = { keys: ["anvil"], content: "Heavy." };
const bellows = { keys: ["bellows"], content: "Airy." };
const tongs = { keys: ["tongs"], content: "Grips." };

test("entries pair by id, then identical content, then keys", () => {
  const a = [{ id: 1, ...anvil }, tongs, bellows, { keys: ["gone"], content: "Old." }];
  const b = [{ ...bellows, content: "Windy." }, tongs, { id: 1, ...anvil, content: "Heavier." }, { keys: ["new"], content: "New." }];
  expect(pairEntries(a, b)).toEqual([
    [0, 2],
    [1, 1],
    [2, 0],
    [3, -1],
    [-1, 3],
  ]);
});

test("a lorebook review has a row per added, removed, and changed entry", () => {
  const start = { name: "Forge", description: "Smithing.", entries: [anvil, bellows, tongs] };
  const proposal = { name: "Forge", description: "Smithing lore.", entries: [{ ...anvil, content: "Heavier." }, tongs, { keys: ["hammer"], content: "Rings." }] };
  const { rows } = lorebookSubject.review(start, start, proposal);
  expect(rows.map((r) => [r.label, r.change])).toEqual([
    ["Description", undefined],
    ["Entry anvil", "changed"],
    ["Entry bellows", "removed"],
    ["Entry hammer", "added"],
  ]);
  expect(rows[1]).toMatchObject({ before: "Keys: anvil\n\nHeavy.", after: "Keys: anvil\n\nHeavier." });
  expect(rows[2]!.after).toBeUndefined();
  expect(rows[3]!.mine).toBeUndefined();
});

test("Apply merges only the taken entry rows into the draft", () => {
  const start = { entries: [anvil, bellows, tongs] };
  const proposal = { entries: [{ ...anvil, content: "Heavier." }, tongs, { keys: ["hammer"], content: "Rings." }] };
  // The owner added an entry while the run went.
  const draft = { entries: [anvil, bellows, tongs, { keys: ["quench"], content: "Hiss." }] };
  const { rows, apply } = lorebookSubject.review(start, draft, proposal);
  const key = (change: string) => rows.find((r) => r.change === change)!.key;
  expect(apply(new Set([key("added")]))).toEqual({ entries: [...draft.entries, { keys: ["hammer"], content: "Rings." }] });
  expect(apply(new Set([key("removed"), key("changed")]))).toEqual({ entries: [{ ...anvil, content: "Heavier." }, tongs, { keys: ["quench"], content: "Hiss." }] });
  expect(apply(new Set())).toEqual({});
});

test("an entry edited since the run started is marked edited", () => {
  const start = { entries: [anvil, bellows] };
  const draft = { entries: [{ ...anvil, content: "Very heavy." }, bellows] };
  const { rows, apply } = lorebookSubject.review(start, draft, { entries: [{ ...anvil, content: "Heavier." }, bellows] });
  expect(rows).toHaveLength(1);
  expect(rows[0]).toMatchObject({ edited: true, mine: "Keys: anvil\n\nVery heavy." });
  expect(apply(new Set([rows[0]!.key]))).toEqual({ entries: [{ ...anvil, content: "Heavier." }, bellows] });
});

test("an entry's other changed properties show in its text", () => {
  const start = { entries: [{ ...anvil, enabled: true }] };
  const { rows } = lorebookSubject.review(start, start, { entries: [{ ...anvil, enabled: false }] });
  expect(rows[0]!.after).toBe("Keys: anvil\n\nHeavy.\n\nenabled: false");
});
