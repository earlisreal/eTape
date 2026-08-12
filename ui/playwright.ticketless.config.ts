import { defineConfig, devices } from "@playwright/test";

const PORT = process.env.ETAPE_UIHUB_PORT ?? "8686";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "ticketless-hotkeys.spec.ts",
  fullyParallel: false,
  workers: 1,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never", outputFolder: "e2e/.report-ticketless" }]],
  use: {
    headless: true,
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
  webServer: {
    command: "bash e2e/serve-ticketless.sh",
    url: `http://127.0.0.1:${PORT}/`,
    reuseExistingServer: false,
    timeout: 120_000,
    stdout: "pipe",
    stderr: "pipe",
  },
});
