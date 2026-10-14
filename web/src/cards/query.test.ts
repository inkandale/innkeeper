import {
  countSearch,
  filterFor,
  onlyTerm,
  parseQuery,
  sectionsFor,
  stateCountSearch,
  toLorebookSearch,
  toParams,
  toSearch,
  sameValue,
  toggleTerm,
  type CardQuery,
  type Term,
} from "./query";

const base: CardQuery = { text: "", terms: [], sort: "updated", page: 1, size: 48, view: "grid" };
const term = (kind: Term["kind"], value: string, not = false): Term => ({ kind, value, not });

test("an empty URL is the default query", () => {
  expect(parseQuery(new URLSearchParams())).toEqual(base);
});

test("a query round-trips through the URL", () => {
  const q: CardQuery = {
    text: "dwarf smith",
    terms: [term("tag", "fantasy"), term("tag", "a:b", true), term("target", "2"), term("state", "ahead"), term("collection", "4")],
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

test("terms with values their section can't take are dropped, including old target links", () => {
  const bad = ["target:2:ahead", "target:0", "target:x", "state:pushed", "collection:", "collection:-1", "tag:", "source:"];
  const q = parseQuery(new URLSearchParams(bad.map((f) => ["f", f])));
  expect(q.terms).toEqual([]);
});

test("filterFor ORs within a section, ANDs sections, and negates each excluded value", () => {
  expect(
    filterFor([
      term("tag", "fantasy"),
      term("tag", "pirate"),
      term("tag", "nsfw", true),
      term("source", "chub"),
      term("collection", "3", true),
    ]),
  ).toEqual({
    and: [
      { or: [{ tag: "fantasy" }, { tag: "pirate" }] },
      { source: "chub" },
      { not: { tag: "nsfw" } },
      { not: { collection: 3 } },
    ],
  });
  expect(filterFor([])).toBeUndefined();
});

test("selected targets and states share one target_state", () => {
  expect(filterFor([term("target", "2"), term("target", "5"), term("state", "ahead")])).toEqual({
    and: [{ target_state: { instances: [2, 5], states: ["ahead"] } }],
  });
  expect(filterFor([term("state", "ahead")])).toEqual({ and: [{ target_state: { states: ["ahead"] } }] });
  expect(filterFor([term("target", "2", true)])).toEqual({ and: [{ not: { target_state: { instances: [2] } } }] });
});

test("toSearch builds the API body", () => {
  const body = toSearch({ ...base, text: "dwarf", terms: [term("tag", "fantasy")], page: 2 });
  expect(body).toMatchObject({
    text: "dwarf",
    sort: "updated",
    filter: { and: [{ tag: "fantasy" }] },
    page: { size: 48, number: 2 },
  });
  expect(toSearch(base).filter).toBeUndefined();
});

test("a section's count search leaves out the section's own terms", () => {
  const q = { ...base, text: "dwarf", terms: [term("tag", "fantasy"), term("source", "chub"), term("state", "ahead")] };
  expect(countSearch(q, "tag")).toEqual({
    text: "dwarf",
    filter: { and: [{ target_state: { states: ["ahead"] } }, { source: "chub" }] },
    facets: ["tags"],
    page: { size: 1 },
  });
  expect(countSearch(q, "target").facets).toEqual(["targets"]);
});

test("a state's count search swaps in that state and keeps the selected targets", () => {
  const q = { ...base, terms: [term("target", "2"), term("state", "ahead"), term("tag", "fantasy")] };
  expect(stateCountSearch(q, "in_sync")).toEqual({
    filter: { and: [{ target_state: { instances: [2], states: ["in_sync"] } }, { tag: "fantasy" }] },
    page: { size: 1 },
  });
});

test("toggleTerm adds, flips, and removes, and resets the page", () => {
  const tag = term("tag", "fantasy");
  const added = toggleTerm({ ...base, page: 4 }, tag);
  expect(added.terms).toEqual([tag]);
  expect(added.page).toBe(1);
  expect(toggleTerm(added, { ...tag, not: true }).terms).toEqual([{ ...tag, not: true }]);
  expect(toggleTerm(added, tag).terms).toEqual([]);
});

test("toggleTerm treats tags in any case as the same tag", () => {
  expect(toggleTerm({ ...base, terms: [term("tag", "Fantasy")] }, term("tag", "fantasy")).terms).toEqual([]);
});

test("only tags compare in any case", () => {
  expect(sameValue("tag", "Fantasy", "fantasy")).toBe(true);
  expect(sameValue("collection", "Ab", "ab")).toBe(false);
  expect(sameValue("target", "2", "2")).toBe(true);
});

test("onlyTerm keeps one value in its section and leaves other sections alone", () => {
  const q = { ...base, page: 3, terms: [term("tag", "fantasy"), term("tag", "nsfw", true), term("source", "chub")] };
  const only = onlyTerm(q, term("tag", "pirate"));
  expect(only.terms).toEqual([term("source", "chub"), term("tag", "pirate")]);
  expect(only.page).toBe(1);
});

test("lorebooks have no tag or collection sections", () => {
  expect(sectionsFor("card")).toEqual(["target", "state", "tag", "source", "collection"]);
  expect(sectionsFor("lorebook")).toEqual(["target", "state", "source"]);
});

test("toLorebookSearch asks for linked cards and sync", () => {
  const q = parseQuery(new URLSearchParams("q=anvil&f=source:chub&page=2"));
  expect(toLorebookSearch(q)).toMatchObject({
    text: "anvil",
    filter: { and: [{ source: "chub" }] },
    sort: "relevance",
    fields: ["name", "description"],
    include: ["cards", "targets"],
    page: { size: 48, number: 2 },
  });
});
