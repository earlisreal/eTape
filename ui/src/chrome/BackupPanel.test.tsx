// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { BackupPanel } from "./BackupPanel";
import type { Workspace } from "./workspace";
import type { ActionTemplate, OrderConfig } from "./exec/actionTemplate";
import type { SettingsExport } from "./backup";
import type { ToastApi } from "./Toast";

function makeWorkspace(name = "main"): Workspace {
  return {
    name,
    panels: [{ id: "p1", panelId: "chart", group: "red", settings: { symbol: "AAPL" } }],
    layout: { panels: { p1: {} } },
  };
}
function makeTemplate(id: string, hotkey?: string): ActionTemplate {
  return {
    kind: "place", id, label: "Buy 100", side: "BUY", type: "LIMIT", tif: "DAY",
    session: "AUTO", priceSource: "Ask", priceOffset: 0, priceOffsetUnit: "$",
    sizing: { mode: "Shares", shares: 100 }, ...(hotkey !== undefined ? { hotkey } : {}),
  };
}
function makeOrderConfig(templates: ActionTemplate[], activeVenue = "alpaca"): OrderConfig {
  return { templates, activeVenue };
}

// Minimal synchronous stand-in for FileReader — mirrors BackupSection.test.tsx's
// FakeFileReader: the component only calls `readAsText` then reads `.result` in
// `onload`, so a plain object carrying the text under `__text` is enough.
class FakeFileReader {
  result: string | null = null;
  onload: (() => void) | null = null;
  readAsText(file: unknown): void {
    this.result = (file as { __text: string }).__text;
    this.onload?.();
  }
}
function fileWithText(text: string): File {
  return { __text: text } as unknown as File;
}

// jsdom's Blob has no `.text()`/`.arrayBuffer()`, but its (real, unstubbed)
// FileReader can read a Blob's contents — used only by the export tests.
function readBlobText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

function selectFile(text: string): void {
  fireEvent.change(screen.getByTestId("import-file"), { target: { files: [fileWithText(text)] } });
}

function todayFilename(prefix: string): string {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${prefix}-${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}.json`;
}

describe("BackupPanel", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  describe("layout part", () => {
    function setup() {
      const workspace = makeWorkspace();
      const props = {
        part: "layout" as const,
        getWorkspace: () => workspace,
        onImportWorkspace: vi.fn(),
        toast: { push: vi.fn(), dismiss: vi.fn() } as ToastApi,
      };
      const utils = render(<ThemeProvider><BackupPanel {...props} /></ThemeProvider>);
      return { ...utils, props };
    }

    it("shows the layout-only scope note (no mention of hotkeys sharing)", () => {
      setup();
      const note = screen.getByTestId("scope-note").textContent ?? "";
      expect(note).toContain("applies only to this window");
      expect(note).not.toContain("Hotkeys are shared");
    });

    it("downloads a blob containing only the layout, with a layout-named filename", async () => {
      const createObjectURL = vi.fn<[Blob], string>(() => "blob:mock");
      vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL: vi.fn() });
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      const { props } = setup();

      expect(screen.getByTestId("download-json")).toBeTruthy();
      fireEvent.click(screen.getByTestId("download-json"));

      expect(clickSpy).toHaveBeenCalledTimes(1);
      expect(createObjectURL).toHaveBeenCalledTimes(1);
      const blob = createObjectURL.mock.calls[0][0];
      expect(blob.type).toBe("application/json");
      const text = await readBlobText(blob);
      const parsed = JSON.parse(text) as SettingsExport;
      expect(parsed.app).toBe("eTape");
      expect(parsed.kind).toBe("settings-export");
      expect(parsed.layout).toEqual(props.getWorkspace());
      expect(parsed.hotkeys).toBeUndefined();

      const anchor = clickSpy.mock.instances[0] as unknown as HTMLAnchorElement;
      expect(anchor.download).toBe(todayFilename("etape-layout"));
    });

    describe("import", () => {
      beforeEach(() => {
        vi.stubGlobal("FileReader", FakeFileReader);
      });

      it("shows the Apply button when the file has a layout section", () => {
        setup();
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          layout: makeWorkspace("other"),
        };
        selectFile(JSON.stringify(fileData));
        expect(screen.getByTestId("apply-import")).toBeTruthy();
      });

      it("shows the missing-layout message (no Apply button) when the file has no layout", () => {
        setup();
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          hotkeys: { templates: [] },
        };
        selectFile(JSON.stringify(fileData));
        expect(screen.queryByTestId("apply-import")).toBeNull();
        expect(screen.getByText("This file has no layout to import.")).toBeTruthy();
      });

      it("applies the imported layout on confirm, forcing the current workspace name, and toasts info", () => {
        vi.spyOn(window, "confirm").mockReturnValue(true);
        const importedLayout = makeWorkspace("other-machine");
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          layout: importedLayout,
        };
        const { props } = setup();

        selectFile(JSON.stringify(fileData));
        fireEvent.click(screen.getByTestId("apply-import"));

        expect(window.confirm).toHaveBeenCalledWith("Replace your current layout with the imported one?");
        expect(props.onImportWorkspace).toHaveBeenCalledWith({ ...importedLayout, name: "main" });
        expect(props.toast.push).toHaveBeenCalledWith({ level: "info", text: "Imported layout." });
      });

      it("does not import when confirm is declined", () => {
        vi.spyOn(window, "confirm").mockReturnValue(false);
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          layout: makeWorkspace("other"),
        };
        const { props } = setup();

        selectFile(JSON.stringify(fileData));
        fireEvent.click(screen.getByTestId("apply-import"));

        expect(props.onImportWorkspace).not.toHaveBeenCalled();
      });

      it("shows a danger toast and no Apply button for invalid JSON", () => {
        const { props } = setup();
        selectFile("this is not { json");

        expect(props.toast.push).toHaveBeenCalledWith(expect.objectContaining({ level: "danger" }));
        expect(screen.queryByTestId("apply-import")).toBeNull();
        expect(props.onImportWorkspace).not.toHaveBeenCalled();
      });
    });
  });

  describe("hotkeys part", () => {
    function setup() {
      const orderConfig = makeOrderConfig([makeTemplate("t1", "Ctrl+1")]);
      const props = {
        part: "hotkeys" as const,
        orderConfig,
        onImportOrderConfig: vi.fn(),
        toast: { push: vi.fn(), dismiss: vi.fn() } as ToastApi,
      };
      const utils = render(<ThemeProvider><BackupPanel {...props} /></ThemeProvider>);
      return { ...utils, props };
    }

    it("shows the hotkeys-only scope note (no mention of layout being per-window)", () => {
      setup();
      const note = screen.getByTestId("scope-note").textContent ?? "";
      expect(note).toContain("Hotkeys are shared across all");
      expect(note).toContain("won't see an import until it reloads");
      expect(note).not.toContain("applies only to this window");
    });

    it("downloads a blob containing only the hotkeys, with a hotkeys-named filename", async () => {
      const createObjectURL = vi.fn<[Blob], string>(() => "blob:mock");
      vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL: vi.fn() });
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      const { props } = setup();

      fireEvent.click(screen.getByTestId("download-json"));

      const blob = createObjectURL.mock.calls[0][0];
      const text = await readBlobText(blob);
      const parsed = JSON.parse(text) as SettingsExport;
      expect(parsed.hotkeys).toEqual({ templates: props.orderConfig.templates });
      expect(parsed.layout).toBeUndefined();

      const anchor = clickSpy.mock.instances[0] as unknown as HTMLAnchorElement;
      expect(anchor.download).toBe(todayFilename("etape-hotkeys"));
    });

    describe("import", () => {
      beforeEach(() => {
        vi.stubGlobal("FileReader", FakeFileReader);
      });

      it("shows the Apply button when the file has a hotkeys section", () => {
        setup();
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          hotkeys: { templates: [makeTemplate("import-1", "Ctrl+9")] },
        };
        selectFile(JSON.stringify(fileData));
        expect(screen.getByTestId("apply-import")).toBeTruthy();
      });

      it("shows the missing-hotkeys message (no Apply button) when the file has no hotkeys", () => {
        setup();
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          layout: makeWorkspace("other"),
        };
        selectFile(JSON.stringify(fileData));
        expect(screen.queryByTestId("apply-import")).toBeNull();
        expect(screen.getByText("This file has no hotkeys to import.")).toBeTruthy();
      });

      it("applies the imported hotkeys on confirm, regenerating ids, and toasts info", () => {
        vi.spyOn(crypto, "randomUUID").mockReturnValue("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
        vi.spyOn(window, "confirm").mockReturnValue(true);
        const importedTemplates = [makeTemplate("import-1", "Ctrl+9")];
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          hotkeys: { templates: importedTemplates },
        };
        const { props } = setup();

        selectFile(JSON.stringify(fileData));
        fireEvent.click(screen.getByTestId("apply-import"));

        expect(window.confirm).toHaveBeenCalledWith("Replace your current hotkeys with the imported ones?");
        expect(props.onImportOrderConfig).toHaveBeenCalledWith({
          templates: [{ ...importedTemplates[0], id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" }],
          activeVenue: props.orderConfig.activeVenue,
          extHoursMarketBufferPct: 1,
        });
        expect(props.toast.push).toHaveBeenCalledWith({ level: "info", text: "Imported hotkeys." });
      });

      it("pushes a warn toast (in addition to the info toast) when imported hotkeys conflict", () => {
        vi.spyOn(crypto, "randomUUID").mockReturnValue("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb");
        vi.spyOn(window, "confirm").mockReturnValue(true);
        const importedTemplates = [makeTemplate("import-1", "Ctrl+9"), makeTemplate("import-2", "Ctrl+9")];
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          hotkeys: { templates: importedTemplates },
        };
        const { props } = setup();

        selectFile(JSON.stringify(fileData));
        fireEvent.click(screen.getByTestId("apply-import"));

        expect(props.toast.push).toHaveBeenCalledWith({
          level: "warn", text: "Imported hotkeys have conflicting bindings: Ctrl+9",
        });
        expect(props.toast.push).toHaveBeenCalledWith({ level: "info", text: "Imported hotkeys." });
      });

      it("does not import when confirm is declined", () => {
        vi.spyOn(window, "confirm").mockReturnValue(false);
        const fileData: SettingsExport = {
          app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
          hotkeys: { templates: [makeTemplate("import-1", "Ctrl+9")] },
        };
        const { props } = setup();

        selectFile(JSON.stringify(fileData));
        fireEvent.click(screen.getByTestId("apply-import"));

        expect(props.onImportOrderConfig).not.toHaveBeenCalled();
      });

      it("shows a danger toast and no Apply button for invalid JSON", () => {
        const { props } = setup();
        selectFile("this is not { json");

        expect(props.toast.push).toHaveBeenCalledWith(expect.objectContaining({ level: "danger" }));
        expect(screen.queryByTestId("apply-import")).toBeNull();
        expect(props.onImportOrderConfig).not.toHaveBeenCalled();
      });
    });
  });
});
