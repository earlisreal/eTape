// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider, useTheme } from "./ThemeProvider";

function Probe() {
  const { mode, palette, setMode } = useTheme();
  return (
    <div>
      <span data-testid="mode">{mode}</span>
      <span data-testid="bg">{palette.bg}</span>
      <button onClick={() => setMode(mode === "light" ? "dark" : "light")}>toggle</button>
    </div>
  );
}

describe("ThemeProvider", () => {
  it("defaults to light", () => {
    render(<ThemeProvider><Probe /></ThemeProvider>);
    expect(screen.getByTestId("mode").textContent).toBe("light");
  });

  it("loads the persisted mode from the config store", async () => {
    const commands = { sendCommand: vi.fn(async (n: string) =>
      n === "GetConfig" ? { status: "accepted", value: "dark" } : { status: "accepted" }) };
    render(<ThemeProvider commands={commands}><Probe /></ThemeProvider>);
    await waitFor(() => expect(screen.getByTestId("mode").textContent).toBe("dark"));
  });

  it("toggling persists via SetConfig and swaps the palette", async () => {
    const commands = { sendCommand: vi.fn(async () => ({ status: "accepted" })) };
    render(<ThemeProvider commands={commands}><Probe /></ThemeProvider>);
    fireEvent.click(screen.getByText("toggle"));
    await waitFor(() => expect(screen.getByTestId("mode").textContent).toBe("dark"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "theme", value: "dark" });
  });

  it("mirrors the palette onto :root and sets data-theme", async () => {
    render(<ThemeProvider><div /></ThemeProvider>);
    await waitFor(() => {
      expect(document.documentElement.style.getPropertyValue("--bg")).toBe("#FBFAF7");
      expect(document.documentElement.dataset.theme).toBe("light");
    });
  });
});
