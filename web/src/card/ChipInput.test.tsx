import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { ChipInput } from "./ChipInput";

function Harness({ start }: { start: string[] }) {
  const [values, setValues] = useState(start);
  return (
    <>
      <ChipInput label="Entry 1 keys" values={values} onChange={setValues} />
      <output>{values.join("|")}</output>
    </>
  );
}

test("Enter and comma add a chip, × removes one, and Backspace on empty removes the last", async () => {
  render(<Harness start={["anvil"]} />);
  const input = screen.getByLabelText("Add to Entry 1 keys");
  await userEvent.type(input, "forge{Enter}iron,");
  expect(screen.getByRole("status")).toHaveTextContent("anvil|forge|iron");
  await userEvent.type(input, "anvil{Enter}");
  expect(screen.getByRole("status")).toHaveTextContent("anvil|forge|iron");
  await userEvent.click(screen.getByRole("button", { name: "Remove forge from Entry 1 keys" }));
  expect(screen.getByRole("status")).toHaveTextContent("anvil|iron");
  await userEvent.type(input, "{Backspace}");
  expect(screen.getByRole("status")).toHaveTextContent("anvil");
});

test("leaving the input adds what was typed", async () => {
  render(<Harness start={[]} />);
  await userEvent.type(screen.getByLabelText("Add to Entry 1 keys"), "kraken");
  await userEvent.tab();
  expect(screen.getByRole("status")).toHaveTextContent("kraken");
});
