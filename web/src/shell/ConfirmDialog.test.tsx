import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ConfirmDialog } from "./ConfirmDialog";

function Asker({ onConfirm }: { onConfirm: () => void }) {
  const [open, setOpen] = useState(true);
  return open ? (
    <ConfirmDialog
      title="Delete the library Side projects?"
      body={<p>24 cards</p>}
      confirmLabel="Delete library"
      danger
      typeToConfirm="Side projects"
      onConfirm={onConfirm}
      onClose={() => setOpen(false)}
    />
  ) : (
    <p>closed</p>
  );
}

test("the confirm button stays disabled until the typed name matches", async () => {
  const onConfirm = vi.fn();
  render(<Asker onConfirm={onConfirm} />);
  const user = userEvent.setup();
  const confirm = screen.getByRole("button", { name: "Delete library" });
  await user.type(screen.getByLabelText("Type Side projects to confirm"), "side projects{Enter}");
  expect(confirm).toBeDisabled();
  expect(onConfirm).not.toHaveBeenCalled();
  await user.clear(screen.getByLabelText("Type Side projects to confirm"));
  await user.type(screen.getByLabelText("Type Side projects to confirm"), "Side projects");
  await user.click(confirm);
  expect(onConfirm).toHaveBeenCalledOnce();
  expect(screen.getByText("closed")).toBeInTheDocument();
});

test("cancel closes without confirming", async () => {
  const onConfirm = vi.fn();
  render(<Asker onConfirm={onConfirm} />);
  await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(onConfirm).not.toHaveBeenCalled();
  expect(screen.getByText("closed")).toBeInTheDocument();
});
