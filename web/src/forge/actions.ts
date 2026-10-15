import type { components } from "../api/schema";
import { same, type Data } from "../card/draft";

export type Action = components["schemas"]["PluginAction"];

// A report, result, and progress match internal/actions and protocol.Report; the job schema leaves them untyped.
export type Severity = "high" | "medium" | "low";
export type Finding = { severity: Severity; text: string; fields?: string[] | null; move?: { excerpt?: string; to: string } | null };
export type Dimension = { key: string; label: string; score: number; comment?: string };
export type Report = { summary?: string; score?: number | null; dimensions?: Dimension[] | null; findings?: Finding[] | null; suggestions?: string[] | null };
export type Result = { proposal?: Data | null; report?: Report | null; raw?: string; warnings?: string[] | null };
export type Progress = { message?: string; done?: number; total?: number; result?: Result };
export type Payload = { action: string; field?: string; entity: { kind: string; data?: Data }; instruction?: string };

/** Scope is the keys a run may change: a field, the whole entity, or nothing for a report. */
export type Scope = "all" | string[];

export const scopeOf = (a: Action | undefined, field?: string): Scope => (a?.report_only ? [] : field ? [field] : "all");

const empty = (s: Scope) => s !== "all" && s.length === 0;

export const overlaps = (a: Scope, b: Scope) =>
  !empty(a) && !empty(b) && (a === "all" || b === "all" || a.some((k) => b.includes(k)));

/** lockOf returns, for a key, the label of the run that locks it, if any. */
export function lockOf(runs: { scope: Scope; label: string }[]): (key: string) => string | undefined {
  return (key) => runs.find((r) => r.scope === "all" || r.scope.includes(key))?.label;
}

/** instructionAction is the whole-card action the Reforge panel's instruction box runs. */
export const instructionAction = (list: Action[]) => list.find((a) => a.applies === "card" && !a.fields?.length && a.instruction);

/** newAction is the action the Start tab's Forge runs. */
export const newAction = (list: Action[]) => list.find((a) => a.applies === "new");

/** changedKeys lists the keys whose values differ between base and proposal, sorted. */
export function changedKeys(base: Data, proposal: Data): string[] {
  return [...new Set([...Object.keys(base), ...Object.keys(proposal)])].filter((k) => !same(base[k], proposal[k])).sort();
}

/** refineInstruction is the revise action's instruction for the findings and suggestions the owner ticked. */
export function refineInstruction(findings: Finding[], suggestions: string[], label: (key: string) => string): string {
  const finding = (f: Finding) => {
    const where = f.fields?.length ? `${f.fields.map(label).join(", ")}: ` : "";
    const move = f.move ? ` Move “${f.move.excerpt ?? ""}” to ${label(f.move.to)}.` : "";
    return `- ${where}${f.text}${move}`;
  };
  const parts = [];
  if (findings.length) parts.push(["Fix these problems:", ...findings.map(finding)].join("\n"));
  if (suggestions.length) parts.push(["Apply these suggestions:", ...suggestions.map((s) => `- ${s}`)].join("\n"));
  return parts.join("\n\n");
}

const settledKey = "innkeeper.forge.settled";

/** readSettled returns the IDs of runs the owner has applied, discarded, or dismissed. */
export function readSettled(): Set<number> {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(settledKey) ?? "[]");
    return new Set(Array.isArray(v) ? v.filter((n): n is number => typeof n === "number") : []);
  } catch {
    return new Set();
  }
}

/** settle remembers that the owner dealt with run id. Without storage, a reload shows the run again. */
export function settle(id: number) {
  try {
    localStorage.setItem(settledKey, JSON.stringify([...readSettled(), id].slice(-500)));
  } catch {
    // Kept for this page only.
  }
}
