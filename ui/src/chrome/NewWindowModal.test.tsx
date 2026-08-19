// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { NewWindowModal } from "./NewWindowModal";

describe("NewWindowModal", () => {
  afterEach(() => vi.restoreAllMocks());

  it("shows Monitoring as a protected workspace and reuses its named window", async () => {
    const commands = {
      sendCommand: vi.fn(async (name: string) => name === "GetConfig"
        ? { status: "accepted", value: { version: 1, entries: [{ id: "desk", name: "Desk" }] } }
        : { status: "accepted" }),
    };
    const open = vi.spyOn(window, "open").mockReturnValue(null);

    render(<ThemeProvider><NewWindowModal open currentId="main" commands={commands} onClose={() => {}} /></ThemeProvider>);
    await waitFor(() => expect(screen.getByRole("button", { name: "Monitoring" })).toBeTruthy());

    const monitoring = screen.getByRole("button", { name: "Monitoring" });
    expect(monitoring.parentElement?.querySelector("button:nth-of-type(2)")).toBeNull();
    fireEvent.click(monitoring);
    expect(open).toHaveBeenCalledWith(expect.stringContaining("workspace=monitoring"), "etape-workspace-monitoring", expect.stringContaining("popup=yes,width="));

    fireEvent.click(screen.getByRole("button", { name: "Desk" }));
    expect(open).toHaveBeenCalledWith(expect.stringContaining("workspace=desk"), "etape-workspace-desk", expect.stringContaining("popup=yes,width="));
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

    render(<ThemeProvider><NewWindowModal open currentId="main" commands={commands} onClose={() => {}} /></ThemeProvider>);
    await waitFor(() => expect(screen.getByRole("button", { name: "Create new" })).toBeTruthy());
    fireEvent.change(screen.getByLabelText("Workspace name"), { target: { value: "Desk" } });
    fireEvent.click(screen.getByRole("button", { name: "Create new" }));

    await waitFor(() => expect(open).toHaveBeenCalledWith("about:blank", "_blank", expect.stringContaining("popup=yes,width=")));
  });
});
