// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { SoundConfigProvider } from "./SoundConfigProvider";
import { SoundsSection } from "./SoundsSection";
import { soundEngine } from "./SoundEngine";
import { ThemeProvider } from "../chrome/ThemeProvider";

function wrap() {
  const commands = { sendCommand: vi.fn(async () => ({ kind: "ack", corrId: "c", status: "accepted", value: undefined })) };
  render(<ThemeProvider><SoundConfigProvider commands={commands as never}><SoundsSection /></SoundConfigProvider></ThemeProvider>);
  return { commands };
}

afterEach(() => { vi.restoreAllMocks(); });

describe("SoundsSection", () => {
  it("saves a changed fill sound via SetConfig", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.change(screen.getByTestId("sound-fill"), { target: { value: "marimba" } });
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "marimba" }) });
  });

  it("preview button calls the engine", async () => {
    const spy = vi.spyOn(soundEngine, "preview");
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.click(screen.getByTestId("sound-preview-fill"));
    expect(spy).toHaveBeenCalledWith("fill", expect.any(String));
  });

  it("toggling master enable persists", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("sound-enabled"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ enabled: false }) });
  });
});
