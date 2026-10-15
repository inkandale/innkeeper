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
      draft={{ ...start, description: "A grumpy dwarf." }}
      proposal={{ name: "Brakka", description: "A tall dwarf.", scenario: "A cold forge." }}
      warnings={["dropped the change to name: outside the action's scope"]}
      onApply={onApply}
      onDiscard={() => {}}
    />,
  );
  expect(screen.getByText(/dropped the change to name/)).toBeInTheDocument();
  // The draft column shows the draft now, not the run's starting data.
  expect(screen.getByText("A grumpy dwarf.")).toBeInTheDocument();
  const user = userEvent.setup();
  await user.click(screen.getAllByRole("radio", { name: "Keep mine" })[1]!);
  await user.click(screen.getByRole("button", { name: "Apply 1 of 2 changes to draft" }));
  expect(onApply).toHaveBeenCalledWith({ description: "A tall dwarf." });
});
