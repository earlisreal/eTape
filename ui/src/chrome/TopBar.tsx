import { LatencyReadout } from "./LatencyReadout";
import type { HealthStore } from "../data/HealthStore";
import { useTheme } from "./ThemeProvider";

export interface TopBarProps {
  workspaceName: string;
  health: HealthStore;
  armed: boolean;
  onArmToggle: () => void;
  onAddPanel: () => void;
  onNewWindow: () => void;
  onOpenSettings: () => void;
  onOpenConnection: () => void;
}

// Daylight Ledger top bar: eTape wordmark + workspace name + connection latency +
// shell actions (add panel / new window / settings) + the arm/disarm chip. The
// link-group symbol boxes from the old WorkspaceHeader are gone — Task 13's
// type-to-load replaces that interaction, per-panel.
export function TopBar(p: TopBarProps): JSX.Element {
  const { palette } = useTheme();
  return (
    <div style={{ display: "flex", alignItems: "center", gap: 10, padding: "7px 12px",
      background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      <span className="serif" style={{ fontWeight: 600, fontSize: 14 }}>eTape</span>
      <span style={{ color: palette.textMuted }}>· {p.workspaceName}</span>
      <LatencyReadout health={p.health} onOpen={p.onOpenConnection} />
      <span style={{ flex: 1 }} />
      <button className="btn" onClick={p.onAddPanel}>+ Add panel</button>
      <button className="btn" onClick={p.onNewWindow}>⧉ New window</button>
      <button className="btn" aria-label="Settings" onClick={p.onOpenSettings}>⚙ Settings</button>
      <button data-testid="arm-chip" className="btn" onClick={p.onArmToggle}
        style={{ fontWeight: 600, letterSpacing: ".08em",
          color: p.armed ? palette.accent : palette.textMuted,
          borderColor: p.armed ? palette.accent : palette.borderStrong,
          background: p.armed ? "rgba(154,106,27,.12)" : "rgba(106,114,128,.12)" }}>
        {p.armed ? "ARMED" : "DISARMED"}
      </button>
    </div>
  );
}
