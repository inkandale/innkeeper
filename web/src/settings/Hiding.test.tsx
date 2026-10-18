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
beforeEach(() => {
  calls = [];
  current = {
    score_instance: setting(5, 5),
    hide_below: setting(null, null),
    duplicates: setting("off", "off"),
    duplicate_distance: setting(3, 3),
    duplicate_keep: setting("best", "best"),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const path = new URL(req.url).pathname;
      if (req.method !== "GET") calls.push(`${req.method} ${path} ${await req.clone().text()}`);
      if (path === "/api/libraries/2/settings" && req.method === "PATCH") {
        const body = await req.json();
        if ("duplicates" in body) current.duplicates = setting(body.duplicates ?? "off", "off", body.duplicates != null);
        if ("hide_below" in body) current.hide_below = setting(body.hide_below, null, body.hide_below != null);
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

test("a blank threshold follows the default", async () => {
  renderIt();
  const user = userEvent.setup();
  const below = await screen.findByRole("spinbutton", { name: /Hide below score/ });
  await user.type(below, "40");
  await user.tab();
  await waitFor(() => expect(calls).toEqual(['PATCH /api/libraries/2/settings {"hide_below":40}']));
  await user.clear(screen.getByRole("spinbutton", { name: /Hide below score/ }));
  await user.tab();
  await waitFor(() => expect(calls).toEqual(['PATCH /api/libraries/2/settings {"hide_below":40}', 'PATCH /api/libraries/2/settings {"hide_below":null}']));
});
