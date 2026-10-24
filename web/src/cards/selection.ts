import type { components } from "../api/schema";

type Search = components["schemas"]["Query"];

/** Selection is the picked cards, or every card the search matches. key is the search it belongs to. */
export type Selection = { key: string; ids: number[]; all: boolean; anchor: number | null };

export const emptySelection = (key: string): Selection => ({ key, ids: [], all: false, anchor: null });

/** pick toggles id. With shift, every card from the last click to id, in page order, takes id's new state. */
export function pick(s: Selection, id: number, shift: boolean, page: number[]): Selection {
  const on = !s.ids.includes(id);
  const from = s.anchor === null ? -1 : page.indexOf(s.anchor);
  const to = page.indexOf(id);
  const range = shift && from >= 0 && to >= 0 ? page.slice(Math.min(from, to), Math.max(from, to) + 1) : [id];
  const ids = on ? [...new Set([...s.ids, ...range])] : s.ids.filter((x) => !range.includes(x));
  return { ...s, ids, all: false, anchor: id };
}

/** target is the bulk query for a selection: the picked IDs, or the search without paging. */
export function target(s: Selection, search: Search): Search {
  const library = search.library;
  if (!s.all) return { library, filter: { ids: s.ids } };
  // Every card with a vector matches a semantic search, so its text selects nothing; the filter does.
  const text = search.semantic ? undefined : search.text;
  if (text || search.filter) return { library, text, filter: search.filter };
  // /bulk refuses a query with no filter or text, so name every unarchived card.
  return { library, filter: { archived: false } };
}
