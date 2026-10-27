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

test("oneOf choices show their titles and store their values", async () => {
  const user = userEvent.setup();
  const labelled: Schema = {
    type: "object",
    properties: {
      provider: { type: "string", title: "Provider", oneOf: [{ const: "llamacpp", title: "llama.cpp" }, { const: "openai", title: "OpenAI" }] },
      kinds: { type: "array", title: "Provides", items: { type: "string", oneOf: [{ const: "chat", title: "Chat" }, { const: "embed", title: "Search by meaning" }] } },
    },
  };
  render(<Harness schema={labelled} initial={{}} />);
  await user.selectOptions(screen.getByLabelText("Provider"), "llama.cpp");
  await user.click(screen.getByLabelText("Search by meaning"));
  expect(value()).toEqual({ provider: "llamacpp", kinds: ["embed"] });
});

test("fields follow x-order", () => {
  const ordered: Schema = { type: "object", "x-order": ["url", "auth"], properties: { auth: { type: "string", title: "Login" }, url: { type: "string", title: "Server URL" } } };
  render(<Harness schema={ordered} initial={{}} />);
  expect(screen.getAllByRole("textbox").map((b) => b.closest("label")?.textContent)).toEqual(["Server URL", "Login"]);
});

test("x-when hides a field until a sibling, or its default, has a listed value", async () => {
  const user = userEvent.setup();
  const conditional: Schema = {
    type: "object",
    properties: {
      auth: { type: "string", title: "Login", enum: ["none", "basic"] },
      username: { type: "string", title: "Username", "x-when": { auth: ["basic"] } },
    },
  };
  render(<WithHelpers schema={conditional} initial={{}} helpers={{ defaults: { "/auth": "none" } }} />);
  expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Login"), "basic");
  expect(screen.getByLabelText("Username")).toBeInTheDocument();
});

test("a list of strings is edited one per line and shows its default", async () => {
  const user = userEvent.setup();
  const list: Schema = { type: "object", properties: { folders: { type: "array", title: "Folders", items: { type: "string" } } } };
  render(<WithHelpers schema={list} initial={{}} helpers={{ defaults: { "/folders": ["/a"] } }} />);
  const box = screen.getByLabelText("Folders");
  expect(box).toHaveValue("/a");
  await user.type(box, "{Enter}/b");
  expect(value()).toEqual({ folders: ["/a", "/b"] });
});

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

const providerModel: Schema = {
  type: "object",
  properties: {
    provider: { type: "string", title: "Provider", enum: ["hosted", "compatible"] },
    base_url: { type: "string", title: "Base URL" },
    model: { type: "string", title: "Model", "x-options": true, "x-options-needs": { provider: {}, base_url: { provider: ["compatible"] } } },
  },
};

test("load choices waits for the fields it needs, as they apply", async () => {
  const user = userEvent.setup();
  render(<WithHelpers schema={providerModel} initial={{}} helpers={{ loadOptions: vi.fn(async () => []) }} />);
  const button = screen.getByRole("button", { name: "Load choices for Model" });
  expect(button).toBeDisabled();
  expect(screen.getByText("Fill in Provider first.")).toBeInTheDocument();
  await user.selectOptions(screen.getByLabelText("Provider"), "compatible");
  expect(button).toBeDisabled();
  expect(screen.getByText("Fill in Base URL first.")).toBeInTheDocument();
  await user.type(screen.getByLabelText("Base URL"), "http://x");
  expect(button).toBeEnabled();
  await user.clear(screen.getByLabelText("Base URL"));
  await user.selectOptions(screen.getByLabelText("Provider"), "hosted");
  expect(button).toBeEnabled();
});

const tuned: Schema = {
  type: "object",
  properties: {
    connection: { type: "string", title: "Connection" },
    temperature: { type: "number", title: "Temperature", "x-advanced": true },
  },
};

test("advanced fields sit in a collapsed section, open once one is changed", () => {
  const { container, unmount } = render(<Harness schema={tuned} initial={{}} />);
  const details = container.querySelector("details")!;
  expect(details).not.toHaveAttribute("open");
  expect(details).toContainElement(screen.getByLabelText("Temperature"));
  expect(details).not.toContainElement(screen.getByLabelText("Connection"));
  unmount();
  render(<Harness schema={tuned} initial={{ temperature: 0.5 }} />);
  expect(document.querySelector("details")).toHaveAttribute("open");
});
