import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { getPalette, type Palette, type ThemeMode } from "../render/palette";
import { applyPaletteVars } from "./cssVars";
import { openConfigSync, type ConfigSync } from "../configSync";

interface Commands { sendCommand(name: string, args: unknown): Promise<{ status: string; value?: unknown }> }
interface ThemeCtx { mode: ThemeMode; palette: Palette; setMode(m: ThemeMode): void }

const Ctx = createContext<ThemeCtx | null>(null);

export function ThemeProvider({ commands, children }: { commands?: Commands; children: ReactNode }): JSX.Element {
  const [mode, setModeState] = useState<ThemeMode>("light"); // light is the app default
  const syncRef = useRef<ConfigSync | null>(null);

  useEffect(() => {
    if (!commands) return;
    let live = true;
    const refresh = () => {
      void commands.sendCommand("GetConfig", { key: "theme" }).then((ack) => {
        if (!live) return;
        if (ack.status === "accepted" && (ack.value === "dark" || ack.value === "light")) setModeState(ack.value);
      }).catch(() => {});
    };
    const sync = openConfigSync("theme", refresh);
    syncRef.current = sync;
    refresh();
    return () => {
      live = false;
      sync.close();
      if (syncRef.current === sync) syncRef.current = null;
    };
  }, [commands]);

  const setMode = useCallback((m: ThemeMode) => {
    setModeState(m);
    void commands?.sendCommand("SetConfig", { key: "theme", value: m }).then((ack) => {
      if (ack.status === "accepted") syncRef.current?.notify();
    }).catch(() => {});
  }, [commands]);

  const value = useMemo<ThemeCtx>(() => ({ mode, palette: getPalette(mode), setMode }), [mode, setMode]);

  useEffect(() => {
    const root = document.documentElement;
    applyPaletteVars(root, getPalette(mode));
    root.dataset.theme = mode;
  }, [mode]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useTheme(): ThemeCtx {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useTheme must be used within a ThemeProvider");
  return ctx;
}
