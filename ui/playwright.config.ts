import { defineConfig, devices } from "@playwright/test";

// The E2E boots the REAL engine (replay mode) serving the production ui/dist
// bundle. No CI — this is a local run on Earl's mac (`npm run e2e`).
//
// Port is parameterized by ETAPE_UIHUB_PORT (serve.sh honors the same var
// when writing the engine's config) so an agent-driven run can bind a port
// other than the default 8686 and never collide with Earl's own running
// eTape instance.
const PORT = process.env.ETAPE_UIHUB_PORT ?? "8686";

export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // one shared engine + one WS-backed origin
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "e2e/.report" }]],
  use: {
    headless: true, // explicit: self-documenting, guards against future default changes
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bash e2e/serve.sh",
    url: `http://127.0.0.1:${PORT}/`,
    reuseExistingServer: false,
    timeout: 120_000, // includes the UI build + go run compile on first boot
    stdout: "pipe",
    stderr: "pipe",
  },
});
