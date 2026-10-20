import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { enrichmentText, Tile, type Hit, type Names } from "./Tile";

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
      <Tile hit={hit} names={names} pick={{ selected: false, locked: false, onPick: () => {} }} />
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
  const hit: Hit = { id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" }, enrichments: { summary: { value: long, stale: false } } };
  render(
    <MemoryRouter>
      <Tile hit={hit} names={names} pick={{ selected: false, locked: false, onPick: () => {} }} grid={{ key: "summary", label: "Summary", type: "text" }} />
    </MemoryRouter>,
  );
  expect(screen.getByRole("img", { name: `Summary ${long}` })).toHaveClass("score", "text");
});

function renderScore(value: number, stale = false) {
  const hit: Hit = { id: 7, slug: "brakka", revision_id: 1, fields: { name: "Brakka" }, enrichments: { lint: { value, stale } } };
  render(
    <MemoryRouter>
      <Tile hit={hit} names={names} pick={{ selected: false, locked: false, onPick: () => {} }} grid={{ key: "lint", label: "Lint", type: "score", range: [0, 100] }} />
    </MemoryRouter>,
  );
  return screen.getByRole("img", { name: stale ? `Lint ${value}, stale: from an older revision` : `Lint ${value}` });
}

test("a score badge names its key and bands by where the value falls in the range, higher being better", () => {
  expect(renderScore(80)).toHaveTextContent("Lint 80");
  expect(screen.getByRole("img", { name: "Lint 80" })).toHaveClass("score", "good");
  cleanup();
  expect(renderScore(65)).toHaveClass("fair");
  cleanup();
  expect(renderScore(0)).toHaveClass("poor");
});

test("a stale score keeps its band and dims", () => {
  expect(renderScore(90, true)).toHaveClass("good", "stale");
});
