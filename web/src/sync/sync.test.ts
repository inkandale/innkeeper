import { bulkResolutions, changePath, dismissBinding, jsonKey, lineCounts, needsYou, optionLabel, pushSummary, queueReviews, resolution, reviewPath, rows, sentenceCount, siteOf, snapshotOf, sourceLabel, sourceRemoteId, stateCounts, wordDiff, type Comparison, type Snapshot } from "./sync";

const cmp: Comparison = {
  instance_id: 3, remote_id: "a", card_id: 1, head_revision_id: 118, accepted_hash: "", base: "none", app_hash: "fresh", fields: [],
};

const snap: Snapshot = {
  params: { instance_id: 3 },
  items: [
    {
      remote_id: "a", name: "Ann", app_hash: "ha", verdict: "exact",
      options: [{ action: "bind", card_id: 1 }, { action: "import" }, { action: "skip" }], suggested: { action: "bind", card_id: 1 },
      candidates: [{ card_id: 1, name: "Ann", reasons: { content: 0 }, differing_fields: 0, conflicts: 0 }],
    },
    { remote_id: "b", name: "Bo", app_hash: "hb", verdict: "new", binding_id: 4, options: [{ action: "import" }, { action: "skip" }], suggested: { action: "import" } },
  ],
  lorebooks: [{ remote_id: "w", name: "World", app_hash: "hw", verdict: "likely", options: [{ action: "bind", card_id: 9 }], suggested: { action: "bind", card_id: 9 } }],
  missing: null,
  unpushed: null,
  totals: { exact: 1, new: 1, likely: 1 },
};

test("a bind names its comparison and sends only the choices that aren't library", () => {
  expect(resolution("a", "ha", "bind", cmp, { "first_mes#1": "app", "description#0": "library" })).toEqual({
    remote_id: "a", action: "bind", card_id: 1, app_hash: "fresh", base_revision_id: 118, accepted_hash: "", default: "library",
    choices: { "first_mes#1": "app" },
  });
  expect(resolution("a", "ha", "import")).toEqual({ remote_id: "a", app_hash: "ha", action: "import", binding_id: 0 });
  expect(resolution("a", "ha", "import", undefined, {}, 7)).toEqual({ remote_id: "a", app_hash: "ha", action: "import", binding_id: 7 });
  expect(resolution("a", "ha", "skip", undefined, {}, 7)).toEqual({ remote_id: "a", app_hash: "ha", action: "skip" });
  expect(() => resolution("a", "ha", "bind")).toThrow();
});

test("bulk resolutions compare each bind and keep the snapshot's app hash", async () => {
  const compared: string[] = [];
  const out = await bulkResolutions(3, rows(snap), (r) => r.item.suggested!, async (r, id) => {
    compared.push(`${r.kind}/${r.item.remote_id}/${id}`);
    return { ...cmp, card_id: id };
  });
  expect(compared).toEqual(["card/a/1", "lorebook/w/9"]);
  expect(out).toEqual([
    { instance_id: 3, remote_id: "a", action: "bind", card_id: 1, app_hash: "ha", base_revision_id: 118, accepted_hash: "", default: "library" },
    { instance_id: 3, remote_id: "b", action: "import", app_hash: "hb", binding_id: 4 },
    { instance_id: 3, kind: "lorebook", remote_id: "w", action: "bind", card_id: 9, app_hash: "hw", base_revision_id: 118, accepted_hash: "", default: "library" },
  ]);
});

test("state counts fold drift, and a push summary counts new copies", () => {
  const targets = [
    { instance: 2, is: "in_sync", cards: 4 },
    { instance: 2, is: "ahead", cards: 2 },
    { instance: 2, is: "diverged", cards: 1 },
    { instance: 2, is: "remote_changed", cards: 1 },
    { instance: 2, is: "missing", cards: 1 },
    { instance: 5, is: "ahead", cards: 7 },
  ];
  const c = stateCounts(targets, 2);
  expect(c).toEqual({ in_sync: 4, ahead: 2, drifted: 2, missing: 1, cant_check: 0, bound: 9 });
  expect(pushSummary({ total: 12, new: 4, ahead: 2, in_sync: 4, cant_check: 0, drifted: 2, confirm: "q" })).toBe(
    "4 new, 2 not pushed, 4 in sync, 2 drifted (pushing overwrites their app edits)",
  );
});

test("labels, review paths, and snapshots", () => {
  expect(optionLabel({ action: "bind", card_id: 1 }, snap.items![0]!.candidates)).toBe("Bind to Ann");
  expect(optionLabel({ action: "bind", card_id: 4 })).toBe("Bind to #4");
  expect(reviewPath(3, "card", "a/b", { job: 5 })).toBe("/sync/3/card/a%2Fb?job=5");
  expect(snapshotOf({ status: "succeeded", progress: snap })).toBe(snap);
  // Job lists and streams cut the snapshot to its totals.
  expect(snapshotOf({ status: "succeeded", progress: { totals: {} } })).toBeUndefined();
  expect(snapshotOf({ status: "running", progress: { done: 1, total: 4 } })).toBeUndefined();
});

test("wordDiff marks the words each side changed and keeps the rest", () => {
  expect(wordDiff("Bradley shouts at Tiff.", "Bradley yells at Tiff and Dex.")).toEqual([
    { op: "same", text: "Bradley " },
    { op: "del", text: "shouts " },
    { op: "ins", text: "yells " },
    { op: "same", text: "at " },
    { op: "del", text: "Tiff." },
    { op: "ins", text: "Tiff and Dex." },
  ]);
  expect(wordDiff("", "New text")).toEqual([{ op: "ins", text: "New text" }]);
  expect(wordDiff("Gone now", "")).toEqual([{ op: "del", text: "Gone now" }]);
  expect(wordDiff("", "")).toEqual([]);
});

test("lineCounts counts a changed line as one removed and one added, and leaves the rest", () => {
  expect(lineCounts("A dwarf.\nShort.\nGruff.", "A dwarf.\nTall.\nBroad.\nGruff.")).toEqual({ removed: 1, added: 2 });
  expect(lineCounts("", "One\nTwo")).toEqual({ removed: 0, added: 2 });
  expect(lineCounts("One\nTwo\n", "")).toEqual({ removed: 2, added: 0 });
  expect(lineCounts("Same", "Same")).toEqual({ removed: 0, added: 0 });
});

test("wordDiff works on characters, not bytes, so curly quotes and dashes stay whole", () => {
  expect(wordDiff("“Hi,” she said—quietly.", "“Hi,” he said—quietly.")).toEqual([
    { op: "same", text: "“Hi,” " },
    { op: "del", text: "she " },
    { op: "ins", text: "he " },
    { op: "same", text: "said—quietly." },
  ]);
});

test("sentenceCount splits where the server's sentences() does", () => {
  expect(sentenceCount("One. Two. Three. ")).toBe(3);
  expect(sentenceCount("“Stop!” she said. Then left.")).toBe(3);
  expect(sentenceCount("Line one\nline two")).toBe(2);
  expect(sentenceCount(" Four. Five.")).toBe(2);
  expect(sentenceCount("")).toBe(0);
});

test("jsonKey names a hunk by its path inside the field", () => {
  expect(jsonKey("extensions", "extensions.depth_prompt.depth#0")).toBe("depth_prompt.depth");
  expect(jsonKey("extensions", "extensions#0")).toBe("");
});

test("a followed source's state names its site", () => {
  expect(siteOf("chub:alice/vyr")).toBe("chub");
  expect(siteOf("odd")).toBe("odd");
  expect(sourceRemoteId({ kind: "character-tavern", ref: "u/vyr" })).toBe("character-tavern:u/vyr");
  expect(sourceLabel("update", "risu:abc")).toBe("update on risu");
  expect(sourceLabel("changed", "risu:abc")).toBe("update on risu");
  expect(sourceLabel("missing", "chub:a/b")).toBe("no longer on chub");
  expect(sourceLabel("unchanged", "chub:a/b")).toBe("up to date");
});

test("a source update reviews against its card or lorebook, three ways", () => {
  const c = { instance_id: 4, push: false, state: "update" as const, remote_id: "chub:alice/vyr", binding_id: 21 };
  expect(changePath({ ...c, card_id: 8 })).toBe("/sync/4/card/chub%3Aalice%2Fvyr?card=8&queue=1");
  expect(changePath({ ...c, lorebook_id: 9 }, { state: "update" })).toBe("/sync/4/lorebook/chub%3Aalice%2Fvyr?card=9&queue=1&state=update");
  // A world book from a snapshot reviews on the lorebook route, and an ahead one compares without the accepted copy.
  expect(changePath({ ...c, state: "differs", job_id: 7, kind: "lorebook", lorebook_id: 9 })).toBe("/sync/4/lorebook/chub%3Aalice%2Fvyr?job=7&queue=1");
  expect(changePath({ ...c, state: "ahead", kind: "lorebook", lorebook_id: 9 })).toBe("/sync/4/lorebook/chub%3Aalice%2Fvyr?card=9&base=none&queue=1");
  expect(queueReviews([{ lorebook_id: 9, apps: [c] }])).toEqual([{ ...c, card_id: undefined, lorebook_id: 9 }]);
});

test("dismissBinding sends the app_hash the owner saw", async () => {
  let sent: { url: string; body: string } | undefined;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      sent = { url: req.url, body: await req.text() };
      return new Response(JSON.stringify({ id: 21 }), { status: 200, headers: { "Content-Type": "application/json" } });
    }),
  );
  await dismissBinding(21, "site-hash");
  expect(sent?.url).toMatch(/\/api\/bindings\/21\/dismiss$/);
  expect(JSON.parse(sent?.body ?? "")).toEqual({ app_hash: "site-hash" });
  vi.unstubAllGlobals();
});

test("an edit made only in the app needs the owner, like other drift", () => {
  expect(needsYou({ remote_changed: 1, both_changed: 2, ahead: 5 })).toBe(3);
});
