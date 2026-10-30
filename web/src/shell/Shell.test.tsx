import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Shell } from "./Shell";

test("the rail links every section and marks the current one", () => {
  const router = createMemoryRouter(
    [{ path: "/", element: <Shell />, children: [{ path: "cards", element: <p>cards page</p> }] }],
    { initialEntries: ["/cards"] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  for (const name of ["Cards", "Lorebooks", "Collections", "Sync", "Settings"]) {
    expect(screen.getByRole("link", { name })).toBeInTheDocument();
  }
  expect(screen.queryByRole("link", { name: "Archive" })).not.toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Cards" })).toHaveAttribute("aria-current", "page");
  expect(screen.getByText("cards page")).toBeInTheDocument();
});

test("logout goes to /login even when the request fails", async () => {
  const assign = vi.fn();
  vi.stubGlobal("location", { ...window.location, origin: window.location.origin, assign });
  vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("offline"))));
  const router = createMemoryRouter([{ path: "/", element: <Shell /> }]);
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  await userEvent.click(screen.getByRole("button", { name: "Collapse menu" }));
  await userEvent.click(screen.getByRole("button", { name: "Log out" }));
  await waitFor(() => expect(assign).toHaveBeenCalledWith("/login"));
  vi.unstubAllGlobals();
});

const libraries = [
  { id: 1, name: "Library", cards: 3, lorebooks: 0, created_at: "2026-10-01T00:00:00Z" },
  { id: 2, name: "Chub archive", cards: 9, lorebooks: 1, created_at: "2026-10-02T00:00:00Z" },
];

function renderShell(entry: string) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => new Response(JSON.stringify(libraries), { headers: { "Content-Type": "application/json" } })),
  );
  const router = createMemoryRouter(
    [{ path: "/", element: <Shell />, children: [{ path: "*", element: <p>page</p> }] }],
    { initialEntries: [entry] },
  );
  render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  return router;
}

describe("library switcher", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => vi.unstubAllGlobals());

  test("shows the URL's library, remembers it, and keeps it in the rail links", async () => {
    renderShell("/cards?library=2");
    expect(await screen.findByRole("option", { name: "Chub archive" })).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Library" })).toHaveValue("2");
    expect(screen.getByRole("link", { name: "Lorebooks" })).toHaveAttribute("href", "/lorebooks?library=2");
    expect(localStorage.getItem("innkeeper.library")).toBe("2");
  });

  test("without ?library= it shows the remembered library", async () => {
    localStorage.setItem("innkeeper.library", "2");
    renderShell("/settings/jobs");
    await screen.findByRole("option", { name: "Chub archive" });
    expect(screen.getByRole("combobox", { name: "Library" })).toHaveValue("2");
    expect(screen.getByRole("link", { name: "Cards" })).toHaveAttribute("href", "/cards?library=2");
  });

  test("falls back to library 1 when storage throws", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    renderShell("/cards");
    await screen.findByRole("option", { name: "Chub archive" });
    expect(screen.getByRole("combobox", { name: "Library" })).toHaveValue("1");
    vi.restoreAllMocks();
  });

  test("a remembered library that no longer exists falls back to library 1", async () => {
    localStorage.setItem("innkeeper.library", "5");
    renderShell("/settings/jobs");
    await screen.findByRole("option", { name: "Chub archive" });
    expect(screen.getByRole("combobox", { name: "Library" })).toHaveValue("1");
    expect(screen.getByRole("link", { name: "Cards" })).toHaveAttribute("href", "/cards?library=1");
    expect(localStorage.getItem("innkeeper.library")).toBe("1");
  });

  test("choosing a library opens the same list in it without the old filters", async () => {
    const router = renderShell("/lorebooks?library=1&f=tag:x");
    await screen.findByRole("option", { name: "Chub archive" });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Library" }), "2");
    expect(router.state.location.pathname).toBe("/lorebooks");
    expect(router.state.location.search).toBe("?library=2");
    expect(localStorage.getItem("innkeeper.library")).toBe("2");
  });

  test("choosing a library on Sync stays on Sync", async () => {
    const router = renderShell("/sync?library=1");
    await screen.findByRole("option", { name: "Chub archive" });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Library" }), "2");
    expect(router.state.location.pathname + router.state.location.search).toBe("/sync?library=2");
  });

  test("choosing a library from a card page opens its cards", async () => {
    const router = renderShell("/cards/7");
    await screen.findByRole("option", { name: "Chub archive" });
    await userEvent.selectOptions(screen.getByRole("combobox", { name: "Library" }), "2");
    expect(router.state.location.pathname + router.state.location.search).toBe("/cards?library=2");
  });
});

test("the eye button blurs NSFW card images and remembers it", async () => {
  localStorage.clear();
  const router = createMemoryRouter([{ path: "/", element: <Shell />, children: [{ index: true, element: <img className="nsfw" alt="art" /> }] }]);
  const view = () =>
    render(
      <QueryClientProvider client={new QueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  const { unmount } = view();
  const eye = screen.getByRole("button", { name: "Blur NSFW cards" });
  expect(screen.getByAltText("art").closest(".blur-nsfw")).toBeNull();
  await userEvent.click(eye);
  expect(eye).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByAltText("art").closest(".blur-nsfw")).not.toBeNull();
  unmount();
  view();
  expect(screen.getByAltText("art").closest(".blur-nsfw")).not.toBeNull();
});
