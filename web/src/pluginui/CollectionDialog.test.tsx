import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { CollectionDialog } from "./CollectionDialog";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

let cardResponse: () => Response;
let put: () => Response;
let puts: string[];
let collectionQueries: string[];
beforeEach(() => {
  cardResponse = () => json({ id: 7, library_id: 2, revision: { id: 1, data: { name: "Alice" } } });
  put = () => new Response(null, { status: 204 });
  puts = [];
  collectionQueries = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const url = new URL(req.url);
      if (req.method === "PUT") {
        puts.push(url.pathname);
        return put();
      }
      if (url.pathname === "/api/cards/7") return cardResponse();
      if (url.pathname === "/api/collections") {
        collectionQueries.push(url.search);
        return json([{ id: 5, name: "Favorites" }]);
      }
      return json([{ id: 1 }, { id: 2 }]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderDialog(onClose = () => {}) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter initialEntries={["/?library=2"]}>
        <CollectionDialog card={7} onClose={onClose} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

async function pick() {
  await screen.findByRole("option", { name: "Favorites" });
  await userEvent.selectOptions(screen.getByRole("combobox", { name: "Collection" }), "Favorites");
}

test("it names the card and lists the current library's collections", async () => {
  renderDialog();
  expect(await screen.findByRole("dialog", { name: "Add Alice to a collection" })).toBeInTheDocument();
  expect(await screen.findByRole("option", { name: "Favorites" })).toBeInTheDocument();
  expect(collectionQueries).toEqual(["?library=2"]);
});

test("Add puts the card in the picked collection", async () => {
  const onClose = vi.fn();
  renderDialog(onClose);
  await pick();
  await userEvent.click(screen.getByRole("button", { name: "Add" }));
  await waitFor(() => expect(onClose).toHaveBeenCalled());
  expect(puts).toEqual(["/api/collections/5/cards/7"]);
});

test("an API error shows and keeps the dialog open", async () => {
  put = () => json({ detail: "card is in another library" }, 422);
  const onClose = vi.fn();
  renderDialog(onClose);
  await pick();
  await userEvent.click(screen.getByRole("button", { name: "Add" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("card is in another library");
  expect(onClose).not.toHaveBeenCalled();
});

test.each([
  ["doesn't exist", () => json({ detail: "not found" }, 404), "This card doesn't exist."],
  ["is in another library", () => json({ id: 7, library_id: 1, revision: { id: 1, data: { name: "Alice" } } }), "This card is in another library."],
])("a card that %s offers only Cancel", async (_case, response, message) => {
  cardResponse = response;
  renderDialog();
  expect(await screen.findByText(message)).toBeInTheDocument();
  expect(screen.getAllByRole("button").map((b) => b.textContent)).toEqual(["Cancel"]);
  expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
});
