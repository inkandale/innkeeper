/// <reference types="vitest/config" />
import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// A local `innkeeper api` serves everything that isn't the UI.
const api = process.env.INNKEEPER_URL ?? "http://localhost:8080";

// Sonner injects its CSS as a <style> tag, which the CSP's style-src 'self' blocks; Toasts imports its stylesheet instead.
const sonnerNoInject: Plugin = {
  name: "sonner-no-inject",
  transform: (code, id) => (id.includes("/node_modules/sonner/dist/") ? code.replace(/^__insertCSS\(/m, "void (") : undefined),
};

export default defineConfig({
  plugins: [react(), sonnerNoInject],
  server: {
    proxy: Object.fromEntries(["/api", "/auth", "/oauth"].map((p) => [p, api])),
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
