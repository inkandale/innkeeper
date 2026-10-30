import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Report } from "./Report";
import { cardSubject } from "../card/subject";

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

test("Refine adds the owner's feedback after the ticked items", async () => {
  const onRefine = vi.fn();
  render(<Report report={report} subject={cardSubject} onRefine={onRefine} />);
  const user = userEvent.setup();
  await user.click(screen.getByRole("checkbox", { name: "Give her a rival." }));
  await user.type(screen.getByRole("textbox", { name: "Feedback" }), "Keep her accent.");
  await user.click(screen.getByRole("button", { name: "Refine with 1 selected" }));
  expect(onRefine).toHaveBeenCalledWith("Apply these suggestions:\n- Give her a rival.\n\nKeep her accent.");
});

test("Refine runs on feedback alone", async () => {
  const onRefine = vi.fn();
  render(<Report report={{ summary: "Fine." }} subject={cardSubject} onRefine={onRefine} />);
  const user = userEvent.setup();
  const refine = screen.getByRole("button", { name: "Refine" });
  expect(refine).toBeDisabled();
  await user.type(screen.getByRole("textbox", { name: "Feedback" }), "  Make her older.  ");
  await user.click(refine);
  expect(onRefine).toHaveBeenCalledWith("Make her older.");
});

test("the checkboxes say they feed Refine only when there is a Refine", () => {
  const hint = "Tick findings and suggestions to include them when you refine.";
  const { rerender } = render(<Report report={report} subject={cardSubject} onRefine={vi.fn()} />);
  expect(screen.getByText(hint)).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "Also consider" })).toBeInTheDocument();
  rerender(<Report report={report} subject={cardSubject} />);
  expect(screen.queryByText(hint)).toBeNull();
});

test("findings on fields changed since the evaluation fold away under a banner", () => {
  const { container } = render(<Report report={report} subject={cardSubject} changed={["description"]} />);
  expect(screen.getByText("This card has changed since the evaluation ran.")).toBeInTheDocument();
  expect(container.querySelector("#report-description")?.tagName).toBe("DETAILS");
  expect(container.querySelector("#report-description")).not.toHaveAttribute("open");
  expect(container.querySelector("#report-scenario")?.tagName).toBe("SECTION");
});
