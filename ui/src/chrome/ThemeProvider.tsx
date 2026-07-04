import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getPalette, type Palette, type ThemeMode } from "../render/palette";

interface Commands { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }
interface ThemeCtx { mode: ThemeMode; palette: Palette; setMode(m: ThemeMode): void }

const Ctx = createContext<ThemeCtx | null>(null);

export function ThemeProvider({ commands, children }: { commands?: Commands; children: ReactNode }): JSX.Element {
  const [mode, setModeState] = useState<ThemeMode>("light"); // light is the app default

  useEffect(() => {
    if (!commands) return;
    void commands.sendCommand("GetConfig", { key: "theme" }).then((ack) => {
      if (ack.status === "accepted" && (ack.value === "dark" || ack.value === "light")) setModeState(ack.value);
    });
  }, [commands]);

  const setMode = (m: ThemeMode) => {
    setModeState(m);
    void commands?.sendCommand("SetConfig", { key: "theme", value: m });
  };

  const value = useMemo<ThemeCtx>(() => ({ mode, palette: getPalette(mode), setMode }), [mode]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTheme(): ThemeCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useTheme must be used within a ThemeProvider");
  return ctx;
}
