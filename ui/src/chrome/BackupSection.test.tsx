// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { BackupSection } from "./BackupSection";
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

// Minimal synchronous stand-in for FileReader: the component only calls
// `readAsText` then reads `.result` in `onload`, so a real async FileReader
// (and a real Blob/File) isn't needed — a plain object carrying the text
// under `__text` is enough, wired through this fake.
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
// FileReader can read a Blob's contents — used only by the export tests,
// which deliberately don't stub FileReader (the import tests do, via
// FakeFileReader above, since the component's own FileReader use is what's
// under test there).
function readBlobText(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(blob);
  });
}

interface Props {
  getWorkspace: () => Workspace;
  onImportWorkspace: (ws: Workspace) => void;
  orderConfig: OrderConfig;
  onImportOrderConfig: (next: OrderConfig) => void;
  toast: ToastApi;
}

function setup(overrides: Partial<Props> = {}) {
  const workspace = makeWorkspace();
  const orderConfig = makeOrderConfig([makeTemplate("t1", "Ctrl+1")]);
  const props: Props = {
    getWorkspace: () => workspace,
    onImportWorkspace: vi.fn(),
    orderConfig,
    onImportOrderConfig: vi.fn(),
    toast: { push: vi.fn(), dismiss: vi.fn() },
    ...overrides,
  };
  const utils = render(<ThemeProvider><BackupSection {...props} /></ThemeProvider>);
  return { ...utils, props };
}

function selectFile(text: string): void {
  fireEvent.change(screen.getByTestId("import-file"), { target: { files: [fileWithText(text)] } });
}

describe("BackupSection", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  describe("export block", () => {
    it("disables Download JSON only when both checkboxes are unchecked", () => {
      setup();
      const dl = () => screen.getByTestId("download-json") as HTMLButtonElement;
      expect(dl().disabled).toBe(false); // both default-checked

      fireEvent.click(screen.getByTestId("export-layout"));
      expect(dl().disabled).toBe(false); // hotkeys still checked

      fireEvent.click(screen.getByTestId("export-hotkeys"));
      expect(dl().disabled).toBe(true); // both unchecked now

      fireEvent.click(screen.getByTestId("export-layout"));
      expect(dl().disabled).toBe(false); // layout re-checked
    });

    it("downloads a blob whose JSON matches the checked sections and a local-date filename", async () => {
      const createObjectURL = vi.fn<[Blob], string>(() => "blob:mock");
      vi.stubGlobal("URL", { ...URL, createObjectURL, revokeObjectURL: vi.fn() });
      const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
      const { props } = setup();

      // Uncheck hotkeys — export layout only.
      fireEvent.click(screen.getByTestId("export-hotkeys"));
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
      const now = new Date();
      const pad = (n: number) => String(n).padStart(2, "0");
      const expected = `etape-settings-${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}.json`;
      expect(anchor.download).toBe(expected);
    });
  });

  describe("import block", () => {
    beforeEach(() => {
      vi.stubGlobal("FileReader", FakeFileReader);
    });

    it("applies both sections on confirm, calling both callbacks with the exact prepared values", () => {
      vi.spyOn(crypto, "randomUUID").mockReturnValue("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa");
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const importedLayout = makeWorkspace("other-machine");
      const importedTemplates = [makeTemplate("import-1", "Ctrl+9")];
      const fileData: SettingsExport = {
        app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
        layout: importedLayout, hotkeys: { templates: importedTemplates },
      };
      const { props } = setup();

      selectFile(JSON.stringify(fileData));
      expect(screen.getByTestId("import-layout")).toBeTruthy();
      expect(screen.getByTestId("import-hotkeys")).toBeTruthy();
      fireEvent.click(screen.getByTestId("apply-import"));

      // name forced to the *current* workspace name ("main"), not the imported doc's own name.
      expect(props.onImportWorkspace).toHaveBeenCalledWith({ ...importedLayout, name: "main" });
      expect(props.onImportOrderConfig).toHaveBeenCalledWith({
        templates: [{ ...importedTemplates[0], id: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa" }],
        activeVenue: props.orderConfig.activeVenue,
      });
      expect(props.toast.push).toHaveBeenCalledWith(expect.objectContaining({ level: "info" }));
    });

    it("gates callbacks by which per-section checkboxes are checked", () => {
      vi.spyOn(crypto, "randomUUID").mockReturnValue("cccccccc-cccc-cccc-cccc-cccccccccccc");
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const importedLayout = makeWorkspace("other-machine");
      const importedTemplates = [makeTemplate("import-1", "Ctrl+9")];
      const fileData: SettingsExport = {
        app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
        layout: importedLayout, hotkeys: { templates: importedTemplates },
      };
      const { props } = setup();

      selectFile(JSON.stringify(fileData));
      fireEvent.click(screen.getByTestId("import-hotkeys")); // uncheck hotkeys, leave layout checked
      fireEvent.click(screen.getByTestId("apply-import"));

      expect(props.onImportWorkspace).toHaveBeenCalledWith({ ...importedLayout, name: "main" });
      expect(props.onImportOrderConfig).not.toHaveBeenCalled();
    });

    it("does not call either callback when confirm is declined", () => {
      vi.spyOn(window, "confirm").mockReturnValue(false);
      const fileData: SettingsExport = {
        app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
        layout: makeWorkspace("other"), hotkeys: { templates: [makeTemplate("import-1", "Ctrl+9")] },
      };
      const { props } = setup();

      selectFile(JSON.stringify(fileData));
      fireEvent.click(screen.getByTestId("apply-import"));

      expect(props.onImportWorkspace).not.toHaveBeenCalled();
      expect(props.onImportOrderConfig).not.toHaveBeenCalled();
    });

    it("shows a danger toast and applies nothing for an invalid file", () => {
      const { props } = setup();

      selectFile("this is not { json");

      expect(props.toast.push).toHaveBeenCalledWith(expect.objectContaining({ level: "danger" }));
      expect(screen.queryByTestId("apply-import")).toBeNull();
      expect(props.onImportWorkspace).not.toHaveBeenCalled();
      expect(props.onImportOrderConfig).not.toHaveBeenCalled();
    });

    it("shows no control for a missing section and only calls the relevant callback (hotkeys-only file)", () => {
      vi.spyOn(crypto, "randomUUID").mockReturnValue("dddddddd-dddd-dddd-dddd-dddddddddddd");
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const importedTemplates = [makeTemplate("import-1", "Ctrl+9")];
      const fileData: SettingsExport = {
        app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
        hotkeys: { templates: importedTemplates },
      };
      const { props } = setup();

      selectFile(JSON.stringify(fileData));
      expect(screen.queryByTestId("import-layout")).toBeNull();
      expect(screen.getByTestId("import-hotkeys")).toBeTruthy();

      fireEvent.click(screen.getByTestId("apply-import"));
      expect(props.onImportWorkspace).not.toHaveBeenCalled();
      expect(props.onImportOrderConfig).toHaveBeenCalledWith({
        templates: [{ ...importedTemplates[0], id: "dddddddd-dddd-dddd-dddd-dddddddddddd" }],
        activeVenue: props.orderConfig.activeVenue,
      });
    });

    // Task 1's reviewer flagged this: parseImport only checks the top-level
    // envelope, not data.hotkeys.templates's shape. A malformed/partial file
    // must be treated as "that section isn't present," never crash `.map`.
    it("treats hotkeys as absent (not a crash) when templates isn't an array", () => {
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const fileData = {
        app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
        layout: makeWorkspace("other"), hotkeys: { templates: "not-an-array" },
      };
      const { props } = setup();

      expect(() => selectFile(JSON.stringify(fileData))).not.toThrow();
      expect(screen.queryByTestId("import-hotkeys")).toBeNull();
      expect(screen.getByTestId("import-layout")).toBeTruthy();

      expect(() => fireEvent.click(screen.getByTestId("apply-import"))).not.toThrow();
      expect(props.onImportOrderConfig).not.toHaveBeenCalled();
      expect(props.onImportWorkspace).toHaveBeenCalled();
    });

    it("treats layout as absent (not a crash) when it isn't an object", () => {
      vi.spyOn(crypto, "randomUUID").mockReturnValue("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee");
      vi.spyOn(window, "confirm").mockReturnValue(true);
      const importedTemplates = [makeTemplate("import-1", "Ctrl+9")];
      const fileData = {
        app: "eTape", kind: "settings-export", version: 1, exportedAt: new Date().toISOString(),
        layout: "garbage-not-an-object", hotkeys: { templates: importedTemplates },
      };
      const { props } = setup();

      expect(() => selectFile(JSON.stringify(fileData))).not.toThrow();
      expect(screen.queryByTestId("import-layout")).toBeNull();
      expect(screen.getByTestId("import-hotkeys")).toBeTruthy();

      fireEvent.click(screen.getByTestId("apply-import"));
      expect(props.onImportWorkspace).not.toHaveBeenCalled();
      expect(props.onImportOrderConfig).toHaveBeenCalledWith({
        templates: [{ ...importedTemplates[0], id: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee" }],
        activeVenue: props.orderConfig.activeVenue,
      });
    });
  });
});
