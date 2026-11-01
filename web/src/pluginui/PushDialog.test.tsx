import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { PushDialog } from "./PushDialog";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const instances = [
  { id: 2, plugin: "lumiverse", label: "Lumiverse" },
  { id: 3, plugin: "tavern", label: "Tavern" },
  { id: 4, plugin: "chub", label: "Chub" },
];
const plugins = [
  { name: "lumiverse", capabilities: ["push", "pull"] },
  { name: "tavern", capabilities: ["push"] },
  { name: "chub", capabilities: ["pull"] },
];

let cardResponse: () => Response;
beforeEach(() => {
  cardResponse = () => json({ id: 7, library_id: 1, revision: { id: 1, data: { name: "Alice" } } });
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const { pathname } = new URL(req.url);
      if (pathname === "/api/cards/7") return cardResponse();
      if (pathname === "/api/instances") return json(instances);
      if (pathname === "/api/plugins") return json(plugins);
      if (pathname === "/api/cards/7/bindings") return json([{ id: 1, instance_id: 3, push: true }, { id: 2, instance_id: 4, push: false }]);
      if (pathname === "/api/cards/7/push-preview") return json({ replaces: true, new_copy: false, confirm: "c", changes: [] });
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderDialog() {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <PushDialog card={7} onClose={() => {}} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

test("the picker names the card and leaves out pull-only instances", async () => {
  renderDialog();
  expect(await screen.findByRole("dialog", { name: "Push Alice" })).toBeInTheDocument();
  expect(await screen.findByRole("checkbox", { name: "Lumiverse" })).toBeInTheDocument();
  expect(screen.queryByRole("checkbox", { name: "Chub" })).not.toBeInTheDocument();
});

test("apps holding a copy start picked, and Preview hands over to the push preview", async () => {
  renderDialog();
  expect(await screen.findByRole("checkbox", { name: "Tavern" })).toBeChecked();
  await userEvent.click(screen.getByRole("button", { name: "Preview" }));
  expect(await screen.findByRole("dialog", { name: "Push to Tavern" })).toBeInTheDocument();
});

test.each([
  ["doesn't exist", () => json({ detail: "not found" }, 404), "This card doesn't exist."],
  ["is in another library", () => json({ id: 7, library_id: 2, revision: { id: 1, data: { name: "Alice" } } }), "This card is in another library."],
])("a card that %s offers only Cancel", async (_case, response, message) => {
  cardResponse = response;
  renderDialog();
  expect(await screen.findByText(message)).toBeInTheDocument();
  expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Cancel"]);
  expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
});
