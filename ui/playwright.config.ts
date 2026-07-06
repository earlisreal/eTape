import { defineConfig, devices } from "@playwright/test";

// The E2E boots the REAL engine (replay mode) serving the production ui/dist
// bundle. No CI — this is a local run on Earl's mac (`npm run e2e`).
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false, // one shared engine + one WS-backed origin
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "e2e/.report" }]],
  use: {
    baseURL: "http://127.0.0.1:8686",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bash e2e/serve.sh",
    url: "http://127.0.0.1:8686/",
    reuseExistingServer: false,
    timeout: 120_000, // includes the UI build + go run compile on first boot
    stdout: "pipe",
    stderr: "pipe",
  },
});
