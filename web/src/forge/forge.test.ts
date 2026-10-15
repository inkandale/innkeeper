import { changedKeys, instructionAction, lockOf, newAction, overlaps, refineInstruction, scopeOf, type Action } from "./forge";

const act = (a: Partial<Action> & { id: string }): Action => ({ label: a.id, applies: "card", instance_id: 1, instance_name: "Local", ...a });
const tighten = act({ id: "tighten", label: "Tighten", fields: ["description", "personality"] });
const revise = act({ id: "revise", label: "Reforge", instruction: true });
const rewrite = act({ id: "rewrite", label: "Rewrite…", instruction: true, fields: ["description"] });
const evaluate = act({ id: "evaluate", label: "Evaluate", report_only: true });
const generate = act({ id: "generate", label: "Forge", applies: "new", instruction: true });

test("scope is the field, the whole card, or nothing for a report", () => {
  expect(scopeOf(tighten, "description")).toEqual(["description"]);
  expect(scopeOf(revise)).toBe("all");
  expect(scopeOf(evaluate)).toEqual([]);
});

test("field locks follow each run's scope", () => {
  const field = lockOf([{ scope: scopeOf(tighten, "description"), label: "Tighten" }]);
  expect(field("description")).toBe("Tighten");
  expect(field("personality")).toBeUndefined();
  const whole = lockOf([{ scope: scopeOf(revise), label: "Reforge" }]);
  expect(whole("personality")).toBe("Reforge");
  expect(whole("name")).toBe("Reforge");
  const report = lockOf([{ scope: scopeOf(evaluate), label: "Evaluate" }]);
  expect(report("description")).toBeUndefined();
});

test("scopes overlap on a shared key, and the whole card overlaps any field", () => {
  expect(overlaps("all", ["description"])).toBe(true);
  expect(overlaps("all", "all")).toBe(true);
  expect(overlaps("all", [])).toBe(false);
  expect(overlaps(["description"], ["personality"])).toBe(false);
  expect(overlaps(["description"], ["description"])).toBe(true);
});

test("the instruction box runs the whole-card action with an instruction, not a field one", () => {
  expect(instructionAction([rewrite, tighten, revise, generate])?.id).toBe("revise");
  expect(instructionAction([rewrite, tighten])).toBeUndefined();
  expect(newAction([revise, generate])?.id).toBe("generate");
});

test("changedKeys lists added, changed, and removed keys", () => {
  expect(changedKeys({ a: "1", b: "2", c: ["x"], d: "gone" }, { a: "1", b: "3", c: ["x"], e: "new" })).toEqual(["b", "d", "e"]);
});

test("refineInstruction lists the ticked findings, then the suggestions", () => {
  const label = (k: string) => k.toUpperCase();
  const out = refineInstruction(
    [
      { severity: "high", text: "Too short.", fields: ["description", "scenario"] },
      { severity: "low", text: "Misplaced.", fields: ["scenario"], move: { excerpt: "She owns a ship", to: "description" } },
    ],
    ["Give her a rival."],
    label,
  );
  expect(out).toBe(
    "Fix these problems:\n- DESCRIPTION, SCENARIO: Too short.\n- SCENARIO: Misplaced. Move “She owns a ship” to DESCRIPTION.\n\nApply these suggestions:\n- Give her a rival.",
  );
  expect(refineInstruction([], ["One."], label)).toBe("Apply these suggestions:\n- One.");
});
