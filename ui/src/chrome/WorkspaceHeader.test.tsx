// @vitest-environment jsdom
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { ThemeProvider } from "./ThemeProvider";
import { WorkspaceHeader, normalizeSymbol } from "./WorkspaceHeader";
import { LinkGroups, BroadcastChannelBus } from "./linkGroups";

describe("WorkspaceHeader", () => {
  it("typing a symbol into a group box focuses that link group", () => {
    const echo = vi.fn();
    const lg = new LinkGroups(new BroadcastChannelBus(), echo);
    render(<ThemeProvider><WorkspaceHeader workspaceName="trading" linkGroups={lg} /></ThemeProvider>);
    const box = screen.getByLabelText("focus green");
    fireEvent.change(box, { target: { value: "US.NVDA" } });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(lg.symbolFor("green")).toBe("US.NVDA");
    expect(echo).toHaveBeenCalledWith("green", "US.NVDA");
  });

  it("renders a theme toggle", () => {
    const lg = new LinkGroups(new BroadcastChannelBus(), () => {});
    render(<ThemeProvider><WorkspaceHeader workspaceName="trading" linkGroups={lg} /></ThemeProvider>);
    expect(screen.getByRole("button", { name: /theme/i })).toBeTruthy();
  });

  it("typing a bare lowercase ticker uppercases it and prefixes the default market", () => {
    const echo = vi.fn();
    const lg = new LinkGroups(new BroadcastChannelBus(), echo);
    render(<ThemeProvider><WorkspaceHeader workspaceName="trading" linkGroups={lg} /></ThemeProvider>);
    const box = screen.getByLabelText("focus blue");
    fireEvent.change(box, { target: { value: "aapl" } });
    fireEvent.keyDown(box, { key: "Enter" });
    expect(lg.symbolFor("blue")).toBe("US.AAPL");
    expect(echo).toHaveBeenCalledWith("blue", "US.AAPL");
  });
});

describe("normalizeSymbol", () => {
  it("prefixes a bare ticker with the default US. market", () => {
    expect(normalizeSymbol("AAPL")).toBe("US.AAPL");
  });

  it("passes an already-qualified symbol through unchanged", () => {
    expect(normalizeSymbol("HK.00700")).toBe("HK.00700");
    expect(normalizeSymbol("US.NVDA")).toBe("US.NVDA");
  });
});
