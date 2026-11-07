import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router";
import { Hiding } from "./Hiding";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const setting = <T,>(value: T | null, def: T | null, overridden = false) => ({ value, default: def, overridden });
let current: Record<string, unknown>;
let calls: string[];
let refuse: string | undefined;
let rules: unknown[];
beforeEach(() => {
  calls = [];
  refuse = undefined;
  rules = [];
  current = {
    duplicates: setting("off", "off"),
    duplicate_distance: setting(3, 3),
    duplicate_keep: setting("best", "best"),
    duplicate_decide: setting(null, null),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (path === "/api/rules") return json(rules);
      if (req.method !== "GET") calls.push(`${req.method} ${path} ${await req.clone().text()}`);
      if (path === "/api/libraries/2/settings" && req.method === "PATCH") {
        if (refuse) return json({ detail: refuse }, 400);
        const body = await req.json();
        if ("duplicates" in body) current.duplicates = setting(body.duplicates ?? "off", "off", body.duplicates != null);
      }
      if (path === "/api/libraries/2/settings") return json(current);
      if (path === "/api/libraries/2/hide") return json({ id: 9, kind: "library.hide", status: "queued", created_at: "", payload: {} }, 202);
      return json({ id: 9, kind: "library.hide", status: "running", created_at: "", payload: {} });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const renderIt = () =>
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <MemoryRouter>
        <Hiding library={2} />
      </MemoryRouter>
    </QueryClientProvider>,
  );

test("hide now waits for something to hide, then runs the job", async () => {
  renderIt();
  const user = userEvent.setup();
  const hide = await screen.findByRole("button", { name: "Hide now" });
  expect(hide).toBeDisabled();
  await user.selectOptions(screen.getByRole("combobox", { name: /Duplicates/ }), "Exact copies");
  await waitFor(() => expect(calls).toEqual(['PATCH /api/libraries/2/settings {"duplicates":"exact"}']));
  expect(await screen.findByRole("combobox", { name: /Keep/ })).toBeInTheDocument();
  await waitFor(() => expect(hide).toBeEnabled());
  await user.click(hide);
  await waitFor(() => expect(calls).toContain("POST /api/libraries/2/hide "));
  expect(await screen.findByRole("link", { name: /Hiding/ })).toHaveAttribute("href", "/settings/jobs/9");
});

test("a saved change says so next to the settings", async () => {
  renderIt();
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByRole("combobox", { name: /Duplicates/ }), "Exact copies");
  expect(await screen.findByRole("status")).toHaveTextContent("Saved");
});

test("a refused change shows why next to the settings", async () => {
  refuse = "duplicates must be off, exact, or near";
  renderIt();
  const user = userEvent.setup();
  await user.selectOptions(await screen.findByRole("combobox", { name: /Duplicates/ }), "Exact copies");
  expect(await screen.findByText("duplicates must be off, exact, or near")).toBeInTheDocument();
  expect(screen.queryByRole("status")).not.toBeInTheDocument();
});

test("rules that cover the library are listed and let Hide now run", async () => {
  rules = [{ id: 9, name: "Hide junk", saved_search_id: 3, saved_search: "Junk", action: "archive", all_libraries: true, enabled: true, affected: [] }];
  renderIt();
  expect(await screen.findByRole("link", { name: "Hide junk" })).toHaveAttribute("href", "/rules/9");
  expect(screen.getByRole("button", { name: "Hide now" })).toBeEnabled();
});
