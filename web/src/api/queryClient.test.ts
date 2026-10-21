import { expect, test } from "vitest";
import { newQueryClient } from "./queryClient";

test("a successful change marks cached searches out of date", async () => {
  const client = newQueryClient();
  client.setQueryData(["search", "card", { library: 1 }], { hits: [], total: 0 });
  client.setQueryData(["instances"], []);
  await client.getMutationCache().build(client, { mutationFn: async () => "saved" }).execute(undefined);
  expect(client.getQueryState(["search", "card", { library: 1 }])?.isInvalidated).toBe(true);
  expect(client.getQueryState(["instances"])?.isInvalidated).toBe(false);
});
