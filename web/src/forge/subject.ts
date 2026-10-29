import { same, show, type Data } from "../card/draft";

/** Row is one change Review offers. A missing mine or after shows as empty. */
export type Row = { key: string; label: string; change?: "added" | "removed" | "changed"; mine?: string; before: string; after?: string; edited: boolean };

/** Proposed is a proposal's rows, and apply, the draft changes that taking some of them makes. */
export type Proposed = { rows: Row[]; apply: (taken: Set<string>) => Data };

/** Shown is one titled piece of text in a preview of an entity's data. */
export type Shown = { title: string; text: string };

/**
 * Subject is what a Forge works on: the entity kind, how its data keys are named, how a proposal is reviewed, how its
 * data reads in a preview, the job list filter that finds its runs, and the instruction box's placeholder.
 */
export type Subject = {
  kind: "card" | "lorebook";
  label: (key: string) => string;
  review: (start: Data, draft: Data, proposal: Data) => Proposed;
  preview: (data: Data) => Shown[];
  jobFilter: (id: number | undefined) => { card_id?: number; lorebook_id?: number };
  placeholder: string;
};

/** fieldRows is a row per key, each the whole value. A field the owner edited since start is edited. */
export function fieldRows(start: Data, draft: Data, proposal: Data, label: (key: string) => string, keys: string[]): Row[] {
  return keys.map((k) => ({
    key: k,
    label: label(k),
    mine: draft[k] === undefined ? undefined : show(draft[k]),
    before: show(start[k]),
    after: proposal[k] === undefined ? undefined : show(proposal[k]),
    edited: !same(draft[k], start[k]),
  }));
}

/** fieldChanges is the draft changes taking the rows in taken makes: the proposal's value per key. */
export const fieldChanges = (proposal: Data, keys: string[], taken: Set<string>): Data =>
  Object.fromEntries(keys.filter((k) => taken.has(k)).map((k) => [k, proposal[k]]));
