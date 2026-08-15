import { configDefaults, defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// Default node environment for pure logic; chrome/*.test.tsx opt into jsdom
// per-file with `// @vitest-environment jsdom`.
export default defineConfig({
  plugins: [react()],
  test: {
    globals: true,
    environment: "node",
    // e2e/**/*.spec.ts are Playwright specs (owned by playwright.config.ts's
    // testDir) that use @playwright/test's test()/expect, a different
    // test-registration API than vitest's — vitest's default *.spec.ts glob
    // would otherwise pick them up and either report bogus "0 test" entries
    // or crash on Playwright's test.describe(). Keep vitest's own defaults
    // (node_modules, dist, etc.) alongside the exclusion.
    exclude: [...configDefaults.exclude, "e2e/**"],
    // node-canvas's native addon isn't safe to load into more than one
    // worker thread per process ("Module did not self-register" once a
    // second file requires it) — jsdom auto-loads it for any real
    // `<canvas>.getContext("2d")` call. Routing these files to the "forks"
    // pool (real child processes, not worker_threads) is necessary but NOT
    // sufficient on its own: verified 2026-07-12 that vitest's forks-pool
    // scheduler still packs multiple matched files into a single forked
    // process for a small batch like this, which re-triggers the same
    // self-register crash the moment a second canvas file loads in that
    // shared process. package.json's "test"/"test:golden"/"test:golden:update"
    // scripts work around this by invoking `vitest run <file>` once per
    // canvas file (golden-image tests, and the panel chrome tests that render
    // an actual <canvas> rather than mocking it — LadderPanel, TapePanel),
    // each its own top-level process; this projects entry still matters
    // so each of those single-file invocations avoids worker_threads.
    // Everything else keeps the faster default (threads) pool.
    projects: [
      {
        extends: true,
        test: {
          name: 'chart-core',
          include: [
            'src/data/MarketClock.test.ts',
            'src/data/BarStore.test.ts',
            'src/render/chart/barClose.test.ts',
            'src/render/chart/ChartController.test.ts',
            'src/render/chart/chartTheme.test.ts',
            'src/render/chart/sessions.test.ts',
            'src/render/ladder/ladderState.test.ts',
            'src/render/tape/tapeState.test.ts',
            'src/chrome/panels/tv/legendView.test.ts',
          ],
        },
      },
      {
        extends: true,
        test: {
          name: 'chart-panel',
          include: ['src/chrome/panels/ChartPanel.test.tsx', 'src/chrome/panels/tv/BarCloseTimer.test.tsx'],
        },
      },
      {
        extends: true,
        test: {
          name: 'news',
          include: ['src/data/NewsStore.test.ts', 'src/chrome/panels/StockInfoPanel.test.tsx', 'fixtures/monitoring.test.ts'],
        },
      },
      {
        extends: true,
        test: {
          name: 'wire',
          include: ['src/wire/WsClient.test.ts'],
        },
      },
      {
        extends: true,
        test: {
          name: 'exec',
          include: ['src/chrome/exec/commands.test.ts', 'src/chrome/exec/fireTemplate.test.ts', 'src/chrome/exec/useHotkeys.test.tsx'],
        },
      },
      {
        extends: true,
        test: {
          name: 'layout',
          include: ['src/render/tape/tapeLayout.test.ts'],
        },
      },
      {
        test: {
          name: 'golden',
          include: ['test/golden/**/*.test.ts', 'test/golden/**/*.golden.test.ts'],
          pool: 'forks',
        },
      },
      {
        test: {
          name: 'ladder',
          include: [
            'src/chrome/panels/LadderPanel.test.tsx',
            'src/chrome/panels/LadderSettingsDialog.test.tsx',
            'src/chrome/panels/LocatesPanel.test.tsx',
          ],
          pool: 'forks',
        },
      },
      {
        extends: true,
        test: {
          name: 'chrome-regressions',
          include: [
            'src/chrome/AppShell.test.tsx',
            'src/chrome/Catalog.test.tsx',
            'src/chrome/EmptyState.test.tsx',
            'src/chrome/hotkeyTarget.test.ts',
            'src/chrome/NewWindowModal.test.tsx',
            'src/chrome/presets.test.ts',
            'src/chrome/SessionClock.test.tsx',
            'src/chrome/TopBar.test.tsx',
            'src/chrome/backup.test.ts',
            'src/chrome/panels/registry.test.tsx',
            'src/chrome/workspace.test.ts',
          ],
          pool: 'forks',
        },
      },
      {
        test: {
          name: 'tape',
          include: ['src/chrome/panels/TapePanel.test.tsx', 'src/chrome/panels/TapeSettingsDialog.test.tsx'],
          pool: 'forks',
        },
      },
    ],
  },
});
