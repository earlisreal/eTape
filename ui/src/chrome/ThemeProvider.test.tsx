// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { ThemeProvider, useTheme } from "./ThemeProvider";

function Probe({ id = "one" }: { id?: string }) {
  const { mode, palette, setMode } = useTheme();
  return (
    <div>
      <span data-testid={`mode-${id}`}>{mode}</span>
      <span data-testid={`bg-${id}`}>{palette.bg}</span>
      <button data-testid={`toggle-${id}`} onClick={() => setMode(mode === "light" ? "dark" : "light")}>toggle</button>
    </div>
  );
}

describe("ThemeProvider", () => {
  it("defaults to light", () => {
    render(<ThemeProvider><Probe /></ThemeProvider>);
    expect(screen.getByTestId("mode-one").textContent).toBe("light");
  });

  it("loads the persisted mode from the config store", async () => {
    const commands = { sendCommand: vi.fn(async (n: string) =>
      n === "GetConfig" ? { status: "accepted", value: "dark" } : { status: "accepted" }) };
    render(<ThemeProvider commands={commands}><Probe /></ThemeProvider>);
    await waitFor(() => expect(screen.getByTestId("mode-one").textContent).toBe("dark"));
  });

  it("toggling persists via SetConfig and swaps the palette", async () => {
    const commands = { sendCommand: vi.fn(async () => ({ status: "accepted" })) };
    render(<ThemeProvider commands={commands}><Probe /></ThemeProvider>);
    fireEvent.click(screen.getByTestId("toggle-one"));
    await waitFor(() => expect(screen.getByTestId("mode-one").textContent).toBe("dark"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "theme", value: "dark" });
  });

  it("applies a theme change made by another window", async () => {
    let stored = "light";
    const makeCommands = () => ({
      sendCommand: vi.fn(async (name: string, args: unknown) => {
        if (name === "SetConfig") stored = (args as { value: string }).value;
        return { status: "accepted", value: name === "GetConfig" ? stored : undefined };
      }),
    });
    const first = makeCommands();
    const second = makeCommands();
    render(
      <>
        <ThemeProvider commands={first}><Probe id="one" /></ThemeProvider>
        <ThemeProvider commands={second}><Probe id="two" /></ThemeProvider>
      </>,
    );
    await waitFor(() => expect(screen.getByTestId("mode-two").textContent).toBe("light"));
    fireEvent.click(screen.getByTestId("toggle-one"));
    await waitFor(() => expect(screen.getByTestId("mode-two").textContent).toBe("dark"));
  });

  it("mirrors the palette onto :root and sets data-theme", async () => {
    render(<ThemeProvider><div /></ThemeProvider>);
    await waitFor(() => {
      expect(document.documentElement.style.getPropertyValue("--bg")).toBe("#FBFAF7");
      expect(document.documentElement.dataset.theme).toBe("light");
    });
  });
});
