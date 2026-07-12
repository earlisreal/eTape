import { LatencyReadout } from "./LatencyReadout";
import { SessionClock } from "./SessionClock";
import type { HealthStore } from "../data/HealthStore";
import { useTheme } from "./ThemeProvider";
import { Button } from "./controls/Button";

export interface TopBarProps {
  workspaceName: string;
  health: HealthStore;
  armed: boolean;
  onArmToggle: () => void;
  onAddPanel: () => void;
  onNewWindow: () => void;
  onOpenSettings: () => void;
  onOpenConnection: () => void;
  onOpenReplay: () => void;
}

// Daylight Ledger top bar: eTape wordmark + workspace name + connection latency on the
// left, a live ET clock + session badge dead-center, and shell actions (add panel / new
// window / settings) + the arm/disarm chip on the right. The link-group symbol boxes
// from the old WorkspaceHeader are gone — Task 13's type-to-load replaces that
// interaction, per-panel.
export function TopBar(p: TopBarProps): JSX.Element {
  const { palette } = useTheme();
  return (
    <div style={{ display: "grid", gridTemplateColumns: "1fr auto 1fr", alignItems: "center", gap: 10,
      padding: "7px 12px", background: palette.surface, borderBottom: `1px solid ${palette.border}` }}>
      <div style={{ display: "flex", alignItems: "center", gap: 10, minWidth: 0 }}>
        <span className="serif" style={{ fontWeight: 600, fontSize: 14 }}>eTape</span>
        <span style={{ color: palette.textMuted }}>· {p.workspaceName}</span>
        <LatencyReadout health={p.health} onOpen={p.onOpenConnection} />
      </div>
      <SessionClock />
      <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 10, minWidth: 0 }}>
        <Button onClick={p.onAddPanel}>+ Add panel</Button>
        <Button onClick={p.onNewWindow}>⧉ New window</Button>
        <Button aria-label="Practice" title="Practice: synthetic demo market or replay a recorded day" onClick={p.onOpenReplay}>▶ Practice</Button>
        <Button aria-label="Settings" onClick={p.onOpenSettings}>⚙ Settings</Button>
        <Button data-testid="arm-chip" onClick={p.onArmToggle}
          style={{ fontWeight: 600, letterSpacing: ".08em",
            color: p.armed ? palette.accent : palette.textMuted,
            borderColor: p.armed ? palette.accent : palette.borderStrong,
            background: p.armed ? "rgba(154,106,27,.12)" : "rgba(106,114,128,.12)" }}
          // The chip's color/border/background ARE the armed/disarmed state
          // indicator — a permanent inline background that would permanently
          // defeat a plain CSS :hover rule (see HoverButton's own doc
          // comment; Button.tsx wraps it for exactly this kind of override).
          // Rather than washing to the default neutral overlay, hover adds an
          // inset ring in the SAME state color so armed reads brighter/armed
          // and disarmed reads brighter/disarmed, never neutral.
          hoverStyle={{ boxShadow: `inset 0 0 0 1px ${p.armed ? palette.accent : palette.borderStrong}` }}>
          {p.armed ? "ARMED" : "DISARMED"}
        </Button>
      </div>
    </div>
  );
}
