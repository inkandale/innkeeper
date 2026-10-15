import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Report } from "./Report";
import { cardSubject } from "./subject";

const report = {
  summary: "Strong **voice**. <b>raw</b>",
  score: 72,
  dimensions: [{ key: "voice", label: "Voice", score: 88, comment: "Distinct." }],
  findings: [
    { severity: "high" as const, text: "Too short.", fields: ["description", "scenario"] },
    { severity: "low" as const, text: "Repeats the name.", fields: ["description"] },
    { severity: "medium" as const, text: "Belongs elsewhere.", fields: ["scenario"], move: { excerpt: "She owns a ship", to: "description" } },
  ],
  suggestions: ["Give her a rival.", "Add a greeting."],
};

test("Refine builds its instruction from the ticked findings and suggestions", async () => {
  const onRefine = vi.fn();
  const { container } = render(<Report report={report} subject={cardSubject} onRefine={onRefine} />);
  expect(screen.getByText("voice").tagName).toBe("STRONG");
  expect(container.querySelector("b")).toBeNull();
  expect(screen.getByText(/Also: Scenario/)).toBeInTheDocument();
  expect(screen.getByText("Move “She owns a ship” to Description")).toBeInTheDocument();

  const user = userEvent.setup();
  await user.click(screen.getByRole("checkbox", { name: /Too short/ }));
  await user.click(screen.getByRole("checkbox", { name: "Give her a rival." }));
  await user.click(screen.getByRole("button", { name: "Refine with 2 selected" }));
  expect(onRefine).toHaveBeenCalledWith("Fix these problems:\n- Description, Scenario: Too short.\n\nApply these suggestions:\n- Give her a rival.");
});
