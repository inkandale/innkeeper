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
  setScore,
  parseScore,
  withoutScores,
  toggleTerm,
  type CardQuery,
  type Term,
} from "./query";

const base: CardQuery = { library: 1, text: "", terms: [], sort: "updated", page: 1, size: 48, view: "grid", sharesSource: false };
const term = (kind: Term["kind"], value: string, not = false): Term => ({ kind, value, not });

test("an empty URL is the default query", () => {
  expect(parseQuery(new URLSearchParams(), 1)).toEqual(base);
});

test("a query round-trips through the URL", () => {
  const q: CardQuery = {
    library: 2,
    text: "dwarf smith",
    terms: [term("tag", "fantasy"), term("tag", "a:b", true), term("target", "2"), term("state", "ahead"), term("collection", "4")],
    sort: "name",
    page: 3,
    size: 96,
    view: "list",
    sharesSource: true,
  };
  expect(parseQuery(toParams(q), 1)).toEqual(q);
});

test("defaults stay out of the URL, but the library is always named", () => {
  expect(toParams(base).toString()).toBe("library=1");
  expect(toParams({ ...base, text: "x", sort: "relevance" }).toString()).toBe("q=x&library=1");
});

test("without ?library= the query is in the given library", () => {
  expect(parseQuery(new URLSearchParams(), 3).library).toBe(3);
  expect(parseQuery(new URLSearchParams("library=2"), 3).library).toBe(2);
  expect(parseQuery(new URLSearchParams("library=x"), 3).library).toBe(3);
});

test("text searches default to relevance", () => {
  expect(parseQuery(new URLSearchParams("q=x"), 1).sort).toBe("relevance");
  expect(parseQuery(new URLSearchParams("q=x&sort=name"), 1).sort).toBe("name");
});

test("bad values fall back to defaults", () => {
  const q = parseQuery(new URLSearchParams("page=-2&size=7&sort=size&view=x&f=color:red&f=nocolon"), 1);
  expect(q).toEqual(base);
});

test("terms with values their section can't take are dropped, including old target links", () => {
  const bad = ["target:2:ahead", "target:0", "target:x", "state:pushed", "collection:", "collection:-1", "tag:", "source:"];
  const q = parseQuery(new URLSearchParams(bad.map((f) => ["f", f])), 1);
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
    library: 1,
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
    library: 1,
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
    library: 1,
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
  const q = parseQuery(new URLSearchParams("q=anvil&f=source:chub&page=2"), 1);
  expect(toLorebookSearch(q)).toMatchObject({
    text: "anvil",
    filter: { and: [{ source: "chub" }] },
    sort: "relevance",
    fields: ["name", "description"],
    include: ["cards", "targets"],
    page: { size: 48, number: 2 },
  });
});

test("score terms and enrichment sorts round-trip through the URL", () => {
  const q: CardQuery = { ...base, terms: [term("score", "quality:70..100"), term("score", "voice:none")], sort: "enrichment:quality" };
  expect(parseQuery(toParams(q), 1)).toEqual(q);
  const bad = parseQuery(new URLSearchParams("f=score:quality:..&f=score:Quality:1..&sort=enrichment:Bad"), 1);
  expect(bad).toEqual(base);
});

test("score terms become enrichment filters, each key on its own", () => {
  expect(filterFor([term("score", "quality:70.."), term("score", "voice:..40.5"), term("score", "summary:none", true)])).toEqual({
    and: [{ enrichment: "quality", gte: 70 }, { enrichment: "voice", lte: 40.5 }, { not: { enrichment: "summary", missing: true } }],
  });
});

test("setScore replaces a key's term and clears it when empty", () => {
  let q = setScore(base, { key: "quality", gte: 70 });
  q = setScore(q, { key: "voice", none: true });
  q = setScore(q, { key: "quality", lte: 20 });
  expect(q.terms).toEqual([term("score", "voice:none"), term("score", "quality:..20")]);
  expect(setScore(q, { key: "voice" }).terms).toEqual([term("score", "quality:..20")]);
});

test("score ranges take negative and exponent numbers", () => {
  const q = setScore(base, { key: "mood", gte: -1, lte: 1 });
  expect(q.terms).toEqual([term("score", "mood:-1..1")]);
  expect(parseQuery(toParams(q), 1)).toEqual(q);
  expect(filterFor(q.terms)).toEqual({ and: [{ enrichment: "mood", gte: -1, lte: 1 }] });
  expect(parseScore("mood:-2.5e-1..1E2")).toEqual({ key: "mood", gte: -0.25, lte: 100 });
});

test("withoutScores drops score terms and an enrichment sort", () => {
  const q: CardQuery = { ...base, terms: [term("tag", "a"), term("score", "quality:1..")], sort: "enrichment:quality" };
  expect(withoutScores(q)).toEqual({ ...base, terms: [term("tag", "a")] });
  expect(withoutScores(base)).toBe(base);
});

test("Shares a source is shared=1 in the URL and a shares_source filter", () => {
  const q = { ...base, sharesSource: true };
  expect(toParams(q).toString()).toBe("shared=1&library=1");
  expect(toSearch(q).filter).toEqual({ and: [{ shares_source: true }] });
  expect(countSearch(q, "tag").filter).toEqual({ and: [{ shares_source: true }] });
});

test("value terms OR within a key, AND across keys, and only clears its own key", () => {
  const q: CardQuery = {
    ...base,
    terms: [term("value", "language=en"), term("value", "language=ja"), term("value", "problems=markup", true), term("value", "problems=oversized")],
  };
  expect(filterFor(q.terms)).toEqual({
    and: [
      { or: [{ enrichment: "language", is: "en" }, { enrichment: "language", is: "ja" }] },
      { enrichment: "problems", is: "oversized" },
      { not: { enrichment: "problems", is: "markup" } },
    ],
  });
  expect(onlyTerm(q, term("value", "language=zh")).terms).toEqual([
    term("value", "problems=markup", true),
    term("value", "problems=oversized"),
    term("value", "language=zh"),
  ]);
  expect(parseQuery(toParams(q), 1).terms).toEqual(q.terms);
  expect(parseQuery(new URLSearchParams("f=value:Bad=x&f=value:nokey"), 1).terms).toEqual([]);
  expect(withoutScores(q).terms).toEqual([]);
});
