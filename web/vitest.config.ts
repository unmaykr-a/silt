import { defineConfig } from "vitest/config";
import { resolve } from "node:path";

// A separate config from vite.config.ts on purpose: the unit tests cover the
// plain-TypeScript logic (the YAML tokenizer, the line and word diff), and
// loading the Tailwind and Svelte plugins to run them means a full project
// scan for no benefit.
export default defineConfig({
  resolve: {
    alias: { $lib: resolve(import.meta.dirname, "src/lib") },
  },
  test: {
    environment: "node",
    include: ["src/**/*.test.ts"],
    // These are pure functions; anything that hangs is a bug, not slowness.
    testTimeout: 5000,
  },
});
