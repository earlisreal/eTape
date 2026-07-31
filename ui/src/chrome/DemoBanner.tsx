import { useSyncExternalStore } from "react";
import type { SessionStore } from "../data/SessionStore";
import { useTheme } from "./ThemeProvider";

export function DemoBanner({ session }: { session: SessionStore }): JSX.Element | null {
  const { palette } = useTheme();
  const s = useSyncExternalStore((cb) => session.subscribe(cb), () => session.getSnapshot());
  if (s.mode !== "demo") return null;
  return (
    <div data-testid="demo-banner" style={{
      display: "flex", alignItems: "center", justifyContent: "center", gap: 12,
      padding: "4px 12px", background: palette.demo, color: "#fff", fontWeight: 600,
    }}>
      <span>DEMO — synthetic market · practice orders only</span>
    </div>
  );
}
