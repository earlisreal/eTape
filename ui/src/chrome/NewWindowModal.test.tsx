// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { NewWindowModal } from "./NewWindowModal";

describe("NewWindowModal", () => {
  afterEach(() => vi.restoreAllMocks());

  it("renames a saved workspace inline and closes on Escape", async () => {
    const commands = { sendCommand: vi.fn(async (name: string) => name === "GetConfig"
      ? { status: "accepted", value: { version: 1, entries: [{ id: "desk", name: "Desk" }] } }
      : { status: "accepted" }) };
    const close = vi.fn();
    render(<ThemeProvider><NewWindowModal open currentId="main" commands={commands} onClose={close} /></ThemeProvider>);
    await waitFor(() => expect(screen.getByRole("button", { name: "Rename" })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    fireEvent.change(screen.getByLabelText("Rename workspace"), { target: { value: "Trading desk" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Trading desk" })).toBeTruthy());
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "windows.v1", value: {
      version: 1, entries: [{ id: "desk", name: "Trading desk" }],
    } });
    fireEvent.keyDown(window, { key: "Escape" });
    expect(close).toHaveBeenCalledTimes(1);
  });

  it("shows Monitoring as a protected workspace and reuses its named window", async () => {
    const commands = {
      sendCommand: vi.fn(async (name: string) => name === "GetConfig"
        ? { status: "accepted", value: { version: 1, entries: [{ id: "desk", name: "Desk" }] } }
        : { status: "accepted" }),
    };
    const open = vi.spyOn(window, "open").mockReturnValue({} as Window);
    const onClose = vi.fn();

    render(<ThemeProvider><NewWindowModal open currentId="main" commands={commands} onClose={onClose} /></ThemeProvider>);
    await waitFor(() => expect(screen.getByRole("button", { name: "Monitoring" })).toBeTruthy());

    const monitoring = screen.getByRole("button", { name: "Monitoring" });
    expect(monitoring.parentElement?.querySelector("button:nth-of-type(2)")).toBeNull();
    fireEvent.click(monitoring);
    expect(open).toHaveBeenCalledWith(expect.stringContaining("workspace=monitoring"), "etape-workspace-monitoring", expect.stringContaining("popup=yes,width="));
    expect(onClose).toHaveBeenCalledTimes(1);

    fireEvent.click(screen.getByRole("button", { name: "Desk" }));
    expect(open).toHaveBeenCalledWith(expect.stringContaining("workspace=desk"), "etape-workspace-desk", expect.stringContaining("popup=yes,width="));
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("keeps the modal open when the workspace popup is blocked", async () => {
    const commands = {
      sendCommand: vi.fn(async (name: string) => name === "GetConfig"
        ? { status: "accepted", value: { version: 1, entries: [{ id: "desk", name: "Desk" }] } }
        : { status: "accepted" }),
    };
    vi.spyOn(window, "open").mockReturnValue(null);
    const onClose = vi.fn();

    render(<ThemeProvider><NewWindowModal open currentId="main" commands={commands} onClose={onClose} /></ThemeProvider>);
    await waitFor(() => expect(screen.getByRole("button", { name: "Desk" })).toBeTruthy());
    fireEvent.click(screen.getByRole("button", { name: "Desk" }));

    expect(onClose).not.toHaveBeenCalled();
  });

  it("creates a workspace in a popup window", async () => {
    const commands = {
      sendCommand: vi.fn(async (name: string) => name === "GetConfig"
        ? { status: "accepted", value: { version: 1, entries: [] } }
        : { status: "accepted" }),
    };
    const popup = { location: { href: "" }, close: vi.fn() } as unknown as Window;
    const open = vi.spyOn(window, "open").mockReturnValue(popup);
    vi.spyOn(crypto, "randomUUID").mockReturnValue("123e4567-e89b-12d3-a456-426614174000");
    const onClose = vi.fn();

    render(<ThemeProvider><NewWindowModal open currentId="main" commands={commands} onClose={onClose} /></ThemeProvider>);
    await waitFor(() => expect(screen.getByRole("button", { name: "Create new" })).toBeTruthy());
    fireEvent.change(screen.getByLabelText("Workspace name"), { target: { value: "Desk" } });
    fireEvent.click(screen.getByRole("button", { name: "Create new" }));

    await waitFor(() => expect(open).toHaveBeenCalledWith("about:blank", "_blank", expect.stringContaining("popup=yes,width=")));
    await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1));
  });
});
