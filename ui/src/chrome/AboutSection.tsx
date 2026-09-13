import { useEffect, useState } from "react";
import { useTheme } from "./ThemeProvider";
import type { AppInfo } from "../wire/contract";

type AboutCommands = { sendQuery?: (name: string, args: unknown) => Promise<unknown> };

export function AboutSection({ commands }: { commands: AboutCommands }): JSX.Element {
  const { palette } = useTheme();
  const [version, setVersion] = useState<string | null>(null);

  useEffect(() => {
    if (!commands.sendQuery) {
      setVersion("dev");
      return;
    }
    let active = true;
    void commands.sendQuery("QueryAppInfo", {}).then((raw) => {
      if (!active) return;
      const info = raw as Partial<AppInfo>;
      setVersion(typeof info.version === "string" ? info.version : "Unavailable");
    }, () => {
      if (active) setVersion("Unavailable");
    });
    return () => { active = false; };
  }, [commands]);

  return (
    <div style={{ color: palette.text }}>
      <div className="col-head serif" style={{ marginBottom: 14 }}>About</div>
      <div style={{ fontSize: 18, fontWeight: 600, marginBottom: 12 }}>eTape</div>
      <div style={{ display: "flex", gap: 12, fontSize: 12 }}>
        <span style={{ color: palette.textMuted }}>Version</span>
        <span>{version ?? "Loading…"}</span>
      </div>
    </div>
  );
}
