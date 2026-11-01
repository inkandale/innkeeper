import { parseSSE } from "./sse";

function stream(...chunks: string[]) {
  const encoder = new TextEncoder();
  return new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(encoder.encode(chunk));
      controller.close();
    },
  });
}

test("parses events split across chunks and skips pings", async () => {
  const got: unknown[] = [];
  await parseSSE(stream("event: event\nda", 'ta: {"a":1}\n\n: ping\n\nevent: result\ndata: null\n\n'), (event, data) => got.push([event, data]));
  expect(got).toEqual([["event", { a: 1 }], ["result", null]]);
});

test("U+2028 inside the data stays in the data", async () => {
  const got: unknown[] = [];
  await parseSSE(stream('event: result\ndata: {"a":"x y z"}\n\n'), (event, data) => got.push([event, data]));
  expect(got).toEqual([["result", { a: "x y z" }]]);
});

test("a character split across chunks decodes", async () => {
  const bytes = new TextEncoder().encode('event: result\ndata: {"a":"é"}\n\n');
  const split = bytes.indexOf(0xc3) + 1;
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(bytes.slice(0, split));
      controller.enqueue(bytes.slice(split));
      controller.close();
    },
  });
  const got: unknown[] = [];
  await parseSSE(body, (_event, data) => got.push(data));
  expect(got).toEqual([{ a: "é" }]);
});

function openStream(chunk: string) {
  const cancel = vi.fn();
  const body = new ReadableStream<Uint8Array>({
    start: (controller) => controller.enqueue(new TextEncoder().encode(chunk)),
    cancel,
  });
  return { body, cancel };
}

test("bad JSON cancels the stream", async () => {
  const { body, cancel } = openStream("event: result\ndata: {nope\n\n");
  await expect(parseSSE(body, () => {})).rejects.toThrow(SyntaxError);
  expect(cancel).toHaveBeenCalled();
});

test("a throwing onMessage cancels the stream", async () => {
  const { body, cancel } = openStream("event: error\ndata: {}\n\n");
  const boom = new Error("boom");
  await expect(
    parseSSE(body, () => {
      throw boom;
    }),
  ).rejects.toBe(boom);
  expect(cancel).toHaveBeenCalled();
});

test("a stream that errors rejects with its own error", async () => {
  const boom = new Error("aborted");
  const body = new ReadableStream<Uint8Array>({ start: (controller) => controller.error(boom) });
  await expect(parseSSE(body, () => {})).rejects.toBe(boom);
});
