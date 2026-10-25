import { configDefaults, defineConfig, type TestProjectInlineConfiguration } from "vitest/config";
import react from "@vitejs/plugin-react";

function withUnitFallback(projects: TestProjectInlineConfiguration[]): TestProjectInlineConfiguration[] {
  return [...projects, {
    extends: true,
    test: {
      name: "unit",
      include: configDefaults.include,
      exclude: [...configDefaults.exclude, "e2e/**", ...projects.flatMap((project) => project.test?.include ?? [])],
      pool: "forks",
    },
  }];
}

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
    // (node_modules and .git) alongside the exclusion.
    exclude: [...configDefaults.exclude, "e2e/**"],
    // Native node-canvas tests need separate Node processes. test/run.mjs
    // runs each golden/ladder/tape file alone and keeps the other batches.
    projects: withUnitFallback([
      {
        extends: true,
        test: {
          name: 'chart-core',
          include: [
            'src/data/MarketClock.test.ts',
            'src/data/BarStore.test.ts',
            'src/data/IndicatorStore.test.ts',
            'src/render/chart/barClose.test.ts',
            'src/render/chart/ChartController.test.ts',
            'src/render/chart/visibleExtremaPrimitive.test.ts',
            'src/render/chart/volumeProfileController.test.ts',
            'src/render/chart/volumeProfilePrimitive.test.ts',
            'src/render/chart/chartTheme.test.ts',
            'src/render/chart/crosshairSync.test.ts',
            'src/render/chart/orderMarkers.test.ts',
            'src/render/chart/indicatorSeries.test.ts',
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
          include: [
            'src/chrome/panels/ChartPanel.test.tsx', 'src/chrome/panels/tv/BarCloseTimer.test.tsx',
            'src/chrome/panels/tv/ChartOrderMarkers.test.tsx',
            'src/render/chart/orderCrosshair.test.ts',
            'src/chrome/panels/tv/ChartConditionalOrderEntry.test.tsx',
            'src/chrome/panels/tv/ChartRiskEntry.test.tsx',
            'src/chrome/panels/tv/ChartHeaderControls.test.tsx', 'src/chrome/panels/tv/ChartSettingsDialog.test.tsx',
            'src/chrome/panels/tv/IndicatorPickerPopover.test.tsx', 'src/chrome/panels/tv/IndicatorSettingsDialog.test.tsx',
            'src/chrome/panels/tv/TVLegend.test.tsx',
            'src/chrome/panels/tv/TVDialog.test.tsx', 'src/chrome/panels/tv/TVContextMenu.test.tsx',
            'src/chrome/panels/tv/TVDrawingRail.test.tsx',
          ],
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
          include: [
            'src/data/ExecStore.test.ts',
            'src/chrome/exec/actionTemplate.test.ts', 'src/chrome/exec/commands.test.ts',
            'src/chrome/exec/fireTemplate.test.ts', 'src/chrome/exec/OrderSettingsSection.test.tsx',
            'src/chrome/exec/resolveTemplate.test.ts', 'src/chrome/exec/useHotkeys.test.tsx',
            'src/chrome/exec/resolveChartConditionalOrder.test.ts',
          ],
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
            'test/icons.test.ts',
            'src/sound/**/*.test.{ts,tsx}',
            'src/chrome/alerts/**/*.test.{ts,tsx}',
            'src/data/PriceAlertStore.test.ts',
            'src/chrome/ThemeProvider.test.tsx',
            'src/chrome/exec/useOrderConfig.test.tsx',
            'src/chrome/AppShell.test.tsx',
            'src/chrome/AboutSection.test.tsx',
            'src/chrome/Catalog.test.tsx',
            'src/chrome/EmptyState.test.tsx',
            'src/chrome/hotkeyTarget.test.ts',
            'src/chrome/NewWindowModal.test.tsx',
            'src/chrome/presets.test.ts',
            'src/chrome/SessionClock.test.tsx',
            'src/chrome/SettingsModal.test.tsx',
            'src/chrome/controls/**/*.test.{ts,tsx}',
            'src/chrome/GeneralSection.test.tsx', 'src/chrome/AppearanceSection.test.tsx',
            'src/chrome/GroupPicker.test.tsx', 'src/chrome/PracticeLauncherModal.test.tsx',
            'src/chrome/VenueSetupPrompt.test.tsx', 'src/chrome/BackupPanel.test.tsx',
            'src/chrome/panels/ExportTradesPopover.test.tsx',
            'src/chrome/TopBar.test.tsx',
            'src/chrome/PanelFrame.test.tsx',
            'src/chrome/backup.test.ts',
            'src/chrome/crosshairSync.test.ts',
            'src/chrome/windows.test.ts',
            'src/chrome/windowState.test.ts',
            'src/chrome/panels/registry.test.tsx',
            'src/chrome/panels/OrderTicketPanel.test.tsx',
            'src/chrome/panels/AccountPanel.test.tsx', 'src/chrome/panels/ResizableColumns.test.tsx',
            'src/render/format.test.ts',
            'src/chrome/panels/ScannerPanel.test.tsx',
            'src/chrome/panels/WatchlistPanel.test.tsx',
            'src/chrome/panels/TradeHistoryTable.test.tsx',
            'src/data/ScannerStore.test.ts',
            'src/chrome/scannerSync.test.ts',
            'src/chrome/workspace.test.ts',
            'src/render/chart/drawings/toolStyles.test.ts',
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
    ]),
  },
});
