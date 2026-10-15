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
    ["Entry 1 · anvil", "changed"],
    ["Entry 2 · bellows", "removed"],
    ["Entry 3 · hammer", "added"],
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

test("duplicate ids and key sets don't pair entries; unique keys still do", () => {
  const a = { id: 0, keys: ["a"], content: "A." };
  const b = { id: 0, keys: ["b"], content: "B." };
  const start = { entries: [a, b] };
  const proposal = { entries: [{ ...b, content: "B2." }, { ...a, content: "A2." }] };
  expect(pairEntries(start.entries, proposal.entries)).toEqual([
    [0, 1],
    [1, 0],
  ]);
  const { rows, apply } = lorebookSubject.review(start, start, proposal);
  expect(rows.map((r) => r.change)).toEqual(["changed", "changed"]);
  expect(apply(new Set([rows[0]!.key]))).toEqual({ entries: [{ ...a, content: "A2." }, b] });

  // Two entries share keys on both sides: neither keys nor ids tell them apart.
  const x = { keys: ["k"], content: "X." };
  const y = { keys: ["k"], content: "Y." };
  expect(pairEntries([x, y], [{ ...y, content: "Y2." }, { ...x, content: "X2." }])).toEqual([
    [0, -1],
    [1, -1],
    [-1, 0],
    [-1, 1],
  ]);
});

test("rows for entries with equal keys get distinct labels", () => {
  const x = { keys: ["k"], content: "X." };
  const y = { keys: ["k"], content: "Y." };
  const { rows } = lorebookSubject.review({ entries: [x, y] }, { entries: [x, y] }, { entries: [{ ...y, content: "Y2." }, { ...x, content: "X2." }] });
  expect(new Set(rows.map((r) => `${r.label} ${r.change}`)).size).toBe(4);
});
