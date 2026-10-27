import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { FormContext, SchemaForm, type FormHelpers, type Schema } from "./SchemaForm";

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
  expect(value()).toBeNull();
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

test("a decimal in an integer field keeps the number input", async () => {
  const user = userEvent.setup();
  render(<Harness schema={schema} initial={{}} />);
  await user.type(screen.getByLabelText("Port"), "1.5");
  expect(screen.getByLabelText("Port")).toHaveAttribute("type", "number");
});

test("clearing a nested object's last field removes the object", async () => {
  const user = userEvent.setup();
  render(<Harness schema={schema} initial={{ limits: { rate: 2 } }} />);
  await user.clear(screen.getByLabelText("Rate"));
  expect(value()).toBeNull();
});

test("a string with a media type is a multi-line box", async () => {
  const user = userEvent.setup();
  render(<Harness schema={{ type: "object", properties: { body: { type: "string", title: "Body", contentMediaType: "text/markdown" } } }} />);
  const box = screen.getByLabelText("Body");
  expect(box.tagName).toBe("TEXTAREA");
  await user.type(box, "a{Enter}b");
  expect(value()).toEqual({ body: "a\nb" });
});

function WithHelpers({ schema, initial, helpers }: { schema: Schema; initial?: unknown; helpers: Partial<FormHelpers> }) {
  return (
    <FormContext.Provider value={{ defaults: {}, updated: new Set(), ...helpers }}>
      <Harness schema={schema} initial={initial} />
    </FormContext.Provider>
  );
}

const prompts: Schema = {
  type: "object",
  properties: { prompts: { type: "object", properties: { greet: { type: "string", title: "Greet", contentMediaType: "text/plain" } } } },
};

test("a field shows its default, and typing it back drops the override", async () => {
  const user = userEvent.setup();
  render(<WithHelpers schema={prompts} initial={{}} helpers={{ defaults: { "/prompts/greet": "Hello" } }} />);
  const box = screen.getByLabelText("Greet");
  expect(box).toHaveValue("Hello");
  expect(screen.queryByRole("button", { name: "Reset Greet" })).toBeNull();
  await user.type(box, "!");
  expect(value()).toEqual({ prompts: { greet: "Hello!" } });
  await user.type(box, "{Backspace}");
  expect(value()).toBeNull();
});

test("reset clears an override and the updated mark shows on it", async () => {
  const user = userEvent.setup();
  render(
    <WithHelpers
      schema={prompts}
      initial={{ prompts: { greet: "Yo" } }}
      helpers={{ defaults: { "/prompts/greet": "Hello" }, updated: new Set(["/prompts/greet"]) }}
    />,
  );
  expect(screen.getByText("Default updated")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: "Reset Greet" }));
  expect(value()).toBeNull();
  expect(screen.getByLabelText("Greet")).toHaveValue("Hello");
  expect(screen.queryByText("Default updated")).toBeNull();
});

test("an array of string enum is a checkbox group", async () => {
  const user = userEvent.setup();
  const kinds: Schema = { type: "object", properties: { kinds: { type: "array", title: "Kinds", items: { type: "string", enum: ["chat", "embed"] } } } };
  render(<Harness schema={kinds} initial={{}} />);
  expect(screen.getByRole("group", { name: "Kinds" })).toBeInTheDocument();
  await user.click(screen.getByLabelText("embed"));
  await user.click(screen.getByLabelText("chat"));
  expect(value()).toEqual({ kinds: ["chat", "embed"] });
  await user.click(screen.getByLabelText("chat"));
  await user.click(screen.getByLabelText("embed"));
  expect(value()).toBeNull();
});

test("x-options loads choices into a datalist and still takes free text", async () => {
  const user = userEvent.setup();
  const loadOptions = vi.fn(async () => [{ value: "m1", label: "Model one" }]);
  const model: Schema = { type: "object", properties: { model: { type: "string", title: "Model", "x-options": true } } };
  const { container } = render(<WithHelpers schema={model} initial={{}} helpers={{ loadOptions }} />);
  await user.click(screen.getByRole("button", { name: "Load choices for Model" }));
  expect(loadOptions).toHaveBeenCalledWith("/model");
  expect(container.querySelector("datalist option")).toHaveAttribute("value", "m1");
  await user.type(screen.getByLabelText("Model"), "custom");
  expect(value()).toEqual({ model: "custom" });
});

test("a failed choices lookup shows its message", async () => {
  const user = userEvent.setup();
  const loadOptions = vi.fn(async () => {
    throw new Error("server down");
  });
  const model: Schema = { type: "object", properties: { model: { type: "string", title: "Model", "x-options": true } } };
  render(<WithHelpers schema={model} initial={{}} helpers={{ loadOptions }} />);
  await user.click(screen.getByRole("button", { name: "Load choices for Model" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("server down");
});

test("format instance picks from the instances offering the service", async () => {
  const user = userEvent.setup();
  const instances = vi.fn(() => [{ id: 7, label: "Local LLM" }]);
  const conn: Schema = { type: "object", properties: { connection: { type: "integer", title: "Connection", format: "instance", "x-offers": "ai.chat" } } };
  render(<WithHelpers schema={conn} initial={{}} helpers={{ instances }} />);
  expect(instances).toHaveBeenCalledWith("ai.chat");
  await user.selectOptions(screen.getByLabelText("Connection"), "Local LLM");
  expect(value()).toEqual({ connection: 7 });
});

const requiredConn: Schema = {
  type: "object",
  required: ["connection"],
  properties: { connection: { type: "integer", title: "Connection", format: "instance", "x-offers": "ai.chat" } },
};

test("a required instance picker with one choice starts with it chosen", () => {
  render(<WithHelpers schema={requiredConn} initial={{}} helpers={{ instances: () => [{ id: 7, label: "Local LLM" }] }} />);
  expect(value()).toEqual({ connection: 7 });
});

test("a required instance picker with several choices waits for the owner", () => {
  const two = () => [{ id: 7, label: "Local LLM" }, { id: 8, label: "OpenAI" }];
  render(<WithHelpers schema={requiredConn} initial={{}} helpers={{ instances: two }} />);
  expect(value()).toEqual({});
});

test("an instance picker with no choices says where to set one up", () => {
  render(<WithHelpers schema={requiredConn} initial={{}} helpers={{ instances: () => [] }} />);
  expect(screen.getByRole("link", { name: "Set one up in Settings › Plugins" })).toHaveAttribute("href", "/settings/plugins");
});

const kindsWithDefault: Schema = {
  type: "object",
  properties: { kinds: { type: "array", title: "Kinds", items: { type: "string", enum: ["chat", "embed"] } } },
};

test("unchecking the last default choice is an empty override", async () => {
  const user = userEvent.setup();
  render(<WithHelpers schema={kindsWithDefault} initial={{}} helpers={{ defaults: { "/kinds": ["chat"] } }} />);
  await user.click(screen.getByLabelText("chat"));
  expect(value()).toEqual({ kinds: [] });
  expect(screen.getByLabelText("chat")).not.toBeChecked();
  expect(screen.getByRole("button", { name: "Reset Kinds" })).toBeInTheDocument();
});

test("picking a default's choices in another order is no override", async () => {
  const user = userEvent.setup();
  render(<WithHelpers schema={kindsWithDefault} initial={{}} helpers={{ defaults: { "/kinds": ["embed", "chat"] } }} />);
  await user.click(screen.getByLabelText("chat"));
  await user.click(screen.getByLabelText("chat"));
  expect(value()).toBeNull();
});

test("clearing a defaulted field is an empty override", async () => {
  const user = userEvent.setup();
  render(<WithHelpers schema={prompts} initial={{}} helpers={{ defaults: { "/prompts/greet": "Hello" } }} />);
  await user.clear(screen.getByLabelText("Greet"));
  expect(value()).toEqual({ prompts: { greet: "" } });
  expect(screen.getByLabelText("Greet")).toHaveValue("");
  expect(screen.getByRole("button", { name: "Reset Greet" })).toBeInTheDocument();
});

test("load choices is disabled while loading and shows a thrown non-error", async () => {
  const user = userEvent.setup();
  let fail!: (reason: unknown) => void;
  const loadOptions = vi.fn(() => new Promise<{ value: string }[]>((_, reject) => (fail = reject)));
  const model: Schema = { type: "object", properties: { model: { type: "string", title: "Model", "x-options": true } } };
  render(<WithHelpers schema={model} initial={{}} helpers={{ loadOptions }} />);
  const button = screen.getByRole("button", { name: "Load choices for Model" });
  await user.click(button);
  expect(button).toBeDisabled();
  fail("no route");
  expect(await screen.findByRole("alert")).toHaveTextContent("no route");
  expect(button).toBeEnabled();
});

test("an x-options field without a loader has no datalist", () => {
  const model: Schema = { type: "object", properties: { model: { type: "string", title: "Model", "x-options": true } } };
  render(<Harness schema={model} initial={{}} />);
  expect(screen.getByLabelText("Model")).not.toHaveAttribute("list");
});
