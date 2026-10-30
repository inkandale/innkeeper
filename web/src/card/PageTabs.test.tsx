import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { PageTabs, type PageTab } from "./PageTabs";

const tabs: PageTab[] = [
  { id: "fields", label: "Fields", panel: <p>fields panel</p> },
  { id: "review", label: "Review", disabled: true, panel: <p>review panel</p> },
  { id: "metadata", label: "Metadata", panel: <p>metadata panel</p> },
];

function Harness({ start = "fields" }: { start?: string }) {
  const [tab, setTab] = useState(start);
  return <PageTabs label="Card" tabs={tabs} selected={tab} onSelect={setTab} actions={<button>Reforge</button>} notices={<p>run header</p>} />;
}

test("arrow keys skip a disabled tab and show the next panel", async () => {
  const user = userEvent.setup();
  render(<Harness />);
  await user.click(screen.getByRole("tab", { name: "Fields" }));
  await user.keyboard("{ArrowRight}");
  const metadata = screen.getByRole("tab", { name: "Metadata" });
  expect(metadata).toHaveFocus();
  expect(metadata).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("tabpanel")).toHaveTextContent("metadata panel");
  expect(screen.getByRole("tabpanel")).toHaveAttribute("aria-labelledby", metadata.id);
});

test.each(["review", "nope"])("a disabled or unknown selection (%s) shows the first usable tab", (start) => {
  render(<Harness start={start} />);
  expect(screen.getByRole("tab", { name: "Fields" })).toHaveAttribute("aria-selected", "true");
  expect(screen.getByRole("tabpanel")).toHaveTextContent("fields panel");
});

test("actions sit in the bar and notices above the panel", () => {
  render(<Harness />);
  const bar = screen.getByRole("tablist").parentElement!;
  expect(bar).toContainElement(screen.getByRole("button", { name: "Reforge" }));
  const notice = screen.getByText("run header");
  expect(bar).not.toContainElement(notice);
  expect(notice.compareDocumentPosition(screen.getByRole("tabpanel")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
});
