import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { themeLink } from "../theme/link";
import { Theme } from "./Theme";

function json(body: unknown) {
  return new Response(JSON.stringify(body), { headers: { "Content-Type": "application/json" } });
}

let sent: unknown[];
beforeEach(() => {
  sent = [];
  themeLink().remove();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/plugins")
        return json([
          { name: "dusk", version: "1.0.0", capabilities: ["theme"], secrets: [] },
          { name: "sillytavern", version: "0.1.0", capabilities: ["push"], secrets: [] },
        ]);
      if (req.method === "PATCH") {
        const body = await req.json();
        sent.push(body);
        return json(body);
      }
      return json({ theme: "" });
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

function renderTheme() {
  return render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <Theme />
    </QueryClientProvider>,
  );
}

test("previews a theme and saves it", async () => {
  const user = userEvent.setup();
  const { unmount } = renderTheme();
  expect(await screen.findByRole("radio", { name: /Default/ })).toBeChecked();
  await user.click(await screen.findByRole("radio", { name: /dusk/ }));
  expect(screen.queryByRole("radio", { name: /sillytavern/ })).not.toBeInTheDocument();
  expect(themeLink().getAttribute("href")).toBe("/api/themes/dusk.css");
  expect(themeLink().disabled).toBe(false);
  await user.click(screen.getByRole("button", { name: "Save" }));
  await vi.waitFor(() => expect(sent).toEqual([{ theme: "dusk" }]));
  unmount();
  expect(themeLink().disabled).toBe(false);
});

test("leaving without saving puts the saved theme back", async () => {
  const user = userEvent.setup();
  const { unmount } = renderTheme();
  await user.click(await screen.findByRole("radio", { name: /dusk/ }));
  expect(themeLink().disabled).toBe(false);
  unmount();
  expect(themeLink().disabled).toBe(true);
  expect(sent).toEqual([]);
});
