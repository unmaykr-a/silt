import { defineConfig, devices } from "@playwright/test";

/**
 * End-to-end checks against a real Silt binary and a seeded database.
 *
 * These exist because of what a manual round found: a page that dies on
 * /search and an unknown URL, where nothing is selected. Every screenshot of a
 * working screen looked right, because the broken branch was the one that only
 * runs where there is nothing to show. That class is only caught by visiting
 * every route, and only stays caught if something visits them on every push.
 *
 * The binary and the database are built by `make e2e`; this config only points
 * at the server it starts.
 */
const PORT = Number(process.env.SILT_E2E_PORT ?? 8410);

export default defineConfig({
  testDir: "./tests",
  // Silt is one server with one database; parallel workers would fight over
  // the settings they change.
  workers: 1,
  fullyParallel: false,
  forbidOnly: !!process.env.CI,
  retries: 0,
  reporter: process.env.CI ? [["github"], ["list"]] : [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    // A trace on the first failure is the difference between "a test failed"
    // and knowing why, on a machine you cannot open a browser on.
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: process.env.SILT_E2E_COMMAND ?? "true",
    url: `http://127.0.0.1:${PORT}/healthz`,
    reuseExistingServer: true,
    timeout: 30_000,
  },
});
