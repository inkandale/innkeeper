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
