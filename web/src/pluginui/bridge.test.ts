import { handleBridge, type Host } from "./bridge";

const host = (grants: string[] = []) => {
  const h: Host = {
    grants,
    context: vi.fn(() => ({ library: 1, card: 7, theme: {} })),
    openCard: vi.fn(),
    openPush: vi.fn(),
    openCollection: vi.fn(),
    call: vi.fn(async (_m, _p, _signal, onEvent) => {
      onEvent({ text: "hi" });
      return { status: "ok" };
    }),
  };
  return h;
};

function run(h: Host, msg: unknown, running = new Map<number, AbortController>()) {
  const posted: unknown[] = [];
  return handleBridge(msg, h, running, (m) => posted.push(m)).then(() => posted);
}

test("host.context replies with the host's grants", async () => {
  expect(await run(host(["cards.push"]), { id: 1, call: "host.context" })).toEqual([
    { id: 1, result: { library: 1, card: 7, theme: {}, grants: ["cards.push"] } },
  ]);
});

test("ui.call forwards events, then the result", async () => {
  const posted = await run(host(), { id: 2, call: "ui.call", method: "send", params: {} });
  expect(posted).toEqual([{ id: 2, event: { text: "hi" } }, { id: 2, result: { status: "ok" } }]);
});

test("ui.call with a non-string method is refused", async () => {
  const h = host();
  expect(await run(h, { id: 9, call: "ui.call", method: { x: 1 } })).toEqual([{ id: 9, error: "bad method" }]);
  expect(h.call).not.toHaveBeenCalled();
});

test("ui.call reusing a running id is refused and keeps the running controller", async () => {
  const running = new Map<number, AbortController>();
  const controller = new AbortController();
  running.set(10, controller);
  const h = host();
  expect(await run(h, { id: 10, call: "ui.call", method: "send" }, running)).toEqual([{ id: 10, error: "id in use" }]);
  expect(h.call).not.toHaveBeenCalled();
  expect(running.get(10)).toBe(controller);
});

test("host.request without its grant is refused and opens nothing", async () => {
  const h = host();
  expect(await run(h, { id: 3, call: "host.request", request: "push", card: 7 })).toEqual([{ id: 3, error: "not granted" }]);
  expect(h.openPush).not.toHaveBeenCalled();
});

test("host.request with its grant opens core's dialog", async () => {
  const h = host(["cards.push", "cards.collect"]);
  await run(h, { id: 4, call: "host.request", request: "push", card: 7 });
  await run(h, { id: 5, call: "host.request", request: "add-to-collection", card: 7 });
  expect(h.openPush).toHaveBeenCalledWith(7);
  expect(h.openCollection).toHaveBeenCalledWith(7);
});

test.each(["constructor", "__proto__", "toString"])("prototype-named request %s is unknown", async (request) => {
  expect(await run(host(), { id: 11, call: "host.request", request, card: 7 })).toEqual([{ id: 11, error: "unknown request" }]);
});

test.each(["7/../../settings", 0, -3, 1.5, Number.MAX_SAFE_INTEGER + 1])("card %s is refused", async (card) => {
  const h = host(["cards.push"]);
  expect(await run(h, { id: 6, call: "host.openCard", card })).toEqual([{ id: 6, error: "bad card" }]);
  expect(h.openCard).not.toHaveBeenCalled();
});

test("malformed and unknown messages are ignored or refused", async () => {
  expect(await run(host(), "nonsense")).toEqual([]);
  expect(await run(host(), { id: 7, call: "host.delete" })).toEqual([{ id: 7, error: "unknown call" }]);
});

test("ui.abort aborts the matching call and ignores unknown ids", async () => {
  const running = new Map<number, AbortController>();
  const controller = new AbortController();
  running.set(8, controller);
  expect(await run(host(), { id: 99, call: "ui.abort" }, running)).toEqual([]);
  expect(controller.signal.aborted).toBe(false);
  await run(host(), { id: 8, call: "ui.abort" }, running);
  expect(controller.signal.aborted).toBe(true);
});

test.each([1.5, Number.NaN, Number.MAX_SAFE_INTEGER + 1, "1"])("id %s is ignored", async (id) => {
  const h = host();
  expect(await run(h, { id, call: "host.context" })).toEqual([]);
  expect(await run(h, { id, call: "ui.call", method: "send" })).toEqual([]);
  expect(h.call).not.toHaveBeenCalled();
});

test("ui.call past the running limit is refused", async () => {
  const running = new Map([1, 2, 3, 4].map((id) => [id, new AbortController()]));
  const h = host();
  expect(await run(h, { id: 5, call: "ui.call", method: "send" }, running)).toEqual([{ id: 5, error: "too many calls" }]);
  expect(h.call).not.toHaveBeenCalled();
  expect(running.size).toBe(4);
});

test("a finished call frees its id and its slot", async () => {
  const running = new Map([1, 2, 3].map((id) => [id, new AbortController()]));
  const h = host();
  await run(h, { id: 4, call: "ui.call", method: "send" }, running);
  expect(running.has(4)).toBe(false);
  expect(await run(h, { id: 4, call: "ui.call", method: "send" }, running)).toContainEqual({ id: 4, result: { status: "ok" } });
  vi.mocked(h.call).mockRejectedValueOnce(new Error("boom"));
  await run(h, { id: 5, call: "ui.call", method: "send" }, running);
  expect(running.size).toBe(3);
});

test.each([
  [new Error("boom"), "boom"],
  ["not an Error", "the call failed"],
])("a call that throws %s replies with its error", async (thrown, error) => {
  const h = host();
  vi.mocked(h.call).mockRejectedValueOnce(thrown);
  expect(await run(h, { id: 12, call: "ui.call", method: "send" })).toEqual([{ id: 12, error }]);
});
