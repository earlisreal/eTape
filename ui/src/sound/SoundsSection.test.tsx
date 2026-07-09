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

  it("unchecking Scanner persists off and disables the dropdown and preview", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.click(screen.getByTestId("sound-scanner-on"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ scannerSound: "off" }) });
    expect((screen.getByTestId("sound-scanner") as HTMLSelectElement).disabled).toBe(true);
    expect((screen.getByTestId("sound-preview-scanner") as HTMLButtonElement).disabled).toBe(true);
  });

  it("re-checking Scanner restores the previously selected sound, not off", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.change(screen.getByTestId("sound-scanner"), { target: { value: "chirp" } });
    fireEvent.click(screen.getByTestId("sound-scanner-on")); // uncheck -> off
    fireEvent.click(screen.getByTestId("sound-scanner-on")); // re-check -> restore
    expect(commands.sendCommand).toHaveBeenLastCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ scannerSound: "chirp" }) });
  });

  it("unchecking/re-checking Fill behaves the same as Scanner", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.change(screen.getByTestId("sound-fill"), { target: { value: "marimba" } });
    fireEvent.click(screen.getByTestId("sound-fill-on"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "off" }) });
    expect((screen.getByTestId("sound-fill") as HTMLSelectElement).disabled).toBe(true);
    expect((screen.getByTestId("sound-preview-fill") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("sound-fill-on"));
    expect(commands.sendCommand).toHaveBeenLastCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ fillSound: "marimba" }) });
  });

  it("unchecking/re-checking Reject behaves the same as Scanner", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    fireEvent.change(screen.getByTestId("sound-reject"), { target: { value: "buzz" } });
    fireEvent.click(screen.getByTestId("sound-reject-on"));
    expect(commands.sendCommand).toHaveBeenCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ rejectSound: "off" }) });
    expect((screen.getByTestId("sound-reject") as HTMLSelectElement).disabled).toBe(true);
    expect((screen.getByTestId("sound-preview-reject") as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByTestId("sound-reject-on"));
    expect(commands.sendCommand).toHaveBeenLastCalledWith("SetConfig", { key: "soundConfig", value: expect.objectContaining({ rejectSound: "buzz" }) });
  });

  it("the off option is absent from all three dropdowns", async () => {
    const { commands } = wrap();
    await waitFor(() => expect(commands.sendCommand).toHaveBeenCalledWith("GetConfig", { key: "soundConfig" }));
    for (const testid of ["sound-fill", "sound-reject", "sound-scanner"]) {
      const options = Array.from(screen.getByTestId(testid).querySelectorAll("option"));
      expect(options.some((o) => (o as HTMLOptionElement).value === "off")).toBe(false);
    }
  });
});
