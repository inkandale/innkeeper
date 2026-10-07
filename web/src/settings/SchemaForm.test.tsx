import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { SchemaForm, type Schema } from "./SchemaForm";

function Harness({ schema, initial }: { schema: Schema; initial?: unknown }) {
  const [v, setV] = useState<unknown>(initial);
  return (
    <>
      <SchemaForm schema={schema} label="" value={v} onChange={setV} />
      <output data-testid="value">{JSON.stringify(v ?? null)}</output>
    </>
  );
}
const value = () => JSON.parse(screen.getByTestId("value").textContent!);

const schema: Schema = {
  type: "object",
  required: ["url"],
  properties: {
    url: { type: "string", title: "Server URL", description: "The server." },
    auth: { type: "string", title: "Login", enum: ["none", "basic"] },
    port: { type: "integer", title: "Port" },
    tls: { type: "boolean", title: "TLS" },
    limits: { type: "object", title: "Limits", properties: { rate: { type: "number", title: "Rate" } } },
    hosts: { type: "array", title: "Hosts" },
  },
};

test("renders each type and builds the value", async () => {
  const user = userEvent.setup();
  render(<Harness schema={schema} initial={{}} />);
  expect(screen.getByLabelText("Server URL")).toBeRequired();
  expect(screen.getByText("The server.")).toBeInTheDocument();
  await user.type(screen.getByLabelText("Server URL"), "http://st");
  await user.selectOptions(screen.getByLabelText("Login"), "basic");
  await user.type(screen.getByLabelText("Port"), "8000");
  await user.click(screen.getByLabelText("TLS"));
  await user.type(screen.getByLabelText("Rate"), "1.5");
  await user.click(screen.getByLabelText("Hosts"));
  await user.paste('["a"]');
  expect(value()).toEqual({ url: "http://st", auth: "basic", port: 8000, tls: true, limits: { rate: 1.5 }, hosts: ["a"] });
});

test("clearing a field removes its key", async () => {
  const user = userEvent.setup();
  render(<Harness schema={schema} initial={{ url: "x", port: 1 }} />);
  await user.clear(screen.getByLabelText("Server URL"));
  await user.clear(screen.getByLabelText("Port"));
  expect(value()).toEqual({});
});

test("bad JSON keeps the last good value and blocks the submit", () => {
  render(<Harness schema={schema} initial={{ hosts: ["a"] }} />);
  const box = screen.getByLabelText("Hosts");
  fireEvent.change(box, { target: { value: "[" } });
  expect(box).toBeInvalid();
  expect(value()).toEqual({ hosts: ["a"] });
});

test("a value of the wrong type is shown as JSON", () => {
  render(<Harness schema={schema} initial={{ port: "eighty" }} />);
  expect(screen.getByLabelText("Port")).toHaveValue('"eighty"');
});
