/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// A local `innkeeper api` serves everything that isn't the UI.
const api = process.env.INNKEEPER_URL ?? "http://localhost:8080";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: Object.fromEntries(["/api", "/auth", "/oauth"].map((p) => [p, api])),
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
  },
});
