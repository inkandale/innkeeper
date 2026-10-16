import "@testing-library/jest-dom/vitest";

// jsdom has no EventSource, and pages that follow a job open one. Tests that need events stub their own.
if (!("EventSource" in globalThis)) {
  globalThis.EventSource = class {
    addEventListener() {}
    close() {}
  } as unknown as typeof EventSource;
}
