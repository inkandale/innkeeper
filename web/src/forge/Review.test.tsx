import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { Data } from "../card/draft";
import { Review } from "./Review";
import { cardSubject } from "../card/subject";

const preview = (draft: Data) => (changes: Data) =>
  cardSubject.preview({ ...draft, ...changes });

test("Review applies only the fields the owner takes", async () => {
  const onApply = vi.fn();
  const start = {
    name: "Brakka",
    description: "A dwarf.",
    scenario: "A forge.",
  };
  render(
    <Review
      title="Reforge"
      proposed={cardSubject.review(start, start, {
        name: "Brakka",
        description: "A tall dwarf.",
        scenario: "A cold forge.",
      })}
      preview={preview(start)}
      warnings={["dropped the change to name: outside the action's scope"]}
      onApply={onApply}
      onDiscard={() => {}}
    />,
  );
  expect(screen.getByText(/dropped the change to name/)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getAllByRole("radio", { name: "Keep mine" })[1]!);
  await user.click(
    screen.getByRole("button", { name: "Apply 1 of 2 changes to draft" }),
  );
  expect(onApply).toHaveBeenCalledWith({ description: "A tall dwarf." });
});

test("a field edited since the run started defaults to Keep mine", async () => {
  const onApply = vi.fn();
  const start = { description: "A dwarf.", scenario: "A forge." };
  render(
    <Review
      title="Reforge"
      proposed={cardSubject.review(
        start,
        { ...start, description: "A grumpy dwarf." },
        { description: "A tall dwarf.", scenario: "A cold forge." },
      )}
      preview={preview({ ...start, description: "A grumpy dwarf." })}
      onApply={onApply}
      onDiscard={() => {}}
    />,
  );
  // The draft column shows the draft now, not the run's starting data.
  expect(screen.getByText("A grumpy dwarf.")).toBeInTheDocument();
  expect(
    screen.getByText("You've edited this since the run started."),
  ).toBeInTheDocument();
  expect(screen.getAllByRole("radio", { name: "Keep mine" })[0]).toBeChecked();
  await userEvent.click(
    screen.getByRole("button", { name: "Apply 1 of 2 changes to draft" }),
  );
  expect(onApply).toHaveBeenCalledWith({ scenario: "A cold forge." });
});

test("the original shows beside a proposal only when asked, or when it changed during the run", async () => {
  const start = { description: "A dwarf.", scenario: "A forge." };
  render(
    <Review
      title="Reforge"
      proposed={cardSubject.review(
        start,
        { ...start, description: "A grumpy dwarf." },
        { description: "A tall dwarf.", scenario: "A cold forge." },
      )}
      preview={preview({ ...start, description: "A grumpy dwarf." })}
      onApply={() => {}}
      onDiscard={() => {}}
    />,
  );
  const scenario = screen.getByRole("region", { name: "Scenario" });
  expect(screen.getByText("A grumpy dwarf.")).toBeInTheDocument();
  // The diff's own text nodes also read "A forge.", so look in the original alone.
  expect(
    within(scenario).queryByText("A forge.", { selector: ".mine" }),
  ).not.toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("checkbox", { name: "Show original" }),
  );
  expect(
    within(scenario).getByText("A forge.", { selector: ".mine" }),
  ).toBeInTheDocument();
});

test("Show draft shows the whole card with only the taken changes made", async () => {
  const start = {
    name: "Brakka",
    description: "A dwarf.",
    scenario: "A forge.",
    personality: "Gruff.",
  };
  render(
    <Review
      title="Reforge"
      proposed={cardSubject.review(start, start, {
        ...start,
        description: "A tall dwarf.",
        scenario: "A cold forge.",
      })}
      preview={preview(start)}
      onApply={() => {}}
      onDiscard={() => {}}
    />,
  );
  const user = userEvent.setup();
  await user.click(screen.getAllByRole("radio", { name: "Keep mine" })[1]!);
  await user.click(screen.getByRole("button", { name: "Show draft" }));
  const draft = screen.getByRole("dialog", {
    name: "Draft with 1 of 2 changes",
  });
  expect(
    within(draft).getByRole("region", { name: "Description" }),
  ).toHaveTextContent("A tall dwarf.");
  expect(
    within(draft).getByRole("region", { name: "Scenario" }),
  ).toHaveTextContent("A forge.");
  // A field the proposal leaves alone is still part of the card.
  expect(
    within(draft).getByRole("region", { name: "Personality" }),
  ).toHaveTextContent("Gruff.");
  await user.click(within(draft).getByRole("button", { name: "Close" }));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
