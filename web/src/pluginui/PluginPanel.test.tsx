import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { createMemoryRouter, RouterProvider, useLocation } from "react-router";
import { PanelLinkProvider, usePanelLink, usePublishDraft, type OpenDraft } from "../forge/panelLink";
import { PluginPanel } from "./PluginPanel";

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

function sse(text: string) {
  return new Response(text, { headers: { "Content-Type": "text/event-stream" } });
}

const entity = (id: number, name: string) => ({ id, library_id: 1, revision: { id: 1, data: { name } } });

let onCall: (req: Request) => Promise<Response>;
beforeEach(() => {
  onCall = async () => sse("event: result\ndata: null\n\n");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const { pathname } = new URL(req.url);
      if (pathname === "/api/cards/7") return json(entity(7, "Alice"));
      if (pathname === "/api/cards/8") return json(entity(8, "Bob"));
      if (pathname.endsWith("/ui/call")) return onCall(req);
      if (pathname === "/api/libraries") return json([{ id: 1 }, { id: 2 }]);
      return json([]);
    }),
  );
});
afterEach(() => vi.unstubAllGlobals());

const draft: OpenDraft = { card: 7, revision: 11, data: { name: "Alice" } };

// OpenForge stands in for the card page's Forge, open only on /cards/7.
function OpenForge() {
  const { pathname } = useLocation();
  return pathname === "/cards/7" ? <Publish /> : null;
}
function Publish() {
  usePublishDraft(draft);
  return null;
}

function renderPanel(grants: string[] = [], extra?: ReactNode) {
  const panel = <PluginPanel instance={3} title="Advisor" src="/api/plugin-ui/card-advisor/index.html" grants={grants} onClose={() => {}} />;
  const element = (
    <PanelLinkProvider>
      {panel}
      <OpenForge />
      {extra}
    </PanelLinkProvider>
  );
  const router = createMemoryRouter([{ path: "*", element }], { initialEntries: ["/cards/7"] });
  const view = render(
    <QueryClientProvider client={new QueryClient()}>
      <RouterProvider router={router} />
    </QueryClientProvider>,
  );
  const frame = screen.getByTitle("Advisor") as HTMLIFrameElement;
  const post = vi.spyOn(frame.contentWindow!, "postMessage");
  const send = (data: unknown) => act(async () => void fireEvent(window, new MessageEvent("message", { source: frame.contentWindow, data })));
  return { frame, post, send, router, ...view };
}

test("the frame has an opaque origin", () => {
  expect(renderPanel().frame.getAttribute("sandbox")).toBe("allow-scripts");
});

test("messages from another window are ignored", async () => {
  const { post } = renderPanel();
  await act(async () => void fireEvent(window, new MessageEvent("message", { source: window, data: { id: 1, call: "host.context" } })));
  expect(post).not.toHaveBeenCalled();
});

test("messages from the frame are answered", async () => {
  const { post, send } = renderPanel();
  await send({ id: 1, call: "host.context" });
  expect(post).toHaveBeenCalledWith(expect.objectContaining({ id: 1 }), "*");
});

test("host.context follows the route, on the same frame", async () => {
  const { frame, post, send, router } = renderPanel();
  await send({ id: 1, call: "host.context" });
  expect(post).toHaveBeenLastCalledWith({ id: 1, result: expect.objectContaining({ card: 7 }) }, "*");
  await act(() => router.navigate("/cards/9?library=2"));
  await send({ id: 2, call: "host.context" });
  expect(post).toHaveBeenLastCalledWith({ id: 2, result: expect.objectContaining({ card: 9, library: 2 }) }, "*");
  expect(screen.getByTitle("Advisor")).toBe(frame);
});

test("host.context carries the open card's draft to a panel granted cards.edit, until the Forge closes", async () => {
  const { post, send, router } = renderPanel(["cards.edit"]);
  await send({ id: 1, call: "host.context" });
  expect(post).toHaveBeenLastCalledWith({ id: 1, result: expect.objectContaining({ draft: { ...draft, snapshot: expect.any(Number) } }) }, "*");
  await act(() => router.navigate("/sync"));
  await send({ id: 2, call: "host.context" });
  expect(post).toHaveBeenLastCalledWith({ id: 2, result: expect.not.objectContaining({ draft: expect.anything() }) }, "*");
});

test("a panel without cards.edit never sees the draft", async () => {
  const { post, send } = renderPanel();
  await send({ id: 1, call: "host.context" });
  expect(post).toHaveBeenLastCalledWith({ id: 1, result: expect.not.objectContaining({ draft: expect.anything() }) }, "*");
});

test("host.openCard opens any positive id", async () => {
  const { send, router } = renderPanel();
  await send({ id: 1, call: "host.openCard", card: 424242 });
  expect(router.state.location.pathname).toBe("/cards/424242");
});

test("a call goes to the panel's own instance", async () => {
  const urls: string[] = [];
  onCall = async (req) => {
    urls.push(new URL(req.url).pathname);
    return sse("event: result\ndata: null\n\n");
  };
  const { send } = renderPanel();
  await send({ id: 1, call: "ui.call", method: "send", instance: 9 });
  await waitFor(() => expect(urls).toEqual(["/api/instances/3/ui/call"]));
});

test("a call forwards its events, and a null result is a result", async () => {
  onCall = async () => sse('event: event\ndata: {"text":"hi"}\n\nevent: result\ndata: null\n\n');
  const { post, send } = renderPanel();
  await send({ id: 1, call: "ui.call", method: "send" });
  await waitFor(() => expect(post).toHaveBeenCalledWith({ id: 1, result: null }, "*"));
  expect(post).toHaveBeenCalledWith({ id: 1, event: { text: "hi" } }, "*");
});

test("a stream that ends without a result is an error", async () => {
  onCall = async () => sse('event: event\ndata: {"text":"hi"}\n\n');
  const { post, send } = renderPanel();
  await send({ id: 1, call: "ui.call", method: "send" });
  await waitFor(() => expect(post).toHaveBeenCalledWith({ id: 1, error: "the call ended early" }, "*"));
});

test("an error event rejects the call with its message", async () => {
  onCall = async () => sse('event: error\ndata: {"message":"no key set"}\n\n');
  const { post, send } = renderPanel();
  await send({ id: 1, call: "ui.call", method: "send" });
  await waitFor(() => expect(post).toHaveBeenCalledWith({ id: 1, error: "no key set" }, "*"));
});

test("a refused call replies with the API's detail", async () => {
  onCall = async () => json({ detail: "plugin is disabled" }, 409);
  const { post, send } = renderPanel();
  await send({ id: 1, call: "ui.call", method: "send" });
  await waitFor(() => expect(post).toHaveBeenCalledWith({ id: 1, error: "plugin is disabled" }, "*"));
});

test("closing the panel aborts running calls", async () => {
  let signal: AbortSignal | undefined;
  onCall = (req) => {
    signal = req.signal;
    return new Promise(() => {});
  };
  const { send, unmount } = renderPanel();
  await send({ id: 1, call: "ui.call", method: "send" });
  await waitFor(() => expect(signal).toBeDefined());
  unmount();
  expect(signal!.aborted).toBe(true);
});

test("a granted panel opens the owner's dialog naming the card", async () => {
  const { send } = renderPanel(["cards.push"]);
  await send({ id: 1, call: "host.request", request: "push", card: 7 });
  expect(await screen.findByRole("dialog", { name: "Push Alice" })).toBeInTheDocument();
});

test("a panel can't change the card while the dialog is open", async () => {
  const { send } = renderPanel(["cards.push", "cards.collect"]);
  await send({ id: 1, call: "host.request", request: "push", card: 7 });
  await screen.findByRole("dialog", { name: "Push Alice" });
  await send({ id: 2, call: "host.request", request: "push", card: 8 });
  await send({ id: 3, call: "host.request", request: "add-to-collection", card: 8 });
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(screen.getByRole("dialog", { name: "Push Alice" })).toBeInTheDocument();
});

test("the same holds for the collection dialog", async () => {
  const { send } = renderPanel(["cards.push", "cards.collect"]);
  await send({ id: 1, call: "host.request", request: "add-to-collection", card: 7 });
  await screen.findByRole("dialog", { name: "Add Alice to a collection" });
  await send({ id: 2, call: "host.request", request: "add-to-collection", card: 8 });
  await send({ id: 3, call: "host.request", request: "push", card: 8 });
  expect(screen.getAllByRole("dialog")).toHaveLength(1);
  expect(screen.getByRole("dialog", { name: "Add Alice to a collection" })).toBeInTheDocument();
});

// Peek shows the proposal the link holds.
function Peek() {
  const { proposal } = usePanelLink();
  return <output aria-label="Proposal">{JSON.stringify(proposal ?? null)}</output>;
}
const proposed = () => JSON.parse(screen.getByLabelText("Proposal").textContent!);

// snapshotOf is the draft snapshot in the panel's last host.context reply.
const snapshotOf = (post: ReturnType<typeof renderPanel>["post"]) => (post.mock.lastCall![0] as { result: { draft: { snapshot: number } } }).result.draft.snapshot;

test("a granted propose hands Review the draft its snapshot names, and opens the card", async () => {
  const { post, send, router } = renderPanel(["cards.edit"], <Peek />);
  await send({ id: 1, call: "host.context" });
  const snapshot = snapshotOf(post);
  await act(() => router.navigate("/sync"));
  await send({ id: 2, call: "host.request", request: "propose", card: 7, params: { revision: 11, snapshot, proposal: { name: "Bob" } } });
  expect(post).toHaveBeenLastCalledWith({ id: 2, result: null }, "*");
  expect(router.state.location.pathname).toBe("/cards/7");
  expect(proposed()).toEqual({ key: 1, at: expect.any(Number), card: 7, revision: 11, start: { name: "Alice" }, proposal: { name: "Bob" }, label: "Advisor" });
});

test("a later read doesn't move the start of a proposal written against an earlier one", async () => {
  // Editor stands in for the owner's hand edit in the Forge after the proposal was written.
  function Editor() {
    const { publish } = usePanelLink();
    return <button onClick={() => publish(7, { ...draft, data: { name: "Alicia" } })}>Edit</button>;
  }
  const { post, send } = renderPanel(["cards.edit"], <><Peek /><Editor /></>);
  await send({ id: 1, call: "host.context" });
  const snapshot = snapshotOf(post);
  fireEvent.click(screen.getByText("Edit"));
  await send({ id: 2, call: "host.context" });
  expect(snapshotOf(post)).not.toBe(snapshot);
  await send({ id: 3, call: "host.request", request: "propose", card: 7, params: { revision: 11, snapshot, proposal: { name: "Bob" } } });
  expect(proposed().start).toEqual({ name: "Alice" });
});

test.each([
  ["no snapshot", {}],
  ["an unknown snapshot", { snapshot: 999 }],
])("a proposal with %s has no start, so Review reads that revision", async (_, extra) => {
  const { send } = renderPanel(["cards.edit"], <Peek />);
  await send({ id: 1, call: "host.context" });
  await send({ id: 2, call: "host.request", request: "propose", card: 7, params: { revision: 11, ...extra, proposal: { name: "Bob" } } });
  expect(proposed()).not.toHaveProperty("start");
});

test("a proposal against another revision has no start, so Review reads that revision", async () => {
  const { post, send } = renderPanel(["cards.edit"], <Peek />);
  await send({ id: 1, call: "host.context" });
  const snapshot = snapshotOf(post);
  await send({ id: 2, call: "host.request", request: "propose", card: 7, params: { revision: 10, snapshot, proposal: { name: "Bob" } } });
  expect(proposed()).not.toHaveProperty("start");
});
