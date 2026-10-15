import { api, must, route, type Kind } from "../api/client";

export type Named = { id: number; name: string };

/** MentionType is what can follow @: its key, as in @cards/, and how its names are found. */
export type MentionType = { key: "cards" | "lorebooks"; noun: string; find: (name: string, size: number) => Promise<Named[]> };

// findNamed lists a kind's names that contain name, in any case.
async function findNamed(kind: Kind, name: string, size: number): Promise<Named[]> {
  const res = must(
    await api.POST(route(kind, "/cards/search"), {
      body: { filter: name ? { name } : undefined, fields: ["name"], sort: "name", page: { size } },
    }),
  );
  return (res.hits ?? []).map((h) => ({ id: h.id, name: typeof h.fields.name === "string" ? h.fields.name : "" }));
}

export const mentionTypes: MentionType[] = [
  { key: "cards", noun: "card", find: (q, n) => findNamed("card", q, n) },
  { key: "lorebooks", noun: "lorebook", find: (q, n) => findNamed("lorebook", q, n) },
];

const typeOf = (key: string) => mentionTypes.find((t) => t.key === key);

/** Picked is a name the owner picked from the list, with its ID. */
export type Picked = Named & { type: string };

/**
 * Mention is one @type/name in a text, from start to end. state is ok with id, unknown, ambiguous (two items share the
 * name), or pending while the lookup for word runs.
 */
export type Mention = { start: number; end: number; type: string; word: string; name: string; state: "ok" | "unknown" | "ambiguous" | "pending"; id?: number };

const mentionRe = /(?<=^|\s)@([a-z]+)\/(\S*)/g;

// at reports whether rest starts with name as whole words.
const at = (rest: string, name: string) => name !== "" && rest.startsWith(name) && !/[\p{L}\p{N}_]/u.test(rest[name.length] ?? " ");

const longest = (names: Named[], rest: string) =>
  names.filter((n) => at(rest, n.name)).reduce<Named[]>((best, n) => {
    if (!best[0] || n.name.length > best[0].name.length) return [n];
    return n.name.length === best[0].name.length ? [...best, n] : best;
  }, []);

/**
 * mentionsIn finds the mentions in text. A name the owner picked resolves to its ID; a typed one resolves to the
 * longest name lookup returns that the text continues with. lookup returns the names containing a type's word, or
 * undefined while it loads.
 */
export function mentionsIn(text: string, picked: Picked[], lookup: (type: string, word: string) => Named[] | undefined): Mention[] {
  const out: Mention[] = [];
  for (const m of text.matchAll(mentionRe)) {
    const type = m[1]!;
    if (!typeOf(type)) continue;
    const start = m.index;
    const from = start + type.length + 2;
    const rest = text.slice(from).split("\n")[0]!;
    const word = m[2]!.replace(/[^\p{L}\p{N}]+$/u, "");
    const mention = (state: Mention["state"], names: Named[] = []): Mention => {
      const name = names[0]?.name ?? word;
      return { start, end: from + name.length, type, word, name, state, id: state === "ok" ? names[0]!.id : undefined };
    };
    const own = longest(picked.filter((p) => p.type === type), rest);
    if (own.length) {
      out.push(mention("ok", own));
      continue;
    }
    const found = word ? lookup(type, word) : [];
    if (!found) {
      out.push(mention("pending"));
      continue;
    }
    const named = longest(found, rest);
    out.push(mention(named.length === 1 ? "ok" : named.length ? "ambiguous" : "unknown", named));
  }
  return out;
}

/** contextOf is a run's context: the IDs the resolved mentions name, by type. */
export function contextOf(mentions: Mention[]): Partial<Record<MentionType["key"], number[]>> {
  const out: Partial<Record<MentionType["key"], number[]>> = {};
  for (const m of mentions) {
    if (m.state !== "ok" || m.id === undefined) continue;
    const ids = (out[m.type as MentionType["key"]] ??= []);
    if (!ids.includes(m.id)) ids.push(m.id);
  }
  return out;
}

/**
 * suggestAt finds the mention being typed before caret: at is where its @ is. Without type, query is a partial type;
 * with it, a partial name.
 */
export function suggestAt(text: string, caret: number): { at: number; type?: MentionType; query: string } | null {
  const before = text.slice(0, caret);
  const i = before.lastIndexOf("@");
  if (i < 0 || (i > 0 && !/\s/.test(before[i - 1]!))) return null;
  const q = before.slice(i + 1);
  const slash = q.indexOf("/");
  if (slash < 0) return /\s/.test(q) ? null : { at: i, query: q };
  const type = typeOf(q.slice(0, slash));
  const query = q.slice(slash + 1);
  // ponytail: names of up to three words; a longer name is picked by its first words.
  if (!type || query.includes("\n") || query.split(" ").length > 3) return null;
  return { at: i, type, query };
}
