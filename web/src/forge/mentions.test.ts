import { contextOf, mentionsIn, suggestAt, type Named } from "./mentions";

const library: Record<string, Named[]> = {
  cards: [
    { id: 1, name: "Billy" },
    { id: 2, name: "Twin" },
    { id: 3, name: "Twin" },
  ],
  lorebooks: [
    { id: 8, name: "Northern" },
    { id: 9, name: "Northern Reach" },
  ],
};
// lookup answers like the name filter: names containing the word, in any case.
const lookup = (type: string, word: string) => library[type]?.filter((n) => n.name.toLowerCase().includes(word.toLowerCase()));

test("a picked mention resolves to the ID recorded when it was picked", () => {
  const [m] = mentionsIn("make @cards/Twin the brother", [{ type: "cards", id: 3, name: "Twin" }], lookup);
  expect(m).toMatchObject({ state: "ok", id: 3, name: "Twin", start: 5, end: 16 });
});

test("a typed mention resolves by exact name, including names with spaces", () => {
  const ms = mentionsIn("@cards/Billy, of @lorebooks/Northern Reach is cold", [], lookup);
  expect(ms.map((m) => [m.state, m.id, m.name])).toEqual([
    ["ok", 1, "Billy"],
    ["ok", 9, "Northern Reach"],
  ]);
  expect(contextOf(ms)).toEqual({ cards: [1], lorebooks: [9] });
});

test("a typed name nobody has is unknown, over the typed word", () => {
  const text = "ask @cards/Bil about it";
  const [m] = mentionsIn(text, [], lookup);
  expect(m).toMatchObject({ state: "unknown" });
  expect(text.slice(m!.start, m!.end)).toBe("@cards/Bil");
});

test("a typed name two cards share is ambiguous", () => {
  expect(mentionsIn("@cards/Twin", [], lookup)[0]).toMatchObject({ state: "ambiguous", name: "Twin" });
});

test("a typed name waits for its lookup", () => {
  expect(mentionsIn("@cards/Billy", [], () => undefined)[0]).toMatchObject({ state: "pending", word: "Billy" });
});

test("only known types after a space or the start are mentions", () => {
  expect(mentionsIn("@nope/Billy mail@cards/Billy", [], lookup)).toEqual([]);
});

test("suggestAt offers types, then names, for the mention at the caret", () => {
  expect(suggestAt("hi @ca", 6)).toEqual({ at: 3, query: "ca" });
  expect(suggestAt("hi @cards/Bi", 12)).toMatchObject({ at: 3, type: { key: "cards" }, query: "Bi" });
  expect(suggestAt("hi @nope/Bi", 11)).toBeNull();
  expect(suggestAt("mail@ca", 7)).toBeNull();
});
