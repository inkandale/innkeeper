/** parseSSE reads a text/event-stream body, calling onMessage with each event's name and JSON data. */
export async function parseSSE(body: ReadableStream<Uint8Array>, onMessage: (event: string, data: unknown) => void) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) return;
      buffer += decoder.decode(value, { stream: true });
      let end: number;
      while ((end = buffer.indexOf("\n\n")) >= 0) {
        const block = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        // Not `.`: it stops at U+2028 and U+2029, which JSON may hold raw.
        const event = /^event: ([^\n]*)/m.exec(block)?.[1];
        const data = /^data: ([^\n]*)/m.exec(block)?.[1];
        if (event && data !== undefined) onMessage(event, JSON.parse(data));
      }
    }
  } finally {
    // cancel rejects on a stream that errored, and that error is already on its way up.
    void reader.cancel().catch(() => {});
  }
}
