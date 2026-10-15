import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Review } from "./Review";

test("Review applies only the fields the owner takes", async () => {
  const onApply = vi.fn();
  const start = { name: "Brakka", description: "A dwarf.", scenario: "A forge." };
  render(
    <Review
      title="Reforge"
      start={start}
      draft={start}
      proposal={{ name: "Brakka", description: "A tall dwarf.", scenario: "A cold forge." }}
      warnings={["dropped the change to name: outside the action's scope"]}
      onApply={onApply}
      onDiscard={() => {}}
    />,
  );
  expect(screen.getByText(/dropped the change to name/)).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getAllByRole("radio", { name: "Keep mine" })[1]!);
  await user.click(screen.getByRole("button", { name: "Apply 1 of 2 changes to draft" }));
  expect(onApply).toHaveBeenCalledWith({ description: "A tall dwarf." });
});

test("a field edited since the run started defaults to Keep mine", async () => {
  const onApply = vi.fn();
  const start = { description: "A dwarf.", scenario: "A forge." };
  render(
    <Review
      title="Reforge"
      start={start}
      draft={{ ...start, description: "A grumpy dwarf." }}
      proposal={{ description: "A tall dwarf.", scenario: "A cold forge." }}
      onApply={onApply}
      onDiscard={() => {}}
    />,
  );
  // The draft column shows the draft now, not the run's starting data.
  expect(screen.getByText("A grumpy dwarf.")).toBeInTheDocument();
  expect(screen.getByText("You've edited this since the run started.")).toBeInTheDocument();
  expect(screen.getAllByRole("radio", { name: "Keep mine" })[0]).toBeChecked();
  await userEvent.click(screen.getByRole("button", { name: "Apply 1 of 2 changes to draft" }));
  expect(onApply).toHaveBeenCalledWith({ scenario: "A cold forge." });
});
