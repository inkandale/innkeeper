import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { enrichmentText, Tile, type Hit, type Names, type Scorer } from "./Tile";

const names: Names = {
  instances: new Map([
    [2, "Home ST"],
    [3, "Lumiverse"],
  ]),
  collections: new Map(),
  logos: new Map([
    [2, { plugin: "sillytavern", icon: "/api/plugins/sillytavern/icon" }],
    [3, { plugin: "lumiverse" }],
  ]),
};

function renderTile(targets: Hit["targets"]) {
  const hit: Hit = { id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" }, targets };
  render(
    <MemoryRouter>
      <Tile hit={hit} names={names} pick={{ selected: false, locked: false, onPick: () => {} }} scorers={new Map()} />
    </MemoryRouter>,
  );
}

test("each linked instance shows its plugin's logo, or a monogram without one", () => {
  renderTile([
    { instance: 2, is: "in_sync" },
    { instance: 3, is: "in_sync" },
  ]);
  expect(screen.getByRole("img", { name: "Home ST" }).querySelector("img")).toHaveAttribute("src", "/api/plugins/sillytavern/icon");
  expect(screen.getByRole("img", { name: "Lumiverse" })).toHaveTextContent("L");
  expect(screen.queryByRole("img", { name: /needs attention/i })).not.toBeInTheDocument();
});

test("a logo that fails to load falls back to the monogram", () => {
  renderTile([{ instance: 2, is: "in_sync" }]);
  const logo = screen.getByRole("img", { name: "Home ST" });
  fireEvent.error(logo.querySelector("img")!);
  expect(logo).toHaveTextContent("S");
});

test("the attention marker lists every instance not in sync, ahead included", () => {
  renderTile([
    { instance: 2, is: "ahead" },
    { instance: 3, is: "remote_changed" },
  ]);
  expect(screen.getByRole("img", { name: "Needs attention: Home ST: not pushed · Lumiverse: changed in app" })).toBeInTheDocument();
});

test("enrichment values format by their range", () => {
  expect(enrichmentText(3.25, [1, 5])).toBe("3.3");
  expect(enrichmentText(77.6, [0, 100])).toBe("78");
  expect(enrichmentText(["a", "b"])).toBe("a, b");
  expect(enrichmentText("Fine.")).toBe("Fine.");
});

test("a text grid key shows truncated, with the whole value in its title", () => {
  const long = "A very long summary of the card that goes on and on";
  const hit: Hit = { id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" }, score: { instance: 4, key: "summary", value: long, stale: false } };
  const scorers = new Map<number, Scorer>([[4, { keys: [{ key: "summary", label: "Summary", type: "text" }] }]]);
  render(
    <MemoryRouter>
      <Tile hit={hit} names={names} pick={{ selected: false, locked: false, onPick: () => {} }} scorers={scorers} />
    </MemoryRouter>,
  );
  expect(screen.getByRole("img", { name: `Summary ${long}` })).toHaveClass("score", "text");
});

const ranked = new Map<number, Scorer>([
  [5, { keys: [{ key: "quality", label: "Quality", type: "score", range: [0, 100] }], medal: "gold" }],
  [6, { keys: [{ key: "quality", label: "Crucible quality", type: "score", range: [0, 100] }, { key: "voice", label: "Voice", type: "score", range: [0, 100] }], medal: "silver" }],
  [7, { keys: [{ key: "lint", label: "Lint", type: "score", range: [0, 100] }], medal: "bronze" }],
  [8, { keys: [{ key: "score", label: "Other", type: "score", range: [0, 100] }] }],
]);

function renderScore(instance: number, key: string, value: number, stale = false) {
  const hit: Hit = { id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" }, score: { instance, key, value, stale } };
  render(
    <MemoryRouter>
      <Tile hit={hit} names={names} pick={{ selected: false, locked: false, onPick: () => {} }} scorers={ranked} />
    </MemoryRouter>,
  );
}

test("a score badge wears its scorer's medal and names the key in its title", () => {
  renderScore(5, "quality", 80);
  expect(screen.getByRole("img", { name: "Quality 80" })).toHaveClass("score", "gold");
  expect(screen.getByRole("img", { name: "Quality 80" })).toHaveTextContent("★ 80");
  cleanup();
  renderScore(6, "quality", 65);
  expect(screen.getByRole("img", { name: "Crucible quality 65" })).toHaveClass("silver");
  cleanup();
  renderScore(7, "lint", 40);
  expect(screen.getByRole("img", { name: "Lint 40" })).toHaveClass("bronze");
  cleanup();
  renderScore(8, "score", 30);
  const plain = screen.getByRole("img", { name: "Other 30" });
  expect(plain).toHaveTextContent(/^30$/);
  expect(plain).not.toHaveClass("gold");
});

test("a stale score keeps its medal and dims", () => {
  renderScore(5, "quality", 90, true);
  expect(screen.getByRole("img", { name: "Quality 90, stale: from an older revision" })).toHaveClass("gold", "stale");
});

test("a score from an instance that is no scorer shows no badge", () => {
  renderScore(9, "quality", 90);
  expect(screen.queryByRole("img", { name: /90/ })).not.toBeInTheDocument();
});

test("a score of the key sorted by wears its instance's medal", () => {
  renderScore(6, "voice", 70);
  expect(screen.getByRole("img", { name: "Voice 70" })).toHaveClass("silver");
});
