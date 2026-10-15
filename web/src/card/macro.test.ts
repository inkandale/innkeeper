import { macroParts } from "./macro";

test("macroParts puts each {{…}} token at an odd index", () => {
  expect(macroParts("Hi {{user}}, I'm {{char}}.")).toEqual(["Hi ", "{{user}}", ", I'm ", "{{char}}", "."]);
  expect(macroParts("{{random::a,b}}")).toEqual(["", "{{random::a,b}}", ""]);
});

test("macroParts leaves unclosed braces as text", () => {
  expect(macroParts("{{user and {char}}")).toEqual(["{{user and {char}}"]);
});

test("macroParts tints the innermost token of a nested macro", () => {
  expect(macroParts("{{pick::{{user}}}}")).toEqual(["{{pick::", "{{user}}", "}}"]);
});

test("macroParts marks <START> only in example dialogue", () => {
  expect(macroParts("<START>\n{{char}}: Hi")).toEqual(["<START>\n", "{{char}}", ": Hi"]);
  expect(macroParts("<START>\n{{char}}: Hi", true)).toEqual(["", "<START>", "\n", "{{char}}", ": Hi"]);
});
