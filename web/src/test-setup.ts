import "@testing-library/jest-dom/vitest";
import { toast } from "sonner";

// Sonner keeps toasts in a module-level store and shows a new Toaster the ones still open, so clear them between tests.
afterEach(() => {
  toast.dismiss();
});

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

// jsdom has no pointer capture, which Sonner's swipe-to-dismiss calls on pointer down.
Element.prototype.setPointerCapture ??= () => {};
Element.prototype.releasePointerCapture ??= () => {};
