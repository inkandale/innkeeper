import "@testing-library/jest-dom/vitest";

// jsdom has no EventSource, and pages that follow a job open one. Tests that need events stub their own.
if (!("EventSource" in globalThis)) {
  globalThis.EventSource = class {
    addEventListener() {}
    close() {}
  } as unknown as typeof EventSource;
}

// jsdom hides closed popovers, so tests couldn't find their items. Show them; real browsers do the hiding.
const style = document.createElement("style");
style.textContent = "[popover] { display: block !important; }";
document.head.append(style);
