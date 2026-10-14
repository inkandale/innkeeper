import { parseQuery, toLorebookSearch, toParams, toSearch, toggleTerm, type CardQuery } from "./query";

const base: CardQuery = { text: "", terms: [], sort: "updated", page: 1, size: 48, view: "grid" };

test("an empty URL is the default query", () => {
  expect(parseQuery(new URLSearchParams())).toEqual(base);
});

test("a query round-trips through the URL", () => {
  const q: CardQuery = {
    text: "dwarf smith",
    terms: [
      { kind: "tag", value: "fantasy", not: false },
      { kind: "tag", value: "a:b", not: true },
      { kind: "target", value: "2:ahead", not: false },
    ],
    sort: "name",
    page: 3,
    size: 96,
    view: "list",
  };
  expect(parseQuery(toParams(q))).toEqual(q);
});

test("defaults stay out of the URL", () => {
  expect(toParams(base).toString()).toBe("");
  expect(toParams({ ...base, text: "x", sort: "relevance" }).toString()).toBe("q=x");
});

test("text searches default to relevance", () => {
  expect(parseQuery(new URLSearchParams("q=x")).sort).toBe("relevance");
  expect(parseQuery(new URLSearchParams("q=x&sort=name")).sort).toBe("name");
});

test("bad values fall back to defaults", () => {
  const q = parseQuery(new URLSearchParams("page=-2&size=7&sort=size&view=x&f=color:red&f=nocolon"));
  expect(q).toEqual(base);
});

test("toSearch builds the API body", () => {
  const body = toSearch({
    ...base,
    text: "dwarf",
    terms: [
      { kind: "tag", value: "fantasy", not: false },
      { kind: "collection", value: "3", not: true },
      { kind: "source", value: "chub", not: false },
      { kind: "target", value: "2:ahead", not: false },
      { kind: "collection", value: "x", not: false },
      { kind: "collection", value: "", not: false },
      { kind: "collection", value: "0", not: false },
      { kind: "target", value: "2:bogus", not: false },
      { kind: "target", value: ":ahead", not: false },
      { kind: "target", value: "0:ahead", not: false },
    ],
    page: 2,
  });
  expect(body).toMatchObject({
    text: "dwarf",
    sort: "updated",
    filter: {
      and: [
        { tag: "fantasy" },
        { not: { collection: 3 } },
        { source: "chub" },
        { target_state: { instances: [2], states: ["ahead"] } },
      ],
    },
    page: { size: 48, number: 2 },
  });
  expect(toSearch(base).filter).toBeUndefined();
});

test("toggleTerm adds, flips, and removes, and resets the page", () => {
  const tag = { kind: "tag", value: "fantasy", not: false } as const;
  const added = toggleTerm({ ...base, page: 4 }, tag);
  expect(added.terms).toEqual([tag]);
  expect(added.page).toBe(1);
  expect(toggleTerm(added, { ...tag, not: true }).terms).toEqual([{ ...tag, not: true }]);
  expect(toggleTerm(added, tag).terms).toEqual([]);
});

test("toLorebookSearch asks for linked cards, sync, and lorebook facets", () => {
  const q = parseQuery(new URLSearchParams("q=anvil&f=source:chub&page=2"));
  expect(toLorebookSearch(q)).toEqual({
    text: "anvil",
    filter: { and: [{ source: "chub" }] },
    sort: "relevance",
    fields: ["name", "description"],
    include: ["cards", "targets"],
    facets: ["targets", "sources"],
    page: { size: 48, number: 2 },
  });
});
