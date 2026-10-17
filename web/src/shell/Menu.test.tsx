import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Menu } from "./Menu";

test("opens its items from the ⋯ button and runs the one clicked", async () => {
  const user = userEvent.setup();
  const run = vi.fn();
  render(
    <Menu label="More for sillytavern">
      <button onClick={run}>Uninstall</button>
    </Menu>,
  );
  const button = screen.getByRole("button", { name: "More for sillytavern" });
  const list = document.getElementById(button.getAttribute("popovertarget")!)!;
  expect(list).toHaveAttribute("popover", "auto");
  const item = screen.getByRole("button", { name: "Uninstall" });
  expect(item.closest("[popover]")).toBe(list);
  await user.click(item);
  expect(run).toHaveBeenCalled();
});
