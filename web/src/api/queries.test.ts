import { namesQuery } from "./queries";

test("namesQuery asks for no more than the API's page maximum", () => {
  const ids = Array.from({ length: 501 }, (_, i) => i + 1);
  expect(namesQuery("card", 1, ids).queryKey).toEqual(["search", "card", expect.objectContaining({ page: { size: 500 } })]);
});
