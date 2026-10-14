import { fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import { Tile, type Hit, type Names } from "./Tile";

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
