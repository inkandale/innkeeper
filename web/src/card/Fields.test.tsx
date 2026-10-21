import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { Fields } from "./Fields";

const data = { description: "A grumpy dwarf. Old.", personality: "Gruff.", alternate_greetings: ["Hail.", "Well met."] };

const summary = (label: string) => [...document.querySelectorAll("summary")].find((s) => s.firstChild?.textContent === label)!;
const section = (label: string) => summary(label).closest("details")!;

function renderFields(dirty: string[] = []) {
  render(<Fields data={data} dirty={new Set(dirty)} edit={() => {}} />);
  return within(screen.getByRole("navigation", { name: "Fields" }));
}

afterEach(() => vi.restoreAllMocks());

test("the outline lists every field with its tokens, marks unsaved ones, and totals the card", () => {
  const outline = renderFields(["personality"]);
  expect(outline.getByRole("button", { name: "Description, ~5 tokens" })).toBeInTheDocument();
  expect(outline.getByRole("button", { name: "Personality, unsaved, ~2 tokens" })).toBeInTheDocument();
  expect(outline.getByRole("button", { name: "Alternate greetings, ~4 tokens" })).toBeInTheDocument();
  expect(outline.getByRole("button", { name: "Extensions" })).toBeInTheDocument();
  expect(screen.getByText("~11 tokens total")).toBeInTheDocument();
});

test("collapse all closes every section and expand all opens them", async () => {
  renderFields();
  await userEvent.click(screen.getByRole("button", { name: "Collapse all" }));
  for (const d of document.querySelectorAll("details")) expect(d).not.toHaveAttribute("open");
  await userEvent.click(screen.getByRole("button", { name: "Expand all" }));
  for (const d of document.querySelectorAll("details")) expect(d).toHaveAttribute("open");
});

test("clicking an outline entry opens its collapsed section and scrolls to it", async () => {
  const scrolled = vi.fn();
  Element.prototype.scrollIntoView = scrolled;
  const outline = renderFields();
  await userEvent.click(screen.getByRole("button", { name: "Collapse all" }));
  await userEvent.click(outline.getByRole("button", { name: "Personality, ~2 tokens" }));
  expect(section("Personality")).toHaveAttribute("open");
  expect(section("Description")).not.toHaveAttribute("open");
  expect(scrolled.mock.contexts[0]).toBe(section("Personality"));
});

test("a section still opens and closes on its own after collapse all", async () => {
  renderFields();
  await userEvent.click(screen.getByRole("button", { name: "Collapse all" }));
  await userEvent.click(summary("Scenario"));
  await waitFor(() => expect(section("Scenario")).toHaveAttribute("open"));
  expect(section("Description")).not.toHaveAttribute("open");
  await userEvent.click(summary("Scenario"));
  await waitFor(() => expect(section("Scenario")).not.toHaveAttribute("open"));
});
