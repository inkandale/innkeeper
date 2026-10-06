import { formFields } from "./fields";

test("formFields lists each property, in order, with its input type", () => {
  const schema = {
    type: "object",
    properties: {
      username: { type: "string", title: "User name" },
      password: { type: "string", format: "password" },
      code: { type: "string", writeOnly: true },
    },
    required: ["password"],
  };
  expect(formFields(schema)).toEqual([
    { name: "username", label: "User name", type: "text", required: false, autoComplete: "username" },
    { name: "password", label: "password", type: "password", required: true, autoComplete: "current-password" },
    { name: "code", label: "code", type: "password", required: false, autoComplete: "one-time-code" },
  ]);
});

test("formFields treats a field named password as secret", () => {
  expect(formFields({ type: "object", properties: { password: { type: "string" } } })[0]?.type).toBe("password");
});

test("formFields returns nothing for a schema without properties", () => {
  expect(formFields({})).toEqual([]);
});
